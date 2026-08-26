// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

// recordingLease is a Lease that answers from a script and records which
// remedies were asked for. It is how "rotation is reachable from exactly one
// class" is proved end to end rather than only in the remedy table.
type recordingLease struct {
	replies  []reply
	n        int
	warm     bool
	rotated  int
	renewed  int
	recycled int
	sessions int
}

type reply struct {
	status int
	body   string
}

func (l *recordingLease) Do(*http.Request) (*http.Response, error) {
	r := l.replies[min(l.n, len(l.replies)-1)]
	l.n++
	return &http.Response{
		StatusCode: r.status,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       bodyOf(r.body),
	}, nil
}

func (l *recordingLease) Session() string { return "sess-" + itoa(l.sessions) }
func (l *recordingLease) Label() string   { return "test:1" }

// Egress is deliberately constant across rotations here: this fake has one
// exit, and a test that wanted several would say so.
func (l *recordingLease) Egress() string { return "test-egress" }

func (l *recordingLease) Profile() Profile { return Profile{} }
func (l *recordingLease) Warm() bool       { return l.warm }
func (l *recordingLease) Release()         {}

func (l *recordingLease) RotateEgress(context.Context) error {
	l.rotated++
	return nil
}

// Recycle is the strongest reset: a new endpoint entirely. The counter is
// separate from renewed on purpose — the two were conflated in the design and a
// soak run showed they are not the same act.
func (l *recordingLease) Recycle(context.Context) error {
	l.recycled++
	l.sessions++
	l.warm = true
	return nil
}

func (l *recordingLease) RenewIdentity(context.Context) error {
	l.renewed++
	l.sessions++
	l.warm = true
	return nil
}

// stubLeaser hands out the one recording lease.
type stubLeaser struct {
	lease *recordingLease
	caps  Caps
}

func (s *stubLeaser) Acquire(context.Context) (Lease, error) { return s.lease, nil }
func (s *stubLeaser) Mode() Mode                             { return ModeDirect }
func (s *stubLeaser) Caps() Caps                             { return s.caps }
func (s *stubLeaser) Close() error                           { return nil }

// newTestFetcher runs on a clock the test moves, so a two-hundred-second
// backoff costs nothing and is still measurable.
func newTestFetcher(t *testing.T, l *recordingLease, caps Caps, now *time.Time) (*Fetcher, *[]time.Duration) {
	t.Helper()
	var slept []time.Duration
	pacer := NewPacer(PacerConfig{
		Floor: 0, Ceiling: 100000, Window: time.Hour,
		Now:        func() time.Time { return *now },
		SleepUntil: func(_ context.Context, tm time.Time) error { *now = tm; return nil },
	})
	f := NewFetcher(&stubLeaser{lease: l, caps: caps}, pacer, FetchConfig{
		Surface: avito.SurfaceMobile,
		Now:     func() time.Time { return *now },
		Sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			*now = now.Add(d)
			return nil
		},
	})
	return f, &slept
}

const okBody = `<html><body>` +
	`<script>window.__initialData__ = "{\"search\":{\"count\":10}}";</script>` +
	`<div data-marker="item-wrapper(7800000001)">` +
	`<div data-marker="titleLabelGrid">Товар</div>` +
	`<div data-marker="priceLabelGrid">1 000 ₽</div>` +
	`<a data-marker="item/link" data-marker-id="7800000001" href="/m/x_7800000001"></a>` +
	`</div></body></html>`

const emptyBody = `<html><body><script>window.__initialData__ = ` +
	`"{\"search\":{\"count\":0,\"emptySearch\":\"Ничего не найдено\"}}";</script></body></html>`

const wallHTML = `<html><body><h1>Возникла проблема с IP-адресом</h1></body></html>`

const captchaHTML = `<html><body>Продолжить для решения gcaptcha4</body></html>`

// TestFetchSucceedsOnAWarmSession is the ordinary path: one attempt, ClassOK.
func TestFetchSucceedsOnAWarmSession(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{200, okBody}}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: true, RotatesEgress: true}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Verdict.Class != ClassOK || res.Attempts != 1 {
		t.Errorf("class=%q attempts=%d, want ok/1", res.Verdict.Class, res.Attempts)
	}
	if len(res.Page.Listings) != 1 {
		t.Errorf("listings = %d, want 1", len(res.Page.Listings))
	}
}

