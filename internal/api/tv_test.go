package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// tvProvider serves a small lineup, an XMLTV guide around "now" and
// endless live streams; it counts concurrent streams like a provider
// enforcing max_connections would.
type tvProvider struct {
	*httptest.Server
	mu       sync.Mutex
	active   int
	maxSeen  int
	opened   []string
	logoHits map[string]int
	ranges   []string
	refused  int             // live requests refused for exceeding the connection limit
	broken   map[string]bool // live streams ("101.ts") the provider does not serve
	stalls   map[string]bool // live streams that stop sending after a few packets, connection open
	extra    string          // more live streams, JSON objects
}

// fakeFilm is the content served for films and episodes.
var fakeFilm = func() []byte {
	b := make([]byte, 1<<20)
	copy(b[4:], "ftypisom")
	for i := 16; i < len(b); i++ {
		b[i] = byte(i)
	}
	return b
}()

func newTVProvider() *tvProvider {
	p := &tvProvider{logoHits: map[string]int{}}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/logo/"):
			p.mu.Lock()
			p.logoHits[r.URL.Path]++
			p.mu.Unlock()
			time.Sleep(50 * time.Millisecond) // let concurrent requests overlap
			if strings.HasSuffix(r.URL.Path, ".svg") {
				w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
				return
			}
			w.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...))
		case r.URL.Path == "/xmltv.php":
			now := time.Now().UTC()
			f := func(t time.Time) string { return t.Format("20060102150405 +0000") }
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><tv>
				<channel id="Rai1.it"><display-name>Rai 1</display-name></channel>
				<channel id="cielo.guide"><display-name>Cielo</display-name></channel>
				<programme start="%s" stop="%s" channel="Rai1.it"><title>TG1</title></programme>
				<programme start="%s" stop="%s" channel="Rai1.it"><title>Film della sera</title></programme>
				<programme start="%s" stop="%s" channel="cielo.guide"><title>Case da sogno</title></programme>
				<programme start="%s" stop="%s" channel="cielo.guide"><title>Calcio: Juventus - Inter</title><previously-shown/></programme>
				</tv>`, f(now.Add(-10*time.Minute)), f(now.Add(20*time.Minute)), f(now.Add(20*time.Minute)), f(now.Add(2*time.Hour)),
				f(now.Add(-time.Hour)), f(now.Add(time.Hour)), f(now.Add(3*time.Hour)), f(now.Add(5*time.Hour)))
		case strings.HasPrefix(r.URL.Path, "/movie/"), strings.HasPrefix(r.URL.Path, "/series/"):
			p.mu.Lock()
			p.active++
			p.maxSeen = max(p.maxSeen, p.active)
			p.ranges = append(p.ranges, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]+" "+r.Header.Get("Range"))
			p.mu.Unlock()
			defer func() { p.mu.Lock(); p.active--; p.mu.Unlock() }()
			w.Header().Set("Content-Type", "video/mp4")
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(fakeFilm))
		case strings.HasPrefix(r.URL.Path, "/live/"):
			// Like a real provider with max_connections=1: a request
			// arriving while a stream is still open is refused, not served.
			p.mu.Lock()
			if p.broken[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]] {
				p.mu.Unlock()
				http.Error(w, "stream not found", http.StatusNotFound)
				return
			}
			if p.active >= 1 {
				p.refused++
				p.mu.Unlock()
				http.Error(w, "max connections reached", 458)
				return
			}
			p.active++
			p.maxSeen = max(p.maxSeen, p.active)
			p.opened = append(p.opened, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
			p.mu.Unlock()
			defer func() { p.mu.Lock(); p.active--; p.mu.Unlock() }()
			w.Header().Set("Content-Type", "video/mp2t")
			packet := make([]byte, 188)
			packet[0] = 0x47
			p.mu.Lock()
			stalls := p.stalls[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]
			p.mu.Unlock()
			for sent := 0; ; sent++ {
				if stalls && sent >= 20 {
					<-r.Context().Done()
					return
				}
				if _, err := w.Write(packet); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(5 * time.Millisecond):
				}
			}
		default:
			switch r.URL.Query().Get("action") {
			case "":
				w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","max_connections":"1"}}`))
			case "get_live_categories":
				w.Write([]byte(`[{"category_id":"1","category_name":"Italia"},{"category_id":"2","category_name":"Intrattenimento"},
					{"category_id":"3","category_name":"Dazn Locale"}]`))
			case "get_live_streams":
				logo := "http://" + r.Host + "/logo/"
				w.Write([]byte(`[{"name":"Canale 5 HD","stream_id":105,"category_id":"1"},{"name":"Rai 1 HD","stream_id":101,"category_id":"1","epg_channel_id":"Rai1.it","stream_icon":"` + logo + `rai1.png","tv_archive":1,"tv_archive_duration":3},
					{"name":"Cielo HD","stream_id":126,"category_id":"2","stream_icon":"` + logo + `cielo.svg"},{"name":"Alma Tv","stream_id":300,"category_id":"2"},
					{"name":"DAZN Calcio 1 HD","stream_id":400,"category_id":"3"}` + extraStreams(p.extra) + `]`))
			case "get_vod_categories":
				w.Write([]byte(`[{"category_id":"10","category_name":"Horror"},{"category_id":"11","category_name":"Thriller"}]`))
			case "get_vod_streams":
				w.Write([]byte(`[{"name":"1408 (2007)","stream_id":555,"category_id":"10","container_extension":"mp4","tmdb":"3021","rating":"6.9"},
					{"name":"1408 (2007) 4K","stream_id":557,"category_id":"11","container_extension":"mp4","tmdb":"3021","rating":"6.9"},
					{"name":"Psycho (1960)","stream_id":556,"category_id":"10","container_extension":"mkv","tmdb":"539","rating":"8.5"}]`))
			case "get_vod_info":
				if id := r.URL.Query().Get("vod_id"); id == "555" || id == "557" {
					w.Write([]byte(`{"info":{"plot":"Uno scrittore scettico...","cast":"John Cusack","director":"Mikael Håfström",
						"genre":"Horror","rating":"6.9","duration_secs":6240,"youtube_trailer":"abcDEF12345",
						"backdrop_path":["http://` + r.Host + `/logo/rai1.png"]},"movie_data":{"stream_id":555}}`))
				} else {
					w.Write([]byte(`{"info":[],"movie_data":{"stream_id":556}}`))
				}
			case "get_series_categories":
				w.Write([]byte(`[{"category_id":"20","category_name":"Serie Netflix"}]`))
			case "get_series":
				w.Write([]byte(`[{"name":"Twin Peaks","series_id":77,"category_id":"20"}]`))
			case "get_series_info":
				w.Write([]byte(`{"info":{"name":"Twin Peaks"},"episodes":{
					"1":[{"id":"9001","episode_num":1,"title":"Pilot","container_extension":"mp4","info":{"duration_secs":5400,
						"plot":"Laura Palmer è morta.","movie_image":"http://` + r.Host + `/logo/rai1.png"}},
					     {"id":"9002","episode_num":2,"title":"Traces to Nowhere","container_extension":"mp4","info":{"duration_secs":2800}}],
					"2":[{"id":"9101","episode_num":1,"title":"May the Giant Be with You","container_extension":"mp4"}]}}`))
			default:
				w.Write([]byte(`[]`))
			}
		}
	}))
	return p
}

