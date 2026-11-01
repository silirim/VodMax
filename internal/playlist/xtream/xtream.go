// Package xtream is a client for the Xtream Codes player API
// (player_api.php), the de facto standard of playlist providers.
package xtream

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

	"github.com/silirim/vodmax/internal/provider"
)

// MaxResponse bounds a single API response. Large providers return 60+ MB
// for get_vod_streams; the bound must stay well above that.
const MaxResponse = 512 << 20

// Client talks to one provider account. HTTP must come from
// provider.NewHTTPClient so that every request uses the internal VPN.
type Client struct {
	base     *url.URL
	username string
	password string
	http     *http.Client
	agent    string
}

// New validates the server address. The password is kept in memory only.
func New(server, username, password string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(server), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("indirizzo del server Xtream non valido (atteso http:// o https://)")
	}
	if username == "" || password == "" {
		return nil, errors.New("utente e password Xtream sono obbligatori")
	}
	u.Path = strings.TrimSuffix(u.Path, "/player_api.php")
	u.RawQuery, u.Fragment = "", ""
	return &Client{base: u, username: username, password: password, http: httpClient, agent: "VodMax"}, nil
}

// Flex accepts JSON strings, numbers, booleans and null as text: providers
// are inconsistent about types even within one response.
type Flex string

func (f *Flex) UnmarshalJSON(b []byte) error {
	switch {
	case string(b) == "null":
		*f = ""
	case len(b) > 0 && b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = Flex(s)
	default:
		*f = Flex(strings.Trim(string(b), " "))
	}
	return nil
}

func (f Flex) String() string { return string(f) }

// Int parses the value, returning 0 when empty or not a number.
func (f Flex) Int() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(string(f)), 10, 64)
	if err != nil {
		if v, ferr := strconv.ParseFloat(strings.TrimSpace(string(f)), 64); ferr == nil {
			return int64(v)
		}
		return 0
	}
	return n
}

// Account is the user_info block of the login response.
type Account struct {
	Status         Flex `json:"status"`
	ExpDate        Flex `json:"exp_date"`
	MaxConnections Flex `json:"max_connections"`
	ActiveCons     Flex `json:"active_cons"`
	IsTrial        Flex `json:"is_trial"`
	Auth           Flex `json:"auth"`
}

// Active reports whether the provider considers the account usable.
func (a Account) Active() bool {
	return a.Auth.Int() == 1 && strings.EqualFold(a.Status.String(), "Active")
}