// TestEmptyIsNotAFailure. It is the site's answer, and answering it with a
// remedy would re-pay a twenty-second solve on a quiet query.
func TestEmptyIsNotAFailure(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{200, emptyBody}}}
	f, slept := newTestFetcher(t, l, Caps{SolvesChallenges: true, RotatesEgress: true}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=nothing")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Verdict.Class != ClassEmpty || res.Attempts != 1 {
		t.Errorf("class=%q attempts=%d, want empty/1", res.Verdict.Class, res.Attempts)
	}
	if l.rotated != 0 || l.renewed != 0 || len(*slept) != 0 {
		t.Errorf("an empty answer cost a remedy: rotated=%d renewed=%d slept=%v",
			l.rotated, l.renewed, *slept)
	}
}

// TestEachRefusalGetsItsOwnRemedyAndNoOther.
//
// Rotation destroys a Qrator clearance worth nineteen to twenty-three seconds,
// so exactly one class may reach it. And a 403 must NOT renew on its first
// refusal: measured, eighteen of eighteen cleared by waiting alone, and an arm
// that renewed was no faster than one that could not.
func TestEachRefusalGetsItsOwnRemedyAndNoOther(t *testing.T) {
	cases := []struct {
		name        string
		replies     []reply
		wantRotates int
		wantRenews  int
	}{
		{"throttled", []reply{{429, "<html>Телефоны</html>"}, {200, okBody}}, 1, 0},
		// The measured remedy for a 403 is time. Renewing would throw away a
		// clearance to fix something the identity was not causing.
		{"burned", []reply{{403, "<html>Телефоны</html>"}, {200, okBody}}, 0, 0},
		// A soft block replaces the endpoint. It must not renew, which is the
		// weaker act that measurably did nothing, and must not rotate, which
		// would spend an address on a stuck endpoint.
		{"soft block", []reply{{200, "<html><div>Авито</div></html>"}, {200, okBody}}, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
			l := &recordingLease{warm: true, replies: c.replies}
			f, _ := newTestFetcher(t, l,
				Caps{SolvesChallenges: true, RotatesEgress: true, Addresses: 2}, &now)
			if _, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x"); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if l.rotated != c.wantRotates {
				t.Errorf("rotated %d times, want %d — only a throttle may rotate, "+
					"because rotation throws away a clearance that was working",
					l.rotated, c.wantRotates)
			}
			if l.renewed != c.wantRenews {
				t.Errorf("renewed %d times, want %d", l.renewed, c.wantRenews)
			}
		})
	}
}

// TestChallengeInBlankTrailModeRepeatsOnTheSamePort. The solve is in progress
// on that identity; rotating would throw it away.
func TestChallengeInBlankTrailModeRepeatsOnTheSamePort(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{439, captchaHTML}}}
	f, slept := newTestFetcher(t, l, Caps{SolvesChallenges: true, RotatesEgress: true}, &now)

	_, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	var halt *HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("err = %v, want a *HaltError after the repeats ran out", err)
	}
	if l.rotated != 0 {
		t.Errorf("a challenge rotated the egress %d times; the solve is in "+
			"progress on this identity", l.rotated)
	}
	if l.n != challengeRepeats {
		t.Errorf("made %d attempts, want %d", l.n, challengeRepeats)
	}
	for _, d := range *slept {
		if d != challengeGap {
			t.Errorf("slept %v between challenge attempts, want %v", d, challengeGap)
		}
	}
}

// TestChallengeInDirectModeHaltsImmediately. POST
// /web/N/firewallCaptcha/verify needs an X-Cube WebGL proof of work, so
// retrying cannot succeed and spending the budget on it is worse than stopping.
func TestChallengeInDirectModeHaltsImmediately(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{439, captchaHTML}}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: false}, &now)

	_, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	var halt *HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("err = %v, want a *HaltError", err)
	}
	if l.n != 1 {
		t.Errorf("made %d requests before halting, want 1", l.n)
	}
	if halt.Boundary() == "" {
		t.Error("the halt carries no boundary message; it is the one thing a " +
			"person needs to read at this moment")
	}
}

