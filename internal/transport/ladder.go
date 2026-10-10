// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
//
// Raised from six to twelve with ClassBadExit. On a real list four exits in
// five can be intercepting (measured 11 October 2026), and six attempts left a
// fair chance of every one of them landing on a bad exit. Every other branch
// still bounds itself well inside this.
const maxAttempts = 12

// exitBudget is how many exits one fetch may walk past.
func (f *Fetcher) exitBudget() int {
	n := f.leaser.Caps().Exits
	if n < 1 {
		n = f.leaser.Caps().Addresses
	}
	if n < 1 {
		n = 1
	}
	return min(n, maxAttempts-2)
}

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
	// Egress is the exit address, and it is NOT Label. Label names the
	// endpoint — "direct:chrome-133-android#0", "blanktrail:20000" — and the
	// passes table has a column for each. Writing the label into the egress
	// column would repeat in history the mistake the pacer made in memory, and
	// history cannot be corrected the way a live rate can.
	Egress string
	// Body is kept only for a verdict worth reading later — a shape change or
	// a mangled body. Keeping every body would be an archive nobody asked for.
	Body []byte
	// Bytes is how large the reply was, whether or not Body was kept.
	//
	// Separate from len(Body) precisely because Body is dropped on success:
	// passes.bytes exists to say what a pass cost, and reading it off a field
	// that is empty for every successful pass made the column report zero for
	// the only case it was about.
	Bytes int
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
	// KeepBody keeps the final response's bytes on every outcome, a success
	// included. For the command-line diagnostic: a question about what the
	// site actually sent is a question about a body nobody kept otherwise.
	KeepBody bool
}

// Fetcher runs requests to a verdict.
type Fetcher struct {
	leaser Leaser
	pacer  *Pacer
	cfg    FetchConfig
}

// Mode is which transport is behind this fetcher. The pass row records it, and
// the ladder is the only thing holding the leaser.
func (f *Fetcher) Mode() Mode { return f.leaser.Mode() }

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

