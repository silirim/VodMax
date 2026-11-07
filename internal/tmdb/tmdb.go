// Package tmdb reads film and series metadata from The Movie Database
// with the user's own API key (v3 key or v4 read access token).
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBase  = "https://api.themoviedb.org/3"
	ImageBase    = "https://image.tmdb.org/t/p/"
	PosterSize   = "w342"
	BackdropSize = "w1280"
	LogoSize     = "w500"
	ProfileSize  = "w185"
)

// ErrKey means TMDB refused the key.
var ErrKey = errors.New("chiave TMDB non valida")

type Client struct {
	key    string
	base   string
	images string
	http   *http.Client
}

func New(key string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{key: strings.TrimSpace(key), base: DefaultBase, images: ImageBase, http: hc}
}

// WithBase points the client elsewhere (tests): API at base, images at
// base + "/img/".
func (c *Client) WithBase(base string) *Client { c.base, c.images = base, base+"/img/"; return c }

// Title is what VodMax uses of a TMDB film or series, in Italian.
type Title struct {
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	Poster        string   `json:"poster,omitempty"`   // image URL
	Backdrop      string   `json:"backdrop,omitempty"` // image URL
	Genres        []string `json:"genres,omitempty"`
	Released      string   `json:"released,omitempty"`
	Runtime       int      `json:"runtime,omitempty"` // minutes
	Rating        float64  `json:"rating,omitempty"`
	Cast          []string `json:"cast,omitempty"`
	People        []Person `json:"people,omitempty"` // cast with photos, longer than Cast
	Director      string   `json:"director,omitempty"`
	Trailer       string   `json:"trailer,omitempty"` // YouTube id
	Logo          string   `json:"logo,omitempty"`    // title logo image URL (transparent PNG when there is one)
	ImdbID        string   `json:"imdb_id,omitempty"`
}

// Person is a cast member with the photo TMDB has.
type Person struct {
	Name  string `json:"name"`
	Role  string `json:"role,omitempty"`  // character
	Photo string `json:"photo,omitempty"` // image URL
}

// Check verifies the key.
func (c *Client) Check(ctx context.Context) error {
	var out struct{}
	return c.get(ctx, "/configuration", nil, &out)
}

// Movie and TV read a title by TMDB id.
func (c *Client) Movie(ctx context.Context, id string) (Title, error) {
	return c.title(ctx, "movie", id)
}
func (c *Client) TV(ctx context.Context, id string) (Title, error) { return c.title(ctx, "tv", id) }

