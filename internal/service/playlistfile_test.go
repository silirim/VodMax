package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
	"github.com/silirim/vodmax/internal/vpn"
)

// playlistOf is a synthetic playlist of n channels.
func playlistOf(n int) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "#EXTINF:-1 tvg-id=\"c%d\" group-title=\"Notizie\",Canale %d\nhttp://panel.example/live/u/p/%d.ts\n", i, i, i)
	}
	return b.String()
}

func TestCloudLink(t *testing.T) {
	dl := "https://drive.google.com/uc?export=download&id=FILE_id-1"
	cases := []struct{ link, drive, download, err string }{
		{"https://drive.google.com/file/d/FILE_id-1/view?usp=sharing", "Google Drive", dl, ""},
		{"https://drive.google.com/file/d/FILE_id-1/edit", "Google Drive", dl, ""},
		{"https://drive.google.com/file/d/FILE_id-1/preview", "Google Drive", dl, ""},
		{"https://drive.google.com/file/u/1/d/FILE_id-1/view", "Google Drive", dl, ""},
		{"https://drive.google.com/u/0/file/d/FILE_id-1/view?usp=drive_link", "Google Drive", dl, ""},
		{"https://drive.google.com/open?id=FILE_id-1", "Google Drive", dl, ""},
		{"https://drive.google.com/uc?id=FILE_id-1&export=download", "Google Drive", dl, ""},
		{"https://docs.google.com/uc?export=download&id=FILE_id-1", "Google Drive", dl, ""},
		{"https://drive.google.com/file/d/FILE_id-1/view?usp=sharing&resourcekey=0-RK1", "Google Drive", dl + "&resourcekey=0-RK1", ""},
		{"https://drive.google.com/drive/folders/DIR1?usp=sharing", "", "", "cartella di Google Drive"},
		{"https://drive.google.com/drive/u/0/folders/DIR1", "", "", "cartella di Google Drive"},
		{"https://drive.google.com/drive/my-drive", "", "", "non riconosciuto"},
		{"https://docs.google.com/document/d/DOC1/edit", "", "", "documento di Google"},
		{"https://www.dropbox.com/scl/fi/abc123/lista.m3u?rlkey=KEY1&dl=0", "Dropbox", "https://www.dropbox.com/scl/fi/abc123/lista.m3u?dl=1&rlkey=KEY1", ""},
		{"https://www.dropbox.com/scl/fi/abc123/lista.m3u?rlkey=KEY1&st=x9&dl=0", "Dropbox", "https://www.dropbox.com/scl/fi/abc123/lista.m3u?dl=1&rlkey=KEY1&st=x9", ""},
		{"https://dropbox.com/s/old123/lista.m3u?dl=0", "Dropbox", "https://dropbox.com/s/old123/lista.m3u?dl=1", ""},
		{"https://www.dropbox.com/scl/fo/dir123/xyz?rlkey=KEY1&dl=0", "", "", "cartella di Dropbox"},
		{"https://www.dropbox.com/sh/dir123/xyz?dl=0", "", "", "cartella di Dropbox"},
		{"https://1drv.ms/u/s!ABCDEF?e=xyz", "", "", "OneDrive non dà un link"},
		{"https://1drv.ms/u/c/abc/EF123", "", "", "OneDrive non dà un link"},
		{"https://onedrive.live.com/redir?resid=ABC!123", "", "", "OneDrive non dà un link"},
		{"https://casa-my.sharepoint.com/:u:/g/personal/x/EF123", "", "", "OneDrive non dà un link"},
		// Any other address is downloaded as it is.
		{"http://panel.example/lista.m3u?u=1", "", "http://panel.example/lista.m3u?u=1", ""},
	}
	for _, c := range cases {
		drive, download, err := cloudLink(c.link)
		if c.err != "" {
			var bad ErrInvalid
			if !errors.As(err, &bad) || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: error %v, want %q", c.link, err, c.err)
			}
			continue
		}
		if err != nil || drive != c.drive || download != c.download {
			t.Errorf("%s → %q %q %v", c.link, drive, download, err)
		}
	}
}