// TestAHaltedFetcherIssuesNoFurtherRequests is the operational half of
// "loudly". A caller that keeps calling must not keep spending.
func TestAHaltedFetcherIssuesNoFurtherRequests(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{439, captchaHTML}}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: false}, &now)

	if _, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x"); err == nil {
		t.Fatal("expected a halt")
	}
	after := l.n
	// The Fetcher does not remember a halt — the caller does, and increment 3's
	// watch is where halted_reason lives. What this asserts is the cost of ONE
	// halted fetch: exactly one request, not a ladder's worth.
	if after != 1 {
		t.Errorf("one halted fetch cost %d requests, want 1", after)
	}
}

// TestShapeIsNotRetriedAtAll. Repeating a request whose payload we have stopped
// understanding spends the budget on our own bug.
func TestShapeIsNotRetriedAtAll(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	// The Cyrillic is not decoration: Document.Check refuses a body without
	// any, because a body that arrived still compressed has none and would
	// otherwise read downstream as "the query matched nothing". A real page
	// always has some, and a test fixture that does not gets classified
	// mangled — which is the guard working.
	shape := `<html><body><h1>Телефоны</h1><script>window.__initialData__ = ` +
		`"{\"search\":{\"count\":59959}}";</script></body></html>`
	l := &recordingLease{warm: true, replies: []reply{{200, shape}}}
	f, slept := newTestFetcher(t, l, Caps{SolvesChallenges: true, RotatesEgress: true}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Verdict.Class != ClassShape {
		t.Fatalf("class = %q, want %q", res.Verdict.Class, ClassShape)
	}
	if l.n != 1 || len(*slept) != 0 || l.rotated != 0 || l.renewed != 0 {
		t.Errorf("a shape change cost requests=%d sleeps=%d rotate=%d renew=%d; "+
			"it must cost none of them", l.n, len(*slept), l.rotated, l.renewed)
	}
	if len(res.Body) == 0 {
		t.Error("the body was not kept; it is the only thing a fix can be " +
			"written from")
	}
}

// TestColdWarmsRatherThanHalting. The measured direct baseline is a 429
// carrying wall markers on an unwarmed identity. Treating that as a challenge
// would halt mode (b) on request one, permanently.
func TestColdWarmsRatherThanHalting(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: false, replies: []reply{{429, wallHTML}, {200, okBody}}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: false}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	var halt *HaltError
	if errors.As(err, &halt) {
		t.Fatal("an unwarmed identity's first refusal halted the install; that " +
			"is the state the whole warm-jar design exists to leave, not a wall")
	}
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Attempts < 2 {
		t.Errorf("attempts = %d; the cold row must retry once after warming",
			res.Attempts)
	}
}

// TestAThrottleRotatesRatherThanWaits.
//
// The backoff ladder that used to live here climbed 25 s, 60 s, 200 s on a 429,
// on the theory that a 429 is a rate limit that expires. A soak run put
// sixty-six consecutive 429s through it — eleven fetches, about fifteen minutes
// of waiting each — and cleared none. The only two that ever recovered did so
// through a 302, which is how a challenge flow ends rather than how a rate
// limit expires.
//
// So a 429 costs one rotation and no waiting at all.
func TestAThrottleRotatesRatherThanWaits(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{
		{429, "<html>Телефоны</html>"}, {200, okBody},
	}}
	f, slept := newTestFetcher(t, l,
		Caps{SolvesChallenges: true, RotatesEgress: true, Addresses: 3}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Verdict.Class != ClassOK {
		t.Errorf("class = %q, want ok from the new address", res.Verdict.Class)
	}
	if l.rotated != 1 {
		t.Errorf("rotated %d times, want exactly 1", l.rotated)
	}
	if len(*slept) != 0 {
		t.Errorf("slept %v on a 429; waiting is measured not to help, and the "+
			"new address has no history to wait out", *slept)
	}
}