func (c *Client) title(ctx context.Context, kind, id string) (Title, error) {
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return Title{}, fmt.Errorf("id TMDB non valido %q", id)
	}
	var raw struct {
		Title, Name, OriginalTitle, OriginalName, Overview string
		PosterPath                                         string  `json:"poster_path"`
		BackdropPath                                       string  `json:"backdrop_path"`
		ReleaseDate                                        string  `json:"release_date"`
		FirstAirDate                                       string  `json:"first_air_date"`
		Runtime                                            int     `json:"runtime"`
		EpisodeRunTime                                     []int   `json:"episode_run_time"`
		VoteAverage                                        float64 `json:"vote_average"`
		Genres                                             []struct{ Name string }
		Credits                                            struct {
			Cast []struct {
				Name, Character string
				ProfilePath     string `json:"profile_path"`
			}
			Crew []struct{ Name, Job string }
		}
		CreatedBy []struct{ Name string } `json:"created_by"`
		Videos    struct {
			Results []struct {
				Site, Type, Key string
				Lang            string `json:"iso_639_1"`
			}
		}
		Images struct {
			Logos []struct {
				FilePath string `json:"file_path"`
				Lang     string `json:"iso_639_1"`
			}
		}
		ExternalIDs struct {
			ImdbID string `json:"imdb_id"`
		} `json:"external_ids"`
	}
	q := url.Values{"language": {"it-IT"}, "append_to_response": {"credits,videos,images,external_ids"},
		"include_video_language": {"it,en"}, "include_image_language": {"it,en,null"}}
	if err := c.get(ctx, "/"+kind+"/"+id, q, &raw); err != nil {
		return Title{}, err
	}
	t := Title{Title: first(raw.Title, raw.Name), OriginalTitle: first(raw.OriginalTitle, raw.OriginalName), Overview: raw.Overview,
		Released: first(raw.ReleaseDate, raw.FirstAirDate), Runtime: raw.Runtime, Rating: raw.VoteAverage, ImdbID: raw.ExternalIDs.ImdbID}
	if t.Runtime == 0 && len(raw.EpisodeRunTime) > 0 {
		t.Runtime = raw.EpisodeRunTime[0]
	}
	if raw.PosterPath != "" {
		t.Poster = c.images + PosterSize + raw.PosterPath
	}
	if raw.BackdropPath != "" {
		t.Backdrop = c.images + BackdropSize + raw.BackdropPath
	}
	for _, g := range raw.Genres {
		t.Genres = append(t.Genres, g.Name)
	}
	for i, p := range raw.Credits.Cast {
		if i < 8 {
			t.Cast = append(t.Cast, p.Name)
		}
		if i == 12 {
			break
		}
		person := Person{Name: p.Name, Role: p.Character}
		if p.ProfilePath != "" {
			person.Photo = c.images + ProfileSize + p.ProfilePath
		}
		t.People = append(t.People, person)
	}
	// Italian logo first, then English, then language-neutral; PNG beats
	// SVG because old TV browsers render SVG poorly or not at all.
	best := 0
	for _, l := range raw.Images.Logos {
		score := map[string]int{"it": 6, "en": 4, "": 2}[l.Lang]
		if score == 0 {
			continue
		}
		if strings.HasSuffix(l.FilePath, ".png") {
			score++
		}
		if score > best {
			best, t.Logo = score, c.images+LogoSize+l.FilePath
		}
	}
	for _, p := range raw.Credits.Crew {
		if p.Job == "Director" {
			t.Director = p.Name
			break
		}
	}
	if t.Director == "" && len(raw.CreatedBy) > 0 {
		t.Director = raw.CreatedBy[0].Name
	}
	// An Italian trailer if there is one, else the first.
	for _, v := range raw.Videos.Results {
		if v.Site == "YouTube" && v.Type == "Trailer" && (t.Trailer == "" || v.Lang == "it") {
			t.Trailer = v.Key
			if v.Lang == "it" {
				break
			}
		}
	}
	return t, nil
}

func first(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	// A v4 read access token is a JWT; otherwise a v3 key.
	if strings.HasPrefix(c.key, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+c.key)
	} else {
		q.Set("api_key", c.key)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL may contain the key.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("TMDB: %w", ue.Err)
		}
		return fmt.Errorf("TMDB: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrKey
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("TMDB: titolo non trovato")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("TMDB: risposta HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// Trend is a title of the week's ranking.
type Trend struct {
	ID            string
	Title         string
	OriginalTitle string
	Year          int
}

// Trending is the week's ranking of films ("movie") or series ("tv"),
// two pages (40 titles), most popular first.
func (c *Client) Trending(ctx context.Context, kind string) ([]Trend, error) {
	var out []Trend
	for page := 1; page <= 2; page++ {
		var res struct {
			Results []struct {
				ID            int    `json:"id"`
				Title         string `json:"title"`
				Name          string `json:"name"`
				OriginalTitle string `json:"original_title"`
				OriginalName  string `json:"original_name"`
				ReleaseDate   string `json:"release_date"`
				FirstAirDate  string `json:"first_air_date"`
			} `json:"results"`
		}
		q := url.Values{"language": {"it-IT"}, "page": {strconv.Itoa(page)}}
		if err := c.get(ctx, "/trending/"+kind+"/week", q, &res); err != nil {
			return out, err
		}
		for _, r := range res.Results {
			year, _ := strconv.Atoi(first(r.ReleaseDate, r.FirstAirDate, "0000")[:4])
			out = append(out, Trend{ID: strconv.Itoa(r.ID), Title: first(r.Title, r.Name), OriginalTitle: first(r.OriginalTitle, r.OriginalName), Year: year})
		}
	}
	return out, nil
}