// OneDrive and folders are refused when the subscription is saved, with
// the reason in the person's language.
func TestCloudLinkRefusedOnSave(t *testing.T) {
	ctx := context.Background()
	s, vpnID := linkService(t)
	_, err := s.AddSubscription(ctx, SubscriptionInput{Name: "O", Kind: "m3u", VPNID: vpnID, URL: "https://1drv.ms/u/s!ABCDEF"})
	if err == nil || err.Error() != "OneDrive non dà un link di download senza accesso: scarica il file e caricalo da qui" {
		t.Fatalf("OneDrive: %v", err)
	}
	if en := Localize("en", err); !strings.Contains(en, "upload it here") {
		t.Fatalf("OneDrive in English: %q", en)
	}
	id, err := s.AddSubscription(ctx, SubscriptionInput{Name: "D", Kind: "m3u", VPNID: vpnID, URL: "https://drive.google.com/file/d/F1/view"})
	if err != nil {
		t.Fatal(err)
	}
	folder := "https://www.dropbox.com/scl/fo/dir/x?rlkey=K"
	if err := s.UpdateSubscription(ctx, id, SubscriptionPatch{URL: &folder}); err == nil || !strings.Contains(err.Error(), "cartella di Dropbox") {
		t.Fatalf("folder saved: %v", err)
	}
}

func TestDriveConfirm(t *testing.T) {
	base, _ := url.Parse("https://drive.usercontent.google.com/download?id=F1&export=download")
	page := `<!DOCTYPE html><html><body><p>Google Drive can't scan this file for viruses.</p>
		<form id="search"><input type="hidden" name="q" value="no"></form>
		<FORM ID='download-form' action="https://drive.usercontent.google.com/download" method="get">
		<input type="submit" id="uc-download-link" value="Download anyway">
		<input type="hidden" name="id" value="F1"><input type="hidden" name="export" value="download">
		<input name="confirm" type="hidden" value="t"><input type="hidden" name="uuid" value="u-1&amp;2"></form></body></html>`
	got := driveConfirm(base, page)
	if got != "https://drive.usercontent.google.com/download?confirm=t&export=download&id=F1&uuid=u-1%262" {
		t.Fatalf("confirm link %q", got)
	}
	if got := driveConfirm(base, `<html><form action="/x"></form></html>`); got != "" {
		t.Fatalf("page without the form: %q", got)
	}
}

// cloudTunnel is the subscription's VPN of the test: every connection,
// whatever the host, reaches the fake cloud.
type cloudTunnel struct {
	addr  string
	dials atomic.Int32
}

func (c *cloudTunnel) Up() bool           { return true }
func (c *cloudTunnel) Status() vpn.Status { return vpn.Status{Connected: true} }
func (c *cloudTunnel) Close()             {}
func (c *cloudTunnel) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c.dials.Add(1)
	if strings.HasPrefix(address, "unreachable.example:") {
		return nil, errors.New("no route to host")
	}
	return (&net.Dialer{}).DialContext(ctx, network, c.addr)
}