// TestAThrottleWithNowhereToGoHalts.
//
// This is the boundary of running without a solver, and it must be reached
// rather than ground away at. Before this was measured the ladder waited a 429
// out on a single-address install, which cost about fifteen minutes per fetch
// and never once worked.
func TestAThrottleWithNowhereToGoHalts(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{
		{429, "<html>Телефоны</html>"}, {200, okBody},
	}}
	f, slept := newTestFetcher(t, l,
		Caps{SolvesChallenges: false, RotatesEgress: false}, &now)

	_, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	var halt *HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("err = %v, want a *HaltError: there is no address to move to "+
			"and waiting is measured not to clear a 429", err)
	}
	if l.rotated != 0 {
		t.Errorf("rotated %d times with nowhere to move to", l.rotated)
	}
	if len(*slept) != 0 {
		t.Errorf("slept %v before halting; the wait cannot help", *slept)
	}
	if l.n != 1 {
		t.Errorf("made %d requests, want 1 — a halt spends no more budget", l.n)
	}
	// The message must name the remedy the operator actually has.
	b := halt.Boundary()
	for _, want := range []string{"429", "-proxies", "BlankTrail"} {
		if !strings.Contains(b, want) {
			t.Errorf("the boundary message does not mention %q:\n%s", want, b)
		}
	}
}

// TestEveryAddressGetsOneTurnAndThenItHalts. Cycling a pool that has already
// refused only spends the pool.
func TestEveryAddressGetsOneTurnAndThenItHalts(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{429, "<html>Телефоны</html>"}}}
	f, _ := newTestFetcher(t, l,
		Caps{SolvesChallenges: false, RotatesEgress: true, Addresses: 2}, &now)

	_, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	var halt *HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("err = %v, want a *HaltError once every address has refused", err)
	}
	if l.rotated != 2 {
		t.Errorf("rotated %d times with 2 addresses, want 2 — one turn each",
			l.rotated)
	}
}

// bodyOf and itoa keep the recording lease terse.
func bodyOf(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
func itoa(i int) string             { return strconv.Itoa(i) }

// TestTraceRecordsWhatTheFinalVerdictHides.
//
// This is the defect the first live soak run exposed. A fetch that met a soft
// block, renewed its identity and then succeeded reports ClassOK, and the block
// leaves no other mark than a longer elapsed time. A harness measuring how
// often the site pushes back would have written that down as an untroubled
// request — the same silent success this design refuses everywhere else.
func TestTraceRecordsWhatTheFinalVerdictHides(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{
		{200, "<html><div>Авито</div></html>"}, // a soft block
		{200, okBody}, // and then, after the renewal, fine
	}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: true, RotatesEgress: true}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Verdict.Class != ClassOK {
		t.Fatalf("final class = %q, want ok — the remedy worked", res.Verdict.Class)
	}
	if len(res.Trace) != 2 {
		t.Fatalf("Trace has %d steps, want 2: the block and the recovery", len(res.Trace))
	}
	if res.Trace[0].Class != ClassSoftBlock {
		t.Errorf("Trace[0].Class = %q, want %q — the block must survive in the "+
			"record even though the fetch succeeded", res.Trace[0].Class, ClassSoftBlock)
	}
	if res.Trace[0].Remedy != RemedyRecycle {
		t.Errorf("Trace[0].Remedy = %q, want %q", res.Trace[0].Remedy, RemedyRecycle)
	}
	if res.Trace[1].Class != ClassOK {
		t.Errorf("Trace[1].Class = %q, want ok", res.Trace[1].Class)
	}
}

// TestTraceOfAnUntroubledFetchIsOneStep. The common case must stay cheap and
// must not read as though something happened.
func TestTraceOfAnUntroubledFetchIsOneStep(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{200, okBody}}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: true, RotatesEgress: true}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.Trace) != 1 || res.Trace[0].Class != ClassOK ||
		res.Trace[0].Remedy != RemedyNone {
		t.Errorf("Trace = %+v, want exactly one ok step needing no remedy", res.Trace)
	}
}

