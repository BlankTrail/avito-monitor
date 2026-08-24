// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

// There is no backoff ladder any more, and its absence is the finding.
//
// It used to climb 25 s, 60 s, 200 s on a 429, on the theory that a 429 is a
// rate limit that expires. A soak run put sixty-six consecutive 429s through
// that ladder — eleven fetches, about fifteen minutes each — and cleared none
// of them. Meanwhile eighteen of eighteen 403s cleared by waiting alone. The
// two are opposites, and the ladder was applied to the one it could not help.
//
// A 429 now costs one rotation or one halt. Waiting is what a 403 gets.

// challengeRepeats and challengeGap are what a solver-capable transport spends
// on a challenge: three attempts on the SAME identity, about twenty-five
// seconds apart. Rotating instead would throw away the solve in progress.
const (
	challengeRepeats = 3
	challengeGap     = 25 * time.Second
)

// maxAttempts bounds one Fetch however the remedies fall out.
const maxAttempts = 6

// blockWait is what a 403 costs. Measured: recovery tracks the number of these
// and nothing else, so it is the whole remedy rather than an accompaniment to
// one.
const blockWait = 240 * time.Second

// maxRecycles bounds what a soft block may cost. Two, because a third would
// spend minutes re-learning what the second already showed: if replacing the
// endpoint twice did not help, the endpoint is not the problem.
const maxRecycles = 2

// recycleSettle is the pause after replacing an endpoint. It is short on
// purpose. The branch this replaced waited four minutes between renewals and
// was still being handed an empty page twenty-seven minutes later, so the wait
// was never what this class needed.
const recycleSettle = 5 * time.Second

// burnWaitsBeforeRenewing is how many waits the measured remedy gets before the
// unmeasured one is tried. Three, because eighteen of eighteen observed 403s
// cleared within four waits, so a fetch still refused after three has left the
// ground the measurement covers.
const burnWaitsBeforeRenewing = 3

// HaltError says this transport cannot continue against this target, and that
// no amount of retrying will change it.
//
// It is a state rather than an incident. The caller records it, shows it, and
// never clears it: a monitor that decides on its own that the wall has gone
// away is a monitor that resumes into it all night.
type HaltError struct {
	Verdict Verdict
	URL     string
	Status  int
	At      time.Time
}

func (e *HaltError) Error() string {
	return fmt.Sprintf("transport: halted on %s: %s (status %d, marker %q)",
		e.URL, e.Verdict.Class, e.Status, e.Verdict.Wall.Marker)
}

// Boundary is the sentence a person reads when mode (b) stops. It explains
// rather than advertises: the reader has just hit something and wants to know
// what, not to be sold to.
func (e *HaltError) Boundary() string {
	if e.Verdict.Class == ClassThrottled {
		return "The site answered with 429 and will not stop while this exit " +
			"address is the one asking. Measured: sixty-six consecutive 429s " +
			"across about fifteen minutes of waiting each cleared none, and " +
			"the only ones that cleared did so through a redirect, which is " +
			"how a challenge ends rather than how a rate limit expires. The " +
			"remedy is a different address, and none is configured — pass " +
			"-proxies with a list, one entry per session, or let BlankTrail " +
			"answer the challenge instead. Collection on this watch has " +
			"stopped rather than retrying, because retrying cannot succeed."
	}
	return "This request met an interactive challenge, which a plain HTTP client " +
		"cannot pass: the site verifies it with a proof of work only a browser " +
		"can compute. Collection on this watch has stopped rather than " +
		"retrying, because retrying cannot succeed. BlankTrail passes these; " +
		"without it this watch stays stopped until you resume it."
}

// Result is one fetch, run to a verdict.
type Result struct {
	Page     avito.Page
	Verdict  Verdict
	Status   int
	Attempts int
	Session  string
	Label    string
	// Body is kept only for a verdict worth reading later — a shape change or
	// a mangled body. Keeping every body would be an archive nobody asked for.
	Body []byte
	// Trace is one entry per attempt, in order. It exists because Verdict is
	// the FINAL verdict: a fetch that met a soft block, renewed its identity
	// and then succeeded reports ClassOK, and the block leaves no other mark
	// than a longer elapsed time. A caller measuring how often the site pushes
	// back would read that as an untroubled request, which is the same silent
	// success this design refuses everywhere else.
	//
	// Only populated past one entry when a remedy was applied, so the common
	// case costs one small append.
	Trace []Step
}

// Step is one attempt inside one fetch: what came back, and what was done
// about it.
type Step struct {
	Class  Class
	Status int
	Remedy Remedy
}

// FetchConfig is the ladder's own knobs. Everything else it reads from Caps.
type FetchConfig struct {
	Surface avito.Surface
	Now     func() time.Time
	Sleep   func(context.Context, time.Duration) error
	// MaxBody bounds what is read from one response. A search page is about
	// 2.3 MB; anything far past that is not a page.
	MaxBody int64
}

// Fetcher runs requests to a verdict.
type Fetcher struct {
	leaser Leaser
	pacer  *Pacer
	cfg    FetchConfig
}

