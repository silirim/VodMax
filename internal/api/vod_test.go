package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func findItem(t *testing.T, e *env, kind, q string) int64 {
	t.Helper()
	_, body := e.do(t, "GET", "/api/v1/catalog/"+kind+"?q="+q, nil, true)
	var page struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &page) != nil || len(page.Items) == 0 {
		t.Fatalf("%s %q not found: %s", kind, q, body)
	}
	return page.Items[0].ID
}

func TestFilmDirectPlaybackAndProgress(t *testing.T) {
	e, p := tvSetup(t)
	film := findItem(t, e, "movie", "1408")
	id := strconv.FormatInt(film, 10)

	code, body := e.do(t, "POST", "/api/v1/vod/movie/"+id+"/session", map[string]string{"viewer": "salotto-pc-01"}, true)
	var sess struct {
		Mode, URL string
		Resume    *struct{ Position int }
	}
	if code != 200 || json.Unmarshal(body, &sess) != nil || sess.Mode != "direct" || sess.Resume != nil {
		t.Fatalf("session: %d %s", code, body)
	}
	// The browser asks for ranges: they reach the provider through the server.
	req, _ := http.NewRequest("GET", e.srv.URL+sess.URL, nil)
	req.Header.Set("Range", "bytes=100-199")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != "bytes 100-199/1048576" || len(got) != 100 || got[0] != 100 {
		t.Fatalf("range: %d %q %d", resp.StatusCode, resp.Header.Get("Content-Range"), len(got))
	}
	// The server reads the provider file in chunks through one connection.
	if !strings.Contains(strings.Join(p.ranges, ","), ".mp4 bytes=0-") {
		t.Fatalf("provider requests: %v", p.ranges)
	}

	// Progress: saved, shown in "continue", then completed.
	if code, body := e.do(t, "PUT", "/api/v1/progress/movie/"+id, map[string]int{"position": 1800, "duration": 6000}, true); code != 200 {
		t.Fatalf("progress: %d %s", code, body)
	}
	_, body = e.do(t, "GET", "/api/v1/continue", nil, true)
	if !strings.Contains(string(body), `"title":"1408"`) || !strings.Contains(string(body), `"position":1800`) {
		t.Fatalf("continue: %s", body)
	}
	_, body = e.do(t, "POST", "/api/v1/vod/movie/"+id+"/session", map[string]string{"viewer": "salotto-pc-01"}, true)
	if json.Unmarshal(body, &sess); sess.Resume == nil || sess.Resume.Position != 1800 {
		t.Fatalf("resume offered: %s", body)
	}
	e.do(t, "PUT", "/api/v1/progress/movie/"+id, map[string]int{"position": 5800, "duration": 6000}, true)
	if _, body = e.do(t, "GET", "/api/v1/continue", nil, true); strings.Contains(string(body), "1408") {
		t.Fatalf("finished film still in continue: %s", body)
	}
	if code, _ := e.do(t, "PUT", "/api/v1/progress/movie/"+id, map[string]int{"position": 9999, "duration": 100}, true); code != 400 {
		t.Fatal("impossible position accepted")
	}
}