// TestA403WaitsBeforeItEverRenews is the measured remedy, asserted directly.
//
// The first soak arm was read as "the renewal fixed it" when the ladder had
// renewed AND waited and the arm could not tell them apart. An arm with
// RenewIdentity replaced by a no-op then recovered from four consecutive 403s
// on one unchanged srv_id, twice. Waiting is the remedy; the renewal is a
// fallback for the case the measurement does not cover.
func TestA403WaitsBeforeItEverRenews(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	// Three 403s, then a clean page — within what the measurement observed.
	l := &recordingLease{warm: true, replies: []reply{
		{403, "<html>Телефоны</html>"}, {403, "<html>Телефоны</html>"},
		{403, "<html>Телефоны</html>"}, {200, okBody},
	}}
	f, slept := newTestFetcher(t, l,
		Caps{SolvesChallenges: false, RotatesEgress: true, Addresses: 2}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Verdict.Class != ClassOK {
		t.Errorf("class = %q, want ok — waiting is measured to clear a 403",
			res.Verdict.Class)
	}
	if l.renewed != 0 {
		t.Errorf("renewed %d times inside the measured range; waiting clears a "+
			"403 and renewing discards a clearance that cost 19-23 s", l.renewed)
	}
	if l.rotated != 0 {
		t.Errorf("rotated %d times on a 403; the address was answering, and "+
			"rotation is the 429 remedy", l.rotated)
	}
	if len(*slept) != 3 {
		t.Fatalf("slept %v, want three waits — one per refusal", *slept)
	}
	for i, d := range *slept {
		if d != blockWait {
			t.Errorf("wait %d = %v, want %v", i, d, blockWait)
		}
	}
}

// TestA403RenewsOnlyOnceWaitingHasFailed. Eighteen observations say waiting
// works; they do not say an identity can never be burnt. Past the ground the
// measurement covers, the other remedy is worth a turn.
func TestA403RenewsOnlyOnceWaitingHasFailed(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{403, "<html>Телефоны</html>"}}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: false}, &now)

	if _, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if l.renewed == 0 {
		t.Error("never renewed across six refusals; the fallback exists for " +
			"exactly the case where waiting has stopped working")
	}
	if l.renewed > maxAttempts-burnWaitsBeforeRenewing {
		t.Errorf("renewed %d times; the fallback must not become the habit",
			l.renewed)
	}
}

// TestASoftBlockReplacesTheEndpointRatherThanItsProfile.
//
// The old branch renewed the identity and waited four minutes, up to six times.
// Measured against BlankTrail, that is exactly what does not work: three
// fetches in a row spent twenty-seven minutes each doing it, six renewals
// apiece, and were still handed an empty page — every one of them on port
// 20000, while port 20011 on the SAME exit address answered in under two
// seconds throughout. Renewing rotates the profile; the endpoint survives it.
func TestASoftBlockReplacesTheEndpointRatherThanItsProfile(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{
		{200, "<html><div>Авито</div></html>"}, {200, okBody},
	}}
	f, slept := newTestFetcher(t, l,
		Caps{SolvesChallenges: true, RotatesEgress: true, Addresses: 2}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Verdict.Class != ClassOK {
		t.Errorf("class = %q, want ok from the replaced endpoint", res.Verdict.Class)
	}
	if l.recycled != 1 {
		t.Errorf("recycled %d times, want 1", l.recycled)
	}
	if l.renewed != 0 {
		t.Errorf("renewed %d times; renewing leaves the endpoint in place and "+
			"was measured to do nothing for this class", l.renewed)
	}
	if l.rotated != 0 {
		t.Errorf("rotated %d times; a stuck endpoint is not a refused address, "+
			"and spending one on it treats them as the same thing", l.rotated)
	}
	if len(*slept) != 1 || (*slept)[0] != recycleSettle {
		t.Errorf("slept %v, want one %v settle — the four-minute wait was the "+
			"old branch's mistake, not its remedy", *slept, recycleSettle)
	}
}