func NewFetcher(l Leaser, p *Pacer, cfg FetchConfig) *Fetcher {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepFor
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = 8 << 20
	}
	if p == nil {
		p = NewPacer(PacerConfig{})
	}
	return &Fetcher{leaser: l, pacer: p, cfg: cfg}
}

// Fetch runs one request to a verdict, applying the remedy each class asks for
// and no other.
//
// A challenge is the one branch that depends on the transport, and it depends
// on Caps rather than on Mode: with a solver, repeat on the SAME identity three
// times about twenty-five seconds apart, because rotating would throw away the
// solve in progress. Without one, halt — POST /web/N/firewallCaptcha/verify
// needs an X-Cube header carrying a WebGL proof of work, and no amount of
// retrying produces one.
func (f *Fetcher) Fetch(ctx context.Context, target string) (Result, error) {
	lease, err := f.leaser.Acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer lease.Release()

	var res Result
	challenges := 0
	rotations := 0
	burns := 0
	softBlocks := 0

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Paced on the identity AND on the address. The second argument used to
		// be Label, which names the endpoint rather than the exit — so four
		// jars on one IP were granted four separate per-address windows.
		if err := f.pacer.Wait(ctx, lease.Session(), lease.Egress()); err != nil {
			return res, err
		}
		res.Attempts = attempt
		res.Session, res.Label = lease.Session(), lease.Label()

		status, body, doErr := f.once(ctx, lease, target)
		res.Status = status
		if doErr != nil {
			// A transport error is not a verdict about the target. One re-dial,
			// then give up: a connection that will not open twice is not going
			// to open on the third try either.
			if attempt >= 2 {
				return res, doErr
			}
			continue
		}

		doc := avito.Document{Surface: f.cfg.Surface, URL: target, Body: body}
		page, extractErr := avito.Extract(doc, avito.Options{Now: f.cfg.Now().Unix()})
		res.Page = page
		res.Verdict = Classify(Input{
			Status: status, Doc: doc, Page: page, ExtractErr: extractErr,
			Warm: lease.Warm(),
		})
		// Recorded before the switch, so the trace shows what the site said on
		// every attempt rather than only on the one that happened to be last.
		res.Trace = append(res.Trace, Step{
			Class: res.Verdict.Class, Status: status,
			Remedy: res.Verdict.Class.Remedy(),
		})
		// Keep the body for anything the design did not anticipate, and for the
		// two refusals whose meaning is now in question, so the next occurrence
		// is legible rather than another line in a log.
		//
		// A soak run showed 403 and 429 behave as opposites, which the table
		// does not reflect. Eighteen of eighteen 403s cleared by waiting.
		// Sixty-six consecutive 429s, across eleven fetches and about fifteen
		// minutes of backoff each, cleared none — the only two that recovered
		// did so through a 302, which is how a challenge flow ends. A 429 looks
		// like a challenge being presented rather than a rate limit, and the
		// classifier calls it "throttled" and answers it with a backoff that
		// cannot work.
		//
		// The 429 branch now acts on that: rotate, or halt. What is still
		// unknown is why the captcha markers did not fire on those bodies —
		// either the marker set is short or a 429 challenge page does not look
		// like the one that was measured. That is a question about a body
		// nobody kept, so keep it.
		//
		// ClassSoftBlock is here for the same reason and it was learned the
		// same way. Its remedy is the one thing in the table still running on
		// an assumption, and the first fetch that ever exhausted every attempt
		// on it — six of them, twenty-seven minutes — left nothing behind to
		// look at, because a 200 is neither a 3xx nor an unhandled status.
		//
		// A soak run turned up a 302 that no measurement describes. It reached
		// the classifier at all only because Go returns a 3xx unfollowed when
		// its Location is missing or unusable, and it was then classified by
		// accident: the extractor failed on the redirect body, which lands in
		// ClassSoftBlock through the ErrNoExtractor branch. There is no 3xx
		// branch in the table. Rather than invent what a redirect means, keep
		// what it said.
		switch {
		case status >= 300 && status < 400,
			status == 429, status == 403,
			res.Verdict.Class == ClassOther,
			res.Verdict.Class == ClassSoftBlock:
			res.Body = body
		}

		switch res.Verdict.Class {
		case ClassOK, ClassEmpty:
			return res, nil

		case ClassShape:
			// Ours, not theirs. Repeating a request whose payload we have
			// stopped understanding spends the budget on our own bug.
			res.Body = body
			return res, nil

		case ClassChallenge:
			if !f.leaser.Caps().SolvesChallenges {
				return res, &HaltError{
					Verdict: res.Verdict, URL: target,
					Status: status, At: f.cfg.Now(),
				}
			}
			challenges++
			if challenges >= challengeRepeats {
				return res, &HaltError{
					Verdict: res.Verdict, URL: target,
					Status: status, At: f.cfg.Now(),
				}
			}
			// The same identity, deliberately: the solve is in progress on it.
			if err := f.cfg.Sleep(ctx, challengeGap); err != nil {
				return res, err
			}

		case ClassCold:
			// Not a wall. The identity has never warmed up, and the remedy is
			// to warm it — which Acquire already did, so reaching here means
			// the warm-up did not take. One more attempt on the same identity.
			if attempt >= 2 {
				return res, nil
			}

		case ClassThrottled:
			// Waiting does not clear a 429. Measured: sixty-six consecutive
			// 429s, eleven fetches, about fifteen minutes of escalating
			// backoff apiece, and not one turned into a 200 — while the only
			// two that recovered did so through a 302, which is how a
			// challenge flow ends rather than how a rate limit expires.
			//
			// So the remedy is a different address, and there is no
			// second-best. With nowhere to move, this transport is finished on
			// this target and says so, instead of spending a quarter of an
			// hour per fetch on a backoff that cannot work. That halt is the
			// honest boundary of running without a solver.
			if !f.leaser.Caps().RotatesEgress {
				return res, &HaltError{
					Verdict: res.Verdict, URL: target,
					Status: status, At: f.cfg.Now(),
				}
			}
			// Each configured address gets one turn. A Caps that reports
			// rotation but no address count still gets one, because refusing
			// to try is worse than trying once.
			limit := f.leaser.Caps().Addresses
			if limit < 1 {
				limit = 1
			}
			rotations++
			if rotations > limit {
				// Every configured address has now been asked and refused.
				// Cycling them again would only spend the pool.
				return res, &HaltError{
					Verdict: res.Verdict, URL: target,
					Status: status, At: f.cfg.Now(),
				}
			}
			if err := lease.RotateEgress(ctx); err != nil {
				return res, &HaltError{
					Verdict: res.Verdict, URL: target,
					Status: status, At: f.cfg.Now(),
				}
			}
			// No backoff ladder here. The new address has no history with this
			// target, and the pacer's per-address window is empty for it, so
			// sleeping would only delay a request that is already allowed.

		case ClassSoftBlock:
			// The endpoint is stuck, so replace the endpoint.
			//
			// Measured: three fetches in a row exhausted all six attempts on an
			// empty 200 — twenty-seven minutes each — every one of them on
			// BlankTrail port 20000, while port 20011 on the SAME exit address
			// answered in under two seconds throughout. Six identity renewals
			// inside a single fetch changed nothing, because renewing rotates
			// the profile and leaves the port itself in place.
			//
			// Waiting is not the remedy either, and the same three fetches show
			// it: they waited four minutes between attempts and were still
			// being handed an empty page twenty-seven minutes later. So a
			// recycle gets a short settle rather than a block wait, and two of
			// them is the limit — past that the endpoint is not what is wrong,
			// and spending a quarter of an hour discovering that is the mistake
			// this branch was built out of.
			softBlocks++
			if softBlocks > maxRecycles {
				res.Body = body
				return res, nil
			}
			if err := lease.Recycle(ctx); err != nil {
				return res, err
			}
			if err := f.cfg.Sleep(ctx, recycleSettle); err != nil {
				return res, err
			}

		case ClassBurned:
			// A 403 heals with time, and the identity is not what was refused.
			//
			// Measured, three ways. An arm with renewal replaced by a no-op
			// recovered from four consecutive 403s on one unchanged srv_id, and
			// again two fetches later. An arm with renewal enabled was no
			// faster: recovery tracked the number of 240-second waits and
			// nothing else. Across every arm, eighteen of eighteen 403s cleared
			// by waiting and none failed to.
			//
			// So wait first. Renewing discards a Qrator clearance that cost
			// nineteen to twenty-three seconds to obtain, to fix something the
			// identity was not causing — which is the same mistake this table
			// forbids in the other direction.
			//
			// The renewal is kept as a fallback rather than deleted. Eighteen
			// observations say waiting works; they do not say an identity can
			// never be burnt. Once the measured remedy has failed three times
			// on this fetch, the measured remedy is disconfirmed for this
			// instance and the other one is worth a turn.
			burns++
			if burns > burnWaitsBeforeRenewing {
				if err := lease.RenewIdentity(ctx); err != nil {
					return res, err
				}
			}
			if err := f.cfg.Sleep(ctx, blockWait); err != nil {
				return res, err
			}

		case ClassMangled:
			res.Body = body
			if attempt >= 2 {
				return res, nil
			}

		default:
			return res, nil
		}
	}
	return res, nil
}

// once performs one request and reads its body.
func (f *Fetcher) once(ctx context.Context, lease Lease, target string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := lease.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBody))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// rotate asks for a new address and treats "there is no other one" as a fact
// rather than a failure: on a single-address install a throttle is simply
// waited out.
func (f *Fetcher) rotate(ctx context.Context, lease Lease) error {
	if !f.leaser.Caps().RotatesEgress {
		return nil
	}
	if err := lease.RotateEgress(ctx); err != nil {
		return nil //nolint:nilerr // nowhere to move to is not a failure to fetch
	}
	return nil
}

func sleepFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
