package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/silirim/vodmax/internal/playlist/xtream"
	"github.com/silirim/vodmax/internal/store"
)

// The people index (decision 0036): cast and director of films and
// series, so the assistant finds every title with a person instead of the
// few a model remembers. Detail pages fill it when opened; at night, while
// the assistant is on, VodMax reads the pages it has not seen, slowly.

const (
	peopleNightFrom = 1 // local hours
	peopleNightTo   = 7
	peoplePause     = 3 * time.Second // between pages of a provider
	peopleRetry     = 30 * 24 * time.Hour
)

// peopleText folds the names of a cast ("A, B, C") and a director into
// " a , b , c , d ", the form ItemsWithPerson looks in.
func peopleText(cast, director string) string {
	var names []string
	for _, part := range []string{cast, director} {
		for _, n := range strings.FieldsFunc(part, func(r rune) bool { return r == ',' || r == '/' || r == ';' }) {
			if f := fold(n); f != "" {
				names = append(names, f)
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	return " " + strings.Join(names, " , ") + " "
}

// personKey is the part of a name looked for: the surname (every word
// after the first), since the given name is often written differently.
func personKey(person string) string {
	words := strings.Fields(fold(person))
	if len(words) > 1 {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// titlesWith returns the films and series of the catalog with person.
func (s *Service) titlesWith(ctx context.Context, person string, limit int) []store.CatalogItem {
	items, err := s.st.ItemsWithPerson(ctx, personKey(person), viewerOf(ctx).Kids, limit)
	if err != nil {
		return nil
	}
	s.catalogImages(ctx, items)
	return items
}

// PeopleCoverage: for how many visible titles the people are known.
func (s *Service) PeopleCoverage(ctx context.Context) (known, total int) {
	known, total, _ = s.st.PeopleCoverage(ctx)
	return known, total
}

// SetPeoplePause changes the pause between detail pages (tests).
func (s *Service) SetPeoplePause(d time.Duration) { s.asst.peoplePause = &d }

// FillPeople reads, during the night and while the assistant is on, the
// detail pages of titles whose people are not known. It returns when the
// night is over, the context ends or nothing is left.
func (s *Service) FillPeople(ctx context.Context, log *slog.Logger, night func() bool) int {
	pause := peoplePause
	if s.asst.peoplePause != nil {
		pause = *s.asst.peoplePause
	}
	// After the provider did not answer, the rest of this night is left
	// alone (the loop calls again every 10 minutes).
	s.asst.mu.Lock()
	halted := time.Since(s.asst.peopleHalt) < (peopleNightTo-peopleNightFrom)*time.Hour
	s.asst.mu.Unlock()
	if halted {
		return 0
	}
	done := 0
	for ctx.Err() == nil && night() && s.AssistantOn(ctx) {
		batch, err := s.st.ItemsWithoutPeople(ctx, time.Now().Add(-peopleRetry), 50)
		if err != nil || len(batch) == 0 {
			break
		}
		for _, m := range batch {
			if ctx.Err() != nil || !night() {
				break
			}
			sub, err := s.st.Subscription(ctx, m.SubscriptionID)
			if err != nil {
				continue
			}
			d, err := s.itemDetails(ctx, m)
			switch {
			case err == nil || d.Cast != "" || d.Director != "":
			case errors.Is(err, xtream.ErrUnreadable):
				// A page VodMax cannot read: noted without people, so the
				// night goes on with the next titles; tried again after
				// peopleRetry.
				log.Info("indice delle persone: scheda illeggibile, passo alla prossima", "titolo", m.ID, "err", err)
			default:
				// The provider (or its VPN) did not answer: not "no cast",
				// and no point asking for more pages tonight.
				log.Info("indice delle persone: il provider non risponde, riprovo la prossima notte",
					"abbonamento", m.SubscriptionID, "titolo", m.ID, "err", err)
				s.asst.mu.Lock()
				s.asst.peopleHalt = time.Now()
				s.asst.mu.Unlock()
				return done
			}
			if err := s.st.PutItemPeople(ctx, m.ID, peopleText(d.Cast, d.Director)); err != nil {
				log.Warn("indice delle persone non aggiornato", "err", err)
				return done // the same titles would come back at once
			}
			done++
			if sub.Kind != "local" {
				select {
				case <-ctx.Done():
				case <-time.After(pause):
				}
			}
		}
	}
	if done > 0 {
		known, total := s.PeopleCoverage(ctx)
		log.Info("indice delle persone aggiornato", "schede", done, "noti", known, "titoli", total)
	}
	return done
}

func inPeopleNight(t time.Time) bool {
	h := t.Hour()
	return h >= peopleNightFrom && h < peopleNightTo
}