// TestASoftBlockGivesUpAfterTwoRecycles. If replacing the endpoint twice did
// not help, the endpoint is not what is wrong, and a third costs minutes to
// re-learn what the second already showed.
func TestASoftBlockGivesUpAfterTwoRecycles(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{200, "<html><div>Авито</div></html>"}}}
	f, slept := newTestFetcher(t, l, Caps{SolvesChallenges: true}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Verdict.Class != ClassSoftBlock {
		t.Errorf("class = %q, want the soft block reported back", res.Verdict.Class)
	}
	if l.recycled != maxRecycles {
		t.Errorf("recycled %d times, want %d", l.recycled, maxRecycles)
	}
	if len(res.Body) == 0 {
		t.Error("no body kept; this is the class whose meaning is least " +
			"certain, and the one occurrence that mattered left nothing to read")
	}
	total := time.Duration(0)
	for _, d := range *slept {
		total += d
	}
	if total > time.Minute {
		t.Errorf("spent %v on a soft block; the old branch spent twenty-seven "+
			"minutes and that is the defect this replaced", total)
	}
}

// TestASuccessfulFetchStillReportsHowBigThePageWas.
//
// Body is dropped on success on purpose — keeping every body would be an
// archive nobody asked for — so passes.bytes read it off len(Body) and recorded
// zero for every pass that worked. The column exists to say what a pass cost,
// which makes zero the one value it must never report for a page that arrived.
func TestASuccessfulFetchStillReportsHowBigThePageWas(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{200, okBody}}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: true, RotatesEgress: true}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.Body) != 0 {
		t.Errorf("a successful fetch kept %d bytes of body", len(res.Body))
	}
	if res.Bytes != len(okBody) {
		t.Errorf("Bytes = %d, want %d", res.Bytes, len(okBody))
	}
}

// TestTheByteCountIsTheLastReplyAndNotTheSum. A fetch that was refused once and
// retried read two pages; the pass it writes is about the one it kept.
func TestTheByteCountIsTheLastReplyAndNotTheSum(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{{403, emptyBody}, {200, okBody}}}
	f, _ := newTestFetcher(t, l, Caps{SolvesChallenges: true, RotatesEgress: true}, &now)

	res, err := f.Fetch(context.Background(), "https://m.avito.ru/all?q=x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Bytes != len(okBody) {
		t.Errorf("Bytes = %d, want the %d of the reply it kept", res.Bytes, len(okBody))
	}
}

// fixtureBody reads one of avito's captured pages.
//
// Across the package boundary rather than copied in. These are two and a half
// megabytes of real Avito, and a second copy would be a second thing to keep in
// step — the shell-follow is exactly the behaviour that must be driven by the
// real shell and not by a hand-written approximation of one. A hand-written
// approximation is in fact what failed first here: it tripped the extractor's
// «no Cyrillic» guard and then its state-block selection, and each failure was
// a property of the stub rather than of the code under test.
func fixtureBody(t *testing.T, name string) string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "avito", "testdata", name+".html.gz"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(raw)
}

func newDesktopFetcher(l *recordingLease, now *time.Time) *Fetcher {
	pacer := NewPacer(PacerConfig{
		Floor: 0, Ceiling: 100000, Window: time.Hour,
		Now:        func() time.Time { return *now },
		SleepUntil: func(_ context.Context, tm time.Time) error { *now = tm; return nil },
	})
	return NewFetcher(&stubLeaser{lease: l, caps: Caps{
		SolvesChallenges: true, RotatesEgress: true,
	}}, pacer, FetchConfig{
		Surface: avito.SurfaceDesktop,
		Now:     func() time.Time { return *now },
		Sleep:   func(context.Context, time.Duration) error { return nil },
	})
}

// TestTheDesktopShellIsFollowedToItsCanonicalPage.
//
// Page one of the desktop search is a shell carrying a canonical URL and no
// listings — the spec says desktop costs two requests, and this is the second.
// Without it a desktop watch collects zero, which is what a live run measured
// on 25 August 2026 and what sent the default back to mobile.
func TestTheDesktopShellIsFollowedToItsCanonicalPage(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{
		{200, fixtureBody(t, "desktop-spa-shell")},
		{200, fixtureBody(t, "desktop-canonical-p1")},
	}}
	res, err := newDesktopFetcher(l, &now).Fetch(context.Background(),
		"https://www.avito.ru/moskva/telefony?q=iphone+15")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if l.n != 2 {
		t.Errorf("%d requests, want the shell and then the canonical page", l.n)
	}
	if res.Page.Kind != avito.EnvelopeCatalog {
		t.Errorf("Kind = %q; the shell was returned as the answer", res.Page.Kind)
	}
	if len(res.Page.Listings) != 50 {
		t.Errorf("listings = %d, want the canonical page's 50", len(res.Page.Listings))
	}
	if res.Verdict.Class != ClassOK {
		t.Errorf("class = %q", res.Verdict.Class)
	}
}

