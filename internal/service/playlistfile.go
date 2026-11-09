package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/silirim/vodmax/internal/catalog"
	"github.com/silirim/vodmax/internal/playlist/m3u"
	"github.com/silirim/vodmax/internal/store"
)

// An M3U playlist from a file of the device, or from a share link of
// Google Drive or Dropbox (decision 0057).

// m3uSettings are what an M3U subscription shows: the host of its
// address, or the file uploaded in its place.
type m3uSettings struct {
	Host     string `json:"host,omitempty"`
	File     string `json:"file,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Uploaded int64  `json:"uploaded,omitempty"` // Unix seconds
}

// fromFile tells an M3U subscription read from an uploaded file: nothing
// updates it but a new file.
func fromFile(sub store.Subscription) bool {
	var set m3uSettings
	json.Unmarshal(sub.Settings, &set)
	return sub.Kind == "m3u" && set.File != ""
}

// PlaylistMaxSize bounds an uploaded file, as a backup to restore.
const PlaylistMaxSize = 64 << 20

// playlistMaxSize is PlaylistMaxSize, a variable for tests.
var playlistMaxSize int64 = PlaylistMaxSize

// readPlaylistFile checks an uploaded file as a downloaded playlist is
// checked, without reading past the limit, and returns it gzipped.
func readPlaylistFile(body io.Reader, name string) ([]byte, m3uSettings, error) {
	lr := &io.LimitedReader{R: body, N: playlistMaxSize + 1}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	p, err := m3u.Parse(io.TeeReader(lr, zw), m3u.DefaultLimits)
	switch {
	case lr.N == 0:
		return nil, m3uSettings{}, invalid("file della playlist troppo grande (massimo 64 MB): usa un indirizzo o un link")
	case errors.Is(err, m3u.ErrNotPlaylist):
		return nil, m3uSettings{}, invalid("il file non è una playlist M3U (sembra una pagina web o un altro documento)")
	case err != nil:
		return nil, m3uSettings{}, invalid("%v", err)
	case len(p.Entries) == 0:
		return nil, m3uSettings{}, invalid("la playlist non contiene canali né titoli")
	}
	if err := zw.Close(); err != nil {
		return nil, m3uSettings{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "playlist.m3u"
	}
	for utf8.RuneCountInString(name) > 120 {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	return buf.Bytes(), m3uSettings{File: name, Size: playlistMaxSize + 1 - lr.N, Uploaded: time.Now().Unix()}, nil
}

// AddPlaylistFile adds an M3U subscription from a file, the playlist as
// it is (body). Its channels still play through the subscription's VPN.
func (s *Service) AddPlaylistFile(ctx context.Context, name string, vpnID int64, fileName string, body io.Reader) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return 0, invalid("nome dell'abbonamento obbligatorio (max 80 caratteri)")
	}
	if err := s.checkSubscriptionVPN(ctx, vpnID); err != nil {
		return 0, err
	}
	gz, set, err := readPlaylistFile(body, fileName)
	if err != nil {
		return 0, err
	}
	settings, _ := json.Marshal(set)
	noURL, _ := json.Marshal(subscriptionSecret{})
	id, err := s.st.AddSubscription(ctx, store.Subscription{Name: name, Kind: "m3u", VPNID: vpnID, Settings: settings},
		func(id int64) []byte { return s.box.Seal(noURL, subOwner(id)) })
	if err != nil {
		return 0, err
	}
	// Sealed like the address it replaces: channel addresses often carry
	// a user name and a password.
	if err := s.st.SetPlaylistFile(ctx, id, s.box.Seal(gz, subOwner(id))); err != nil {
		s.st.DeleteSubscription(context.WithoutCancel(ctx), id)
		return 0, err
	}
	return id, nil
}

// ReplacePlaylistFile gives an M3U subscription a new file, which takes
// the place of its address or of the previous file, and starts the sync.
// Replacing is an explicit choice: a file with less than half the titles
// is accepted.
func (s *Service) ReplacePlaylistFile(ctx context.Context, id int64, fileName string, body io.Reader) (SubscriptionView, error) {
	sub, err := s.st.Subscription(ctx, id)
	if err != nil {
		return SubscriptionView{}, err
	}
	if sub.Kind != "m3u" {
		return SubscriptionView{}, invalid("solo una playlist M3U si carica da un file")
	}
	if s.syncRunning(id) {
		return SubscriptionView{}, ErrBusy
	}
	gz, set, err := readPlaylistFile(body, fileName)
	if err != nil {
		return SubscriptionView{}, err
	}
	if s.syncRunning(id) { // started while the file arrived: it would keep the old catalog
		return SubscriptionView{}, ErrBusy
	}
	// The file first: until the address goes, the address is what syncs.
	if err := s.st.SetPlaylistFile(ctx, id, s.box.Seal(gz, subOwner(id))); err != nil {
		return SubscriptionView{}, err
	}
	settings, _ := json.Marshal(set)
	noURL, _ := json.Marshal(subscriptionSecret{})
	if err := s.st.UpdateSubscription(ctx, id, nil, nil, nil, settings, s.box.Seal(noURL, subOwner(id))); err != nil {
		return SubscriptionView{}, err
	}
	return s.StartSync(ctx, id, true)
}

// playlistFromFile reads the uploaded file: nobody is contacted.
func (s *Service) playlistFromFile(ctx context.Context, id int64) (*catalog.Snapshot, error) {
	s.syncPhase(id, "Leggo il file della playlist", 0)
	sealed, err := s.st.PlaylistFile(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, invalid("il file della playlist manca: caricalo di nuovo da Modifica")
	}
	if err != nil {
		return nil, err
	}
	gz, err := s.box.Open(sealed, subOwner(id))
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	p, err := m3u.Parse(zr, m3u.DefaultLimits)
	if err != nil {
		return nil, err
	}
	return s.m3uSnapshot(id, p)
}

func (s *Service) m3uSnapshot(id int64, p *m3u.Playlist) (*catalog.Snapshot, error) {
	if len(p.Entries) == 0 {
		return nil, errors.New("la playlist non contiene canali né titoli")
	}
	return catalog.FromM3U(p, s.box.Seal, id), nil
}

// cloudScheme of the download links: https; the fake clouds of the tests
// have no certificate.
var cloudScheme = "https"

// cloudLink reads a playlist address: the drive it shares a file from
// ("Google Drive", "Dropbox", "" for any other address) and the address
// that downloads it. The link is stored as typed and converted at every
// sync, so a new format of the service only needs a new VodMax. Links
// that cannot give a playlist are refused with the reason.
func cloudLink(raw string) (drive, download string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", invalid("URL della playlist non valido (atteso http:// o https://)")
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "1drv.ms" || host == "onedrive.live.com" || host == "onedrive.com" || strings.HasSuffix(host, ".sharepoint.com"):
		// Not without signing in (see "Formati verificati" in 0057).
		return "", "", invalid("OneDrive non dà un link di download senza accesso: scarica il file e caricalo da qui")
	case host == "docs.google.com" && u.Path != "/uc": // docs.google.com/uc?id= is a Drive file
		return "", "", invalid("questo è un documento di Google, non un file: carica il file .m3u in Google Drive e condividi quello")
	case host == "drive.google.com" || host == "docs.google.com":
		if strings.Contains(u.Path, "/folders/") {
			return "", "", invalid("questo è il link di una cartella di Google Drive: condividi il file della playlist, non la cartella")
		}
		id := driveID(u)
		if id == "" {
			return "", "", invalid("link di Google Drive non riconosciuto: usa “Copia link” sul file della playlist")
		}
		link := cloudScheme + "://drive.google.com/uc?export=download&id=" + url.QueryEscape(id)
		if k := u.Query().Get("resourcekey"); k != "" {
			link += "&resourcekey=" + url.QueryEscape(k) // files shared before 2021 need it
		}
		return "Google Drive", link, nil
	case host == "dropbox.com" || host == "www.dropbox.com":
		if strings.HasPrefix(u.Path, "/scl/fo/") || strings.HasPrefix(u.Path, "/sh/") {
			// With dl=1 a folder comes as a zip.
			return "", "", invalid("questo è il link di una cartella di Dropbox: condividi il file della playlist, non la cartella")
		}
		if strings.HasPrefix(u.Path, "/scl/fi/") || strings.HasPrefix(u.Path, "/s/") {
			d := *u
			q := d.Query()
			q.Set("dl", "1") // the rest stays, rlkey included
			d.Scheme, d.RawQuery = cloudScheme, q.Encode()
			return "Dropbox", d.String(), nil
		}
	}
	return "", raw, nil
}

// driveID is the file id of a Google Drive share link: /file/d/ID/view
// (also /edit, /preview, with /u/N/ for the account), open?id=ID, uc?id=ID.
func driveID(u *url.URL) string {
	var p []string
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i < len(segs); i++ {
		if segs[i] == "u" && i+1 < len(segs) {
			i++ // the account number
			continue
		}
		p = append(p, segs[i])
	}
	switch {
	case len(p) >= 3 && p[0] == "file" && p[1] == "d":
		return p[2]
	case len(p) == 1 && (p[0] == "open" || p[0] == "uc"):
		return u.Query().Get("id")
	}
	return ""
}

// cloudError names the drive that gave a page instead of the file: a link
// not public, a file removed, a download quota, a check of Google on
// unusual traffic from a VPN address.
func cloudError(drive string, confirmed bool) error {
	switch {
	case confirmed:
		return errors.New("Google Drive non ha dato il file nemmeno dopo la conferma: scaricalo e caricalo da qui")
	case drive == "Dropbox":
		return errors.New("Dropbox non ha dato il file: controlla che sia condiviso con chiunque abbia il link")
	}
	return errors.New("Google Drive non ha dato il file: controlla che sia condiviso con chiunque abbia il link")
}

var (
	formTag  = regexp.MustCompile(`(?i)<form\b[^>]*>`)
	inputTag = regexp.MustCompile(`(?i)<input\b[^>]*>`)
	formEnd  = regexp.MustCompile(`(?i)</form\s*>`)
	htmlAttr = regexp.MustCompile(`([A-Za-z_:][-A-Za-z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

func tagAttrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range htmlAttr.FindAllStringSubmatch(tag, -1) {
		out[strings.ToLower(m[1])] = html.UnescapeString(m[2] + m[3] + m[4])
	}
	return out
}

// driveConfirm reads the page Google Drive gives for a large file instead
// of the file ("cannot check for viruses"): its form download-form gets
// the file with its hidden fields (id, export, confirm, uuid). "" when the
// page has no such form.
func driveConfirm(base *url.URL, page string) string {
	for _, loc := range formTag.FindAllStringIndex(page, -1) {
		form := tagAttrs(page[loc[0]:loc[1]])
		if form["id"] != "download-form" {
			continue
		}
		action, err := base.Parse(form["action"])
		if err != nil || (action.Scheme != "http" && action.Scheme != "https") {
			return ""
		}
		inner := page[loc[1]:]
		if end := formEnd.FindStringIndex(inner); end != nil {
			inner = inner[:end[0]]
		}
		q := action.Query()
		for _, tag := range inputTag.FindAllString(inner, -1) {
			in := tagAttrs(tag)
			if strings.EqualFold(in["type"], "hidden") && in["name"] != "" {
				q.Set(in["name"], in["value"])
			}
		}
		action.RawQuery = q.Encode()
		return action.String()
	}
	return ""
}
