// SPDX-License-Identifier: AGPL-3.0-or-later

package collect

import (
	"context"
	"errors"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// stubConfirmer answers from a script and counts the requests it was asked for.
type stubConfirmer struct {
	pages map[int64]avito.Listing
	fail  map[int64]error
	dead  map[int64]bool // the page could not be read at all
	asked []int64
}

func (c *stubConfirmer) Confirm(_ context.Context, x store.Confirmable) (avito.Listing, bool, error) {
	c.asked = append(c.asked, x.ItemID)
	if err := c.fail[x.ItemID]; err != nil {
		return avito.Listing{}, false, err
	}
	if c.dead[x.ItemID] {
		return avito.Listing{}, false, nil
	}
	return c.pages[x.ItemID], true, nil
}

func vanished(t *testing.T, s *store.Store, itemID, at int64) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO listings (item_id, thread_id, url_path, title,
		                      first_seen_at, last_seen_at, state, state_at,
		                      state_source, state_confidence)
		VALUES (?, ?, '/m/x', 'объявление', 1000, ?, 'vanished', ?, 'search', 0.3)`,
		itemID, itemID, at, at); err != nil {
		t.Fatal(err)
	}
}

func stateOf(t *testing.T, s *store.Store, itemID int64) (string, string, float64) {
	t.Helper()
	var state, source string
	var conf float64
	if err := s.DB().QueryRow(`
		SELECT state, state_source, state_confidence FROM listings
		WHERE item_id = ?`, itemID).Scan(&state, &source, &conf); err != nil {
		t.Fatal(err)
	}
	return state, source, conf
}

func sweeper(s *store.Store, c Confirmer, now int64) *Sweeper {
	return &Sweeper{Store: s, Confirmer: c, Now: at(now), PerHour: 5}
}

// TestAnItemPageTurnsAGuessIntoAStatedFact.
//
// A windowed disappearance is a conclusion drawn from something not being on
// page one, recorded at 0.3 for that reason. The item page is the only source
// that can say more, and only it may write «closed».
func TestAnItemPageTurnsAGuessIntoAStatedFact(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	vanished(t, s, 100, 1000)

	c := &stubConfirmer{pages: map[int64]avito.Listing{
		100: {ItemID: 100, ClosedReason: "Объявление снято с публикации"},
	}}
	if err := sweeper(s, c, 2000).Turn(ctx); err != nil {
		t.Fatal(err)
	}

	state, source, conf := stateOf(t, s, 100)
	if state != "closed" || source != "item-page" || conf != 1.0 {
		t.Errorf("state = %q/%q/%v, want closed/item-page/1", state, source, conf)
	}
	changes, err := s.Changes(ctx, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Kind != track.KindListingClosed {
		t.Fatalf("changes = %+v", changes)
	}
	// The second message says the guess became a fact, and the confidence is
	// what makes it say so.
	if changes[0].Confidence != 1.0 {
		t.Errorf("confidence = %v", changes[0].Confidence)
	}
	// The site's own words, verbatim and uninterpreted.
	if changes[0].Was != "Объявление снято с публикации" {
		t.Errorf("the site's words were not kept: %q", changes[0].Was)
	}
}

// TestAListingThatIsStillThereWasNeverGone.
//
// The windowed guess was wrong, and a correction that only changed a database
// row would leave somebody holding a «предположительно пропало» that is now
// false.
func TestAListingThatIsStillThereWasNeverGone(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	vanished(t, s, 100, 1000)

	c := &stubConfirmer{pages: map[int64]avito.Listing{
		100: {ItemID: 100}, // read fine, no closure
	}}
	if err := sweeper(s, c, 2000).Turn(ctx); err != nil {
		t.Fatal(err)
	}
	state, source, conf := stateOf(t, s, 100)
	if state != "active" || source != "item-page" || conf != 1.0 {
		t.Errorf("state = %q/%q/%v, want active/item-page/1", state, source, conf)
	}
	changes, err := s.Changes(ctx, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Kind != track.KindListingReturned {
		t.Errorf("changes = %+v", changes)
	}
}

// TestTheHourlyCapIsSpentAndNotExceeded.
//
// It competes with collection for the same addresses and the same rested
// identities, and collection is the product working while this is the product
// being more certain about something it already reported.
func TestTheHourlyCapIsSpentAndNotExceeded(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for i := int64(0); i < 20; i++ {
		vanished(t, s, 100+i, 1000+i)
	}
	c := &stubConfirmer{pages: map[int64]avito.Listing{}}
	sw := sweeper(s, c, 2000)

	if err := sw.Turn(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.asked) != 5 {
		t.Fatalf("first turn spent %d requests, want 5", len(c.asked))
	}
	// A second turn in the same hour spends nothing, and is not an error: the
	// cap being reached is this sweeper working.
	if err := sw.Turn(ctx); err != nil {
		t.Errorf("a turn at the cap reported failure: %v", err)
	}
	if len(c.asked) != 5 {
		t.Errorf("a second turn in the same hour spent %d more", len(c.asked)-5)
	}
	// An hour later, five more.
	sw.Now = at(2000 + 3601)
	if err := sw.Turn(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.asked) != 10 {
		t.Errorf("after an hour the sweeper had spent %d in total", len(c.asked))
	}
}

// TestThePageThatWouldNotOpenIsNotRetriedFirstForEver.
//
// «Oldest first» with no record of attempts puts the same unreadable listing at
// the head of the queue every hour, and the other four requests are all this
// sweeper ever gets to spend.
func TestThePageThatWouldNotOpenIsNotRetriedFirstForEver(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	vanished(t, s, 100, 1000) // oldest, and broken
	vanished(t, s, 200, 1100)
	vanished(t, s, 300, 1200)

	c := &stubConfirmer{
		pages: map[int64]avito.Listing{},
		fail:  map[int64]error{100: errors.New("не открылась")},
	}
	sw := &Sweeper{Store: s, Confirmer: c, Now: at(2000), PerHour: 1}
	if err := sw.Turn(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.asked) != 1 || c.asked[0] != 100 {
		t.Fatalf("first turn asked %v", c.asked)
	}

	sw.Now = at(2000 + 3601)
	if err := sw.Turn(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.asked) != 2 {
		t.Fatalf("asked %v", c.asked)
	}
	if c.asked[1] == 100 {
		t.Error("the unreadable listing was retried ahead of the others")
	}
}

// TestAPageThatCouldNotBeReadChangesNothing.
//
// Not read is not «still there». Treating an unreadable page as evidence of
// life would revive every listing on the day the item surface changed.
func TestAPageThatCouldNotBeReadChangesNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	vanished(t, s, 100, 1000)

	c := &stubConfirmer{dead: map[int64]bool{100: true}}
	if err := sweeper(s, c, 2000).Turn(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _, conf := stateOf(t, s, 100); state != "vanished" || conf != 0.3 {
		t.Errorf("state = %q at %v; an unreadable page changed the verdict",
			state, conf)
	}
	changes, err := s.Changes(ctx, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("an unreadable page produced %d changes", len(changes))
	}
}

// TestAListingSeenAgainByAPassIsNotOverwritten.
//
// Between the queue read and the write a pass may have seen it. A pass is a
// stronger reading than a queue entry taken minutes ago, and overwriting it
// would replace something observed with something remembered.
func TestAListingSeenAgainByAPassIsNotOverwritten(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	vanished(t, s, 100, 1000)
	if _, err := s.DB().Exec(
		`UPDATE listings SET state = 'active' WHERE item_id = 100`); err != nil {
		t.Fatal(err)
	}

	page := avito.Listing{ItemID: 100, ClosedReason: "снято"}
	if err := s.ApplyConfirmation(ctx, 100, page, true, 2000); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := stateOf(t, s, 100); state != "active" {
		t.Errorf("state = %q; a stale confirmation overwrote a pass", state)
	}
}

// TestOnlyAListingWithAPathIsQueued, because there is nothing to open otherwise.
func TestOnlyAListingWithAPathIsQueued(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.DB().Exec(`
		INSERT INTO listings (item_id, thread_id, url_path, title,
		                      first_seen_at, last_seen_at, state, state_at)
		VALUES (700, 700, '', 'нечего открывать', 1000, 1000, 'vanished', 1000)`); err != nil {
		t.Fatal(err)
	}
	got, err := s.VanishedNeedingConfirmation(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a listing with no path was queued: %+v", got)
	}
}
