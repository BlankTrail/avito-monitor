// SPDX-License-Identifier: AGPL-3.0-or-later

package collect

import (
	"context"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// The confirmation sweeper: the fifth supervised goroutine, and the only one
// that spends requests on its own initiative.
//
// A windowed disappearance is a conclusion drawn from something not being on
// page one, recorded at confidence 0.3 for exactly that reason. The item page
// is the only source that can say more. Without this, every removal alert this
// product ever sends stays «предположительно» for ever.

// confirmationsPerHour is the whole budget of this sweeper.
//
// Five, and the spec is explicit that it is a constant and not a setting. The
// reason is that it competes with collection for the same addresses and the
// same rested identities, and collection is the product working while this is
// the product being more certain about something it has already reported. A
// knob here would be a knob whose wrong setting looks like the site blocking
// the install.
const confirmationsPerHour = 5

// Confirmer reads one listing's own page.
//
// The same shape as Passer and for the same reason: this package must not learn
// what a transport is, and the credentials live in the wiring that does.
type Confirmer interface {
	// Confirm fetches the item page. ok is false when the page could not be
	// read at all, which is different from a page that read fine and showed a
	// live listing.
	Confirm(ctx context.Context, c store.Confirmable) (page avito.Listing, ok bool, err error)
}

// Sweeper turns windowed guesses into stated facts, slowly.
type Sweeper struct {
	Store     *store.Store
	Confirmer Confirmer
	Now       func() time.Time
	Log       func(format string, args ...any)

	// PerHour overrides the constant in tests only. Zero uses it.
	PerHour int

	// spent is when this sweeper's last requests went out, oldest first. The
	// rate is enforced here rather than by the loop's interval, because the
	// supervisor's backoff can change how often a turn happens and the cap
	// must not change with it.
	spent []time.Time
}

func (s *Sweeper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sweeper) perHour() int {
	if s.PerHour > 0 {
		return s.PerHour
	}
	return confirmationsPerHour
}

// budget is how many requests this turn may still spend.
func (s *Sweeper) budget() int {
	now := s.now()
	kept := s.spent[:0]
	for _, t := range s.spent {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	s.spent = kept
	return s.perHour() - len(s.spent)
}

// Turn confirms as many as the hour still allows, and returns.
func (s *Sweeper) Turn(ctx context.Context) error {
	budget := s.budget()
	if budget <= 0 {
		// Not an error. The cap being reached is this sweeper working, and
		// reporting it as a failure would drive the supervisor's backoff to
		// five minutes for the ordinary case.
		return nil
	}

	queue, err := s.Store.VanishedNeedingConfirmation(ctx, budget)
	if err != nil {
		return err
	}
	for _, c := range queue {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		at := s.now()
		s.spent = append(s.spent, at)

		page, ok, err := s.Confirmer.Confirm(ctx, c)
		// Recorded whatever happened, because the request is gone either way
		// and a queue that only remembers successes re-spends it on the same
		// listing next hour.
		if markErr := s.Store.MarkConfirmationAttempted(ctx, c.ItemID,
			at.Unix()); markErr != nil {
			return markErr
		}
		if err != nil {
			// Data and not an outage. A page that will not open is an ordinary
			// answer here — the site archives listings, and an archived page
			// is exactly what this sweeper goes looking at. Reporting it as a
			// failed turn would drive the supervisor's backoff, and a backoff
			// buys nothing when the hourly cap is already the rate limit: it
			// would only delay the four requests still owed to other listings.
			s.logf("collect: объявление %d: %v", c.ItemID, err)
			continue
		}
		if err := s.Store.ApplyConfirmation(ctx, c.ItemID, page, ok,
			at.Unix()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sweeper) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}
