// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/repost"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

func aRepostPair(t *testing.T, s *Server, title string, state string) {
	t.Helper()
	db := s.Store.DB()
	for i, id := range []int64{100, 200} {
		if _, err := db.Exec(`
			INSERT INTO listings (item_id, thread_id, url_path, title,
			                      first_seen_at, last_seen_at, state_at, state)
			VALUES (?, ?, '/m/x', ?, ?, ?, 1000, 'active')`,
			id, id, title, 1000+i*1000, 1500+i*1000); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := s.Store.SaveRepostLink(context.Background(), tx, store.RepostLinkRow{
		SuccessorID: 200, PredecessorID: 100, CreatedAt: 3000,
		Basis: "strong", Score: 4, State: state, DecidedBy: "auto",
		Candidates: 1, GapSec: 1500,
		Evidence: []repost.Term{
			{Name: "Заголовок", Present: true, Detail: "совпадает дословно"},
			{Name: "Фотография", Present: false, Detail: "фотографию прочитать не удалось"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestTheScreenIsReachableAndTabbed.
func TestTheRepostsScreenIsReachableAndTabbed(t *testing.T) {
	s := newServer(t)
	if rec := get(t, s, "/reposts"); rec.Code != http.StatusOK {
		t.Fatalf("GET /reposts = %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(get(t, s, "/").Body.String(), `href="/reposts"`) {
		t.Error("Перепубликации is not in the header navigation")
	}
}

// TestTheScreenShowsWhyTermByTermIncludingTheAbsences.
//
// «Описание не совпало» and «описания не было» are different answers, and the
// second is the more common one: description and image are on twenty rows in
// fifty, measured.
func TestTheScreenShowsWhyTermByTermIncludingTheAbsences(t *testing.T) {
	s := newServer(t)
	aRepostPair(t, s, "iPhone 15, 128 ГБ", "linked")

	body := get(t, s, "/reposts").Body.String()
	if !strings.Contains(body, "совпадает дословно") {
		t.Error("a term that held is not shown")
	}
	if !strings.Contains(body, "фотографию прочитать не удалось") {
		t.Error("a term that was absent is not shown")
	}
	if !strings.Contains(body, "по заголовку и точному совпадению") {
		t.Errorf("the basis is not in words: %s", body)
	}
}

// TestAProposalIsVisiblyNotALink, because a proposal withheld nothing.
func TestAProposalIsVisiblyNotALink(t *testing.T) {
	s := newServer(t)
	aRepostPair(t, s, "iPhone 15, 128 ГБ", "proposed")

	body := get(t, s, "/reposts").Body.String()
	if !strings.Contains(body, "связывать не стали") {
		t.Errorf("a proposal reads as a link: %s", body)
	}
	if strings.Contains(body, "Связано: это одно объявление") {
		t.Error("a proposal was shown as linked")
	}
}

// TestTheUndoPinsSplitsAndReplays.
//
// All three, asserted separately. A pin without the split is a correction that
// did not take; a split without the pin is one that comes back on the next
// pass; and either without the replay leaves a person never told about a
// listing they asked to be told about.
func TestTheUndoPinsSplitsAndReplays(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	aRepostPair(t, s, "iPhone 15, 128 ГБ", "linked")
	if err := s.Store.RecomputeThread(ctx, 200); err != nil {
		t.Fatal(err)
	}

	var replayed int64
	s.ReplayAppearance = func(_ context.Context, itemID int64) (int, error) {
		replayed = itemID
		return 1, nil
	}

	rec := post(t, s, "/reposts/reject",
		url.Values{"successor": {"200"}, "predecessor": {"100"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}

	// Split.
	var thread int64
	if err := s.Store.DB().QueryRow(
		`SELECT thread_id FROM listings WHERE item_id = 200`).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	if thread != 200 {
		t.Errorf("the thread was not split: %d", thread)
	}
	// Replayed.
	if replayed != 200 {
		t.Errorf("the withheld alert was not replayed (item %d)", replayed)
	}
	// Pinned, and the pin is what makes it permanent.
	tx, err := s.Store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	pinned, err := s.Store.IsPinned(ctx, tx, 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !pinned {
		t.Error("the pair was not pinned")
	}
}

// TestTheUndoSaysWhatItDid, rather than redirecting to a page that looks
// unchanged apart from a row having quietly moved.
func TestTheUndoSaysWhatItDid(t *testing.T) {
	s := newServer(t)
	aRepostPair(t, s, "iPhone 15, 128 ГБ", "linked")
	s.ReplayAppearance = func(context.Context, int64) (int, error) { return 1, nil }

	rec := post(t, s, "/reposts/reject",
		url.Values{"successor": {"200"}, "predecessor": {"100"}})
	loc := rec.Header().Get("Location")
	body := get(t, s, loc).Body.String()
	if !strings.Contains(body, "Связь снята") {
		t.Errorf("the screen does not say what happened: %s", body)
	}
	if !strings.Contains(body, "поставлено в очередь") {
		t.Error("the screen does not say the message was queued")
	}
	if !strings.Contains(body, "Вы сказали, что это разные объявления") {
		t.Error("the row does not show as the person's own decision")
	}
}

// TestAFailedReplayDoesNotUndoTheCorrection.
//
// The pin and the split already happened and are right. Rolling them back
// because a message could not be queued would refuse a correction the person
// asked for, over the part of it that matters least.
func TestAFailedReplayDoesNotUndoTheCorrection(t *testing.T) {
	s := newServer(t)
	aRepostPair(t, s, "iPhone 15, 128 ГБ", "linked")
	s.ReplayAppearance = func(context.Context, int64) (int, error) {
		return 0, context.DeadlineExceeded
	}

	rec := post(t, s, "/reposts/reject",
		url.Values{"successor": {"200"}, "predecessor": {"100"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d", rec.Code)
	}
	body := get(t, s, rec.Header().Get("Location")).Body.String()
	if !strings.Contains(body, "не удалось") {
		t.Error("a failed replay was not mentioned")
	}
	if !strings.Contains(body, "Связь снята") {
		t.Error("the correction itself was not reported as done")
	}
}

// TestATitleIsEscaped. Titles on this site are seller-written.
func TestARepostTitleIsEscaped(t *testing.T) {
	s := newServer(t)
	aRepostPair(t, s, `<script>alert(1)</script>`, "linked")

	body := get(t, s, "/reposts").Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("a seller-written title reached the page as markup")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("the title is not on the page: %s", body)
	}
}

// TestAnEmptyScreenSaysWhyItMightBeEmpty.
//
// On the mobile surface it will always be: that surface identifies no seller,
// and without one nothing can be linked. A person who never sees a row deserves
// to know whether that is the market or the transport.
func TestAnEmptyScreenSaysWhyItMightBeEmpty(t *testing.T) {
	body := get(t, newServer(t), "/reposts").Body.String()
	if !strings.Contains(body, "не отдаёт продавца") {
		t.Errorf("an empty screen explains nothing: %s", body)
	}
}
