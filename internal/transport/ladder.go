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

// backoffs are ours, seeded from the measurements: about 23 s for a first
// offence and about 200 s after ninety requests in ten minutes.
//
// Nothing here reads Retry-After. This target never sends one, so a ladder that
// waited for it would wait for a header that never comes.
var backoffs = []time.Duration{25 * time.Second, 60 * time.Second, 200 * time.Second}

// challengeRepeats and challengeGap are what a solver-capable transport spends
// on a challenge: three attempts on the SAME identity, about twenty-five
// seconds apart. Rotating instead would throw away the solve in progress.
const (
	challengeRepeats = 3
	challengeGap     = 25 * time.Second
)

// maxAttempts bounds one Fetch however the remedies fall out.
const maxAttempts = 6

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
	rung := 0
	challenges := 0

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := f.pacer.Wait(ctx, lease.Session(), lease.Label()); err != nil {
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

		switch res.Verdict.Class {
		case ClassOK, ClassEmpty:
			// The ladder resets on the first good answer, so a throttle an hour
			// later starts at twenty-five seconds again rather than at two
			// hundred.
			rung = 0
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
			if err := f.rotate(ctx, lease); err != nil {
				return res, err
			}
			if err := f.cfg.Sleep(ctx, backoffs[min(rung, len(backoffs)-1)]); err != nil {
				return res, err
			}
			rung++

		case ClassBurned, ClassSoftBlock:
			// Bound to the identity rather than to the address. Never rotate:
			// that would destroy a clearance worth nineteen to twenty-three
			// seconds to fix something rotation cannot fix.
			if err := lease.RenewIdentity(ctx); err != nil {
				return res, err
			}
			if err := f.cfg.Sleep(ctx, 240*time.Second); err != nil {
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