func TestSeriesEpisodesAndContinue(t *testing.T) {
	e, _ := tvSetup(t)
	series := findItem(t, e, "series", "twin")
	code, body := e.do(t, "GET", "/api/v1/series/"+strconv.FormatInt(series, 10)+"/episodes", nil, true)
	var eps []struct {
		ID     int64  `json:"id"`
		Season int    `json:"season"`
		Number int    `json:"number"`
		Title  string `json:"title"`
		Plot   string `json:"plot"`
		Thumb  string `json:"thumb"`
	}
	if code != 200 || json.Unmarshal(body, &eps) != nil || len(eps) != 3 || eps[0].Title != "Pilot" || eps[2].Season != 2 {
		t.Fatalf("episodes: %d %s", code, body)
	}
	// The picture of an episode, through the server; not the provider's URL.
	if eps[0].Plot == "" || !strings.HasPrefix(eps[0].Thumb, "/img/episode/") || eps[1].Thumb != "" || strings.Contains(string(body), "/logo/") {
		t.Fatalf("episode picture: %s", body)
	}
	if resp, err := http.Get(e.srv.URL + eps[0].Thumb); err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("thumb: %v %v", err, resp)
	}
	if resp, _ := http.Get(e.srv.URL + strings.Replace(eps[0].Thumb, "?s=", "?s=0", 1)); resp.StatusCode == 200 {
		t.Fatal("thumb with a wrong signature served")
	}
	ep := strconv.FormatInt(eps[1].ID, 10)
	e.do(t, "PUT", "/api/v1/progress/episode/"+ep, map[string]int{"position": 600, "duration": 2800}, true)
	_, body = e.do(t, "GET", "/api/v1/continue", nil, true)
	if !strings.Contains(string(body), `"series_title":"Twin Peaks"`) || !strings.Contains(string(body), `"number":2`) {
		t.Fatalf("continue with episode: %s", body)
	}
	// Finished: the next one is proposed, also across seasons.
	e.do(t, "PUT", "/api/v1/progress/episode/"+ep, map[string]int{"position": 2790, "duration": 2800}, true)
	_, body = e.do(t, "GET", "/api/v1/continue", nil, true)
	if !strings.Contains(string(body), `"next":true`) || !strings.Contains(string(body), `"season":2`) || strings.Contains(string(body), "Traces") {
		t.Fatalf("next episode: %s", body)
	}
	e.do(t, "PUT", "/api/v1/progress/episode/"+ep, map[string]int{"position": 600, "duration": 2800}, true)
	code, body = e.do(t, "POST", "/api/v1/vod/episode/"+ep+"/session", map[string]string{"viewer": "salotto-pc-01"}, true)
	if code != 200 || !strings.Contains(string(body), "S1E2 Traces to Nowhere") || !strings.Contains(string(body), `"position":600`) {
		t.Fatalf("episode session: %d %s", code, body)
	}
	// The next episode, also across seasons; the last has none.
	if !strings.Contains(string(body), `"next":{"id":`+strconv.FormatInt(eps[2].ID, 10)) || strings.Contains(string(body), `"markers"`) {
		t.Fatalf("next in session: %s", body)
	}
	_, body = e.do(t, "POST", "/api/v1/vod/episode/"+strconv.FormatInt(eps[2].ID, 10)+"/session", map[string]string{"viewer": "salotto-pc-01"}, true)
	if strings.Contains(string(body), `"next"`) {
		t.Fatalf("next after the last: %s", body)
	}
	// Opening and credits, learned on one episode, come with the others.
	sid := "/api/v1/series/" + strconv.FormatInt(series, 10) + "/markers"
	if code, _ := e.do(t, "PUT", sid, map[string]int{"intro_start": 50, "intro_end": 40}, true); code != 400 {
		t.Fatal("intro ending before it starts accepted")
	}
	e.do(t, "PUT", sid, map[string]int{"intro_start": 12, "intro_end": 95}, true)
	if code, _ := e.do(t, "PUT", sid, map[string]int{"credits": 70}, true); code != 204 {
		t.Fatal("credits refused")
	}
	_, body = e.do(t, "POST", "/api/v1/vod/episode/"+strconv.FormatInt(eps[0].ID, 10)+"/session", map[string]string{"viewer": "salotto-pc-01"}, true)
	if !strings.Contains(string(body), `"markers":{"intro_start":12,"intro_end":95,"credits":70}`) {
		t.Fatalf("markers in session: %s", body)
	}
}