type tvChannel struct {
	ID       int64  `json:"id"`
	Number   int    `json:"number"`
	Official bool   `json:"official"`
	Name     string `json:"name"`
	Section  string `json:"section"`
	Favorite bool   `json:"favorite"`
	Now      *struct {
		Title string `json:"title"`
	} `json:"now"`
	Next *struct {
		Title string `json:"title"`
	} `json:"next"`
}

func extraStreams(s string) string {
	if s == "" {
		return ""
	}
	return "," + s
}

func tvSetup(t *testing.T) (*env, *tvProvider) {
	return tvSetupWith(t, newTVProvider(), "")
}

// tvSetupWith is tvSetup with more live streams at the provider.
func tvSetupWith(t *testing.T, p *tvProvider, extra string) (*env, *tvProvider) {
	p.extra = extra
	t.Cleanup(p.Close)
	e := setup(t, true)
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "B", "kind": "xtream", "vpn_id": 1,
		"server": p.URL, "username": "u", "password": "p"}, true)
	code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true)
	if code != 200 || !strings.Contains(string(body), `"programmes":4`) {
		t.Fatalf("sync with guide: %d %s", code, body)
	}
	return e, p
}

func channels(t *testing.T, e *env, query string) []tvChannel {
	t.Helper()
	code, body := e.do(t, "GET", "/api/v1/tv/channels"+query, nil, true)
	var list []tvChannel
	if code != 200 || json.Unmarshal(body, &list) != nil {
		t.Fatalf("channels: %d %s", code, body)
	}
	return list
}