// Expires returns the expiry time, zero when unlimited or unknown.
func (a Account) Expires() time.Time {
	if n := a.ExpDate.Int(); n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

type Category struct {
	ID       Flex `json:"category_id"`
	Name     Flex `json:"category_name"`
	ParentID Flex `json:"parent_id"`
}

type LiveStream struct {
	Num               Flex `json:"num"`
	Name              Flex `json:"name"`
	StreamID          Flex `json:"stream_id"`
	Icon              Flex `json:"stream_icon"`
	EPGChannelID      Flex `json:"epg_channel_id"`
	CategoryID        Flex `json:"category_id"`
	TVArchive         Flex `json:"tv_archive"`
	TVArchiveDuration Flex `json:"tv_archive_duration"`
	Added             Flex `json:"added"`
}

type VODStream struct {
	Name               Flex `json:"name"`
	StreamID           Flex `json:"stream_id"`
	Icon               Flex `json:"stream_icon"`
	Rating             Flex `json:"rating"`
	CategoryID         Flex `json:"category_id"`
	ContainerExtension Flex `json:"container_extension"`
	Added              Flex `json:"added"`
	TMDB               Flex `json:"tmdb"`
	Year               Flex `json:"year"`
}

type Series struct {
	Name        Flex `json:"name"`
	SeriesID    Flex `json:"series_id"`
	Cover       Flex `json:"cover"`
	Plot        Flex `json:"plot"`
	Genre       Flex `json:"genre"`
	ReleaseDate Flex `json:"releaseDate"`
	Rating      Flex `json:"rating"`
	CategoryID  Flex `json:"category_id"`
	TMDB        Flex `json:"tmdb"`
}

type Episode struct {
	ID                 Flex `json:"id"`
	EpisodeNum         Flex `json:"episode_num"`
	Title              Flex `json:"title"`
	Season             Flex `json:"season"`
	ContainerExtension Flex `json:"container_extension"`
	Info               struct {
		DurationSecs Flex `json:"duration_secs"`
		Plot         Flex `json:"plot"`
		Image        Flex `json:"movie_image"`
	} `json:"info"`
}

type SeriesInfo struct {
	Info     Series `json:"info"`
	Details  Details
	Episodes []Episode
}

// Login checks the credentials and returns the account state.
func (c *Client) Login(ctx context.Context) (Account, error) {
	a, _, err := c.LoginInfo(ctx)
	return a, err
}

// ServerInfo is the server_info block of the login response.
type ServerInfo struct {
	Timezone Flex `json:"timezone"`
	TimeNow  Flex `json:"time_now"` // "2006-01-02 15:04:05" in Timezone
}

// Location is the server's time zone (catch-up times are written in it);
// UTC when unknown.
func (s ServerInfo) Location() *time.Location {
	if loc, err := time.LoadLocation(s.Timezone.String()); err == nil && s.Timezone.String() != "" {
		return loc
	}
	return time.UTC
}

// CatchupPHPURL is the older form of the archive URL
// (streaming/timeshift.php), which some panels use instead.
func (c *Client) CatchupPHPURL(streamID string, start time.Time, duration time.Duration, loc *time.Location) string {
	minutes := max(1, int(duration.Round(time.Minute)/time.Minute))
	u := c.base.JoinPath("streaming", "timeshift.php")
	u.RawQuery = url.Values{"username": {c.username}, "password": {c.password}, "stream": {streamID},
		"start": {start.In(loc).Format("2006-01-02:15-04")}, "duration": {strconv.Itoa(minutes)}}.Encode()
	return u.String()
}

// LoginInfo is Login with the server information.
func (c *Client) LoginInfo(ctx context.Context) (Account, ServerInfo, error) {
	var resp struct {
		UserInfo   Account    `json:"user_info"`
		ServerInfo ServerInfo `json:"server_info"`
	}
	if err := c.get(ctx, "", nil, &resp); err != nil {
		return Account{}, ServerInfo{}, err
	}
	if resp.UserInfo.Auth.Int() != 1 {
		return resp.UserInfo, resp.ServerInfo, errors.New("credenziali Xtream rifiutate dal provider")
	}
	return resp.UserInfo, resp.ServerInfo, nil
}

func (c *Client) LiveCategories(ctx context.Context) ([]Category, error) {
	return getList[Category](ctx, c, "get_live_categories")
}

func (c *Client) LiveStreams(ctx context.Context) ([]LiveStream, error) {
	return getList[LiveStream](ctx, c, "get_live_streams")
}

func (c *Client) VODCategories(ctx context.Context) ([]Category, error) {
	return getList[Category](ctx, c, "get_vod_categories")
}

func (c *Client) VODStreams(ctx context.Context) ([]VODStream, error) {
	return getList[VODStream](ctx, c, "get_vod_streams")
}

func (c *Client) SeriesCategories(ctx context.Context) ([]Category, error) {
	return getList[Category](ctx, c, "get_series_categories")
}

func (c *Client) Series(ctx context.Context) ([]Series, error) {
	return getList[Series](ctx, c, "get_series")
}

// SeriesInfo returns a series with all its episodes, ordered by season.
func (c *Client) SeriesInfo(ctx context.Context, seriesID string) (SeriesInfo, error) {
	var raw struct {
		Info     json.RawMessage            `json:"info"`
		Episodes map[string]json.RawMessage `json:"episodes"`
	}
	if err := c.get(ctx, "get_series_info", url.Values{"series_id": {seriesID}}, &raw); err != nil {
		return SeriesInfo{}, err
	}
	out := SeriesInfo{Details: ParseDetails(raw.Info)}
	json.Unmarshal(raw.Info, &out.Info)
	for season, msg := range raw.Episodes {
		var eps []Episode
		if err := json.Unmarshal(msg, &eps); err != nil {
			return SeriesInfo{}, fmt.Errorf("episodi stagione %s: %w", season, err)
		}
		for i := range eps {
			if eps[i].Season == "" {
				eps[i].Season = Flex(season)
			}
		}
		out.Episodes = append(out.Episodes, eps...)
	}
	return out, nil
}

// Kind of stream for URL building.
type Kind string

const (
	KindLive    Kind = "live"
	KindMovie   Kind = "movie"
	KindEpisode Kind = "series"
)

// StreamURL builds the playback URL. It contains the credentials: keep it
// in memory, never persist or log it.
func (c *Client) StreamURL(kind Kind, streamID, extension string) string {
	if extension == "" {
		extension = map[Kind]string{KindLive: "ts", KindMovie: "mp4", KindEpisode: "mp4"}[kind]
	}
	return c.base.JoinPath(string(kind), c.username, c.password, streamID+"."+extension).String()
}

// XMLTVURL is the provider's full programme guide. It contains the
// credentials: keep it in memory only.
func (c *Client) XMLTVURL() string {
	u := c.base.JoinPath("xmltv.php")
	u.RawQuery = url.Values{"username": {c.username}, "password": {c.password}}.Encode()
	return u.String()
}

// HTTP returns the client used for provider requests (through the VPN).
func (c *Client) HTTP() *http.Client { return c.http }

// CatchupURL builds a timeshift URL for a programme of a live channel.
// CatchupURL is the archive of a live channel from start, for duration;
// loc is the server's time zone (ServerInfo.Location).
func (c *Client) CatchupURL(streamID string, start time.Time, duration time.Duration, loc *time.Location) string {
	minutes := max(1, int(duration.Round(time.Minute)/time.Minute))
	return c.base.JoinPath("timeshift", c.username, c.password, strconv.Itoa(minutes),
		start.In(loc).Format("2006-01-02:15-04"), streamID+".ts").String()
}

// getList reads a list, leaving out the entries that do not decode: one
// malformed title must not stop the whole catalog from updating.
func getList[T any](ctx context.Context, c *Client, action string) ([]T, error) {
	var raw []json.RawMessage
	if err := c.get(ctx, action, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]T, 0, len(raw))
	for _, r := range raw {
		var v T
		if json.Unmarshal(r, &v) == nil {
			out = append(out, v)
		}
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, action string, extra url.Values, out any) error {
	q := url.Values{"username": {c.username}, "password": {c.password}}
	if action != "" {
		q.Set("action", action)
	}
	for k, v := range extra {
		q[k] = v
	}
	u := *c.base
	u.Path += "/player_api.php"
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return provider.Redact(err)
	}
	req.Header.Set("User-Agent", c.agent)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("xtream %s: %w", label(action), provider.Redact(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("xtream %s: risposta HTTP %d", label(action), resp.StatusCode)
	}
	body := &io.LimitedReader{R: resp.Body, N: MaxResponse + 1}
	if err := json.NewDecoder(body).Decode(out); err != nil {
		if body.N <= 0 {
			return fmt.Errorf("xtream %s: risposta oltre %d MiB", label(action), MaxResponse>>20)
		}
		return fmt.Errorf("xtream %s: risposta non valida: %w", label(action), err)
	}
	return nil
}

func label(action string) string {
	if action == "" {
		return "login"
	}
	return action
}