// TestAShellThatLeadsToAnotherShellIsNotFollowedForever.
//
// One follow and no more. A redirect loop would spend a whole address's budget
// on one page, and the site is under no obligation not to serve one.
func TestAShellThatLeadsToAnotherShellIsNotFollowedForever(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	shell := fixtureBody(t, "desktop-spa-shell")
	l := &recordingLease{warm: true, replies: []reply{
		{200, shell}, {200, shell}, {200, shell},
	}}
	res, err := newDesktopFetcher(l, &now).Fetch(context.Background(),
		"https://www.avito.ru/moskva/telefony?q=iphone+15")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if l.n != 2 {
		t.Errorf("%d requests, want the shell and one follow", l.n)
	}
	// And the shell comes back as what it is rather than dressed up as an
	// answer: a redirect envelope with no listings is a fact worth seeing.
	if res.Page.Kind != avito.EnvelopeRedirect {
		t.Errorf("Kind = %q", res.Page.Kind)
	}
}

// TestTheFollowIsPacedLikeAnyOtherRequest.
//
// It is a second request to the same site from the same address, and a page
// that quietly cost two requests against a budget counted in one is how an
// address gets burned by arithmetic nobody did.
func TestTheFollowIsPacedLikeAnyOtherRequest(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	l := &recordingLease{warm: true, replies: []reply{
		{200, fixtureBody(t, "desktop-spa-shell")},
		{200, fixtureBody(t, "desktop-canonical-p1")},
	}}
	var waits int
	pacer := NewPacer(PacerConfig{
		Floor: time.Second, Ceiling: 100000, Window: time.Hour,
		Now: func() time.Time { return now },
		SleepUntil: func(_ context.Context, tm time.Time) error {
			waits++
			now = tm
			return nil
		},
	})
	f := NewFetcher(&stubLeaser{lease: l, caps: Caps{SolvesChallenges: true}},
		pacer, FetchConfig{
			Surface: avito.SurfaceDesktop,
			Now:     func() time.Time { return now },
			Sleep:   func(context.Context, time.Duration) error { return nil },
		})
	res, err := f.Fetch(context.Background(),
		"https://www.avito.ru/moskva/telefony?q=iphone+15")
	if err != nil {
		t.Fatal(err)
	}
	// Two turns of the loop, and the loop paces at its head — so a follow that
	// shows up as a second attempt is a follow that went through the pacer.
	if res.Attempts != 2 {
		t.Errorf("attempts = %d, want the shell and the follow", res.Attempts)
	}
	// One wait for two requests, not two: the gap is BETWEEN them, and the
	// first needed none on a warm session. Zero would mean the follow slipped
	// past the pacer entirely.
	if waits != 1 {
		t.Errorf("the pacer slept %d times between two requests, want 1", waits)
	}
}

// TestATwoRequestPassReportsBothItsRequests.
//
// passes.bytes exists to say what a pass cost, and a desktop pass costs the
// shell as well as the page it points at. Reporting only the second is the same
// under-reporting this column already had once, when Body was dropped on
// success and every clean pass recorded zero.
func TestATwoRequestPassReportsBothItsRequests(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	shell := fixtureBody(t, "desktop-spa-shell")
	canonical := fixtureBody(t, "desktop-canonical-p1")
	l := &recordingLease{warm: true, replies: []reply{
		{200, shell}, {200, canonical},
	}}
	res, err := newDesktopFetcher(l, &now).Fetch(context.Background(),
		"https://www.avito.ru/moskva/telefony?q=iphone+15")
	if err != nil {
		t.Fatal(err)
	}
	if want := len(shell) + len(canonical); res.Bytes != want {
		t.Errorf("Bytes = %d, want %d — the shell went uncounted", res.Bytes, want)
	}
}