// fakeCloud answers as Google Drive and Dropbox do, by host: Drive's
// download link sends to drive.usercontent.google.com, which gives the
// file or, for a large one, a page to confirm; Dropbox sends to its
// download host, and answers 200 with a page for what it does not know.
func fakeCloud(t *testing.T, list string, downloads *atomic.Int32) *httptest.Server {
	t.Helper()
	page := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<!DOCTYPE html><html><body>"+body+"</body></html>")
	}
	confirm := `<form id="download-form" action="http://drive.usercontent.google.com/download" method="get">
		<input type="hidden" name="id" value="%s"><input type="hidden" name="export" value="download">
		<input type="hidden" name="confirm" value="t"><input type="hidden" name="uuid" value="uu1"></form>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.Host + r.URL.Path {
		case "drive.google.com/uc":
			if q.Get("export") != "download" {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "http://drive.usercontent.google.com/download?id="+q.Get("id")+"&export=download", http.StatusSeeOther)
		case "drive.usercontent.google.com/download":
			downloads.Add(1)
			id := q.Get("id")
			switch {
			case id == "small", id == "big" && q.Get("confirm") == "t" && q.Get("uuid") == "uu1":
				w.Header().Set("Content-Type", "application/octet-stream")
				io.WriteString(w, list)
			case id == "html": // the file, served as a page, longer than the page read to find the form
				w.Header().Set("Content-Type", "text/html")
				io.WriteString(w, strings.Repeat("#\n", 1<<19)+list)
			case id == "unreachable":
				page(w, strings.Replace(fmt.Sprintf(confirm, id), "drive.usercontent.google.com", "unreachable.example", 1))
			case id == "big", id == "stubborn":
				page(w, fmt.Sprintf(confirm, id))
			default:
				page(w, "Sign in to continue to Google Drive")
			}
		case "www.dropbox.com/scl/fi/abc/lista.m3u":
			if q.Get("rlkey") != "K1" {
				page(w, "That didn't work for some reason")
				return
			}
			if q.Get("dl") != "1" {
				page(w, "Preview")
				return
			}
			http.Redirect(w, r, "http://uc1.dl.dropboxusercontent.com/cd/0/get/abc/file", http.StatusFound)
		case "uc1.dl.dropboxusercontent.com/cd/0/get/abc/file":
			downloads.Add(1)
			io.WriteString(w, list)
		default:
			page(w, "Not found")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A share link of Google Drive or Dropbox is synced through the
// subscription's VPN, redirects included; a page instead of the file
// ends with a message that names the service.
func TestPlaylistFromCloud(t *testing.T) {
	defer func(old string) { cloudScheme = old }(cloudScheme)
	cloudScheme = "http"
	ctx := context.Background()
	s, vpnID := linkService(t)
	var downloads atomic.Int32
	cloud := fakeCloud(t, playlistOf(3), &downloads)
	tun := &cloudTunnel{addr: cloud.Listener.Addr().String()}
	s.tunnels[vpnID] = tun

	cases := []struct{ name, link, err string }{
		{"drive", "https://drive.google.com/file/d/small/view?usp=sharing", ""},
		{"drive-grande", "https://drive.google.com/open?id=big", ""},
		{"drive-privato", "https://drive.google.com/file/d/private/view", "Google Drive non ha dato il file: controlla"},
		{"drive-conferma", "https://drive.google.com/file/d/stubborn/view", "nemmeno dopo la conferma"},
		{"drive-come-pagina", "https://drive.google.com/file/d/html/view", ""},
		{"drive-conferma-irraggiungibile", "https://drive.google.com/file/d/unreachable/view", "playlist:"},
		{"dropbox", "https://www.dropbox.com/scl/fi/abc/lista.m3u?rlkey=K1&dl=0", ""},
		{"dropbox-inesistente", "https://www.dropbox.com/scl/fi/abc/lista.m3u?rlkey=OLD&dl=0", "Dropbox non ha dato il file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			downloads.Store(0)
			dials := tun.dials.Load()
			id, err := s.AddSubscription(ctx, SubscriptionInput{Name: c.name, Kind: "m3u", VPNID: vpnID, URL: c.link})
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.Sync(ctx, id, false)
			if tun.dials.Load() == dials {
				t.Fatal("the cloud was not reached through the subscription's VPN")
			}
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("sync: %v, want %q", err, c.err)
				}
				if sub, _ := s.st.Subscription(ctx, id); strings.Contains(c.err, "non ha dato") && TranslateText("de", sub.SyncError) == sub.SyncError {
					t.Fatalf("not translated: %q", sub.SyncError)
				}
				if c.name == "drive-conferma" && downloads.Load() != 2 {
					t.Fatalf("confirmation tried %d times, want once", downloads.Load()-1)
				}
				return
			}
			if err != nil || res.Total["live"] != 3 {
				t.Fatalf("sync: %+v %v", res, err)
			}
			// The link is kept as typed; the download link is made at each sync.
			sub, _ := s.st.Subscription(ctx, id)
			raw, _ := s.box.Open(sub.Secret, subOwner(id))
			var sec subscriptionSecret
			if json.Unmarshal(raw, &sec); sec.URL != c.link {
				t.Fatalf("stored %q", sec.URL)
			}
		})
	}
}

// fileService has a VPN that cannot come up: syncing a file must not need it.
func fileService(t *testing.T) (*Service, int64) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	// Its configuration cannot even be read.
	vpnID, err := st.AddVPN(context.Background(), "v", "wireguard", func(int64) []byte { return []byte("x") })
	if err != nil {
		t.Fatal(err)
	}
	return New(st, box, func(ctx context.Context, cfg *vpn.Config) (Tunnel, error) {
		t.Error("VPN started")
		return nil, errors.New("no VPN in this test")
	}), vpnID
}

func TestPlaylistFile(t *testing.T) {
	ctx := context.Background()
	s, vpnID := fileService(t)
	list := playlistOf(10)
	id, err := s.AddPlaylistFile(ctx, "Da file", vpnID, "lista.m3u8", strings.NewReader(list))
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := s.st.Subscription(ctx, id)
	var set m3uSettings
	if err := json.Unmarshal(sub.Settings, &set); err != nil || set.File != "lista.m3u8" || set.Size != int64(len(list)) || set.Uploaded == 0 || set.Host != "" {
		t.Fatalf("settings %s", sub.Settings)
	}
	sealed, err := s.st.PlaylistFile(ctx, id)
	if err != nil || bytes.Contains(sealed, []byte("panel.example")) {
		t.Fatalf("file stored in clear or missing: %v", err)
	}
	res, err := s.Sync(ctx, id, false)
	if err != nil || res.Total["live"] != 10 {
		t.Fatalf("sync: %+v %v", res, err)
	}

	// The nightly sync leaves a file alone once read; one never read, or
	// whose reading was cut short, it reads.
	other, err := s.AddPlaylistFile(ctx, "Mai", vpnID, "", strings.NewReader(list))
	if err != nil {
		t.Fatal(err)
	}
	syncOf := func() *syncState { s.mu.Lock(); defer s.mu.Unlock(); return s.syncs[id] }
	before := syncOf()
	nightly := func() {
		s.runScheduled(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now().Add(48*time.Hour))
	}
	nightly()
	if sub, _ := s.st.Subscription(ctx, other); sub.SyncedAt == nil || syncOf() != before {
		t.Fatalf("schedule: file never read left empty (%v) or read file synced again", sub.SyncedAt)
	}
	s.st.SetSetting(ctx, syncRunKey(id), syncRun{Started: time.Now(), Attempts: 1})
	if nightly(); syncOf() == before {
		t.Fatal("reading cut short not done again")
	}

	// Replacing it with a much shorter list is a choice: no drop check.
	if _, err := s.ReplacePlaylistFile(ctx, id, "nuova.m3u", strings.NewReader(playlistOf(2))); err != nil {
		t.Fatal(err)
	}
	v := waitView(t, s, func(v SubscriptionView) bool {
		return v.ID == id && !v.Syncing && v.Counts["live"] == 2 || v.LastSync != nil && v.LastSync.Error != ""
	})
	if v.LastSync != nil && v.LastSync.Error != "" {
		t.Fatalf("replace: %s", v.LastSync.Error)
	}

	// A sync that starts while the file arrives would keep the old catalog.
	busy := &onEOF{r: strings.NewReader(list), f: func() { s.beginSync(id) }}
	if _, err := s.ReplacePlaylistFile(ctx, id, "tardi.m3u", busy); !errors.Is(err, ErrBusy) {
		t.Fatalf("replaced under a running sync: %v", err)
	}
	s.mu.Lock()
	s.syncs[id].running = false
	s.mu.Unlock()
	if sub, _ := s.st.Subscription(ctx, id); strings.Contains(string(sub.Settings), "tardi.m3u") {
		t.Fatalf("settings %s", sub.Settings)
	}

	// An address takes the place of the file, and a file of the address.
	link := "http://panel.example/lista.m3u"
	if err := s.UpdateSubscription(ctx, id, SubscriptionPatch{URL: &link}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.PlaylistFile(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("file left: %v", err)
	}
	if sub, _ := s.st.Subscription(ctx, id); fromFile(sub) || !strings.Contains(string(sub.Settings), "panel.example") {
		t.Fatalf("settings %s", sub.Settings)
	}
	if _, err := s.ReplacePlaylistFile(ctx, id, "di nuovo.m3u", strings.NewReader(list)); err != nil {
		t.Fatal(err)
	}
	sub, _ = s.st.Subscription(ctx, id)
	if raw, _ := s.box.Open(sub.Secret, subOwner(id)); strings.Contains(string(raw), "panel.example") || !fromFile(sub) {
		t.Fatalf("address kept: %s %s", raw, sub.Settings)
	}
	waitView(t, s, func(v SubscriptionView) bool { return !v.Syncing })

	// What is not a playlist is refused and not stored.
	if _, err := s.AddPlaylistFile(ctx, "Pagina", vpnID, "x.m3u", strings.NewReader("<html><body>Accedi</body></html>")); err == nil ||
		!strings.Contains(err.Error(), "il file non è una playlist M3U") {
		t.Fatalf("page accepted: %v", err)
	}
	if _, err := s.AddPlaylistFile(ctx, "Vuota", vpnID, "x.m3u", strings.NewReader("#EXTM3U\n")); err == nil {
		t.Fatal("empty playlist accepted")
	}
	subs, _ := s.st.Subscriptions(ctx)
	if len(subs) != 2 {
		t.Fatalf("%d subscriptions", len(subs))
	}
}

// onEOF calls f once the reader is read to the end.
type onEOF struct {
	r io.Reader
	f func()
}

func (o *onEOF) Read(p []byte) (int, error) {
	n, err := o.r.Read(p)
	if err == io.EOF && o.f != nil {
		o.f()
		o.f = nil
	}
	return n, err
}

// A failing get.php address becomes Xtream Codes when saved; the page
// sends a new file first, so saving the name after keeps the file.
func TestPlaylistFileOverXtreamLink(t *testing.T) {
	ctx := context.Background()
	s, vpnID := fileService(t)
	sec, _ := json.Marshal(subscriptionSecret{URL: "http://panel.example/get.php?username=u&password=p&type=m3u_plus"})
	id, err := s.st.AddSubscription(ctx, store.Subscription{Name: "B", Kind: "m3u", VPNID: vpnID, Settings: json.RawMessage(`{"host":"panel.example"}`)},
		func(id int64) []byte { return s.box.Seal(sec, subOwner(id)) })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.st.SetSyncResult(ctx, id, "error", "playlist: risposta HTTP 884", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplacePlaylistFile(ctx, id, "lista.m3u", strings.NewReader(playlistOf(3))); err != nil {
		t.Fatal(err)
	}
	name := "B2"
	if err := s.UpdateSubscription(ctx, id, SubscriptionPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	waitView(t, s, func(v SubscriptionView) bool { return v.ID == id && !v.Syncing })
	if sub, _ := s.st.Subscription(ctx, id); sub.Kind != "m3u" || !fromFile(sub) {
		t.Fatalf("after saving: %s %s", sub.Kind, sub.Settings)
	}
}

// endless is a playlist that never ends, counting what is read of it.
type endless struct{ read int64 }

func (e *endless) Read(p []byte) (int, error) {
	line := "#EXTINF:-1,Canale\nhttp://panel.example/1.ts\n"
	n := 0
	for n < len(p) {
		n += copy(p[n:], line[int((e.read+int64(n))%int64(len(line))):])
	}
	e.read += int64(n)
	return n, nil
}

// The limit holds while the file arrives: reading stops right past it.
func TestPlaylistFileLimit(t *testing.T) {
	defer func(old int64) { playlistMaxSize = old }(playlistMaxSize)
	playlistMaxSize = 64 << 10
	s, vpnID := fileService(t)
	big := &endless{}
	_, err := s.AddPlaylistFile(context.Background(), "Grande", vpnID, "x.m3u", big)
	if err == nil || !strings.Contains(err.Error(), "troppo grande (massimo 64 MB)") {
		t.Fatalf("too big: %v", err)
	}
	if big.read > playlistMaxSize+1 {
		t.Fatalf("read %d bytes past a limit of %d", big.read, playlistMaxSize)
	}
	if _, err := s.AddPlaylistFile(context.Background(), "Giusta", vpnID, "x.m3u", io.LimitReader(&endless{}, playlistMaxSize)); err != nil {
		t.Fatalf("file at the limit: %v", err)
	}
}

// The backup carries the file: restored elsewhere, the subscription syncs
// without uploading it again.
func TestPlaylistFileBackup(t *testing.T) {
	ctx := context.Background()
	s, old := linkService(t)
	s.DeleteVPN(ctx, old) // its configuration is not sealed for a backup
	vpnID, err := s.AddVPNKind(ctx, "Esterna", "system", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlaylistFile(ctx, "Da file", vpnID, "lista.m3u", strings.NewReader(playlistOf(4))); err != nil {
		t.Fatal(err)
	}
	data, err := s.ExportBackup(ctx, "una password lunga")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := linkService(t) // another installation, another key
	if err := n.RestoreBackup(ctx, data, "una password lunga"); err != nil {
		t.Fatal(err)
	}
	v := waitView(t, n, func(v SubscriptionView) bool { return v.Counts["live"] == 4 || v.SyncStatus == "error" })
	if v.Name != "Da file" || v.SyncStatus != "ok" || !fromFile(v.Subscription) {
		t.Fatalf("restored: %+v", v)
	}
}