func TestRecordingWatchWhileRecordingAndDelete(t *testing.T) {
	e, p := tvSetup(t)
	tv := channels(t, e, "?section=tv")
	rai := tv[0]
	now := time.Now().Unix()
	code, body := e.do(t, "POST", "/api/v1/recordings",
		map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 3, "title": "Prova"}, true)
	if code != 201 {
		t.Fatalf("schedule: %d %s", code, body)
	}
	var rec struct{ ID int64 }
	json.Unmarshal(body, &rec)
	if code, _ := e.do(t, "POST", "/api/v1/recordings",
		map[string]any{"channel_id": rai.ID, "start": now + 1, "stop": now + 2}, true); code != 400 {
		t.Fatal("overlapping recording accepted")
	}

	time.Sleep(700 * time.Millisecond)
	// Watching the channel being recorded uses the recording, not a second
	// provider connection.
	live, err := openStream(t, e, ticket(t, e, rai.ID, "salotto-tv-01"))
	if err != nil || live.StatusCode != 200 {
		t.Fatalf("watch while recording: %v %v", err, live)
	}
	readSome(t, live)
	live.Body.Close()
	// Another channel: the only connection is held by the recording.
	busy, _ := openStream(t, e, ticket(t, e, tv[1].ID, "salotto-tv-01")+"?force=1")
	body, _ = io.ReadAll(busy.Body)
	busy.Body.Close()
	if busy.StatusCode != http.StatusLocked || !strings.Contains(string(body), "registrazione") {
		t.Fatalf("recording must not be taken over: %d %s", busy.StatusCode, body)
	}

	time.Sleep(3 * time.Second)
	_, body = e.do(t, "GET", "/api/v1/recordings", nil, true)
	var list []struct {
		ID     int64
		Status string
		Bytes  int64
	}
	json.Unmarshal(body, &list)
	if len(list) != 1 || list[0].Status != "completed" || list[0].Bytes < 188*10 {
		t.Fatalf("recordings: %s", body)
	}
	_, body = e.do(t, "POST", "/api/v1/recordings/"+strconv.FormatInt(rec.ID, 10)+"/ticket", nil, true)
	var tk struct{ URL string }
	json.Unmarshal(body, &tk)
	req, _ := http.NewRequest("GET", e.srv.URL+tk.URL, nil)
	req.Header.Set("Range", "bytes=0-187")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || len(got) != 188 || got[0] != 0x47 {
		t.Fatalf("recording playback: %d %d", resp.StatusCode, len(got))
	}
	p.mu.Lock()
	opened := strings.Join(p.opened, ",")
	p.mu.Unlock()
	if strings.Count(opened, "101.ts") != 1 {
		t.Fatalf("channel opened at the provider: %s (expected once, by the recording)", opened)
	}

	files, _ := os.ReadDir(e.recordings)
	if len(files) != 1 {
		t.Fatalf("recording files: %v", files)
	}
	if code, _ := e.do(t, "DELETE", "/api/v1/recordings/"+strconv.FormatInt(rec.ID, 10), nil, true); code != 204 {
		t.Fatal("delete")
	}
	if files, _ := os.ReadDir(e.recordings); len(files) != 0 {
		t.Fatalf("file not deleted: %v", files)
	}
}

func TestOneProgressPerFilmAcrossVersions(t *testing.T) {
	e, _ := tvSetup(t)
	_, body := e.do(t, "GET", "/api/v1/items/"+strconv.FormatInt(findItem(t, e, "movie", "1408"), 10)+"/variants", nil, true)
	var vars []struct{ ID int64 }
	if json.Unmarshal(body, &vars) != nil || len(vars) != 2 {
		t.Fatalf("variants: %s", body)
	}
	a, b := strconv.FormatInt(vars[0].ID, 10), strconv.FormatInt(vars[1].ID, 10)
	e.do(t, "PUT", "/api/v1/progress/movie/"+a, map[string]int{"position": 600, "duration": 6000}, true)
	// The other version resumes from there.
	if _, body := e.do(t, "GET", "/api/v1/progress/movie/"+b, nil, true); !strings.Contains(string(body), `"position":600`) {
		t.Fatalf("resume on the other version: %s", body)
	}
	e.do(t, "PUT", "/api/v1/progress/movie/"+b, map[string]int{"position": 900, "duration": 6000}, true)
	_, body = e.do(t, "GET", "/api/v1/continue", nil, true)
	if strings.Count(string(body), `"title":"1408"`) != 1 || !strings.Contains(string(body), `"position":900`) {
		t.Fatalf("continue: %s", body)
	}
	if _, body := e.do(t, "GET", "/api/v1/progress/movie/"+a, nil, true); !strings.Contains(string(body), `"position":900`) {
		t.Fatalf("first version: %s", body)
	}
	// Even when an older progress of the opened version is still there
	// (saved before this rule), the most recent one wins.
	time.Sleep(1100 * time.Millisecond)
	e.do(t, "PUT", "/api/v1/progress/movie/"+a, map[string]int{"position": 1500, "duration": 6000}, true)
	if _, body := e.do(t, "GET", "/api/v1/progress/movie/"+b, nil, true); !strings.Contains(string(body), `"position":1500`) {
		t.Fatalf("most recent: %s", body)
	}
}

func TestBrowseByRatingAndCategory(t *testing.T) {
	e, _ := tvSetup(t)
	titles := func(q string) string {
		t.Helper()
		_, body := e.do(t, "GET", "/api/v1/catalog/movie?"+q, nil, true)
		var page struct {
			Items []struct {
				Title string `json:"display_title"`
			}
		}
		json.Unmarshal(body, &page)
		var out []string
		for _, i := range page.Items {
			out = append(out, i.Title)
		}
		return strings.Join(out, ",")
	}
	if got := titles("sort=rating"); got != "Psycho,1408" {
		t.Fatalf("by rating: %s", got)
	}
	if got := titles("min_rating=7"); got != "Psycho" {
		t.Fatalf("min rating: %s", got)
	}
	// Paging by rating keeps the order.
	_, body := e.do(t, "GET", "/api/v1/catalog/movie?sort=rating&limit=1", nil, true)
	var page struct{ Next string }
	json.Unmarshal(body, &page)
	if got := titles("sort=rating&limit=1&cursor=" + page.Next); got != "1408" {
		t.Fatalf("second page: %s", got)
	}
	// 1408's 4K version is the only one in Thriller: the title is there.
	if got := titles("category=11"); got != "1408" {
		t.Fatalf("thriller: %s", got)
	}
	if got := titles("category=10"); got != "1408,Psycho" {
		t.Fatalf("horror: %s", got)
	}
	_, body = e.do(t, "GET", "/api/v1/browse/movie/categories", nil, true)
	if string(bytes.TrimSpace(body)) != `[{"id":"10","name":"Horror","titles":2},{"id":"11","name":"Thriller","titles":1}]` {
		t.Fatalf("categories: %s", body)
	}
}

