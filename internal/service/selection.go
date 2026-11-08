package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/silirim/vodmax/internal/catalog"
	"github.com/silirim/vodmax/internal/store"
)

// loadSelection reads rules, categories and items for computing a
// selection. extra replaces the rules of one subscription (for previews).
func (s *Service) loadSelection(ctx context.Context, subID int64, extra *catalog.Rules) (map[int64]catalog.Rules, []store.SelectionCategory, []store.SelectionItem, error) {
	raw, err := s.st.AllRules(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	rules := make(map[int64]catalog.Rules, len(raw))
	for id, r := range raw {
		if rules[id], err = catalog.ParseRules(r); err != nil {
			return nil, nil, nil, err
		}
	}
	if extra != nil {
		rules[subID] = *extra
	}
	if err := s.withHome(ctx, rules); err != nil {
		return nil, nil, nil, err
	}
	subs, err := s.st.Subscriptions(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	localRules(rules, subs)
	cats, err := s.st.SelectionCategories(ctx, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	items, err := s.st.SelectionItems(ctx)
	return rules, cats, items, err
}

// ApplySelection recomputes what is shown and stores the rows that changed.
// Variants are grouped across subscriptions, so it always runs on the
// whole catalog.
func (s *Service) ApplySelection(ctx context.Context) (catalog.Stats, error) {
	return s.applySelection(ctx, 0)
}

// applySelection is ApplySelection returning the statistics of subscription
// subID (as SubscriptionStats), or of the whole catalog with 0: a sync
// reads the catalog once instead of twice.
func (s *Service) applySelection(ctx context.Context, subID int64) (catalog.Stats, error) {
	s.selMu.Lock()
	defer s.selMu.Unlock()
	rules, cats, items, err := s.loadSelection(ctx, 0, nil)
	if err != nil {
		return nil, err
	}
	before := make([]store.SelectionItem, len(items))
	copy(before, items)
	stats := catalog.Select(rules, cats, items)
	var changed, relabelled []store.SelectionItem
	for i := range items {
		switch a, b := before[i], items[i]; {
		case a.Visible != b.Visible || a.Primary != b.Primary:
			changed = append(changed, b)
		case a.VariantCount != b.VariantCount || a.Section != b.Section || a.Adult != b.Adult:
			relabelled = append(relabelled, b)
		}
	}
	if subID != 0 {
		stats = subscriptionStats(items, subID)
	}
	return stats, s.st.WriteSelection(ctx, changed, relabelled)
}

// SubscriptionStats returns the selection statistics of one subscription.
func (s *Service) SubscriptionStats(ctx context.Context, subID int64, rules *catalog.Rules) (catalog.Stats, error) {
	all, cats, items, err := s.loadSelection(ctx, subID, rules)
	if err != nil {
		return nil, err
	}
	catalog.Select(all, cats, items)
	return subscriptionStats(items, subID), nil
}

// subscriptionStats counts only subID's items, with grouping already
// decided across subscriptions.
func subscriptionStats(items []store.SelectionItem, subID int64) catalog.Stats {
	stats := catalog.Stats{}
	for _, it := range items {
		if it.SubscriptionID != subID {
			continue
		}
		st := stats[it.Kind]
		if st == nil {
			st = &catalog.KindStats{Sections: map[string]int{}}
			stats[it.Kind] = st
		}
		st.Available++
		if it.Visible {
			st.Visible++
		}
		if it.Primary {
			st.Cards++
			if it.Section != "" {
				st.Sections[it.Section]++
			}
		}
	}
	return stats
}

// Rules returns the selection rules of a subscription, with the home
// country chosen at the first start when the subscription has none.
func (s *Service) Rules(ctx context.Context, subID int64) (catalog.Rules, error) {
	raw, err := s.st.Rules(ctx, subID)
	if err != nil {
		return catalog.Rules{}, err
	}
	r, err := catalog.ParseRules(raw)
	if err != nil {
		return r, err
	}
	if r.Home == "" {
		r.Home, err = s.HomeCountry(ctx)
	}
	return r, err
}

const homeCountryKey = "home_country"

// HomeCountry is the country chosen at the first start (decision 0043),
// "" until then. A subscription's own choice in Selection wins over it.
func (s *Service) HomeCountry(ctx context.Context) (string, error) {
	var code string
	err := s.st.Setting(ctx, homeCountryKey, &code)
	return code, err
}

// SetHomeCountry stores the country chosen at the first start and applies
// it to the subscriptions that have not chosen one of their own.
func (s *Service) SetHomeCountry(ctx context.Context, code string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !catalog.IsHomeCountry(code) {
		return invalid("paese non valido")
	}
	if err := s.st.SetSetting(ctx, homeCountryKey, code); err != nil {
		return err
	}
	_, err := s.ApplySelection(ctx)
	return err
}

// withHome gives the server's home country to rules that have none.
func (s *Service) withHome(ctx context.Context, rules map[int64]catalog.Rules) error {
	home, err := s.HomeCountry(ctx)
	if err != nil || home == "" {
		return err
	}
	for id, r := range rules {
		if r.Home == "" {
			r.Home = home
			rules[id] = r
		}
	}
	return nil
}

// SetRules validates, stores and applies new rules.
func (s *Service) SetRules(ctx context.Context, subID int64, r catalog.Rules) (catalog.Stats, error) {
	if err := r.Validate(); err != nil {
		return nil, invalid("%v", err)
	}
	raw, _ := json.Marshal(r)
	if err := s.st.SetRules(ctx, subID, raw); err != nil {
		return nil, err
	}
	return s.applySelection(ctx, subID)
}

// PreviewRules computes the effect of rules without saving them.
func (s *Service) PreviewRules(ctx context.Context, subID int64, r catalog.Rules) (catalog.Stats, error) {
	if err := r.Validate(); err != nil {
		return nil, invalid("%v", err)
	}
	if _, err := s.st.Subscription(ctx, subID); err != nil {
		return nil, err
	}
	return s.SubscriptionStats(ctx, subID, &r)
}

// CategoryView is a category as shown in the selection editor.
type CategoryView struct {
	store.SelectionCategory
	Effective catalog.Effective `json:"effective"`
}

// SelectionCategories lists the categories of a subscription with their
// effective state under the saved rules.
func (s *Service) SelectionCategories(ctx context.Context, subID int64, kind string) ([]CategoryView, error) {
	r, err := s.Rules(ctx, subID)
	if err != nil {
		return nil, err
	}
	cats, err := s.st.SelectionCategories(ctx, subID)
	if err != nil {
		return nil, err
	}
	out := []CategoryView{}
	for _, c := range cats {
		if kind != "" && c.Kind != kind {
			continue
		}
		out = append(out, CategoryView{SelectionCategory: c, Effective: catalog.EffectiveCategory(c, r)})
	}
	return out, nil
}

// SetCategoryOverride stores a manual correction and applies it.
func (s *Service) SetCategoryOverride(ctx context.Context, subID int64, kind, sourceID string, o store.CategoryOverride) error {
	if o.Section != nil {
		switch catalog.Section(*o.Section) {
		case catalog.SectionTV, catalog.SectionSport, catalog.SectionRadio:
		default:
			return invalid("sezione non valida: usa tv, sport o radio")
		}
	}
	if _, err := s.st.Subscription(ctx, subID); err != nil {
		return err
	}
	if err := s.st.SetCategoryOverride(ctx, subID, kind, sourceID, o); err != nil {
		return err
	}
	_, err := s.ApplySelection(ctx)
	return err
}

// SetItemHidden hides or restores a single title or channel.
func (s *Service) SetItemHidden(ctx context.Context, itemID int64, hidden bool) error {
	if err := s.st.SetItemHidden(ctx, itemID, hidden); err != nil {
		return err
	}
	_, err := s.ApplySelection(ctx)
	return err
}

func (s *Service) Variants(ctx context.Context, itemID int64) ([]store.CatalogItem, error) {
	if err := s.allowItem(ctx, itemID); err != nil {
		return nil, err
	}
	list, err := s.st.Variants(ctx, itemID)
	s.catalogImages(ctx, list)
	return list, err
}