func TestTVLineupGuideAndFavorites(t *testing.T) {
	e, _ := tvSetup(t)
	tv := channels(t, e, "?section=tv")
	got := []string{}
	for _, c := range tv {
		got = append(got, fmt.Sprintf("%d %s", c.Number, c.Name))
	}
	// Official numbers first, then the rest from 100.
	if strings.Join(got, ", ") != "1 Rai 1, 5 Canale 5, 26 Cielo, 100 Alma Tv" {
		t.Fatalf("lineup: %v", got)
	}
	if tv[0].Now == nil || tv[0].Now.Title != "TG1" || tv[0].Next == nil || tv[0].Next.Title != "Film della sera" {
		t.Fatalf("now/next by epg id: %+v %+v", tv[0].Now, tv[0].Next)
	}
	if tv[2].Now == nil || tv[2].Now.Title != "Case da sogno" {
		t.Fatalf("now by channel name (no epg id): %+v", tv[2])
	}
	sport := channels(t, e, "?section=sport")
	if len(sport) != 1 || sport[0].Number != 101 {
		t.Fatalf("sport numbering continues after TV: %+v", sport)
	}

	_, body := e.do(t, "GET", "/api/v1/tv/channels/"+strconv.FormatInt(tv[0].ID, 10)+"/guide", nil, true)
	if !strings.Contains(string(body), "Film della sera") {
		t.Fatalf("guide: %s", body)
	}

	// Favourites keep the channel numbers and the order of insertion.
	for _, c := range []tvChannel{tv[2], tv[0]} {
		if code, _ := e.do(t, "PUT", "/api/v1/tv/favorites/"+strconv.FormatInt(c.ID, 10), map[string]bool{"favorite": true}, true); code != 204 {
			t.Fatal("add favorite")
		}
	}
	fav := channels(t, e, "?list=favorites")
	if len(fav) != 2 || fav[0].Name != "Cielo" || fav[0].Number != 26 || fav[1].Number != 1 || !fav[0].Favorite {
		t.Fatalf("favorites: %+v", fav)
	}
	e.do(t, "PUT", "/api/v1/tv/favorites/"+strconv.FormatInt(tv[2].ID, 10), map[string]bool{"favorite": false}, true)
	if fav := channels(t, e, "?list=favorites"); len(fav) != 1 || fav[0].Name != "Rai 1" {
		t.Fatalf("remove favorite: %+v", fav)
	}
	// Favourites survive a new synchronisation.
	e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true)
	if fav := channels(t, e, "?list=favorites"); len(fav) != 1 {
		t.Fatalf("favorites lost after sync: %+v", fav)
	}
}

func ticket(t *testing.T, e *env, id int64, viewer string) string {
	t.Helper()
	code, body := e.do(t, "POST", "/api/v1/live/"+strconv.FormatInt(id, 10)+"/ticket", map[string]string{"viewer": viewer}, true)
	var out struct{ URL string }
	if code != 200 || json.Unmarshal(body, &out) != nil {
		t.Fatalf("ticket: %d %s", code, body)
	}
	return out.URL
}

func openStream(t *testing.T, e *env, url string) (*http.Response, error) {
	resp, err := http.Get(e.srv.URL + url)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func readSome(t *testing.T, resp *http.Response) {
	t.Helper()
	buf := make([]byte, 188*10)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || buf[0] != 0x47 {
		t.Fatalf("stream data: %v", err)
	}
}

func TestLiveRelaySingleConnection(t *testing.T) {
	e, p := tvSetup(t)
	tv := channels(t, e, "?section=tv")
	rai, cielo := tv[0], tv[2]

	first, err := openStream(t, e, ticket(t, e, rai.ID, "salotto-tv-01"))
	if err != nil || first.StatusCode != 200 || first.Header.Get("Content-Type") != "video/mp2t" {
		t.Fatalf("open: %v %v", err, first)
	}
	readSome(t, first)

	// Zapping: the same viewer changes channel, the old stream is closed
	// before the new one opens.
	second, _ := openStream(t, e, ticket(t, e, cielo.ID, "salotto-tv-01"))
	if second.StatusCode != 200 {
		t.Fatalf("zap: %d", second.StatusCode)
	}
	readSome(t, second)
	if _, err := io.ReadAll(first.Body); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Logf("old stream ended: %v", err)
	}
	first.Body.Close()

	// Another device: the only connection is busy.
	url := ticket(t, e, rai.ID, "camera-tablet-02")
	busy, _ := openStream(t, e, url)
	body, _ := io.ReadAll(busy.Body)
	busy.Body.Close()
	if busy.StatusCode != 409 || !strings.Contains(string(body), "in uso") {
		t.Fatalf("second viewer: %d %s", busy.StatusCode, body)
	}
	// The ticket was consumed even though the stream was refused.
	if again, _ := openStream(t, e, url); again.StatusCode != 403 {
		t.Fatalf("ticket reuse: %d", again.StatusCode)
	}

	// Explicit take-over.
	taken, _ := openStream(t, e, ticket(t, e, rai.ID, "camera-tablet-02")+"?force=1")
	if taken.StatusCode != 200 {
		t.Fatalf("force: %d", taken.StatusCode)
	}
	readSome(t, taken)
	taken.Body.Close()
	second.Body.Close()

	time.Sleep(100 * time.Millisecond)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.maxSeen != 1 {
		t.Fatalf("provider saw %d concurrent streams, max 1 allowed", p.maxSeen)
	}
	if strings.Join(p.opened, ",") != "101.ts,126.ts,101.ts" {
		t.Fatalf("streams opened: %v", p.opened)
	}
}