// A title whose category is excluded leaves "continue watching" (its
// progress is kept and comes back with the category).
func TestContinueFollowsSelection(t *testing.T) {
	e, _ := tvSetup(t)
	film := strconv.FormatInt(findItem(t, e, "movie", "1408"), 10)
	e.do(t, "PUT", "/api/v1/progress/movie/"+film, map[string]int{"position": 600, "duration": 6000}, true)
	series := findItem(t, e, "series", "twin")
	_, body := e.do(t, "GET", "/api/v1/series/"+strconv.FormatInt(series, 10)+"/episodes", nil, true)
	var eps []struct{ ID int64 }
	json.Unmarshal(body, &eps)
	e.do(t, "PUT", "/api/v1/progress/episode/"+strconv.FormatInt(eps[0].ID, 10), map[string]int{"position": 600, "duration": 2800}, true)
	cont := func() string { _, b := e.do(t, "GET", "/api/v1/continue", nil, true); return string(b) }
	if c := cont(); !strings.Contains(c, `"title":"1408"`) || !strings.Contains(c, "Twin Peaks") {
		t.Fatalf("before: %s", c)
	}
	for _, cat := range []string{"movie/10", "movie/11", "series/20"} {
		if code, b := e.do(t, "PUT", "/api/v1/subscriptions/1/categories/"+cat, map[string]any{"enabled": false}, true); code != 204 {
			t.Fatalf("exclude %s: %d %s", cat, code, b)
		}
	}
	if c := cont(); strings.Contains(c, "1408") || strings.Contains(c, "Twin Peaks") {
		t.Fatalf("excluded titles still in continue: %s", c)
	}
	for _, cat := range []string{"movie/10", "movie/11", "series/20"} {
		e.do(t, "PUT", "/api/v1/subscriptions/1/categories/"+cat, map[string]any{"enabled": nil}, true)
	}
	if c := cont(); !strings.Contains(c, `"position":600`) || !strings.Contains(c, "Twin Peaks") {
		t.Fatalf("back with the category: %s", c)
	}
}

func TestMyList(t *testing.T) {
	e, _ := tvSetup(t)
	film := findItem(t, e, "movie", "1408")
	series := findItem(t, e, "series", "twin")
	list := func() string {
		_, body := e.do(t, "GET", "/api/v1/mylist", nil, true)
		return string(body)
	}
	if l := list(); l != "[]\n" && l != "[]" {
		t.Fatalf("empty list: %s", l)
	}
	for _, id := range []int64{film, series} {
		if code, body := e.do(t, "PUT", "/api/v1/mylist/"+strconv.FormatInt(id, 10), map[string]bool{"on": true}, true); code != 200 {
			t.Fatalf("add: %d %s", code, body)
		}
	}
	// The last added first; one entry per title (1408 has two versions).
	var items []struct {
		ID    int64  `json:"id"`
		Title string `json:"display_title"`
	}
	json.Unmarshal([]byte(list()), &items)
	if len(items) != 2 || items[0].ID != series || items[1].ID != film {
		t.Fatalf("list: %s", list())
	}
	if _, body := e.do(t, "GET", "/api/v1/mylist/"+strconv.FormatInt(film, 10), nil, true); !strings.Contains(string(body), `"on":true`) {
		t.Fatalf("in list: %s", body)
	}
	// Channels have their favourites, not the list.
	ch := channels(t, e, "?section=tv")[0]
	if code, _ := e.do(t, "PUT", "/api/v1/mylist/"+strconv.FormatInt(ch.ID, 10), map[string]bool{"on": true}, true); code != 400 {
		t.Fatal("channel accepted in the list")
	}
	// Each profile has its own list.
	kid := addKids(t, e)
	if code, body := e.call(t, "GET", "/api/v1/mylist", nil, kid.device, kid.profile); code != 200 || strings.Contains(string(body), "1408") {
		t.Fatalf("kids list: %d %s", code, body)
	}
	e.do(t, "PUT", "/api/v1/mylist/"+strconv.FormatInt(film, 10), map[string]bool{"on": false}, true)
	if l := list(); strings.Contains(l, "1408") || !strings.Contains(l, "Twin Peaks") {
		t.Fatalf("after removing: %s", l)
	}
}