// On is the same transport pointed at the other surface.
//
// The leaser and the pacer are shared, not copied, and that is the whole point:
// the diagnostic's two requests queue behind collection and count against the
// same budget as everything else. A second pacer would give a diagnostic its
// own allowance, which is exactly the sort of accounting that earns a 403.
func (f *Fetcher) On(surface avito.Surface) *Fetcher {
	cfg := f.cfg
	cfg.Surface = surface
	return &Fetcher{leaser: f.leaser, pacer: f.pacer, cfg: cfg}
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
// canonical is where a redirect envelope points, resolved against the request
// that produced it.
//
// Resolved and not concatenated: the shell states a path, and gluing an origin
// onto it by hand is the kind of thing that works until the day the site states
// an absolute URL or a protocol-relative one. net/url already knows the rules.
//
// A canonical that leaves the host is refused. Following one would take a
// watch's budget somewhere its owner never pointed it, and this product has no
// reason to read anything but Avito.
func (f *Fetcher) canonical(from string, page avito.Page) (string, bool) {
	if page.Kind != avito.EnvelopeRedirect || page.CanonicalURL == "" {
		return "", false
	}
	base, err := url.Parse(from)
	if err != nil {
		return "", false
	}
	ref, err := url.Parse(page.CanonicalURL)
	if err != nil {
		return "", false
	}
	next := base.ResolveReference(ref)
	if next.Host != base.Host {
		return "", false
	}
	if next.String() == from {
		// A shell whose canonical is itself. The one follow would be spent
		// re-reading the same bytes.
		return "", false
	}
	return next.String(), true
}

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
	followed := 0
	carried := 0

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Paced on the identity AND on the address. The second argument used to
		// be Label, which names the endpoint rather than the exit — so four
		// jars on one IP were granted four separate per-address windows.
		if err := f.pacer.Wait(ctx, lease.Session(), lease.Egress()); err != nil {
			return res, err
		}
		res.Attempts = attempt
		res.Session, res.Label, res.Egress = lease.Session(), lease.Label(), PublicEgress(lease.Egress())

		status, body, doErr := f.once(ctx, lease, target)
		// carried is what earlier requests of this same answer already cost,
		// and it is not the same thing as a retry.
		//
		// A retry REPLACES an attempt: the refused body was discarded and the
		// pass it writes is about the reply it kept, so its size is assigned.
		// A follow ADDS a request to one answer: the desktop shell and the
		// page it points at are both spent and both belong to the pass. Left
		// out, a two-request desktop pass would report the size of its second
		// request — the same under-reporting this column already had once,
		// when Body was dropped on success and every clean pass recorded zero.
		res.Status = status
		res.Bytes = carried + len(body)
		if doErr != nil {
			// A transport error is not a verdict about the target. One re-dial,
			// then give up: a connection that will not open twice is not going
			// to open on the third try either.
			//
			// Unless there is another exit to try. A connection that will not
			// open through one proxy says nothing about the next, and on a real
			// list most failures are exactly that: an exit that is down or
			// intercepting. Rotation here costs no clearance, because nothing
			// reached the site.
			if f.leaser.Caps().RotatesEgress && rotations < f.exitBudget() {
				rotations++
				move := lease.RotateEgress
				// Strike the exit only when the transport says it was the
				// exit. BlankTrail's refusals name it; a plain dial error
				// through a proxy cannot, and moving on is all it earns.
				if j, ok := lease.(ExitJudge); ok && j.BlamesExit(doErr) {
					move = j.RejectEgress
				}
				if err := move(ctx); err == nil {
					continue
				}
			}
			if attempt >= 2 {
				return res, doErr
			}
			continue
		}

		if f.cfg.KeepBody {
			res.Body = body
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
			// The desktop search page answers with a shell: no catalogue, one
			// canonical URL, about 358 KB of nothing else. The spec says page
			// one of that surface costs two requests, and this is the second.
			// Without it a desktop watch collects zero — measured on live
			// Avito on 25 August 2026, twice, byte for byte identical.
			//
			// The follow re-enters the loop rather than calling once() again,
			// so it is paced, classified and remedied exactly like any other
			// request. It IS another request against the same address, and a
			// page that quietly cost two against a budget counted in one is
			// how an address gets burned by arithmetic nobody did.
			//
			// One follow and no more. A shell pointing at a shell is a loop,
			// and the site is under no obligation not to serve one; the second
			// shell is returned as what it is, so zero listings from a
			// redirect envelope stays visible rather than being retried away.
			if next, ok := f.canonical(target, page); ok && followed == 0 {
				followed++
				carried = res.Bytes
				target = next
				continue
			}
			return res, nil

		case ClassShape:
			// Ours, not theirs. Repeating a request whose payload we have
			// stopped understanding spends the budget on our own bug.
			res.Body = body
			return res, nil

		case ClassChallenge:
			// Without a solver the challenge cannot be passed — but it can be
			// walked away from while there is somewhere to walk. Avito's wall
			// is bound to the address («доступ ограничен: проблема с IP»), and
			// a direct installation with a list of exits used to halt every
			// watch on the first one: measured on 11 October 2026, five
			// watches halted in five passes with a hundred and thirty-five
			// exits never tried. A new address gets a new identity too — the
			// jar that met the wall carries the flag with it. Only with
			// nowhere left to go is it the halt it always was.
			if !f.leaser.Caps().SolvesChallenges && f.leaser.Caps().RotatesEgress &&
				rotations < f.exitBudget() {
				rotations++
				if err := lease.RotateEgress(ctx); err == nil {
					if err := lease.RenewIdentity(ctx); err == nil {
						continue
					}
				}
			}
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

		case ClassBadExit:
			// The exit did not carry the request: BlankTrail said it is down,
			// could not finish TLS with the site, or is intercepting. Move to
			// the next one. Unlike a 429 this never halts the watch — nothing
			// here is about the site, and a halt is a person's problem to clear.
			// With nowhere left to move, the pass ends with the verdict and the
			// next tick starts on a fresh exit.
			if !f.leaser.Caps().RotatesEgress || rotations >= f.exitBudget() {
				return res, nil
			}
			rotations++
			if err := lease.RotateEgress(ctx); err != nil {
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
