package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/silirim/vodmax/internal/provider"
)

// Provider connections are scarce (often one per account), so every
// stream opened towards a provider — live channel, film, episode,
// recording — goes through the same accounting, per subscription:
//   - a viewer (one player) has at most one stream: opening another
//     closes the previous one first (zapping, seeking, next episode);
//   - when all connections are used, a viewer gets ConnectionBusy and may
//     take over the oldest viewer stream ("force");
//   - recordings are never taken over by viewers;
//   - downloads run in the background: they only take a free connection
//     and give it up to anyone who wants to watch, without asking.

type conn struct {
	viewer     string
	recording  bool
	background bool
	cancel     context.CancelCauseFunc
	done       chan struct{}
	once       sync.Once
}

// ErrReplaced ends a stream whose viewer opened another one (zapping,
// a film after a channel). The newer request owns the connection: the
// replaced one must neither count as a broken channel nor reopen.
var ErrReplaced = errors.New("sostituita da una richiesta più recente dello stesso dispositivo")

// replacedBy tells whether a stream context ended because its viewer
// opened something else.
func replacedBy(ctx context.Context) bool { return errors.Is(context.Cause(ctx), ErrReplaced) }

func (c *conn) finish() { c.once.Do(func() { close(c.done) }) }

type connManager struct {
	mu    sync.Mutex
	bySub map[int64][]*conn
}

// ConnectionBusy is returned when every provider connection is in use.
// Recording tells whether a recording holds the connections (it cannot be
// taken over).
type ConnectionBusy struct {
	Max       int
	Recording bool
}

func (e ConnectionBusy) Error() string { return e.localized("it") }

func (e ConnectionBusy) localized(lang string) string {
	if e.Recording {
		return tr(lang, "tutte le connessioni dell'abbonamento (%d) sono occupate da una registrazione in corso", e.Max)
	}
	return tr(lang, "tutte le connessioni dell'abbonamento sono in uso (%d): chiudi un'altra visione o prendi il controllo", e.Max)
}

// maxConnections reads the provider's limit from the account state; 0
// means unknown/unlimited.
func (s *Service) maxConnections(ctx context.Context, subID int64) (int, error) {
	sub, err := s.st.Subscription(ctx, subID)
	if err != nil {
		return 0, err
	}
	var account struct {
		MaxConnections int `json:"max_connections"`
	}
	json.Unmarshal(sub.Account, &account)
	return account.MaxConnections, nil
}

// acquire reserves a provider connection of a subscription for viewer and
// returns a context that is cancelled when the stream must stop, and the
// release function to call when it ends.
func (s *Service) acquire(ctx context.Context, subID int64, viewer string, force, recording bool) (context.Context, func(), error) {
	return s.acquireConn(ctx, subID, viewer, force, recording, false)
}

// acquireBackground reserves a connection only if one is free; it is
// cancelled as soon as anyone else needs it.
func (s *Service) acquireBackground(ctx context.Context, subID int64, key string) (context.Context, func(), error) {
	return s.acquireConn(ctx, subID, key, false, false, true)
}

func (s *Service) acquireConn(ctx context.Context, subID int64, viewer string, force, recording, background bool) (context.Context, func(), error) {
	max, err := s.maxConnections(ctx, subID)
	if err != nil {
		return nil, nil, err
	}
	streamCtx, cancel := context.WithCancelCause(ctx)
	c := &conn{viewer: viewer, recording: recording, background: background, cancel: cancel, done: make(chan struct{})}
	var stop []*conn
	m := &s.conns
	m.mu.Lock()
	if m.bySub == nil {
		m.bySub = map[int64][]*conn{}
	}
	list := m.bySub[subID]
	kept := make([]*conn, 0, len(list)+1)
	for _, other := range list {
		if other.viewer == viewer && !other.recording {
			stop = append(stop, other)
		} else {
			kept = append(kept, other)
		}
	}
	if max > 0 && len(kept) >= max {
		victim := -1
		// A background stream gives way to anything else; it takes nothing.
		for i, other := range kept {
			if other.background && !background {
				victim = i
				break
			}
		}
		for i, other := range kept {
			if victim < 0 && !background && !other.recording && (force || recording) {
				victim = i
				break
			}
		}
		if victim < 0 {
			onlyRecordings := true
			for _, other := range kept {
				onlyRecordings = onlyRecordings && other.recording
			}
			m.mu.Unlock()
			cancel(nil)
			return nil, nil, ConnectionBusy{Max: max, Recording: onlyRecordings}
		}
		stop = append(stop, kept[victim])
		kept = append(kept[:victim], kept[victim+1:]...)
	}
	m.bySub[subID] = append(kept, c)
	m.mu.Unlock()

	// Wait for replaced streams to release their provider connection.
	for _, old := range stop {
		var cause error // nil: taken over by someone else
		if old.viewer == viewer {
			cause = ErrReplaced
		}
		old.cancel(cause)
		select {
		case <-old.done:
		case <-time.After(2 * time.Second):
		}
	}
	release := func() {
		c.cancel(nil)
		m.mu.Lock()
		list := m.bySub[subID]
		for i, other := range list {
			if other == c {
				m.bySub[subID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
		c.finish()
	}
	return streamCtx, release, nil
}

// upstream opens a provider URL through the subscription's VPN, retrying
// briefly when a single-connection account is still releasing the
// previous stream. rangeHeader is forwarded when not empty.
func (s *Service) upstream(ctx context.Context, subID int64, url, rangeHeader string) (*http.Response, error) {
	sub, err := s.st.Subscription(ctx, subID)
	if err != nil {
		return nil, err
	}
	if sub.Kind == "local" {
		return openLocal(url, rangeHeader) // url is the collection's file
	}
	tun, err := s.tunnel(ctx, sub.VPNID)
	if err != nil {
		return nil, fmt.Errorf("VPN: %w", err)
	}
	client := provider.NewHTTPClient(tun, 0)
	var lastStatus int
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 400 * time.Millisecond):
			}
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("User-Agent", "VodMax")
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("provider: %w", provider.Redact(err))
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
			return resp, nil
		}
		lastStatus = resp.StatusCode
		resp.Body.Close()
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable || resp.StatusCode == http.StatusNotFound {
			break
		}
	}
	return nil, &UpstreamError{Status: lastStatus}
}

// UpstreamError means the provider refused the stream.
type UpstreamError struct{ Status int }

func (e *UpstreamError) Error() string { return e.localized("it") }

func (e *UpstreamError) localized(lang string) string {
	return tr(lang, "il provider ha rifiutato il contenuto (HTTP %d)", e.Status)
}

// Stream is an open relay; Close releases the provider connection.
type Stream struct {
	io.ReadCloser
	ContentType string
	Title       string
	// Set for partial content (VOD): the provider's answer to a Range.
	Status        int
	ContentLength int64
	ContentRange  string
	conn          context.Context // the provider connection's, when it has one
}

// replaced tells whether the stream ended because its viewer opened
// another one.
func (st *Stream) replaced() bool { return st.conn != nil && replacedBy(st.conn) }

type releasingBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *releasingBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

var errNotReady = errors.New("non pronto")
