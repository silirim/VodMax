package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/silirim/vodmax/internal/playlist/stalker"
	"github.com/silirim/vodmax/internal/store"
)

// FromStalker reads the genres and live channels of a Stalker portal. The
// cmd of each channel, needed to ask its link, is sealed in the locator.
func FromStalker(ctx context.Context, c *stalker.Client, seal func(plain []byte, owner string) []byte, subID int64) (*Snapshot, error) {
	if err := c.Login(ctx); err != nil {
		return nil, err
	}
	genres, err := c.Genres(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := c.Channels(ctx)
	if err != nil {
		return nil, err
	}
	// A box plays one channel at a time.
	snap := &Snapshot{Kinds: []string{"live"}, Account: json.RawMessage(`{"status":"Active","active":true,"max_connections":1}`)}
	for _, g := range genres {
		if id := g.ID.String(); id != "" && id != "*" {
			snap.Categories = append(snap.Categories, newCategory("live", id, g.Title.String()))
		}
	}
	infos := categoryInfos(snap.Categories)
	seen := map[string]bool{}
	for _, ch := range channels {
		id := ch.ID.String()
		name := ch.Name.String()
		if id == "" || seen[id] || ch.Cmd == "" || IsSeparator(name) {
			snap.Skipped++
			continue
		}
		seen[id] = true
		it := store.Item{Kind: "live", SourceID: id, Title: name, DisplayTitle: ChannelTitle(name),
			CategoryID: ch.GenreID.String(), Image: ch.Logo.String(), EPGID: ch.XMLTVID.String(), Number: int(ch.Number.Int())}
		finish(&it, infos["live\x00"+it.CategoryID])
		it.Locator = seal([]byte(ch.Cmd.String()), fmt.Sprintf("locator:%d:live:%s", subID, id))
		snap.Items = append(snap.Items, it)
	}
	return snap, nil
}
