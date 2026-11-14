package xtream

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Details describes a film or a series as the provider publishes it
// (get_vod_info, the "info" block of get_series_info). Panels differ in
// field types, so the block is read loosely.
type Details struct {
	Plot          string   `json:"plot,omitempty"`
	Cast          string   `json:"cast,omitempty"`
	Director      string   `json:"director,omitempty"`
	Genre         string   `json:"genre,omitempty"`
	Released      string   `json:"released,omitempty"`
	Country       string   `json:"country,omitempty"`
	OriginalTitle string   `json:"original_title,omitempty"`
	Trailer       string   `json:"trailer,omitempty"` // YouTube video id
	Poster        string   `json:"poster,omitempty"`
	Backdrops     []string `json:"backdrops,omitempty"`
	Rating        float64  `json:"rating,omitempty"`
	DurationSecs  int      `json:"duration_secs,omitempty"`
	TMDB          string   `json:"tmdb,omitempty"`
}

// VODInfo returns the details of a film.
func (c *Client) VODInfo(ctx context.Context, vodID string) (Details, error) {
	var raw struct {
		Info json.RawMessage `json:"info"`
	}
	if err := c.get(ctx, "get_vod_info", url.Values{"vod_id": {vodID}}, &raw); err != nil {
		return Details{}, err
	}
	return ParseDetails(raw.Info), nil
}

// ParseDetails reads an info block; anything unexpected is left empty.
func ParseDetails(raw json.RawMessage) Details {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return Details{}
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			switch v := m[k].(type) {
			case string:
				if s := strings.TrimSpace(v); s != "" && s != "null" && s != "N/A" {
					return s
				}
			case float64:
				if v != 0 {
					return strconv.FormatFloat(v, 'f', -1, 64)
				}
			}
		}
		return ""
	}
	d := Details{
		Plot: str("plot", "description"), Cast: str("cast", "actors"), Director: str("director"), Genre: str("genre"),
		Released: str("releasedate", "releaseDate", "release_date"), Country: str("country"), OriginalTitle: str("o_name", "original_name"),
		Trailer: youTubeID(str("youtube_trailer", "trailer")), Poster: str("movie_image", "cover_big", "cover"), TMDB: str("tmdb_id", "tmdb"),
	}
	d.Rating, _ = strconv.ParseFloat(str("rating"), 64)
	// duration_secs is in seconds, episode_run_time (series) in minutes;
	// panels fill the one they lack with "" or 0.
	if n, err := strconv.Atoi(str("duration_secs")); err == nil && n > 0 {
		d.DurationSecs = n
	} else if n, err := strconv.Atoi(str("episode_run_time")); err == nil && n > 0 {
		d.DurationSecs = n * 60
	}
	switch v := m["backdrop_path"].(type) {
	case string:
		if v != "" {
			d.Backdrops = []string{v}
		}
	case []any:
		for _, b := range v {
			if s, ok := b.(string); ok && s != "" {
				d.Backdrops = append(d.Backdrops, s)
			}
		}
	}
	return d
}

// youTubeID accepts a bare id or a YouTube URL.
func youTubeID(s string) string {
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		if v := u.Query().Get("v"); v != "" {
			s = v
		} else {
			s = strings.TrimPrefix(u.Path, "/")
		}
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return ""
		}
	}
	if len(s) < 6 || len(s) > 20 {
		return ""
	}
	return s
}