// A provider that keeps the connection open and sends nothing: the
// recording reconnects instead of waiting until the stop time.
func TestRecordingReconnectsWhenStalled(t *testing.T) {
	e, p := tvSetup(t)
	e.svc.SetRecordStall(300 * time.Millisecond)
	p.mu.Lock()
	p.stalls = map[string]bool{"101.ts": true}
	p.mu.Unlock()
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	if code, body := e.do(t, "POST", "/api/v1/recordings",
		map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 5, "title": "Ferma"}, true); code != 201 {
		t.Fatalf("schedule: %d %s", code, body)
	}
	time.Sleep(6 * time.Second) // stall after 0.3 s, reconnection 3 s later
	p.mu.Lock()
	opened := strings.Count(strings.Join(p.opened, ","), "101.ts")
	p.mu.Unlock()
	_, body := e.do(t, "GET", "/api/v1/recordings", nil, true)
	var list []struct {
		Status string
		Bytes  int64
		Error  string
	}
	json.Unmarshal(body, &list)
	if opened < 2 || len(list) != 1 || list[0].Bytes <= 188*20 || !strings.Contains(list[0].Error, "smesso di mandare dati") {
		t.Fatalf("stalled stream: opened %d times, %s", opened, body)
	}
}

// A restart (an update) during a recording: the recording carries on in
// the same file once the server is back, if the programme is still on.
func TestRecordingCarriesOnAfterRestart(t *testing.T) {
	e, _ := tvSetup(t)
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	if code, body := e.do(t, "POST", "/api/v1/recordings",
		map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 6, "title": "Partita"}, true); code != 201 {
		t.Fatalf("schedule: %d %s", code, body)
	}
	time.Sleep(2 * time.Second)
	e.stop()
	files, _ := os.ReadDir(e.recordings)
	if len(files) != 1 {
		t.Fatalf("files before restart: %v", files)
	}
	first, _ := files[0].Info()
	time.Sleep(time.Second)
	e.start()
	time.Sleep(5 * time.Second)
	_, body := e.do(t, "GET", "/api/v1/recordings", nil, true)
	var list []struct {
		Status string
		Bytes  int64
		Error  string
	}
	json.Unmarshal(body, &list)
	if len(list) != 1 || list[0].Status != "completed" || list[0].Bytes <= first.Size() || !strings.Contains(list[0].Error, "riavvio") {
		t.Fatalf("after restart (first part %d bytes): %s", first.Size(), body)
	}
	if files, _ := os.ReadDir(e.recordings); len(files) != 1 {
		t.Fatalf("files after restart: %v", files)
	}
}

// A recording that never got data leaves no empty file behind.
func TestFailedRecordingLeavesNoFile(t *testing.T) {
	e, p := tvSetup(t)
	p.mu.Lock()
	p.broken = map[string]bool{"101.ts": true}
	p.mu.Unlock()
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 2, "title": "Vuota"}, true)
	time.Sleep(3 * time.Second)
	_, body := e.do(t, "GET", "/api/v1/recordings", nil, true)
	if !strings.Contains(string(body), `"status":"failed"`) {
		t.Fatalf("recording: %s", body)
	}
	if files, _ := os.ReadDir(e.recordings); len(files) != 0 {
		t.Fatalf("files left: %v", files)
	}
}

// A stream that stops once and comes back: the recording ends normally at
// its stop time but still says it has a hole.
func TestRecordingKeepsTheNoteOfAGap(t *testing.T) {
	e, p := tvSetup(t)
	e.svc.SetRecordStall(300 * time.Millisecond)
	p.mu.Lock()
	p.stalls = map[string]bool{"101.ts": true}
	p.mu.Unlock()
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 6, "title": "Buco"}, true)
	time.Sleep(time.Second)
	p.mu.Lock()
	p.stalls = nil // healthy from the reconnection on
	p.mu.Unlock()
	time.Sleep(6 * time.Second)
	_, body := e.do(t, "GET", "/api/v1/recordings", nil, true)
	if !strings.Contains(string(body), `"status":"completed"`) || !strings.Contains(string(body), "smesso di mandare dati") {
		t.Fatalf("recording with a gap: %s", body)
	}
}