func TestWebInterfaceServedWithStrictCSP(t *testing.T) {
	e := setup(t, true)
	for _, path := range []string{"/", "/tv.html", "/tv.js", "/tv.css", "/vod.js", "/vod.css", "/common.js", "/settings.html", "/settings.js", "/settings.css", "/guide.html", "/guide.js", "/guide.css", "/vendor/mpegts-1.8.2.min.js", "/vendor/hls-1.7.3.min.js", "/ui.css", "/icons.js"} {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		csp := resp.Header.Get("Content-Security-Policy")
		if resp.StatusCode != 200 || len(body) == 0 || !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "http") {
			t.Fatalf("%s: %d csp=%q", path, resp.StatusCode, csp)
		}
	}
}

func TestLogosThroughServer(t *testing.T) {
	e, p := tvSetup(t)
	code, body := e.do(t, "GET", "/api/v1/tv/channels?section=tv", nil, true)
	if code != 200 || strings.Contains(string(body), p.URL) || strings.Contains(string(body), `"image"`) {
		t.Fatalf("provider URLs must not reach clients: %s", body)
	}
	var list []struct {
		Name string `json:"name"`
		Logo string `json:"logo"`
	}
	json.Unmarshal(body, &list)
	logos := map[string]string{}
	for _, c := range list {
		logos[c.Name] = c.Logo
	}
	if logos["Rai 1"] == "" || logos["Cielo"] == "" || logos["Canale 5"] != "" {
		t.Fatalf("logo URLs: %v", logos)
	}

	// Five clients at once: one fetch through the VPN, then cache.
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(e.srv.URL + logos["Rai 1"])
			if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
				t.Errorf("logo: %v %v", err, resp)
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()
	resp, _ := http.Get(e.srv.URL + logos["Rai 1"])
	resp.Body.Close()
	p.mu.Lock()
	if p.logoHits["/logo/rai1.png"] != 1 {
		t.Errorf("logo fetched %d times from the provider", p.logoHits["/logo/rai1.png"])
	}
	p.mu.Unlock()

	tampered := strings.Replace(logos["Rai 1"], "?s=", "?s=0", 1)
	if resp, _ := http.Get(e.srv.URL + tampered); resp.StatusCode != 403 {
		t.Errorf("tampered signature: %d", resp.StatusCode)
	}
	other := strings.Replace(logos["Rai 1"], "/logo/", "/logo/9", 1)
	if resp, _ := http.Get(e.srv.URL + other); resp.StatusCode != 403 {
		t.Errorf("signature reused for another item: %d", resp.StatusCode)
	}
	for range 2 {
		if resp, _ := http.Get(e.srv.URL + logos["Cielo"]); resp.StatusCode != 404 {
			t.Errorf("svg must be refused: %d", resp.StatusCode)
		}
	}
	p.mu.Lock()
	if p.logoHits["/logo/cielo.svg"] != 1 {
		t.Errorf("refused image fetched %d times (failure not cached)", p.logoHits["/logo/cielo.svg"])
	}
	p.mu.Unlock()
}

func TestOutputToOtherApps(t *testing.T) {
	e, _ := tvSetup(t)
	get := func(path string) (int, string) {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	// Off by default: nothing answers, not even with a made-up key.
	if code, _ := get("/out/0123456789abcdef0123456789abcdef/lineup.json"); code != 404 {
		t.Fatalf("output answers while off: %d", code)
	}
	code, body := e.do(t, "PUT", "/api/v1/settings/output", map[string]bool{"enabled": true}, true)
	var o struct {
		Enabled bool
		Tuners  int
		Links   map[string]string
	}
	if code != 200 || json.Unmarshal(body, &o) != nil || !o.Enabled || o.Links["tuner"] == "" {
		t.Fatalf("enable: %d %s", code, body)
	}
	tuner := strings.TrimPrefix(o.Links["tuner"], e.srv.URL)
	// HDHomeRun, as Plex and Jellyfin read it.
	if code, d := get(tuner + "/discover.json"); code != 200 || !strings.Contains(d, `"LineupURL":"`+o.Links["tuner"]+`/lineup.json"`) || !strings.Contains(d, `"TunerCount":`) {
		t.Fatalf("discover: %d %s", code, d)
	}
	code, lineup := get(tuner + "/lineup.json")
	var ch []struct{ GuideNumber, GuideName, URL string }
	if code != 200 || json.Unmarshal([]byte(lineup), &ch) != nil || len(ch) == 0 || !strings.HasPrefix(ch[0].URL, o.Links["tuner"]+"/channel/") {
		t.Fatalf("lineup: %d %s", code, lineup)
	}
	// M3U and XMLTV: the same channels, the guide with its programmes.
	if code, m3u := get(tuner + "/playlist.m3u"); code != 200 || !strings.HasPrefix(m3u, "#EXTM3U url-tvg=") || !strings.Contains(m3u, ch[0].URL) {
		t.Fatalf("playlist: %d %s", code, m3u)
	}
	if code, epg := get(tuner + "/epg.xml"); code != 200 || !strings.Contains(epg, "<programme") || !strings.Contains(epg, "Film della sera") ||
		!strings.Contains(epg, "<previously-shown></previously-shown>") {
		t.Fatalf("guide: %d %s", code, epg)
	}
	// A channel plays through VodMax.
	resp, err := http.Get(ch[0].URL)
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/mp2t" {
		t.Fatalf("channel: %v %v", err, resp)
	}
	buf := make([]byte, 188)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("channel data: %v", err)
	}
	resp.Body.Close()
	// A new key cuts off the old addresses; off, nothing answers.
	e.do(t, "PUT", "/api/v1/settings/output", map[string]bool{"enabled": true, "new_key": true}, true)
	if code, _ := get(tuner + "/lineup.json"); code != 404 {
		t.Fatalf("old key still works: %d", code)
	}
	e.do(t, "PUT", "/api/v1/settings/output", map[string]bool{"enabled": false}, true)
	_, body = e.do(t, "GET", "/api/v1/settings/output", nil, true)
	json.Unmarshal(body, &o)
	if code, _ := get(strings.TrimPrefix(o.Links["tuner"], e.srv.URL) + "/discover.json"); code != 404 {
		t.Fatalf("output answers when off: %d", code)
	}
	// Only admins see and change it.
	kid := addKids(t, e)
	if code, _ := e.call(t, "GET", "/api/v1/settings/output", nil, kid.device, kid.profile); code != 403 {
		t.Fatalf("kids see the output: %d", code)
	}
}

func TestLiveFailover(t *testing.T) {
	p := newTVProvider()
	e, _ := tvSetupWith(t, p, `{"name":"Rai 1 SD","stream_id":103,"category_id":"1","epg_channel_id":"Rai1.it"}`)
	rai := channels(t, e, "?section=tv")[0]
	if rai.Name != "Rai 1" {
		t.Fatalf("first channel: %+v", rai)
	}
	p.mu.Lock()
	p.broken = map[string]bool{"101.ts": true}
	p.mu.Unlock()
	// The HD version does not open: the SD one stands in.
	_, body := e.do(t, "POST", "/api/v1/live/"+strconv.FormatInt(rai.ID, 10)+"/ticket", map[string]string{"viewer": "salotto-tv-01"}, true)
	var tk struct{ URL string }
	json.Unmarshal(body, &tk)
	resp, err := http.Get(e.srv.URL + tk.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("failover: %v %v", err, resp)
	}
	buf := make([]byte, 188)
	io.ReadFull(resp.Body, buf)
	resp.Body.Close()
	p.mu.Lock()
	opened := strings.Join(p.opened, ",")
	p.mu.Unlock()
	if !strings.Contains(opened, "103.ts") {
		t.Fatalf("opened: %s", opened)
	}
}
