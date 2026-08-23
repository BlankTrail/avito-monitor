# M1 Increment 2 — Transport, both modes, no database

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fetch an Avito page two ways — through BlankTrail and through a pure-Go client that genuinely works — behind one seam, classify what came back using the extraction rather than the status alone, and stop loudly at the boundary no HTTP client can cross.

**Architecture:** `internal/transport` owns HTTP and being blocked; `avito` owns Avito; neither imports the other's concerns. A `Leaser` hands out a `Lease` — one rested identity — and both modes implement it: `internal/transport/bt` over the vendored `blanktrail` pool, `internal/transport/direct` over a utls dialler with its own cookie jar. Above them sits one classifier and one ladder, and the ladder is caused rather than scheduled: it rotates the egress on 429 only, because a Qrator clearance costs nineteen to twenty-three seconds and any upstream change destroys it.

**Tech Stack:** Go 1.26, no CGO. Three dependencies join this increment: `refraction-networking/utls`, `andybalholm/brotli`, `klauspost/compress`. The vendored `blanktrail` package adds none — its non-test code is stdlib-only.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md` — read **Transport** in full, and the pacing paragraphs in **Collection**. `docs/measurements/2026-08-23-avito-surface.md` §17 and §18 carry two facts this increment is built on that the spec learned late.

**Depends on:** increment 1, merged. `avito.Extract`, `avito.Page`, `avito.Document.Wall()` and `avito.ErrNoExtractor` are the classifier's inputs and this increment does not change them.

## Global Constraints

- Module `github.com/BlankTrail/avito-monitor`, Go `1.26.0`, **no CGO**.
- `avito/` stays offline. `TestPackageReachesNoNetwork` must keep passing; nothing in this increment may add an import to that package.
- **`blanktrail/` is vendored, not authored.** It is copied verbatim except for the two changes named in Task 2 and Task 3. Do not refactor it, do not reformat it, do not fix its style. Its neutrality test is what keeps it copyable again.
- **No database.** This increment holds the pacer's windows in memory and the direct session on disk as one file. Increment 3 rebinds them to `transport_sessions` and `egress_windows`.
- **`Accept-Language: ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7` in both modes**, from one constant, pinned by one test. BlankTrail passes it through verbatim and Avito needs it.
- **`User-Agent` and `sec-ch-ua` are BlankTrail's in mode (a) and ours in mode (b)**, and which is which is a type rather than a convention. A second opinion in mode (a) contradicts the TLS handshake underneath.
- **Rotate the egress on 429 only.** Never on 403, never on 439. `PoolConfig.CountFailure` encodes it at the layer that owns rotation, so no ladder above can override it.
- **There is never a `Retry-After` on this target.** Nothing waits for one.
- Licence header `// SPDX-License-Identifier: AGPL-3.0-or-later` on every authored `.go` file. The vendored ones already carry it.
- Commit messages: imperative subject, a body that says *why*.

## What the measurements say, in one place

Every number below was measured on 2026-08-23 and every one of them ends up encoded in code or in the fake origin. Nothing here may be softened without a new measurement.

| Fact | Where it lands |
|---|---|
| plain client, no jar → **HTTP 429 with wall markers on request one** | `ClassCold`, and `fakeavito`'s default answer |
| one warm jar sustained **40/40 at 3 s spacing** on one address | the pacer's floor, set at 3.5 s |
| a fresh cookieless session per request → 429 at once | `fakeavito` issues the identity cookie only on a root navigation |
| `_avisc` is a **60-second nonce**, refreshed on every response, echoed on the next request | the jar; re-prime at 55 s |
| `srv_id` is the long-lived identity and the thing that gets flagged | the session file |
| recovery ≈ 23 s first offence, ≈ 200 s after **90 requests in 10 minutes**, hardening 429 → 403 | the ladder's 25/60/200, and `fakeavito`'s escalation |
| **never** a `Retry-After` | `fakeavito` never sends one; a test asserts the client never reads one |
| **HTTP 439** is an Avito/Qrator challenge status | `ClassChallenge` |
| a Qrator clearance costs **19–23 s** and dies on any upstream change | rotate on 429 only |
| one solve per (port, host); parallel requests coalesce | throughput scales with ports, never goroutines |
| captcha needs an `X-Cube` WebGL proof of work | the boundary mode (b) halts at |
| Go's `socks5://` resolves the hostname **locally** and there is no `socks5h` | the BlankTrail Leaser opens **HTTP CONNECT** ports |
| `Lease.RenewIdentity` already clears the jar by rotating the fingerprint | the remedy for 403 and soft block; no SDK extension needed |

## File Structure

| File | Responsibility |
|---|---|
| `blanktrail/**` | the SDK, copied from WB_GO verbatim; stdlib-only, so it costs no dependency |
| `internal/testutil/fakebt/**` | the SDK's own in-process fake of the control API, copied with it |
| `internal/transport/mode.go` | `Mode`, `Caps`, `Profile`, and the header-ownership rule |
| `internal/transport/seam.go` | `Leaser` and `Lease`, the one interface both modes satisfy |
| `internal/transport/class.go` | `Class`, `Classify`, and the remedy each class asks for |
| `internal/transport/ladder.go` | `Fetch` — the retry ladder, the backoffs, and halting |
| `internal/transport/pace.go` | the per-identity floor and the per-address ceiling |
| `internal/transport/bt/leaser.go` | `Leaser` over `*blanktrail.Pool`, opened over `Channels` |
| `internal/transport/direct/dial.go` | the utls dialler and the h2 wiring |
| `internal/transport/direct/jar.go` | the `avito.ru`-scoped jar with the nonce rule |
| `internal/transport/direct/session.go` | one identity: jar, profile, mutex, warm-up, persistence |
| `internal/transport/direct/leaser.go` | `Leaser` over a set of sessions, leased most-rested-first |
| `internal/testutil/fakeavito/**` | the origin that enforces every measured fact |
| `cmd/avitomon/main.go` | `avitomon fetch -mode direct|blanktrail <url>` |

---

### Task 1: Copy the SDK, and prove it still guards itself

**Files:**
- Create: `blanktrail/` (18 non-test + 9 test files, copied), `internal/testutil/fakebt/` (2 files, copied)
- Modify: `blanktrail/neutrality_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `blanktrail.Client`, `blanktrail.Pool`, `blanktrail.PoolConfig`, `blanktrail.PortSpec`, `blanktrail.Lease` (with `Do`, `Session`, `Port`, `Egress`, `RotateEgress`, `RenewIdentity`, `Release`), `blanktrail.Channel`, `NewDirectChannel`, `NewListChannel`, `Mixer`, `Upstream`, `Source`, `Rotor`, `Preflight`, `LicenseStatus`; `fakebt.Server`.

The package's non-test code imports nothing outside the standard library, so this costs no dependency. Copy it; do not improve it.

- [ ] **Step 1: Copy both trees and rewrite the module path**

```bash
cd S:/Projects/Avito_Go
mkdir -p blanktrail internal/testutil/fakebt
cp S:/Projects/WB_GO/blanktrail/*.go blanktrail/
cp S:/Projects/WB_GO/internal/testutil/fakebt/*.go internal/testutil/fakebt/
grep -rl "BlankTrail/wildberries-monitor" blanktrail/ internal/testutil/fakebt/ \
  | xargs sed -i 's|github.com/BlankTrail/wildberries-monitor|github.com/BlankTrail/avito-monitor|g'
gofmt -l blanktrail/ internal/testutil/fakebt/
```

Expected: `gofmt -l` prints nothing.

- [ ] **Step 2: Run the copied tests before changing anything**

```bash
cd S:/Projects/Avito_Go && go test ./blanktrail/ ./internal/testutil/fakebt/ 2>&1 | tail -5
```

Expected: both PASS. If they do not, stop — a vendored package that does not pass its own suite on arrival must not be modified on top of that, and the difference is between the two Go versions or the copy, not something to fix by editing the package.

- [ ] **Step 3: Write the failing test for the widened neutrality list**

Open `blanktrail/neutrality_test.go` and replace the `banned` slice and the comment above it:

```go
	// Names of specific sites and specific defence vendors, plus the one
	// classifier concept the package must never grow. The generic term for the
	// category is deliberately absent: retry.go states the boundary with it
	// ("... handling belongs to the proxy, not here"), and a guard that forbids
	// stating a rule is a guard that gets deleted.
	//
	// The Wildberries entries stay although this project does not target it.
	// A guard that forbids only the current target stops being a guard the
	// moment the package is copied a third time, and this is the second copy.
	//
	// "captcha" comes OFF the list, and that is a deliberate loosening rather
	// than an oversight. PortSpec gained CaptchaAction and CaptchaMaxAttempts
	// in this same commit, because a live port reports those controls and the
	// SDK could not set them. They are the proxy's own knobs and apply to any
	// target; the list exists to stop this package learning about *sites*, and
	// a rule that also forbids the vendor's vocabulary would have to be
	// suspended for every such field. "avito" and "avisc" go ON in exchange,
	// so the guard ends up stricter about the thing it was written to prevent.
	banned := []string{
		"wildberries", "wbaas", "avito", "avisc",
		"qrator", "cloudflare", "akamai", "datadome", "perimeterx", "geetest",
	}
```

- [ ] **Step 4: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./blanktrail/ -run TestPackageNamesNoTarget -v 2>&1 | head -8
```

Expected: FAIL — `client.go mentions "avito"` is *not* what you should see. What you should see is a **pass**, because nothing in the package mentions Avito yet. That is the correct outcome and it is why this step exists: run it now so that when Task 2 adds the captcha fields, you know the guard was green immediately before.

If it FAILS, read which file mentions which word. A copied file that already names Avito would mean the copy came from somewhere other than WB_GO.

- [ ] **Step 5: Prove the guard actually bites**

Temporarily append to `blanktrail/doc.go`:

```go
// TEMPORARY: proving the guard bites. Remove before committing.
const avitoProbe = "avito"
```

```bash
cd S:/Projects/Avito_Go && go test ./blanktrail/ -run TestPackageNamesNoTarget 2>&1 | head -4
```

Expected: FAIL — `doc.go mentions "avito"`. Then remove those two lines and re-run:

```bash
cd S:/Projects/Avito_Go && go test ./blanktrail/ -run TestPackageNamesNoTarget 2>&1 | tail -2
```

Expected: PASS. A guard nobody has seen fail is a guard nobody knows works.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add blanktrail/ internal/testutil/fakebt/ && git commit -m "$(cat <<'MSG'
feat(blanktrail): vendor the SDK, and widen the guard that keeps it copyable

Copied from wildberries-monitor with the module path rewritten and nothing
else changed. Its non-test code imports nothing outside the standard library,
so this costs no dependency; only its tests reach outside, to fakebt, which
comes with it.

The neutrality guard gains "avito" and "avisc" and keeps "wildberries" and
"wbaas". A guard that forbids only the current target stops being a guard the
moment the package is copied a third time, and this is the second copy.

"captcha" comes off, and the reason is in the test rather than in this message:
PortSpec is about to gain the two captcha controls a live port reports and the
SDK cannot set, and those are the proxy's own vocabulary for any target. The
list exists to stop this package learning about sites.

The guard was made to fail once, on purpose, before being trusted.

MSG
)"
```

---

### Task 2: The one SDK extension — the captcha controls

**Files:**
- Modify: `blanktrail/client.go` (the `PortSpec` struct, `openPortRequest`, and `PortSpec.request`)
- Create: `blanktrail/captcha_test.go`

**Interfaces:**
- Consumes: `blanktrail.PortSpec` (Task 1).
- Produces: `PortSpec.CaptchaAction string`, `PortSpec.CaptchaMaxAttempts int`, carried on the open request.

A live port reports `captcha_action: "rotate_retry"` and `captcha_max_attempts: 3`. The SDK cannot set either, so every port this project opens takes whatever the proxy's default happens to be — which is a policy decision made somewhere this project cannot see. Additive: an empty action and a zero count are omitted, so an existing caller's behaviour does not move.

- [ ] **Step 1: Write the failing test**

`blanktrail/captcha_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package blanktrail

import (
	"encoding/json"
	"testing"
)

// TestPortSpecCarriesCaptchaPolicy pins the one extension this project makes to
// the vendored SDK. A live port reports both controls; without them every port
// opened here inherits whatever default the proxy happens to hold, which is a
// policy decision made where the caller cannot see it.
func TestPortSpecCarriesCaptchaPolicy(t *testing.T) {
	spec := DefaultPortSpec()
	spec.CaptchaAction = "rotate_retry"
	spec.CaptchaMaxAttempts = 3

	body, err := json.Marshal(spec.request(20001, Egress{}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["captcha_action"] != "rotate_retry" {
		t.Errorf("captcha_action = %v, want rotate_retry", got["captcha_action"])
	}
	if got["captcha_max_attempts"] != float64(3) {
		t.Errorf("captcha_max_attempts = %v, want 3", got["captcha_max_attempts"])
	}
}

// TestPortSpecOmitsUnsetCaptchaPolicy keeps the extension additive. A caller
// that never touches these fields must send exactly what it sent before, or
// vendoring this package into a second project silently changes the first.
func TestPortSpecOmitsUnsetCaptchaPolicy(t *testing.T) {
	body, err := json.Marshal(DefaultPortSpec().request(20001, Egress{}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"captcha_action", "captcha_max_attempts"} {
		if _, present := got[k]; present {
			t.Errorf("%s was sent although it was never set; the extension must "+
				"be additive", k)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./blanktrail/ -run TestPortSpec 2>&1 | head -5
```

Expected: FAIL — `spec.CaptchaAction undefined`.

- [ ] **Step 3: Add the two fields**

In `blanktrail/client.go`, inside `type PortSpec struct`, immediately after the `JSSolver` field:

```go
	// CaptchaAction and CaptchaMaxAttempts are the proxy's own policy for a
	// port that meets an interactive challenge. A live port reports both; the
	// control API accepts both on open. Left unset they are omitted, so a
	// caller that does not care keeps whatever the proxy defaults to.
	CaptchaAction      string
	CaptchaMaxAttempts int
```

In `type openPortRequest struct`, after `JSSolver`:

```go
	CaptchaAction      string `json:"captcha_action,omitempty"`
	CaptchaMaxAttempts int    `json:"captcha_max_attempts,omitempty"`
```

In `func (s PortSpec) request(...)`, in the literal that builds `req`:

```go
		CaptchaAction:      s.CaptchaAction,
		CaptchaMaxAttempts: s.CaptchaMaxAttempts,
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./blanktrail/ 2>&1 | tail -3
```

Expected: PASS for the whole package, `TestPackageNamesNoTarget` included — which is the point of having taken `captcha` off the list in Task 1 rather than now.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add blanktrail/ && git commit -m "$(cat <<'MSG'
feat(blanktrail): let a caller set the port's captcha policy

A live port reports captcha_action and captcha_max_attempts and the control API
accepts both on open, but the SDK could set neither — so every port this
project opens would inherit whatever default the proxy holds, which is a policy
decision made where the caller cannot see it. On this target the policy matters:
the interactive challenge is the thing the paid mode exists to pass.

Additive by construction, and a test pins that: unset means omitted, so a
caller that never touches these fields sends exactly the bytes it sent before.
Vendoring a package into a second project must not change the first.

This is the only extension. The 403 remedy the spec had specified as a second
one already ships as Lease.RenewIdentity — see docs/measurements §18.

MSG
)"
```

---

### Task 3: The seam — one interface, two modes, and who owns which header

**Files:**
- Create: `internal/transport/mode.go`, `internal/transport/seam.go`, `internal/transport/mode_test.go`

**Interfaces:**
- Consumes: `blanktrail.Lease` (Task 1) — it must satisfy the produced `Lease` bar the four methods named below.
- Produces:
  - `type Mode string` with `ModeBlankTrail`, `ModeDirect`
  - `type Caps struct { SolvesChallenges bool; RotatesEgress bool; Addresses int }`
  - `type Profile struct { UserAgent, SecCHUA, Platform, AcceptEncoding string; Surface avito.Surface; OwnedByProxy bool }`
  - `const AcceptLanguage = "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7"`
  - `func (p Profile) Apply(req *http.Request)`
  - `type Lease interface { Do; Session; Label; Profile; RotateEgress; RenewIdentity; Release }`
  - `type Leaser interface { Acquire; Mode; Caps; Close }`

- [ ] **Step 1: Write the failing test**

`internal/transport/mode_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"net/http"
	"testing"
)

// TestAcceptLanguageIsOursInBothModes pins the one header that belongs to this
// program whichever mode is running. BlankTrail overrides User-Agent and
// sec-ch-ua with whatever its solver browser presents, and it passes
// Accept-Language through verbatim — so if we do not send it, nobody does, and
// Avito needs it.
func TestAcceptLanguageIsOursInBothModes(t *testing.T) {
	for _, p := range []Profile{
		{OwnedByProxy: true},
		{OwnedByProxy: false, UserAgent: "UA", SecCHUA: `"Chromium";v="131"`},
	} {
		req, err := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
		if err != nil {
			t.Fatal(err)
		}
		p.Apply(req)
		if got := req.Header.Get("Accept-Language"); got != AcceptLanguage {
			t.Errorf("OwnedByProxy=%v: Accept-Language = %q, want %q",
				p.OwnedByProxy, got, AcceptLanguage)
		}
	}
}

// TestProxyOwnedProfileSetsNoIdentityHeaders is the header-ownership rule made
// executable. In BlankTrail mode the proxy rewrites User-Agent and sec-ch-ua to
// match the TLS handshake it is presenting; a second opinion from here would
// contradict the handshake underneath, which is worse than sending nothing.
func TestProxyOwnedProfileSetsNoIdentityHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	Profile{OwnedByProxy: true, UserAgent: "should-not-be-sent", SecCHUA: "nor-this"}.Apply(req)
	for _, h := range []string{"User-Agent", "sec-ch-ua", "sec-ch-ua-platform"} {
		if got := req.Header.Get(h); got != "" {
			t.Errorf("%s = %q; the proxy owns it and our value would contradict "+
				"the TLS handshake it is presenting", h, got)
		}
	}
}

// TestDirectProfileSetsTheWholeSet is the mirror. In direct mode nothing
// rewrites anything, so a missing header IS the fingerprint.
func TestDirectProfileSetsTheWholeSet(t *testing.T) {
	p := Profile{
		UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0.0.0 Safari/537.36",
		SecCHUA:        `"Chromium";v="131", "Not_A Brand";v="24", "Google Chrome";v="131"`,
		Platform:       `"Windows"`,
		AcceptEncoding: "gzip, deflate, br, zstd",
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Apply(req)
	want := map[string]string{
		"User-Agent":         p.UserAgent,
		"sec-ch-ua":          p.SecCHUA,
		"sec-ch-ua-platform": p.Platform,
		"sec-ch-ua-mobile":   "?0",
		"Accept-Encoding":    p.AcceptEncoding,
		"Accept-Language":    AcceptLanguage,
	}
	for h, w := range want {
		if got := req.Header.Get(h); got != w {
			t.Errorf("%s = %q, want %q", h, got, w)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/ 2>&1 | head -6
```

Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the two files**

`internal/transport/mode.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"net/http"

	"github.com/BlankTrail/avito-monitor/avito"
)

// Mode is which transport is running. It is not a preference: the two differ in
// what they can do, and Caps says so rather than leaving a caller to infer it.
type Mode string

const (
	ModeBlankTrail Mode = "blanktrail"
	ModeDirect     Mode = "direct"
)

// Caps is what this transport can actually do, so the ladder above it does not
// have to know which mode it is talking to.
type Caps struct {
	// SolvesChallenges is false for direct mode, and it is the difference the
	// whole product is priced on: POST /web/N/firewallCaptcha/verify needs an
	// X-Cube header carrying a WebGL proof of work, which no HTTP client can
	// produce. A ladder that retried a challenge here would spend the budget
	// on something that cannot succeed.
	SolvesChallenges bool
	// RotatesEgress is false when every identity shares the host's own address.
	RotatesEgress bool
	// Addresses is how many distinct egress addresses are configured. It is
	// what the budget arithmetic multiplies, and the panel quotes it.
	Addresses int
}

// AcceptLanguage is ours in both modes. BlankTrail rewrites User-Agent and
// sec-ch-ua to match the handshake it presents, but it passes this one through
// verbatim — so if this program does not send it, nobody does, and Avito needs
// it. One constant, one test.
const AcceptLanguage = "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7"

// Accept is Chrome's own document accept string. It is not identity — the proxy
// does not rewrite it — so it is ours in both modes too.
const Accept = "text/html,application/xhtml+xml,application/xml;q=0.9," +
	"image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"

// Profile is the header-ownership split made into a type rather than a
// convention, because the convention is the kind that gets forgotten by a
// well-meaning commit.
type Profile struct {
	UserAgent      string
	SecCHUA        string
	Platform       string // sec-ch-ua-platform, quoted as the header wants it
	AcceptEncoding string
	Surface        avito.Surface

	// OwnedByProxy means the transport underneath rewrites the identity
	// headers to match its own TLS handshake. Setting them here would
	// contradict that handshake, which is worse than sending nothing.
	OwnedByProxy bool
}

// Apply writes this program's headers onto a request. It never writes an
// identity header when the proxy owns it, and it always writes Accept-Language.
func (p Profile) Apply(req *http.Request) {
	req.Header.Set("Accept", Accept)
	req.Header.Set("Accept-Language", AcceptLanguage)
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	if p.OwnedByProxy {
		return
	}
	if p.UserAgent != "" {
		req.Header.Set("User-Agent", p.UserAgent)
	}
	if p.SecCHUA != "" {
		req.Header.Set("sec-ch-ua", p.SecCHUA)
		req.Header.Set("sec-ch-ua-mobile", mobileHint(p.Surface))
	}
	if p.Platform != "" {
		req.Header.Set("sec-ch-ua-platform", p.Platform)
	}
	if p.AcceptEncoding != "" {
		req.Header.Set("Accept-Encoding", p.AcceptEncoding)
	}
}

func mobileHint(s avito.Surface) string {
	if s == avito.SurfaceMobile {
		return "?1"
	}
	return "?0"
}
```

`internal/transport/seam.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"net/http"
)

// Lease is one rested identity, held for the duration of one fetch.
//
// Both modes implement it and the ladder above knows nothing else. The methods
// that change an identity are named for what they change rather than for when
// they are called, because "when" is the ladder's business: RotateEgress moves
// the address and keeps the identity, RenewIdentity moves the identity and
// keeps the address, and confusing the two on this target is expensive —
// changing the address destroys a Qrator clearance that cost nineteen to
// twenty-three seconds.
type Lease interface {
	Do(req *http.Request) (*http.Response, error)

	// Session changes exactly when the identity does, and never otherwise. It
	// is what a caller keys a visit on.
	Session() string

	// Label is for a human reading a log line: "blanktrail:20003" or
	// "direct:chrome-131-win#2".
	Label() string

	// Profile says who owns which header for this lease.
	Profile() Profile

	// RotateEgress replaces the exit address, keeping the identity. Reachable
	// from exactly one branch of the ladder: ClassThrottled. It reports an
	// error wrapping ErrNoOtherAddress when there is nowhere to move to.
	RotateEgress(ctx context.Context) error

	// RenewIdentity replaces the fingerprint, the cookie jar and the session,
	// keeping the address. It costs a re-solve. The remedy for ClassBurned and
	// ClassSoftBlock, which are bound to the identity rather than to the IP.
	RenewIdentity(ctx context.Context) error

	Release()
}

// Leaser hands out rested identities.
type Leaser interface {
	// Acquire blocks until an identity has rested long enough to be used, and
	// returns the one that has rested longest.
	Acquire(ctx context.Context) (Lease, error)
	Mode() Mode
	Caps() Caps
	Close() error
}
```

- [ ] **Step 4: Run the tests and watch them pass**

The seam declares five methods the SDK already has and three that are ours. Nothing asserts that here on purpose: `*blanktrail.Lease` is wrapped in Task 6, and the compile-time assertion belongs where the wrapper is, not where the interface is.

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/transport/ -v 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"
```

Expected: three PASSes.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/transport/ && git commit -m "$(cat <<'MSG'
feat(transport): the seam, and who owns which header

One interface, two modes, and a Caps that says what the transport underneath
can actually do — so the ladder above never has to ask which mode it is talking
to. SolvesChallenges is the field the whole product is priced on.

The header-ownership split is a type rather than a convention, because a
convention is the kind of thing a well-meaning commit forgets. In BlankTrail
mode the proxy rewrites User-Agent and sec-ch-ua to match the handshake it is
presenting, and a second opinion from here contradicts that handshake — worse
than sending nothing. Accept-Language is ours in both modes from one constant,
because BlankTrail passes it through verbatim and Avito needs it.

RotateEgress and RenewIdentity are named for what they change rather than for
when they are called. Confusing them on this target costs a Qrator clearance
worth nineteen to twenty-three seconds.

MSG
)"
```

---

### Task 4: `fakeavito` — an origin that enforces every measured fact

**Files:**
- Create: `internal/testutil/fakeavito/fakeavito.go`, `internal/testutil/fakeavito/fakeavito_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `fakeavito.New(t) *Server`; `(*Server).URL()`; `(*Server).Requests()`; `(*Server).Warm(t) (identity, nonce string)`; `(*Server).Advance(time.Duration)`; the switches `DropState`, `SoftBlock`, `Serve439`, `ForceEmpty`, `Reset`; the constants `CookieIdentity`, `CookieNonce`, `NonceTTL`, `BurstLimit`, `BurstWindow`.

Every rule below is a measurement, not an invention. This server is the only place the ladder's behaviour can be tested without spending real requests on a real target, and **a fake that is kinder than the original teaches the ladder the wrong lesson** — a lesson only ever unlearned in production.

- [ ] **Step 1: Write the failing test**

Create `internal/testutil/fakeavito/fakeavito_test.go` with these seven tests. Each one is a measured fact; none is a design preference.

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package fakeavito

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// get is a bare client: no jar, no fingerprint. It is what an unwarmed
// identity looks like, which is the state most of these rules are about.
func get(t *testing.T, s *Server, path string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.URL()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return string(b)
}

// TestColdRequestIsRefusedWithAWall is the measured baseline: plain curl, no
// jar, HTTP 429 with wall markers on request one.
func TestColdRequestIsRefusedWithAWall(t *testing.T) {
	s := New(t)
	resp := get(t, s, "/all?q=iphone")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 on an unwarmed identity", resp.StatusCode)
	}
	if !strings.Contains(body(t, resp), "проблема с IP") {
		t.Error("the refusal carried no wall marker; the measured one does, and " +
			"that is why the classifier needs a ClassCold row")
	}
}

// TestRetryAfterIsNeverSent. There is no Retry-After on this target, so a
// ladder that waited for one would wait for a header that never comes.
func TestRetryAfterIsNeverSent(t *testing.T) {
	s := New(t)
	resp := get(t, s, "/all?q=iphone")
	defer resp.Body.Close()
	if v := resp.Header.Get("Retry-After"); v != "" {
		t.Errorf("Retry-After = %q; this target never sends one", v)
	}
}

// TestRootNavigationIssuesTheIdentity. A fresh cookieless session per request
// collects a 429 immediately; the way out is a root navigation, which is what
// mints srv_id and the first nonce.
func TestRootNavigationIssuesTheIdentity(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	ok := get(t, s, "/all?q=iphone",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("warmed request status = %d, want 200", ok.StatusCode)
	}
}

// TestNonceMustBeFresh. _avisc lasts sixty seconds. A request one second late
// must go out with the STALE value rather than with none — sending none is
// strictly worse — so the server accepts it inside the window and refuses past.
func TestNonceMustBeFresh(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	s.Advance(NonceTTL + time.Second)
	stale := get(t, s, "/all?q=iphone",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	defer stale.Body.Close()
	if stale.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d with a nonce past its TTL, want 429", stale.StatusCode)
	}
}

// TestNonceIsRefreshedOnEveryResponse. The client has to write the new value
// back after every response; a jar that only stores the first one goes stale
// inside a minute.
func TestNonceIsRefreshedOnEveryResponse(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	resp := get(t, s, "/all?q=iphone",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	defer resp.Body.Close()
	next := ""
	for _, c := range resp.Cookies() {
		if c.Name == CookieNonce {
			next = c.Value
		}
	}
	if next == "" {
		t.Fatal("no fresh nonce on the response")
	}
	if next == nonce {
		t.Error("the nonce did not change; a nonce that never moves is not one")
	}
}

// TestEscalationTo403 is the measured hardening past ninety requests in ten
// minutes.
func TestEscalationTo403(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	last := 0
	for i := 0; i < BurstLimit+5; i++ {
		resp := get(t, s, "/all?q=iphone",
			&http.Cookie{Name: CookieIdentity, Value: srv},
			&http.Cookie{Name: CookieNonce, Value: nonce})
		for _, c := range resp.Cookies() {
			if c.Name == CookieNonce {
				nonce = c.Value
			}
		}
		last = resp.StatusCode
		resp.Body.Close()
	}
	if last != http.StatusForbidden {
		t.Errorf("status after %d requests = %d, want 403 — the measured "+
			"escalation hardens past ninety in ten minutes", BurstLimit+5, last)
	}
}

// TestSwitchesProduceTheThreeShapesTheClassifierMustTellApart. An empty answer,
// a soft block and a 439 are three different things and only the body tells
// them apart.
func TestSwitchesProduceTheThreeShapesTheClassifierMustTellApart(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	fetch := func() (int, string) {
		resp := get(t, s, "/all?q=iphone",
			&http.Cookie{Name: CookieIdentity, Value: srv},
			&http.Cookie{Name: CookieNonce, Value: nonce})
		for _, c := range resp.Cookies() {
			if c.Name == CookieNonce {
				nonce = c.Value
			}
		}
		return resp.StatusCode, body(t, resp)
	}

	s.ForceEmpty(true)
	if st, b := fetch(); st != 200 || !strings.Contains(b, "Ничего не найдено") {
		t.Errorf("empty: status=%d, site wording present=%v", st,
			strings.Contains(b, "Ничего не найдено"))
	}
	s.ForceEmpty(false)

	s.SoftBlock(true)
	if st, b := fetch(); st != 200 || strings.Contains(b, "__initialData__") {
		t.Errorf("soft block: status=%d state-present=%v; a soft block is a 200 "+
			"with no container at all", st, strings.Contains(b, "__initialData__"))
	}
	s.SoftBlock(false)

	s.Serve439(true)
	if st, _ := fetch(); st != 439 {
		t.Errorf("status = %d, want 439", st)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/testutil/fakeavito/ 2>&1 | head -5
```

Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the server**

Create `internal/testutil/fakeavito/fakeavito.go`. The package comment carries the argument, because a reader who does not know why the rules are shaped this way will soften them.

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fakeavito is an origin that behaves the way the real one was measured
// to behave on 2026-08-23.
//
// Every rule here is a measurement rather than an invention, and that is the
// whole point: a fake that is kinder than the original teaches the ladder the
// wrong lesson, and the lesson is only ever unlearned in production.
//
// Time is a parameter. Advance moves the server's clock without sleeping, so a
// test about a sixty-second nonce or a ten-minute window runs instantly and
// deterministically.
package fakeavito

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The cookies that matter. srv_id is the long-lived identity and the thing that
// gets flagged; _avisc is a sixty-second nonce refreshed on every response.
const (
	CookieIdentity = "srv_id"
	CookieNonce    = "_avisc"
)

const (
	// NonceTTL is the measured sixty seconds.
	NonceTTL = 60 * time.Second
	// BurstLimit and BurstWindow are the measured escalation: about ninety
	// requests in ten minutes hardens the refusal from 429 to 403.
	BurstLimit  = 90
	BurstWindow = 10 * time.Minute
)

// Server is the fake origin.
type Server struct {
	mu       sync.Mutex
	now      time.Time
	requests int

	nonces   map[string]string
	issued   map[string]time.Time
	hits     map[string][]time.Time
	nonceSeq int
	idSeq    int

	dropState  bool
	softBlock  bool
	serve439   bool
	forceEmpty bool

	http *httptest.Server
}

// New starts a server and stops it when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		now:    time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
		nonces: map[string]string{},
		issued: map[string]time.Time{},
		hits:   map[string][]time.Time{},
	}
	s.http = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.http.Close)
	return s
}

func (s *Server) URL() string { return s.http.URL }

// Requests counts every request that reached this server, refusals included.
// It is what a halting test asserts against: after a halt, it must not move.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Advance moves the clock without sleeping.
func (s *Server) Advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
}

func (s *Server) DropState(v bool)  { s.set(func() { s.dropState = v }) }
func (s *Server) SoftBlock(v bool)  { s.set(func() { s.softBlock = v }) }
func (s *Server) Serve439(v bool)   { s.set(func() { s.serve439 = v }) }
func (s *Server) ForceEmpty(v bool) { s.set(func() { s.forceEmpty = v }) }

func (s *Server) set(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

// Reset clears every identity and every switch, keeping the clock.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonces = map[string]string{}
	s.issued = map[string]time.Time{}
	s.hits = map[string][]time.Time{}
	s.dropState, s.softBlock, s.serve439, s.forceEmpty = false, false, false, false
}

// Warm performs the root navigation an unwarmed identity has to make, and
// returns the identity and its first nonce.
func (s *Server) Warm(t *testing.T) (identity, nonce string) {
	t.Helper()
	resp, err := http.Get(s.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		switch c.Name {
		case CookieIdentity:
			identity = c.Value
		case CookieNonce:
			nonce = c.Value
		}
	}
	if identity == "" || nonce == "" {
		t.Fatalf("root navigation issued srv_id=%q nonce=%q", identity, nonce)
	}
	return identity, nonce
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++

	// A root navigation is how an identity is minted, and it is the only way.
	// This is the rule behind the measurement that a fresh cookieless session
	// per request collects a 429 immediately.
	if r.URL.Path == "/" {
		s.idSeq++
		s.mint(w, "srv-"+strconv.Itoa(s.idSeq))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html><body>Авито</body></html>")
		return
	}

	id := cookie(r, CookieIdentity)
	nonce := cookie(r, CookieNonce)

	// Unwarmed, or carrying a nonce that has lapsed: refused, with a wall in
	// the body and without a Retry-After. This is the measured cold baseline.
	if id == "" || s.nonces[id] == "" || nonce != s.nonces[id] ||
		s.now.Sub(s.issued[id]) > NonceTTL {
		s.wall(w, http.StatusTooManyRequests)
		return
	}

	// The measured escalation, counted per identity inside a moving window.
	hits := append(s.recent(id), s.now)
	s.hits[id] = hits
	if len(hits) > BurstLimit {
		s.mint(w, id)
		s.wall(w, http.StatusForbidden)
		return
	}

	s.mint(w, id)
	switch {
	case s.serve439:
		s.wall(w, 439)
	case s.softBlock:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html><body><div>Авито</div></body></html>")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, s.page())
	}
}

// recent drops hits that have fallen out of the burst window.
func (s *Server) recent(id string) []time.Time {
	var out []time.Time
	for _, t := range s.hits[id] {
		if s.now.Sub(t) <= BurstWindow {
			out = append(out, t)
		}
	}
	return out
}

// mint issues a fresh nonce, which is what "refreshed on every response" means.
func (s *Server) mint(w http.ResponseWriter, id string) {
	s.nonceSeq++
	n := "nonce-" + strconv.Itoa(s.nonceSeq)
	s.nonces[id] = n
	s.issued[id] = s.now
	http.SetCookie(w, &http.Cookie{Name: CookieIdentity, Value: id, Path: "/"})
	http.SetCookie(w, &http.Cookie{Name: CookieNonce, Value: n, Path: "/"})
}

// wall writes a refusal. Deliberately no Retry-After, ever.
func (s *Server) wall(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, "<html><head><title>Ошибка</title></head><body>"+
		"<h1>Возникла проблема с IP-адресом</h1></body></html>")
}

// page renders a mobile results page in the shape increment 1 reads: an
// attribute-less script holding window.__initialData__, plus item-wrapper
// cards. dropState removes the state block, which is what Avito does to a
// client it has classified.
func (s *Server) page() string {
	count := 59959
	extra := ""
	if s.forceEmpty {
		count = 0
		extra = `,\"emptySearch\":\"Ничего не найдено по запросу\"`
	}
	state := fmt.Sprintf(
		`<script>window.__initialData__ = "{\"search\":{\"count\":%d%s}}";</script>`,
		count, extra)
	if s.dropState {
		state = ""
	}
	var cards strings.Builder
	if !s.forceEmpty {
		for i := 1; i <= 30; i++ {
			id := 7800000000 + i
			fmt.Fprintf(&cards,
				`<div data-marker="item-wrapper(%d)">`+
					`<div data-marker="titleLabelGrid">Товар %d</div>`+
					`<div data-marker="priceLabelGrid">%d ₽</div>`+
					`<a data-marker="item/link" data-marker-id="%d" href="/moskva/t/x_%d"></a>`+
					`</div>`, id, i, 1000*i, id, id)
		}
	}
	return "<html><body>" + state + cards.String() + "</body></html>"
}

func cookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/testutil/fakeavito/ -v 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"
```

Expected: seven PASSes.

- [ ] **Step 5: Prove the fake's page is one the real extractor reads**

A fake origin that serves markup the real parser cannot read tests nothing. Add to `fakeavito_test.go`:

```go
// TestPageIsReadableByTheRealExtractor. A fake whose markup only this package
// understands would let the ladder pass while the parser rots.
func TestPageIsReadableByTheRealExtractor(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	resp := get(t, s, "/all?q=iphone",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	b := body(t, resp)

	page, err := avito.Extract(avito.Document{
		Surface: avito.SurfaceMobile, Body: []byte(b)}, avito.Options{})
	if err != nil {
		t.Fatalf("the real extractor could not read the fake's page: %v", err)
	}
	if !page.Ok() || len(page.Listings) != 30 {
		t.Errorf("listings = %d ok = %v, want 30 and true", len(page.Listings), page.Ok())
	}
	if page.TotalCount == nil || *page.TotalCount != 59959 {
		t.Errorf("TotalCount = %v, want 59959", page.TotalCount)
	}
}
```

with `"github.com/BlankTrail/avito-monitor/avito"` added to the imports.

```bash
cd S:/Projects/Avito_Go && go test ./internal/testutil/fakeavito/ -run TestPageIsReadable -v 2>&1 | grep -E "^(--- |ok|FAIL)"
```

Expected: PASS. If the state block does not decode, the escaping in `page()` is wrong — `window.__initialData__` is a JS string literal *holding* JSON, so the inner quotes are backslash-escaped inside a Go raw string, and that is easy to get wrong in exactly one direction.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/testutil/fakeavito/ && git commit -m "$(cat <<'MSG'
test(fakeavito): an origin that refuses the way the real one was measured to

Every rule is a measurement. An unwarmed identity gets 429 with a wall in the
body; the only way to mint one is a root navigation; the nonce lasts sixty
seconds and is reissued on every response; ninety requests in ten minutes
hardens the refusal to 403; and there is never a Retry-After, so a ladder that
waited for one would wait for a header that never comes.

A fake that is kinder than the original teaches the ladder the wrong lesson,
and that lesson is only ever unlearned in production.

Its clock is a parameter, so a test about a sixty-second nonce or a ten-minute
window runs instantly and identically every time. Its request counter is what
the halting test will assert against: after mode (b) halts, that number must
not move. And its pages are read by the real extractor rather than by a reader
written for it, so the ladder cannot pass while the parser rots.

MSG
)"
```

---

### Task 5: The classifier — it takes the extraction, not just the status

**Files:**
- Create: `internal/transport/class.go`, `internal/transport/class_test.go`

**Interfaces:**
- Consumes: `avito.Page`, `avito.Document.Wall()`, `avito.ErrNoExtractor`, `avito.ErrNotUTF8`, `Caps` (Tasks 1–4).
- Produces: `type Class string` with `ClassOK`, `ClassEmpty`, `ClassShape`, `ClassSoftBlock`, `ClassCold`, `ClassThrottled`, `ClassBurned`, `ClassChallenge`, `ClassMangled`, `ClassOther`; `type Verdict struct { Class Class; Wall avito.WallHit; Detail string }`; `func Classify(in Input) Verdict`; `type Input struct { Status int; Doc avito.Document; Page avito.Page; ExtractErr error; Warm bool }`.

On this target an empty query, a dropped hydration state and a payload we have stopped understanding are all HTTP 200 with no listings, and **only the parse tells them apart**. Giving the classifier the status alone is what makes a monitor go quiet and look healthy.

- [ ] **Step 1: Write the failing test**

`internal/transport/class_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

func doc(body string) avito.Document {
	return avito.Document{Surface: avito.SurfaceMobile, Body: []byte(body)}
}

const wallBody = `<html><body><h1>Возникла проблема с IP-адресом</h1></body></html>`

func TestClassify(t *testing.T) {
	full := avito.Page{Kind: avito.EnvelopeCatalog, Listings: []avito.Listing{{ItemID: 1}}}
	zero := int64(0)
	many := int64(59959)
	empty := avito.Page{Kind: avito.EnvelopeCatalog, TotalCount: &zero, EmptyDeclared: true}
	shape := avito.Page{Kind: avito.EnvelopeCatalog, TotalCount: &many}

	cases := []struct {
		name string
		in   Input
		want Class
	}{
		{"a page with listings", Input{Status: 200, Page: full, Doc: doc("x")}, ClassOK},

		// The three 200s that only the parse tells apart.
		{"the site said nothing matched", Input{Status: 200, Page: empty, Doc: doc("x")}, ClassEmpty},
		{"the container said 59959 and we read none",
			Input{Status: 200, Page: shape, Doc: doc("x")}, ClassShape},
		{"no container at all",
			Input{Status: 200, ExtractErr: avito.ErrNoExtractor, Doc: doc("x")}, ClassSoftBlock},

		// 439 is Avito's own challenge status and does not appear in any RFC.
		{"439", Input{Status: 439, Doc: doc("x")}, ClassChallenge},
		{"a captcha marker at 200", Input{Status: 200,
			Doc: doc(`<html>gcaptcha4</html>`), ExtractErr: avito.ErrNoExtractor}, ClassChallenge},

		// The measured cold baseline. Routing it to ClassChallenge would halt
		// mode (b) permanently before it made a second request.
		{"429 with a wall on an unwarmed identity",
			Input{Status: 429, Doc: doc(wallBody), Warm: false}, ClassCold},
		{"429 with a wall on a warm identity",
			Input{Status: 429, Doc: doc(wallBody), Warm: true}, ClassThrottled},
		{"a bare 429", Input{Status: 429, Doc: doc("x"), Warm: true}, ClassThrottled},

		{"403", Input{Status: 403, Doc: doc("x"), Warm: true}, ClassBurned},
		{"a body that is not UTF-8",
			Input{Status: 200, ExtractErr: avito.ErrNotUTF8, Doc: doc("x")}, ClassMangled},
		{"a 500 is nothing we have a remedy for",
			Input{Status: 500, Doc: doc("x"), Warm: true}, ClassOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.in).Class; got != c.want {
				t.Errorf("Classify() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestColdOnlyAppliesBeforeAWarmUp is the row whose absence kills mode (b).
// The same response means two different things depending on whether this
// identity has ever completed a warm-up, and only the caller knows which.
func TestColdOnlyAppliesBeforeAWarmUp(t *testing.T) {
	in := Input{Status: 429, Doc: doc(wallBody)}
	in.Warm = false
	if got := Classify(in).Class; got != ClassCold {
		t.Errorf("unwarmed: %q, want %q", got, ClassCold)
	}
	in.Warm = true
	if got := Classify(in).Class; got == ClassCold {
		t.Error("a warm identity was classified cold; the row would then never " +
			"stop applying and the ladder would warm up for ever")
	}
}

// TestChallengeOutranksThrottling. A captcha marker at any status is a
// challenge, but a plain wall marker at 429 is not — the measured cold
// baseline carries exactly that, and conflating them halts mode (b) on request
// one, permanently, because halted_reason is never cleared by the program.
func TestChallengeOutranksThrottling(t *testing.T) {
	captchaAt429 := Input{Status: 429, Warm: true,
		Doc: doc(`<html>Продолжить для решения gcaptcha4</html>`)}
	if got := Classify(captchaAt429).Class; got != ClassChallenge {
		t.Errorf("captcha marker at 429 = %q, want %q", got, ClassChallenge)
	}
}

// TestRemedies pins what each class asks for, because the ladder reads this and
// nothing else. Rotation is reachable from exactly one class.
func TestRemedies(t *testing.T) {
	rotates := 0
	for _, c := range []Class{ClassOK, ClassEmpty, ClassShape, ClassSoftBlock,
		ClassCold, ClassThrottled, ClassBurned, ClassChallenge, ClassMangled, ClassOther} {
		if c.Remedy() == RemedyRotateEgress {
			rotates++
			if c != ClassThrottled {
				t.Errorf("%q asks to rotate the egress; only ClassThrottled may, "+
					"because rotation destroys a clearance worth 19-23 seconds", c)
			}
		}
	}
	if rotates != 1 {
		t.Errorf("%d classes rotate the egress, want exactly 1", rotates)
	}
	if ClassShape.Remedy() != RemedyNone {
		t.Errorf("ClassShape.Remedy() = %v; repeating a request whose payload we "+
			"have stopped understanding spends the budget on our own bug",
			ClassShape.Remedy())
	}
	if ClassEmpty.Remedy() != RemedyNone {
		t.Errorf("ClassEmpty.Remedy() = %v; it is the site's answer, not a fault",
			ClassEmpty.Remedy())
	}
	for _, c := range []Class{ClassBurned, ClassSoftBlock} {
		if c.Remedy() != RemedyRenewIdentity {
			t.Errorf("%q.Remedy() = %v, want RemedyRenewIdentity", c, c.Remedy())
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/ -run 'TestClassify|TestCold|TestChallenge|TestRemedies' 2>&1 | head -5
```

Expected: FAIL — `undefined: Classify`.

- [ ] **Step 3: Write the classifier**

`internal/transport/class.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"errors"

	"github.com/BlankTrail/avito-monitor/avito"
)

// Class is what one response turned out to be.
type Class string

const (
	ClassOK        Class = "ok"
	ClassEmpty     Class = "empty"      // the site's own answer: nothing matched
	ClassShape     Class = "shape"      // our parser stopped understanding the payload
	ClassSoftBlock Class = "soft-block" // 200, no container of any kind
	ClassCold      Class = "cold"       // an unwarmed identity being told to warm up
	ClassThrottled Class = "throttled"
	ClassBurned    Class = "burned"    // 403: this identity is finished
	ClassChallenge Class = "challenge" // 439, or a captcha marker anywhere
	ClassMangled   Class = "mangled"   // the bytes are not what they claim to be
	ClassOther     Class = "other"
)

// Remedy is what a class asks the ladder to do about it.
type Remedy string

const (
	RemedyNone          Remedy = "none"
	RemedyRetrySame     Remedy = "retry-same"     // same identity, same address
	RemedyRotateEgress  Remedy = "rotate-egress"  // new address, same identity
	RemedyRenewIdentity Remedy = "renew-identity" // new identity, same address
	RemedyWarmUp        Remedy = "warm-up"        // a root navigation, then one retry
	RemedyRedial        Remedy = "redial"
	RemedyHalt          Remedy = "halt" // only where the transport cannot pass
)

// Remedy is the single table the ladder reads.
//
// Rotation is reachable from exactly one class, and a test enforces that. A
// Qrator clearance is domain-wide, long-lived, costs nineteen to twenty-three
// seconds to obtain and is destroyed by any upstream change — so rotating on a
// 403 or a 439 throws away something that was working in order to fix something
// rotation cannot fix.
func (c Class) Remedy() Remedy {
	switch c {
	case ClassOK, ClassEmpty:
		return RemedyNone
	case ClassShape:
		// Repeating a request whose payload we have stopped understanding
		// spends the budget on our own bug. Archive the body instead.
		return RemedyNone
	case ClassSoftBlock, ClassBurned:
		return RemedyRenewIdentity
	case ClassCold:
		return RemedyWarmUp
	case ClassThrottled:
		return RemedyRotateEgress
	case ClassChallenge:
		// The ladder decides between repeating on the same port and halting,
		// from Caps.SolvesChallenges. This table does not know the mode.
		return RemedyRetrySame
	case ClassMangled:
		return RemedyRedial
	default:
		return RemedyNone
	}
}

// Input is everything the classifier is given. The extraction is part of it
// rather than an afterthought: on this target an empty query, a dropped
// hydration state and a payload we have stopped understanding are all HTTP 200
// with no listings, and only the parse tells them apart.
type Input struct {
	Status int
	Doc    avito.Document
	Page   avito.Page
	// ExtractErr is what avito.Extract returned. avito.ErrNoExtractor means no
	// rule recognised the document, which is NOT the same as an empty result.
	ExtractErr error
	// Warm says whether this identity has ever completed a warm-up. The same
	// 429-with-a-wall means two different things either side of that line and
	// only the caller knows which side it is on.
	Warm bool
}

// Verdict is the class and the evidence for it.
type Verdict struct {
	Class  Class
	Wall   avito.WallHit
	Detail string
}

// captchaLabels are the wall labels that mean an interactive challenge rather
// than a refusal. The distinction is the whole of mode (b)'s boundary.
var captchaLabels = map[string]bool{
	avito.LabelSlider:  true,
	avito.LabelGeetest: true,
}

// Classify turns one response plus its extraction into one verdict.
func Classify(in Input) Verdict {
	wall, hasWall := in.Doc.Wall()

	// A captcha marker at any status is a challenge. 439 is Avito's own
	// challenge status and appears in no RFC.
	if in.Status == 439 || (hasWall && captchaLabels[wall.Label]) {
		return Verdict{ClassChallenge, wall, "captcha or 439"}
	}

	switch in.Status {
	case 429:
		// The measured cold baseline is a 429 whose body carries a wall marker,
		// on an identity that has never warmed up. It is the state the whole
		// warm-jar design exists to leave, not a wall to stop at.
		if hasWall && !in.Warm {
			return Verdict{ClassCold, wall, "unwarmed identity refused; warm up and retry"}
		}
		return Verdict{ClassThrottled, wall, "throttled"}
	case 403:
		return Verdict{ClassBurned, wall, "this identity is finished on this address"}
	}

	if in.ExtractErr != nil {
		switch {
		case errors.Is(in.ExtractErr, avito.ErrNotUTF8),
			errors.Is(in.ExtractErr, avito.ErrEmptyBody),
			errors.Is(in.ExtractErr, avito.ErrNoCyrillic):
			// Almost always a body that arrived still compressed. It reads
			// downstream as "the query matched nothing", which is the single
			// most expensive silent failure in this system.
			return Verdict{ClassMangled, wall, in.ExtractErr.Error()}
		case errors.Is(in.ExtractErr, avito.ErrNoExtractor):
			if hasWall {
				return Verdict{ClassSoftBlock, wall, "no container, and a wall marker"}
			}
			return Verdict{ClassSoftBlock, wall, "no result container for this surface"}
		}
	}

	if in.Status >= 200 && in.Status < 300 {
		switch {
		case in.Page.Ok():
			return Verdict{ClassOK, wall, ""}
		case in.Page.EmptyDeclared,
			in.Page.TotalCount != nil && *in.Page.TotalCount == 0:
			// The container was present and reported zero, corroborated by the
			// site's own wording. That is an answer, and answering it with a
			// remedy would re-pay a twenty-second solve on a quiet query.
			return Verdict{ClassEmpty, wall, "the site reported no matches"}
		case in.Page.Kind == avito.EnvelopeCatalog:
			// The container said there were results and we read none. Ours.
			return Verdict{ClassShape, wall, "container reports results, extractors read none"}
		case in.Page.Kind == avito.EnvelopeRedirect, in.Page.Kind == avito.EnvelopeItem:
			return Verdict{ClassOK, wall, ""}
		}
		return Verdict{ClassSoftBlock, wall, "2xx with nothing recognisable in it"}
	}
	return Verdict{ClassOther, wall, "unhandled status"}
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/transport/ -v 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"
```

Expected: every test PASSes, the three from Task 3 included.

- [ ] **Step 5: Classify the real captures, and pin what they come out as**

Add to `class_test.go`:

```go
// TestClassifyRealCaptures runs the classifier over increment 1's fixtures.
// A classifier that is only ever fed hand-written inputs agrees with the
// person who wrote them.
func TestClassifyRealCaptures(t *testing.T) {
	cases := []struct {
		fixture string
		surface avito.Surface
		want    Class
	}{
		{"desktop-canonical-p1", avito.SurfaceDesktop, ClassOK},
		{"desktop-seller", avito.SurfaceDesktop, ClassOK},
		{"desktop-spa-shell", avito.SurfaceDesktop, ClassOK}, // a redirect is an answer
		{"desktop-item", avito.SurfaceDesktop, ClassOK},
		{"mobile-search-30", avito.SurfaceMobile, ClassOK},
		{"mobile-search-empty", avito.SurfaceMobile, ClassEmpty},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			body := readFixture(t, c.fixture)
			d := avito.Document{Surface: c.surface, Body: body}
			page, err := avito.Extract(d, avito.Options{})
			got := Classify(Input{Status: 200, Doc: d, Page: page, ExtractErr: err, Warm: true})
			if got.Class != c.want {
				t.Errorf("Class = %q, want %q (detail: %s)", got.Class, c.want, got.Detail)
			}
		})
	}
}

// readFixture reads increment 1's captures from the avito package's testdata.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "avito", "testdata", name+".html.gz"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
```

with `compress/gzip`, `io`, `os` and `path/filepath` added to the imports.

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/ -run TestClassifyRealCaptures -v 2>&1 | grep -E "^( *--- |ok|FAIL)"
```

Expected: six PASSes. **`mobile-search-empty` coming out as `ClassEmpty` rather than `ClassSoftBlock` is the whole reason the classifier is per surface** — under the first draft of this design it was the second, and every quiet query would have dropped its session and re-paid a solve.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/transport/ && git commit -m "$(cat <<'MSG'
feat(transport): a classifier that reads the extraction, not just the status

On this target an empty query, a withheld hydration state and a payload our own
parser has stopped understanding are all HTTP 200 with no listings. Only the
parse tells them apart, so the extraction is an input rather than an
afterthought — and getting it wrong is what makes a monitor go quiet and look
healthy.

Rotation is reachable from exactly one class and a test enforces it. A Qrator
clearance costs nineteen to twenty-three seconds, is destroyed by any upstream
change, and rotating on a 403 throws away something that was working to fix
something rotation cannot fix.

ClassCold exists because the measured direct baseline IS a 429 carrying wall
markers. Routing that to ClassChallenge would halt mode (b) on request one and
leave it halted, since halted_reason is never cleared by the program. The same
response either side of a warm-up means two different things, and only the
caller knows which side it is on.

Run over increment 1's real captures as well as hand-written inputs: a
classifier fed only what its author imagined agrees with its author. The empty
mobile query comes out ClassEmpty rather than ClassSoftBlock, which is the
whole reason the container question is asked per surface.

MSG
)"
```

---

### Task 6: The BlankTrail Leaser — a pool of ports, each with its own address

**Files:**
- Create: `internal/transport/bt/leaser.go`, `internal/transport/bt/leaser_test.go`

**Interfaces:**
- Consumes: `blanktrail.*` (Tasks 1–2), `transport.Leaser`/`Lease`/`Caps`/`Profile` (Task 3), `fakebt.Server` (Task 1).
- Produces: `bt.Config{ControlURL, APIKey, Ports, Surface, Channels, CA}`; `func Open(ctx, Config) (*Leaser, error)`; `(*Leaser)` satisfying `transport.Leaser`.

The pool already leases the most-rested port and already refuses to hand one back before its cooldown — `Pool.take()` does both, and this task configures it rather than reimplementing it.

- [ ] **Step 1: Write the failing test**

`internal/transport/bt/leaser_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package bt

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/testutil/fakebt"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// TestLeaserSatisfiesTheSeam is a compile-time assertion written as a test so
// that its failure names the method that is missing.
func TestLeaserSatisfiesTheSeam(t *testing.T) {
	var _ transport.Leaser = (*Leaser)(nil)
}

func TestOpenLeasesAPortAndReportsItsCaps(t *testing.T) {
	srv := fakebt.New(t)
	l, err := Open(context.Background(), Config{
		ControlURL: srv.URL(), APIKey: srv.Key(), Ports: 2,
		Surface: avito.SurfaceMobile,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	if l.Mode() != transport.ModeBlankTrail {
		t.Errorf("Mode = %q", l.Mode())
	}
	caps := l.Caps()
	if !caps.SolvesChallenges {
		t.Error("SolvesChallenges = false; this is the mode that solves them, " +
			"and it is what the product is priced on")
	}

	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()
	if lease.Session() == "" {
		t.Error("Session is empty; it is what a caller keys a visit on")
	}
	if lease.Label() == "" {
		t.Error("Label is empty; it is what a person reads in a log line")
	}
	if !lease.Profile().OwnedByProxy {
		t.Error("Profile.OwnedByProxy = false in BlankTrail mode; the proxy " +
			"rewrites User-Agent and sec-ch-ua to match its own handshake, and " +
			"a second opinion from here contradicts it")
	}
}

// TestPortsAreOpenedAsHTTPConnect is not a style choice. Go's http.Transport
// resolves a socks5:// proxy's hostname LOCALLY and there is no socks5h, so the
// proxy's MITM never sees the name it needs for SNI and the certificate it
// mints — the handshake dies as an EOF. CONNECT carries the hostname.
func TestPortsAreOpenedAsHTTPConnect(t *testing.T) {
	srv := fakebt.New(t)
	l, err := Open(context.Background(), Config{
		ControlURL: srv.URL(), APIKey: srv.Key(), Ports: 1,
		Surface: avito.SurfaceMobile,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, rec := range srv.Requests() {
		if rec.Path != "/api/v1/ports/open" {
			continue
		}
		if got := rec.Body["protocol"]; got != "http" {
			t.Errorf("protocol = %v, want http — Go cannot use a socks5 port "+
				"against a MITM that needs the hostname", got)
		}
	}
}

// TestCaptchaPolicyIsSent proves the one SDK extension is actually wired, not
// merely available.
func TestCaptchaPolicyIsSent(t *testing.T) {
	srv := fakebt.New(t)
	l, err := Open(context.Background(), Config{
		ControlURL: srv.URL(), APIKey: srv.Key(), Ports: 1,
		Surface: avito.SurfaceMobile,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	seen := false
	for _, rec := range srv.Requests() {
		if rec.Path == "/api/v1/ports/open" {
			seen = true
			if rec.Body["captcha_action"] != "rotate_retry" {
				t.Errorf("captcha_action = %v, want rotate_retry", rec.Body["captcha_action"])
			}
		}
	}
	if !seen {
		t.Fatal("no port was opened")
	}
}

// TestSurfaceIsAPortProfileDecision. Measured: BlankTrail replaces the
// User-Agent with its port profile's own, so an iPhone UA through a Chrome port
// still gets the desktop site. The surface is chosen by opening the port with
// an iOS profile, not by setting a header.
func TestSurfaceIsAPortProfileDecision(t *testing.T) {
	for _, c := range []struct{ surface avito.Surface; os, browser string }{
		{avito.SurfaceMobile, "ios", "safari"},
		{avito.SurfaceDesktop, "windows", "chrome"},
	} {
		srv := fakebt.New(t)
		l, err := Open(context.Background(), Config{
			ControlURL: srv.URL(), APIKey: srv.Key(), Ports: 1, Surface: c.surface,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range srv.Requests() {
			if rec.Path != "/api/v1/ports/open" {
				continue
			}
			if rec.Body["os"] != c.os || rec.Body["browser"] != c.browser {
				t.Errorf("%s: os/browser = %v/%v, want %s/%s",
					c.surface, rec.Body["os"], rec.Body["browser"], c.os, c.browser)
			}
		}
		l.Close()
	}
}

// TestRotationIsReachableFromOneStatusOnly. CountFailure is where "rotate on
// 429 only" is encoded, at the layer that owns rotation, so no ladder above can
// override it however wrong it gets.
func TestRotationIsReachableFromOneStatusOnly(t *testing.T) {
	for status, want := range map[int]bool{429: true, 403: false, 439: false, 500: false} {
		if got := CountFailure(status); got != want {
			t.Errorf("CountFailure(%d) = %v, want %v — rotating on anything but "+
				"429 throws away a clearance worth 19-23 seconds", status, got, want)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/bt/ 2>&1 | head -5
```

Expected: FAIL — the package does not exist.

- [ ] **Step 3: Check what `fakebt` actually records**

The tests above assume `fakebt.Server` exposes `URL()`, `Key()` and `Requests()` returning records with `Path` and a decoded `Body`. Confirm before writing against it:

```bash
cd S:/Projects/Avito_Go && grep -n "^func (s \*Server)\|^type Recorded" internal/testutil/fakebt/fakebt.go
```

If the recorded shape differs — for example if it keeps the raw body rather than a decoded map — adapt the tests to what it records rather than changing `fakebt`. It is vendored.

- [ ] **Step 4: Write the Leaser**

`internal/transport/bt/leaser.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package bt is the BlankTrail half of the transport seam.
//
// Almost nothing here is new. The SDK already leases the most-rested port and
// already refuses to hand one back before its cooldown has elapsed; this
// package configures that for this target and wraps a lease so it satisfies the
// seam. What it must get right is the configuration, because three of those
// settings are the difference between a working monitor and one that pays for a
// challenge solve it then throws away.
package bt

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// CountFailure is handed to the pool and is where "rotate the egress on 429
// only" is encoded — at the layer that owns rotation, so that no ladder above
// can rotate on a 403 or a 439 however wrong it gets.
//
// A Qrator clearance is domain-wide, long-lived, costs nineteen to twenty-three
// seconds and is destroyed by any upstream change. A 403 says this identity is
// finished; a 439 says solve a challenge. Neither is fixed by a new address,
// and both are made worse by throwing away a clearance that was working.
func CountFailure(status int) bool { return status == 429 }

// Config is what opening a pool needs.
type Config struct {
	ControlURL string
	APIKey     string
	// Ports is how many identities to hold. Throughput scales with ports and
	// never with goroutines: one solve per (port, host), and parallel requests
	// to one host through one port coalesce onto a single solve.
	Ports    int
	Surface  avito.Surface
	Channels []blanktrail.Channel
	CA       []byte
	// Pace is the floor between two requests on the SAME port. The pool derives
	// its cooldown from it, so N ports serve N times as fast while each
	// identity still rests the full interval.
	Pace time.Duration
}

// DefaultPace is 3.5 seconds: the measurement was 40/40 at three-second
// spacing, and a default should not sit on the edge of what worked.
const DefaultPace = 3500 * time.Millisecond

// Leaser hands out BlankTrail ports as transport leases.
type Leaser struct {
	pool  *blanktrail.Pool
	caps  transport.Caps
	prof  transport.Profile
	ports int
}

// Open builds the pool.
func Open(ctx context.Context, cfg Config) (*Leaser, error) {
	if cfg.Ports <= 0 {
		cfg.Ports = 2
	}
	if cfg.Pace <= 0 {
		cfg.Pace = DefaultPace
	}
	cl, err := blanktrail.NewClient(cfg.ControlURL, cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("bt: control client: %w", err)
	}
	channels := cfg.Channels
	if len(channels) == 0 {
		channels = []blanktrail.Channel{blanktrail.NewDirectChannel("direct")}
	}
	pool, err := blanktrail.NewPool(ctx, blanktrail.PoolConfig{
		Client:         cl,
		Threads:        1,
		PortsPerThread: cfg.Ports,
		Spec:           spec(cfg.Surface),
		Channels:       channels,
		CA:             cfg.CA,
		DelayMin:       cfg.Pace,
		DelayMax:       cfg.Pace,
		// Rotation is caused, never scheduled.
		CountFailure: CountFailure,
	})
	if err != nil {
		return nil, fmt.Errorf("bt: open pool: %w", err)
	}
	return &Leaser{
		pool:  pool,
		ports: cfg.Ports,
		caps: transport.Caps{
			SolvesChallenges: true,
			RotatesEgress:    len(cfg.Channels) > 0,
			Addresses:        addresses(cfg),
		},
		prof: transport.Profile{OwnedByProxy: true, Surface: cfg.Surface},
	}, nil
}

// addresses is how many distinct egresses the budget may multiply by. With no
// channels configured every port shares the host's own address, and saying so
// is what stops a larger pool from looking like a larger budget.
func addresses(cfg Config) int {
	if len(cfg.Channels) == 0 {
		return 1
	}
	return cfg.Ports
}

// spec is where the surface is decided, and it is a port-profile decision
// rather than a header one. Measured: BlankTrail replaces the User-Agent with
// its port profile's own, so an iPhone UA through a Chrome/Windows port is
// still served the desktop site.
func spec(surface avito.Surface) blanktrail.PortSpec {
	s := blanktrail.DefaultPortSpec()
	// HTTP CONNECT and not SOCKS: Go's http.Transport resolves a socks5://
	// proxy's hostname locally and has no socks5h, so the MITM never sees the
	// name it needs for SNI and the certificate it mints.
	s.Protocol = "http"
	s.JSSolver = true
	s.KeepSessions = true
	s.Decompress = true
	s.H2Spoofing = true
	s.SpoofHeaders = true
	s.SpoofUserAgent = true
	// Proactive renewal off: renewing an identity discards a clearance that
	// cost nineteen to twenty-three seconds and is domain-wide and long-lived.
	s.CaptchaAction = "rotate_retry"
	s.CaptchaMaxAttempts = 3
	if surface == avito.SurfaceMobile {
		s.OS, s.Browser = "ios", "safari"
	} else {
		s.OS, s.Browser = "windows", "chrome"
	}
	return s
}

func (l *Leaser) Mode() transport.Mode  { return transport.ModeBlankTrail }
func (l *Leaser) Caps() transport.Caps  { return l.caps }
func (l *Leaser) Close() error          { return l.pool.Close() }

// Acquire blocks until a port has rested and returns the one that rested
// longest. Pool.take does the choosing; this only wraps it.
func (l *Leaser) Acquire(ctx context.Context) (transport.Lease, error) {
	lease, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &portLease{Lease: lease, prof: l.prof}, nil
}

// portLease adds the three seam methods the SDK does not have. Everything else
// — Do, Session, RotateEgress, RenewIdentity, Release — is the SDK's own.
type portLease struct {
	*blanktrail.Lease
	prof transport.Profile
}

func (p *portLease) Label() string {
	return "blanktrail:" + strconv.Itoa(p.Port())
}

func (p *portLease) Profile() transport.Profile { return p.prof }
```

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/transport/... -v 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"
```

Expected: PASS. `TestLeaserSatisfiesTheSeam` failing to compile names whichever method is missing; add it to `portLease` rather than to the vendored package.

If `RenewIdentity` is missing from the seam's viewpoint — the SDK's returns an error and takes a context, which is what the seam declares — nothing needs adding. If `PoolConfig` has no `CountFailure`, stop: that field is the load-bearing one and its absence means the copy came from an older tree.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/transport/bt/ && git commit -m "$(cat <<'MSG'
feat(transport/bt): configure the pool rather than reimplement it

The SDK already leases the most-rested port and already refuses to hand one
back before its cooldown. This package configures that for this target and
wraps a lease with the three methods the seam adds; everything else is the
SDK's own.

Three settings are load-bearing. CountFailure is 429 and nothing else, encoded
at the layer that owns rotation so no ladder above can rotate on a 403 or a 439
however wrong it gets — either would destroy a Qrator clearance that cost
nineteen to twenty-three seconds to fix something rotation cannot fix. Ports
open as HTTP CONNECT because Go resolves a socks5:// proxy's hostname locally
and has no socks5h, so the MITM never sees the name it needs. And the surface
is a port-profile decision: measured, BlankTrail replaces the User-Agent with
its port's own, so an iPhone header through a Chrome port still gets desktop.

Profile.OwnedByProxy is true here, which is the whole header-ownership rule in
one field.

MSG
)"
```

---

### Task 7: The utls dialler, and the h2 trap that makes a perfect handshake useless

**Files:**
- Create: `internal/transport/direct/dial.go`, `internal/transport/direct/profile.go`, `internal/transport/direct/dial_test.go`
- Modify: `go.mod`

**Interfaces:**
- Consumes: `transport.Profile` (Task 3).
- Produces: `direct.Fingerprint{HelloID, UserAgent, SecCHUA, Platform, AcceptEncoding, Surface}`; `direct.ChromeWindows()`, `direct.ChromeAndroid()`; `func newTransport(fp Fingerprint, dial DialFunc) (http.RoundTripper, error)`; `var ErrNotH2 error`.

Go's stock ClientHello ships no GREASE, a cipher order no browser produces, and an extension order identical in every Go binary on earth. A perfect header set over it is a Go client wearing a hat.

- [ ] **Step 1: Take the dependency**

```bash
cd S:/Projects/Avito_Go
go get github.com/refraction-networking/utls@latest
go get github.com/andybalholm/brotli@latest
go get github.com/klauspost/compress@latest
go mod tidy && grep -A6 "^require" go.mod
```

Expected: four direct requires — `golang.org/x/net`, `refraction-networking/utls`, `andybalholm/brotli`, `klauspost/compress`. `utls` pulls the last two in itself; they are listed as direct because this project decodes `br` and `zstd` bodies with them.

Then confirm the offline guard still holds:

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestPackageReachesNoNetwork 2>&1 | tail -1
```

Expected: `ok`. If it fails, something imported `direct` from `avito`, which is backwards — the domain must not know about transport.

- [ ] **Step 2: Write the failing test**

`internal/transport/direct/dial_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	utls "github.com/refraction-networking/utls"
)

// TestFingerprintBindsTheWholeIdentity. The hello id, the user agent, the
// sec-ch-ua string and the platform are one fact about one browser. Setting one
// without the others produces a client that claims to be Chrome on Windows in
// its headers and something else in its handshake, which is a stronger signal
// than either alone.
func TestFingerprintBindsTheWholeIdentity(t *testing.T) {
	for _, fp := range []Fingerprint{ChromeWindows(), ChromeAndroid()} {
		if fp.HelloID.Client == "" {
			t.Errorf("%s: no hello id", fp.Name)
		}
		if fp.UserAgent == "" || fp.SecCHUA == "" || fp.Platform == "" {
			t.Errorf("%s: an incomplete identity: ua=%q sec-ch-ua=%q platform=%q",
				fp.Name, fp.UserAgent, fp.SecCHUA, fp.Platform)
		}
		if fp.AcceptEncoding != "gzip, deflate, br, zstd" {
			t.Errorf("%s: Accept-Encoding = %q; Chrome sends all four and this "+
				"program decodes all four", fp.Name, fp.AcceptEncoding)
		}
	}
}

// TestChromeAndroidIsTheMobileSurface. In direct mode nothing rewrites our
// headers, so the header set IS the surface. This is the one direction that has
// never been tested against the live site — every mobile page in the
// measurement set arrived through a BlankTrail iOS port — and increment 2's
// first live task is to find out whether it works.
func TestChromeAndroidIsTheMobileSurface(t *testing.T) {
	fp := ChromeAndroid()
	if fp.Platform != `"Android"` {
		t.Errorf("platform = %q, want \"Android\"", fp.Platform)
	}
	if !contains(fp.UserAgent, "Android") || !contains(fp.UserAgent, "Mobile") {
		t.Errorf("user agent = %q, want an Android mobile one", fp.UserAgent)
	}
}

// TestDialTLSNegotiatesH2 is the trap this whole file exists to avoid.
//
// http.Transport.ForceAttemptHTTP2 detects h2 by type-asserting the connection
// to *tls.Conn. A *utls.UConn fails that assertion silently, and the client
// then speaks HTTP/1.1 over a perfect Chrome handshake — a combination no
// Chrome produces and a cleaner signal than a plain Go client would have been.
// So h2 is negotiated through http2.Transport.DialTLSContext instead, and the
// dialler refuses anything else rather than degrading quietly.
func TestDialTLSNegotiatesH2(t *testing.T) {
	srv := h2TestServer(t)
	conn, err := dialTLS(context.Background(), ChromeWindows(), "tcp", srv.addr, srv.rootCAs)
	if err != nil {
		t.Fatalf("dialTLS: %v", err)
	}
	defer conn.Close()
	u, ok := conn.(*utls.UConn)
	if !ok {
		t.Fatalf("conn is %T, want *utls.UConn — the whole point is that the "+
			"handshake is not Go's", conn)
	}
	if got := u.ConnectionState().NegotiatedProtocol; got != "h2" {
		t.Errorf("negotiated %q, want h2", got)
	}
}

// TestDialTLSRefusesADowngrade. A server that will not do h2 is a surprise
// worth failing on: HTTP/1.1 under a Chrome handshake is its own fingerprint.
func TestDialTLSRefusesADowngrade(t *testing.T) {
	srv := h1OnlyTestServer(t)
	_, err := dialTLS(context.Background(), ChromeWindows(), "tcp", srv.addr, srv.rootCAs)
	if err == nil {
		t.Fatal("a downgrade to HTTP/1.1 was accepted silently")
	}
	if !isNotH2(err) {
		t.Errorf("err = %v, want one wrapping ErrNotH2", err)
	}
}

// TestNoStockGoHandshakeReachesTheWire is a guard rather than a behaviour: the
// package must never construct a crypto/tls client for this target, because one
// line doing so undoes the whole file.
func TestNoStockGoHandshakeReachesTheWire(t *testing.T) {
	var cfg *tls.Config
	_ = cfg
	// The real assertion is in dial.go's own construction: it takes a
	// utls.Config and never a *tls.Config. This test exists so that changing
	// that signature breaks something with a name.
	var _ func(context.Context, Fingerprint, string, string, *x509Pool) (net.Conn, error) = dialTLS
}
```

Two helpers and a small type are needed by these tests; write them in the same file:

```go
// h2TestServer and h1OnlyTestServer start a TLS server on a loopback port with
// a throwaway certificate, offering ALPN h2 and http/1.1 respectively, and
// return its address and a pool trusting its certificate. Use
// crypto/tls.X509KeyPair over a certificate generated in-test with
// crypto/x509.CreateCertificate; the point is only to have a real handshake to
// negotiate against, so a 1024-bit RSA key and a one-hour validity are fine.
```

Write them as the test needs; nothing in production depends on their shape.

- [ ] **Step 3: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/direct/ 2>&1 | head -6
```

Expected: FAIL — `undefined: Fingerprint`.

- [ ] **Step 4: Write the profiles and the dialler**

`internal/transport/direct/profile.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/transport"
	utls "github.com/refraction-networking/utls"
)

// Fingerprint binds one browser's TLS hello to the headers that go with it.
//
// They are one fact and the struct exists so they cannot be set apart. A client
// whose headers say Chrome on Windows and whose handshake says something else
// is a stronger signal than either would have been alone.
type Fingerprint struct {
	Name           string
	HelloID        utls.ClientHelloID
	UserAgent      string
	SecCHUA        string
	Platform       string
	AcceptEncoding string
	Surface        avito.Surface
}

// Profile is the header half, for the seam.
func (f Fingerprint) Profile() transport.Profile {
	return transport.Profile{
		UserAgent:      f.UserAgent,
		SecCHUA:        f.SecCHUA,
		Platform:       f.Platform,
		AcceptEncoding: f.AcceptEncoding,
		Surface:        f.Surface,
		OwnedByProxy:   false, // nothing rewrites our headers here
	}
}

// chromeEncoding is Chrome's own set, and this program decodes all four. Saying
// br and zstd and then failing to decode them is the silent failure that reads
// downstream as "the query matched nothing".
const chromeEncoding = "gzip, deflate, br, zstd"

// ChromeWindows is the desktop identity.
//
// Chrome and not Safari, despite BlankTrail presenting iOS successfully:
// BlankTrail drives a real browser and can. x/net/http2 emits pseudo-headers in
// the order :authority, :method, :path, :scheme — Chrome's, and neither
// Firefox's nor Safari's — and offers no hook to change it. A Safari handshake
// over Chrome's framing is a self-contradiction, and worse than an honest
// Chrome.
func ChromeWindows() Fingerprint {
	return Fingerprint{
		Name:    "chrome-131-win",
		HelloID: utls.HelloChrome_131,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
			"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		SecCHUA: `"Google Chrome";v="131", "Chromium";v="131", ` +
			`"Not_A Brand";v="24"`,
		Platform:       `"Windows"`,
		AcceptEncoding: chromeEncoding,
		Surface:        avito.SurfaceDesktop,
	}
}

// ChromeAndroid is the mobile identity — and the untested one. Every mobile
// page in the measurement set arrived through a BlankTrail iOS port, so whether
// a utls client sending a mobile header set is served m.avito.ru or redirected
// to www is unverified, and it decides whether mode (b) gets the cheap surface
// at all. It is the first thing the soak run answers.
func ChromeAndroid() Fingerprint {
	return Fingerprint{
		Name:    "chrome-131-android",
		HelloID: utls.HelloChrome_131,
		UserAgent: "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 " +
			"(KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
		SecCHUA: `"Google Chrome";v="131", "Chromium";v="131", ` +
			`"Not_A Brand";v="24"`,
		Platform:       `"Android"`,
		AcceptEncoding: chromeEncoding,
		Surface:        avito.SurfaceMobile,
	}
}

// FingerprintFor picks the identity that matches a surface.
func FingerprintFor(s avito.Surface) Fingerprint {
	if s == avito.SurfaceMobile {
		return ChromeAndroid()
	}
	return ChromeWindows()
}
```

`internal/transport/direct/dial.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// ErrNotH2 is returned when the peer would not negotiate HTTP/2.
//
// It is an error rather than a fallback on purpose. HTTP/1.1 under a byte-exact
// Chrome handshake is a combination no Chrome produces, so degrading quietly
// would replace one fingerprint problem with a sharper one.
var ErrNotH2 = errors.New("direct: peer did not negotiate h2")

func isNotH2(err error) bool { return errors.Is(err, ErrNotH2) }

// x509Pool is the trust anchor set. Nil means the system's.
type x509Pool = x509.CertPool

// dialTLS opens one connection wearing the given fingerprint.
//
// ALPN offers h2 and http/1.1 in that order, because Chrome does; the result is
// then required to be h2 for the reason above.
func dialTLS(ctx context.Context, fp Fingerprint, network, addr string, roots *x509Pool) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	raw, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	conn := utls.UClient(raw, &utls.Config{
		ServerName: host,
		RootCAs:    roots,
		NextProtos: []string{"h2", "http/1.1"},
	}, fp.HelloID)
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("direct: handshake with %s: %w", host, err)
	}
	if got := conn.ConnectionState().NegotiatedProtocol; got != "h2" {
		conn.Close()
		return nil, fmt.Errorf("direct: %s negotiated %q: %w", host, got, ErrNotH2)
	}
	return conn, nil
}

// newTransport wires the dialler into an HTTP/2 transport.
//
// It is http2.Transport.DialTLSContext and NOT http.Transport with
// ForceAttemptHTTP2, and the difference is the single easiest thing to get
// wrong here. ForceAttemptHTTP2 decides whether it may speak h2 by
// type-asserting the connection to *tls.Conn. A *utls.UConn is not one, the
// assertion fails silently, and the client speaks HTTP/1.1 over a byte-exact
// Chrome handshake — which is a cleaner signal than a plain Go client would
// have been, and which nothing in the code says out loud.
func newTransport(fp Fingerprint, roots *x509Pool) http.RoundTripper {
	return &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialTLS(ctx, fp, network, addr, roots)
		},
		// This transport must never be shared between identities: a pooled
		// connection carries the previous identity's TLS session.
		DisableCompression: false,
		ReadIdleTimeout:    30 * time.Second,
	}
}
```

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/transport/direct/ -v 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"
```

Expected: five PASSes. If `utls.HelloChrome_131` does not exist in the version resolved, use the nearest `HelloChrome_*` the module offers and say which in the commit message — the identity must be one utls actually ships, not one the plan wished for.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add go.mod go.sum internal/transport/direct/ && git commit -m "$(cat <<'MSG'
feat(transport/direct): a handshake that is not Go's, and the h2 trap it dies on

Go's stock ClientHello ships no GREASE, a cipher order no browser produces, and
an extension order identical in every Go binary on earth. A perfect header set
over it is a Go client wearing a hat, so direct mode dials with utls.

The trap is worth the file's existence. http.Transport.ForceAttemptHTTP2
decides whether it may speak h2 by type-asserting the connection to *tls.Conn;
a *utls.UConn fails that assertion silently, and the client then speaks
HTTP/1.1 over a byte-exact Chrome handshake — a combination no Chrome produces
and a cleaner signal than a plain Go client would have been. So h2 is
negotiated through http2.Transport.DialTLSContext, and a peer that will not do
h2 is an error rather than a quiet downgrade.

Chrome and not Safari, although BlankTrail presents iOS successfully: it drives
a real browser and can. x/net/http2 emits pseudo-headers in Chrome's order and
offers no hook to change it, so a Safari handshake over Chrome's framing would
contradict itself.

The hello id and the headers live in one struct because they are one fact.
ChromeAndroid is shipped and marked unverified: every mobile page measured so
far arrived through a BlankTrail iOS port, so whether a utls client is served
m.avito.ru at all is the first question the soak run answers.

MSG
)"
```

---

### Task 8: The cookie jar, and why the standard one is worse than none

**Files:**
- Create: `internal/transport/direct/jar.go`, `internal/transport/direct/jar_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Jar struct{}`; `NewJar(host string) *Jar`; `(*Jar) SetCookies(*url.URL, []*http.Cookie)`; `(*Jar) Cookies(*url.URL) []*http.Cookie` (i.e. `http.CookieJar`); `(*Jar).NonceAge(now time.Time) time.Duration`; `(*Jar).Identity() string`; `(*Jar).Snapshot() JarState` / `RestoreJar(JarState) *Jar`; `const NonceName, IdentityName`.

- [ ] **Step 1: Write the failing test**

`internal/transport/direct/jar_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestJarServesALapsedNonceAnyway is the reason this jar is written rather than
// taken from net/http/cookiejar.
//
// _avisc is a sixty-second anti-bot nonce. The standard jar honours Max-Age and
// simply stops returning a lapsed cookie, so a request made one second late
// goes out with NO nonce — which is strictly worse than a one-second-stale one,
// and fails as a 429 that looks like ordinary rate limiting.
func TestJarServesALapsedNonceAnyway(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/all?q=x")
	j.SetCookies(u, []*http.Cookie{{Name: NonceName, Value: "n1", MaxAge: 60}})

	j.advance(90 * time.Second)

	got := j.Cookies(u)
	found := ""
	for _, c := range got {
		if c.Name == NonceName {
			found = c.Value
		}
	}
	if found != "n1" {
		t.Fatalf("the jar dropped a lapsed nonce (%v); sending none is strictly "+
			"worse than sending a stale one, and the standard jar does exactly "+
			"the wrong thing here", got)
	}
	if age := j.NonceAge(); age < 90*time.Second {
		t.Errorf("NonceAge = %v, want at least 90s — the session re-primes off "+
			"this number", age)
	}
}

// TestJarIsScopedToOneHostAtConstruction. No cookie from this target may ever
// be sent anywhere else, and scoping at construction is what makes
// publicsuffix unnecessary.
func TestJarIsScopedToOneHostAtConstruction(t *testing.T) {
	j := NewJar("avito.ru")
	j.SetCookies(mustURL(t, "https://www.avito.ru/"),
		[]*http.Cookie{{Name: IdentityName, Value: "srv-1"}})

	for _, other := range []string{
		"https://example.com/", "https://avito.ru.evil.test/", "https://notavito.ru/",
	} {
		if got := j.Cookies(mustURL(t, other)); len(got) != 0 {
			t.Errorf("%s received %d cookies from the avito jar", other, len(got))
		}
	}
	if got := j.Cookies(mustURL(t, "https://m.avito.ru/all")); len(got) == 0 {
		t.Error("a subdomain of the scoped host received nothing")
	}
}

// TestNonceIsOverwrittenOnEveryResponse. The server reissues it every time and
// a jar that kept the first value would go stale inside a minute.
func TestNonceIsOverwrittenOnEveryResponse(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/")
	j.SetCookies(u, []*http.Cookie{{Name: NonceName, Value: "n1"}})
	j.advance(10 * time.Second)
	j.SetCookies(u, []*http.Cookie{{Name: NonceName, Value: "n2"}})

	if j.NonceAge() > time.Second {
		t.Errorf("NonceAge = %v after a fresh value; the age must reset with it",
			j.NonceAge())
	}
	for _, c := range j.Cookies(u) {
		if c.Name == NonceName && c.Value != "n2" {
			t.Errorf("nonce = %q, want n2", c.Value)
		}
	}
}

// TestIdentitySurvivesASnapshot. srv_id is the long-lived identity and the
// expensive thing: it is what a restart must not throw away.
func TestIdentitySurvivesASnapshot(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/")
	j.SetCookies(u, []*http.Cookie{
		{Name: IdentityName, Value: "srv-42"},
		{Name: NonceName, Value: "n1"},
	})
	restored := RestoreJar(j.Snapshot())
	if restored.Identity() != "srv-42" {
		t.Errorf("Identity after a round trip = %q, want srv-42", restored.Identity())
	}
}

// TestDeletionIsHonoured. A jar that ignored Max-Age entirely would keep a
// cookie the server explicitly cleared, which is a different bug from the one
// this jar exists to avoid.
func TestDeletionIsHonoured(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/")
	j.SetCookies(u, []*http.Cookie{{Name: "v", Value: "1"}})
	j.SetCookies(u, []*http.Cookie{{Name: "v", Value: "", MaxAge: -1}})
	for _, c := range j.Cookies(u) {
		if c.Name == "v" {
			t.Error("a cookie the server deleted is still being sent")
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/direct/ -run TestJar 2>&1 | head -5
```

Expected: FAIL — `undefined: NewJar`.

- [ ] **Step 3: Write the jar**

`internal/transport/direct/jar.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The two cookies that carry the identity on this target.
const (
	// IdentityName is the long-lived one, and the thing that gets flagged. It
	// is what a restart must not throw away.
	IdentityName = "srv_id"
	// NonceName is a sixty-second anti-bot value, reissued on every response
	// and echoed on the next request.
	NonceName = "_avisc"
)

// NonceTTL is the measured sixty seconds, and RePrimeAt is when a session goes
// and fetches a fresh one rather than risking the edge.
const (
	NonceTTL  = 60 * time.Second
	RePrimeAt = 55 * time.Second
)

// Jar is a cookie jar for exactly one host tree.
//
// It is written rather than taken from net/http/cookiejar for three reasons and
// all three are load-bearing.
//
// First, the nonce. The standard jar honours Max-Age and stops returning a
// lapsed cookie, so a request made one second late goes out with NO nonce. That
// is strictly worse than a one-second-stale one and fails as a 429 that looks
// like ordinary rate limiting — a wrong diagnosis leading to a wrong remedy.
// This jar keeps serving the last value it saw and reports its age, and the
// session re-primes at fifty-five seconds rather than waiting to find out.
//
// Second, it persists. srv_id is the expensive thing, and a restart that threw
// it away would start from the cold state every time.
//
// Third, it is scoped to one host at construction, so no cookie from this
// target can ever be sent anywhere else, and publicsuffix is not needed.
type Jar struct {
	mu    sync.Mutex
	host  string
	items map[string]string
	// nonceAt is when the nonce was last written. Age, not expiry.
	nonceAt time.Time
	// now is injectable so a test about a sixty-second window does not sleep.
	now func() time.Time
	off time.Duration
}

// JarState is the persistable form.
type JarState struct {
	Host    string            `json:"host"`
	Items   map[string]string `json:"items"`
	NonceAt time.Time         `json:"nonce_at"`
}

// NewJar makes an empty jar scoped to host and every subdomain of it.
func NewJar(host string) *Jar {
	return &Jar{host: strings.ToLower(host), items: map[string]string{}, now: time.Now}
}

// RestoreJar rebuilds a jar from a snapshot.
func RestoreJar(s JarState) *Jar {
	j := NewJar(s.Host)
	for k, v := range s.Items {
		j.items[k] = v
	}
	j.nonceAt = s.NonceAt
	return j
}

// Snapshot copies the jar out for persistence.
func (j *Jar) Snapshot() JarState {
	j.mu.Lock()
	defer j.mu.Unlock()
	items := make(map[string]string, len(j.items))
	for k, v := range j.items {
		items[k] = v
	}
	return JarState{Host: j.host, Items: items, NonceAt: j.nonceAt}
}

// advance moves this jar's clock. Tests only.
func (j *Jar) advance(d time.Duration) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.off += d
}

func (j *Jar) clock() time.Time { return j.now().Add(j.off) }

// SetCookies records what a response set. A deletion — an empty value with a
// negative Max-Age — is honoured, because ignoring the server clearing a cookie
// is a different bug from the one this jar exists to avoid.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if !j.owns(u) {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cookies {
		switch {
		case c.MaxAge < 0, c.Value == "" && c.MaxAge != 0:
			delete(j.items, c.Name)
		default:
			j.items[c.Name] = c.Value
			if c.Name == NonceName {
				j.nonceAt = j.now().Add(j.off)
			}
		}
	}
}

// Cookies returns everything this jar holds for a URL it owns — including a
// nonce past its stated lifetime, which is the whole point.
func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	if !j.owns(u) {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]*http.Cookie, 0, len(j.items))
	for k, v := range j.items {
		out = append(out, &http.Cookie{Name: k, Value: v})
	}
	return out
}

// NonceAge is how long ago the nonce was last written. A session re-primes off
// this rather than off an expiry, because the remedy for an old nonce is to go
// and get a new one, not to stop sending the old one.
func (j *Jar) NonceAge() time.Duration {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.nonceAt.IsZero() {
		return NonceTTL * 100 // never seen: as stale as it gets
	}
	return j.clock().Sub(j.nonceAt)
}

// Identity is the long-lived cookie, or "" before a warm-up.
func (j *Jar) Identity() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.items[IdentityName]
}

// owns is host-tree scoping done at construction rather than by public-suffix
// rules. "avito.ru" owns "www.avito.ru" and "m.avito.ru" and nothing else — in
// particular not "avito.ru.evil.test", which a naive suffix check would accept.
func (j *Jar) owns(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return h == j.host || strings.HasSuffix(h, "."+j.host)
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/transport/direct/ -run TestJar -v 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"
```

Expected: five PASSes. The `TestJarIsScopedToOneHostAtConstruction` case for `avito.ru.evil.test` is the one worth watching: a scoping check written as `strings.HasSuffix(h, j.host)` without the dot would accept it.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/transport/direct/ && git commit -m "$(cat <<'MSG'
feat(transport/direct): a jar that keeps serving a lapsed nonce, on purpose

net/http/cookiejar honours Max-Age and stops returning a lapsed cookie. On this
target that is the wrong behaviour in the worst way: _avisc is a sixty-second
nonce, and a request made one second late would go out with NO nonce, which is
strictly worse than a one-second-stale one and comes back as a 429 that reads
like ordinary rate limiting. A wrong diagnosis leading to a wrong remedy.

So the jar serves the last value it saw and reports its age, and the session
re-primes at fifty-five seconds rather than finding out at sixty. It persists,
because srv_id is the long-lived identity and the expensive thing. And it is
scoped to one host tree at construction, so no cookie from this target can go
anywhere else and publicsuffix is not needed.

Deletion is still honoured. Ignoring a server that explicitly clears a cookie
is a different bug from the one this jar exists to avoid, and a jar that made
both mistakes would be worse than the standard one.

The scoping test includes avito.ru.evil.test, which a suffix check written
without the leading dot accepts.

MSG
)"
```

---

### Task 9: The pacer — a floor per identity, a ceiling per address

**Files:**
- Create: `internal/transport/pace.go`, `internal/transport/pace_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Pacer struct{}`; `NewPacer(PacerConfig) *Pacer`; `(*Pacer).Wait(ctx, identity, egress string) error`; `(*Pacer).Budget() Budget`; `type Budget struct { PerAddressPerHour, Addresses, WatchesAtFiveMinutes int }`; `(*Pacer).Snapshot() / Restore`.

Two budgets at two scopes, because identities and addresses are exhausted by different things.

- [ ] **Step 1: Write the failing test**

`internal/transport/pace_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"testing"
	"time"
)

func testPacer(now *time.Time) *Pacer {
	return NewPacer(PacerConfig{
		Floor:       3500 * time.Millisecond,
		Jitter:      0, // deterministic in tests; production runs at ±20%
		Ceiling:     60,
		Window:      10 * time.Minute,
		Now:         func() time.Time { return *now },
		SleepUntil:  func(_ context.Context, t time.Time) error { *now = t; return nil },
	})
}

// TestFloorIsPerIdentity. Two identities do not wait for each other: that is
// the whole reason a pool of ports is faster than one port, and getting it
// wrong makes N ports no better than one.
func TestFloorIsPerIdentity(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	start := now

	if err := p.Wait(context.Background(), "a", "addr1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), "b", "addr2"); err != nil {
		t.Fatal(err)
	}
	if now != start {
		t.Errorf("two different identities waited %v for each other; they share "+
			"nothing", now.Sub(start))
	}

	if err := p.Wait(context.Background(), "a", "addr1"); err != nil {
		t.Fatal(err)
	}
	if got := now.Sub(start); got < 3500*time.Millisecond {
		t.Errorf("the same identity waited %v, want at least the 3.5s floor", got)
	}
}

// TestCeilingIsPerAddress. Identities sharing an address share its ceiling —
// which is what stops a larger pool on one IP from looking like a larger
// budget.
func TestCeilingIsPerAddress(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	start := now
	for i := 0; i < 60; i++ {
		id := "id" + string(rune('a'+i%20))
		if err := p.Wait(context.Background(), id, "shared"); err != nil {
			t.Fatal(err)
		}
	}
	// The sixty-first on the same address must wait for the window to move,
	// however many identities are sharing it.
	before := now
	if err := p.Wait(context.Background(), "idz", "shared"); err != nil {
		t.Fatal(err)
	}
	if now.Sub(before) <= 0 {
		t.Errorf("the 61st request on one address did not wait; the ceiling "+
			"belongs to the address, not to the identity (elapsed since start %v)",
			now.Sub(start))
	}
}

// TestSecondAddressHasItsOwnCeiling. This is the arithmetic the panel quotes
// and the reason one upstream per port matters.
func TestSecondAddressHasItsOwnCeiling(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	for i := 0; i < 60; i++ {
		if err := p.Wait(context.Background(), "a", "addr1"); err != nil {
			t.Fatal(err)
		}
	}
	before := now
	if err := p.Wait(context.Background(), "b", "addr2"); err != nil {
		t.Fatal(err)
	}
	if now != before {
		t.Errorf("a fresh address waited %v for another address's window",
			now.Sub(before))
	}
}

// TestBudgetPublishesTheArithmetic. The number the panel refuses a schedule
// with has to come from the configuration in use rather than from a constant in
// a document.
func TestBudgetPublishesTheArithmetic(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	p.SetAddresses(1)
	b := p.Budget()
	if b.PerAddressPerHour != 360 {
		t.Errorf("PerAddressPerHour = %d, want 360 (60 per 10 minutes)", b.PerAddressPerHour)
	}
	if b.WatchesAtFiveMinutes != 30 {
		t.Errorf("WatchesAtFiveMinutes = %d, want 30 — twelve passes an hour each",
			b.WatchesAtFiveMinutes)
	}
	p.SetAddresses(3)
	if got := p.Budget().WatchesAtFiveMinutes; got != 90 {
		t.Errorf("with three addresses = %d, want 90", got)
	}
}

// TestWindowSurvivesARestart. Restarting is otherwise a way to cheat the
// budget, and the budget exists because exceeding it hardens a 429 into a 403.
func TestWindowSurvivesARestart(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	for i := 0; i < 60; i++ {
		if err := p.Wait(context.Background(), "a", "addr1"); err != nil {
			t.Fatal(err)
		}
	}
	state := p.Snapshot()

	restarted := testPacer(&now)
	restarted.Restore(state)
	before := now
	if err := restarted.Wait(context.Background(), "a", "addr1"); err != nil {
		t.Fatal(err)
	}
	if now.Sub(before) <= 0 {
		t.Error("a restart reset the per-address window; restarting must not be " +
			"a way to buy sixty more requests")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/ -run 'TestFloor|TestCeiling|TestSecond|TestBudget|TestWindow' 2>&1 | head -5
```

Expected: FAIL — `undefined: NewPacer`.

- [ ] **Step 3: Write the pacer**

`internal/transport/pace.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// PacerConfig is the two budgets and the clock.
type PacerConfig struct {
	// Floor is the minimum gap between two requests on the SAME identity.
	// 3.5 seconds: the measurement was forty of forty at three-second spacing,
	// and a default should not sit on the edge of what worked.
	Floor time.Duration
	// Jitter is the fraction either side of the floor. 0.2 in production; zero
	// in a test that wants a number it can assert on.
	Jitter float64
	// Ceiling and Window are the rolling cap per EGRESS ADDRESS: sixty per ten
	// minutes, a third under the ninety that was observed to harden a 429 into
	// a 403.
	Ceiling int
	Window  time.Duration

	Now        func() time.Time
	SleepUntil func(context.Context, time.Time) error
}

// Budget is the arithmetic, published rather than documented, because the
// answer moves with the configuration and the panel refuses a schedule with it.
type Budget struct {
	PerAddressPerHour    int
	Addresses            int
	WatchesAtFiveMinutes int
}

// Pacer holds a floor per identity and a ceiling per address.
//
// Two budgets, because identities and addresses are exhausted by different
// things. A pool of ports multiplies identities; it does not multiply the exit
// address, and Avito's reported /24-level aggregation means the address has a
// ceiling of its own that more identities do not raise. Give each port its own
// upstream and the second budget multiplies with the addresses; run them all
// direct and it does not — and the panel says which, because "I added ports and
// it got banned faster" is otherwise an unanswerable support question.
type Pacer struct {
	mu        sync.Mutex
	cfg       PacerConfig
	lastSeen  map[string]time.Time   // identity -> when it last went out
	windows   map[string][]time.Time // egress -> the requests inside the window
	addresses int
}

// PacerState is the persistable form. Increment 3 moves it into
// transport_sessions and egress_windows; here it is one file.
type PacerState struct {
	LastSeen map[string]time.Time   `json:"last_seen"`
	Windows  map[string][]time.Time `json:"windows"`
}

func NewPacer(cfg PacerConfig) *Pacer {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SleepUntil == nil {
		cfg.SleepUntil = sleepUntil
	}
	if cfg.Floor <= 0 {
		cfg.Floor = 3500 * time.Millisecond
	}
	if cfg.Ceiling <= 0 {
		cfg.Ceiling = 60
	}
	if cfg.Window <= 0 {
		cfg.Window = 10 * time.Minute
	}
	return &Pacer{
		cfg:       cfg,
		lastSeen:  map[string]time.Time{},
		windows:   map[string][]time.Time{},
		addresses: 1,
	}
}

// SetAddresses records how many distinct egresses are configured, for Budget.
func (p *Pacer) SetAddresses(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 {
		n = 1
	}
	p.addresses = n
}

// Wait blocks until this identity may go out again on this address, then
// records the request against both budgets.
func (p *Pacer) Wait(ctx context.Context, identity, egress string) error {
	for {
		p.mu.Lock()
		now := p.cfg.Now()
		until := now

		if last, ok := p.lastSeen[identity]; ok {
			if t := last.Add(p.gap()); t.After(until) {
				until = t
			}
		}
		hits := p.recent(egress, now)
		if len(hits) >= p.cfg.Ceiling {
			// The oldest request in the window has to fall out of it before
			// another may go.
			if t := hits[0].Add(p.cfg.Window); t.After(until) {
				until = t
			}
		}
		if !until.After(now) {
			p.lastSeen[identity] = now
			p.windows[egress] = append(hits, now)
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()
		if err := p.cfg.SleepUntil(ctx, until); err != nil {
			return err
		}
	}
}

// gap is the floor with jitter. A metronome is itself a signal.
func (p *Pacer) gap() time.Duration {
	if p.cfg.Jitter <= 0 {
		return p.cfg.Floor
	}
	span := float64(p.cfg.Floor) * p.cfg.Jitter
	return p.cfg.Floor + time.Duration((rand.Float64()*2-1)*span)
}

// recent drops requests that have fallen out of the window. Caller holds mu.
func (p *Pacer) recent(egress string, now time.Time) []time.Time {
	var out []time.Time
	for _, t := range p.windows[egress] {
		if now.Sub(t) < p.cfg.Window {
			out = append(out, t)
		}
	}
	p.windows[egress] = out
	return out
}

// Budget publishes the arithmetic for the configuration in use.
func (p *Pacer) Budget() Budget {
	p.mu.Lock()
	defer p.mu.Unlock()
	perHour := int(float64(p.cfg.Ceiling) * float64(time.Hour) / float64(p.cfg.Window))
	// One page-one pass is one request; a five-minute interval is twelve
	// passes an hour.
	return Budget{
		PerAddressPerHour:    perHour,
		Addresses:            p.addresses,
		WatchesAtFiveMinutes: perHour / 12 * p.addresses,
	}
}

func (p *Pacer) Snapshot() PacerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	ls := make(map[string]time.Time, len(p.lastSeen))
	for k, v := range p.lastSeen {
		ls[k] = v
	}
	w := make(map[string][]time.Time, len(p.windows))
	for k, v := range p.windows {
		w[k] = append([]time.Time(nil), v...)
	}
	return PacerState{LastSeen: ls, Windows: w}
}

// Restore puts a saved state back. Without it, restarting buys sixty more
// requests on every address — and the budget exists because exceeding it
// hardens a 429 into a 403.
func (p *Pacer) Restore(s PacerState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range s.LastSeen {
		p.lastSeen[k] = v
	}
	for k, v := range s.Windows {
		p.windows[k] = append([]time.Time(nil), v...)
	}
}

func sleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/transport/ -v 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"
```

Expected: every test PASSes.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/transport/ && git commit -m "$(cat <<'MSG'
feat(transport): two budgets, because identities and addresses run out
differently

A floor of 3.5 seconds per IDENTITY — the measurement was forty of forty at
three seconds, and a default should not sit on the edge of what worked — and a
rolling ceiling of sixty per ten minutes per ADDRESS, a third under the ninety
that was observed to harden a 429 into a 403.

Keeping them at different scopes is the whole reason a pool is faster than one
port: two identities do not wait for each other. And keeping the ceiling on the
address is what stops a larger pool on one IP from looking like a larger
budget, which is otherwise an unanswerable support question.

Budget publishes the arithmetic rather than a document stating it, because the
answer moves with the configuration and the panel refuses a schedule with it.

The window survives a restart. Without that, restarting is a way to buy sixty
more requests on every address, and the budget exists precisely because
exceeding it hardens the refusal.

MSG
)"
```

---

### Task 10: The direct Leaser — sessions, warm-up, and one request in flight per identity

**Files:**
- Create: `internal/transport/direct/session.go`, `internal/transport/direct/leaser.go`, `internal/transport/direct/leaser_test.go`

**Interfaces:**
- Consumes: `Fingerprint`, `newTransport`, `Jar` (Tasks 7–8), `transport.*` (Task 3), `fakeavito.Server` (Task 4).
- Produces: `direct.Config{Surface, Sessions, Upstreams, StatePath, Roots, Now}`; `func Open(Config) (*Leaser, error)`; `(*Leaser)` satisfying `transport.Leaser`; `(*Leaser).Save() error`; `var ErrNoOtherAddress error`.

A session is one identity: one jar, one fingerprint, one egress, one mutex. The mutex is per session and not per process — two requests on two jars share nothing and are free.

- [ ] **Step 1: Write the failing test**

`internal/transport/direct/leaser_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/testutil/fakeavito"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

func TestLeaserSatisfiesTheSeam(t *testing.T) {
	var _ transport.Leaser = (*Leaser)(nil)
}

// TestCapsSayWhatDirectModeCannotDo. SolvesChallenges is false and it is the
// field the whole product is priced on: POST /web/N/firewallCaptcha/verify
// needs an X-Cube header carrying a WebGL proof of work, which no HTTP client
// can produce.
func TestCapsSayWhatDirectModeCannotDo(t *testing.T) {
	l := openTest(t, 1)
	if l.Caps().SolvesChallenges {
		t.Error("SolvesChallenges = true in direct mode; nothing here can " +
			"produce an X-Cube proof of work, and a ladder told otherwise would " +
			"retry something that cannot succeed")
	}
	if l.Mode() != transport.ModeDirect {
		t.Errorf("Mode = %q", l.Mode())
	}
}

// TestWarmUpHappensBeforeTheFirstRealRequest. A fresh cookieless request
// collects a 429 immediately; the way out is a root navigation.
func TestWarmUpHappensBeforeTheFirstRealRequest(t *testing.T) {
	srv := fakeavito.New(t)
	l := openTestAgainst(t, srv, 1)

	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()

	req, _ := http.NewRequest(http.MethodGet, srv.URL()+"/all?q=iphone", nil)
	lease.Profile().Apply(req)
	resp, err := lease.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d after a warm-up, want 200 — Acquire must leave the "+
			"identity warm, or every first request is spent learning that", resp.StatusCode)
	}
}

// TestOneRequestInFlightPerSession. Two concurrent requests on one jar
// necessarily send the same nonce, and nonce reuse is exactly what a nonce
// detects. The mutex makes that impossible rather than merely discouraged.
func TestOneRequestInFlightPerSession(t *testing.T) {
	srv := fakeavito.New(t)
	l := openTestAgainst(t, srv, 1)

	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	var mu sync.Mutex
	inFlight, peak := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			req, _ := http.NewRequest(http.MethodGet, srv.URL()+"/all?q=x", nil)
			if resp, err := lease.Do(req); err == nil {
				resp.Body.Close()
			}
			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if peak > 1 {
		t.Errorf("peak concurrency on one session = %d, want 1 — two requests "+
			"on one jar send the same nonce, which is what a nonce detects", peak)
	}
}

// TestTwoSessionsDoNotBlockEachOther is the other half. The lock is per
// identity, so mode (b) scales the way mode (a) does: more rested identities.
func TestTwoSessionsDoNotBlockEachOther(t *testing.T) {
	l := openTest(t, 2)
	a, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b, err := l.Acquire(ctx)
	if err != nil {
		t.Fatalf("a second lease could not be acquired while the first was held: %v", err)
	}
	defer b.Release()
	if a.Session() == b.Session() {
		t.Error("two leases share one session; they must be two identities")
	}
}

// TestMostRestedIsLeasedFirst mirrors what the BlankTrail pool does, so the two
// modes behave the same way from the ladder's point of view.
func TestMostRestedIsLeasedFirst(t *testing.T) {
	l := openTest(t, 2)
	first, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	used := first.Label()
	first.Release()

	second, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if second.Label() == used {
		t.Errorf("the just-used identity %q was handed out again while a rested "+
			"one was free", used)
	}
}

// TestRotateEgressWithoutAPoolIsAnHonestRefusal. With no upstreams there is
// nowhere to move to, and pretending otherwise would have the ladder believe it
// had applied a remedy.
func TestRotateEgressWithoutAPoolIsAnHonestRefusal(t *testing.T) {
	l := openTest(t, 1)
	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := lease.RotateEgress(context.Background()); err == nil {
		t.Fatal("RotateEgress succeeded with no upstreams configured")
	} else if !isNoOtherAddress(err) {
		t.Errorf("err = %v, want one wrapping ErrNoOtherAddress", err)
	}
}

// TestRenewIdentityReplacesTheJarAndTheSession. It is the remedy for 403 and
// for a soft block, both of which are bound to the identity rather than the IP.
func TestRenewIdentityReplacesTheJarAndTheSession(t *testing.T) {
	srv := fakeavito.New(t)
	l := openTestAgainst(t, srv, 1)
	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	before := lease.Session()
	if err := lease.RenewIdentity(context.Background()); err != nil {
		t.Fatalf("RenewIdentity: %v", err)
	}
	if lease.Session() == before {
		t.Error("the session did not change; a caller keys its visit identity " +
			"on this and would carry the burnt one forward")
	}
}

// TestIdentitySurvivesARestart. srv_id is the expensive thing, and a restart
// that threw it away would start cold every time — which is the state the whole
// design exists to leave.
func TestIdentitySurvivesARestart(t *testing.T) {
	srv := fakeavito.New(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	l := openTestAgainstPath(t, srv, 1, path)
	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := lease.(*sessionLease).s.jar.Identity()
	lease.Release()
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("no identity was minted by the warm-up")
	}

	restarted := openTestAgainstPath(t, srv, 1, path)
	lease2, err := restarted.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease2.Release()
	if got := lease2.(*sessionLease).s.jar.Identity(); got != id {
		t.Errorf("identity after a restart = %q, want %q", got, id)
	}
}

// TestFirst403OnARestoredJarIsNotBlamedOnTheAddress. A laptop that suspended on
// one network and resumed on another presents a full-history identity from
// nowhere. Counting that towards "this address is burnt" would have every
// laptop that changed networks overnight report its ISP as blacklisted.
func TestFirst403OnARestoredJarIsNotBlamedOnTheAddress(t *testing.T) {
	l := openTest(t, 1)
	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	s := lease.(*sessionLease).s
	s.restored = true
	if s.Note403() {
		t.Error("the first 403 on a restored jar counted towards the burnt total")
	}
	if !s.Note403() {
		t.Error("the second 403 did not count; only the first is forgiven")
	}
}
```

Write the four helpers (`openTest`, `openTestAgainst`, `openTestAgainstPath`, `isNoOtherAddress`) in the same file. `openTest` needs no server for the cases that never make a request; the others point the `Leaser` at `srv.URL()` and disable TLS by giving it a plain-HTTP dialler, because `fakeavito` is `httptest.NewServer` and the point of these tests is the session logic rather than the handshake — which Task 7 already covers.

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/direct/ -run 'TestLeaser|TestCaps|TestWarm|TestOne|TestTwo|TestMost|TestRotate|TestRenew|TestIdentity|TestFirst403' 2>&1 | head -5
```

Expected: FAIL — `undefined: Leaser`.

- [ ] **Step 3: Write `session.go`**

One identity, and everything it owns:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ErrNoOtherAddress is returned by RotateEgress when there is nowhere to move
// to. An honest refusal, because the ladder would otherwise believe a remedy
// had been applied and count an attempt against a budget it did not spend.
var ErrNoOtherAddress = errors.New("direct: no other egress address configured")

func isNoOtherAddress(err error) bool { return errors.Is(err, ErrNoOtherAddress) }

// session is one identity: one jar, one fingerprint, one egress, one lock.
//
// The lock is per session and not per process. Two concurrent requests on one
// jar necessarily send the same nonce, and nonce reuse is exactly what a nonce
// detects — but two requests on two jars share nothing and are free, which is
// how this mode scales at all.
type session struct {
	mu sync.Mutex

	name    string
	fp      Fingerprint
	jar     *Jar
	rt      http.RoundTripper
	client  *http.Client
	egress  string
	seq     int
	lastUse time.Time

	// restored says this identity came off disk rather than being minted here.
	// Its first 403 is treated as the exit address having changed rather than
	// as evidence the address is burnt.
	restored bool
	burnt    int
}

// Session is the identity string a caller keys a visit on. It moves exactly
// when the identity does.
func (s *session) Session() string {
	return fmt.Sprintf("%s#%d/%s", s.name, s.seq, s.jar.Identity())
}

// Note403 records a refusal and reports whether it counts towards the total
// that means this address is finished. The first one on a restored jar does
// not: a laptop that suspended on one network and resumed on another presents
// a full-history identity from nowhere, and conflating the two would have every
// such laptop report its ISP as blacklisted.
func (s *session) Note403() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restored {
		s.restored = false
		return false
	}
	s.burnt++
	return true
}

// warmUp performs the root navigation that mints an identity. A fresh
// cookieless request collects a 429 immediately, so this is not optional and it
// is not lazy: Acquire does it before handing the lease out, or the caller's
// first real request is spent learning it was needed.
func (s *session) warmUp(ctx context.Context, root string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root, nil)
	if err != nil {
		return err
	}
	s.fp.Profile().Apply(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("direct: warm-up: %w", err)
	}
	defer resp.Body.Close()
	if s.jar.Identity() == "" {
		return fmt.Errorf("direct: warm-up returned %d and no identity", resp.StatusCode)
	}
	return nil
}

// needsWarmUp is true before the first identity and again once the nonce is
// close enough to its sixty-second life that a request would race it.
func (s *session) needsWarmUp() bool {
	return s.jar.Identity() == "" || s.jar.NonceAge() >= RePrimeAt
}
```

- [ ] **Step 4: Write `leaser.go`**

The Leaser holds the sessions, leases the most-rested, and persists them. Mirror `blanktrail.Pool.take()` deliberately: skip the leased, take the one used longest ago, and return the exact wait when none is ready. The two modes must behave the same way from the ladder's point of view, and the SDK's version is the one that has been in production.

Key points the implementation must carry, each as a comment:

- `Acquire` warms the session it is about to hand out, and does it **before** returning the lease.
- `sessionLease.Do` takes the session's mutex for the whole request, applies the profile, and lets the jar write the response's cookies back — the nonce is reissued on every response and a jar that stored only the first would go stale inside a minute.
- `RotateEgress` moves to the next configured upstream, or returns `ErrNoOtherAddress`.
- `RenewIdentity` drops the jar, builds a fresh transport (a pooled connection carries the previous identity's TLS session), bumps `seq`, and clears `restored`.
- `Save`/`load` persist `[]JarState` plus each session's egress to `Config.StatePath`, written to a temporary file and renamed, so a crash mid-write does not leave a half-file where the identity used to be.
- `Caps{SolvesChallenges: false, RotatesEgress: len(upstreams) > 1, Addresses: max(1, len(upstreams))}`.

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/transport/... -v 2>&1 | grep -E "^(--- |PASS|FAIL|ok)"
```

Expected: every test PASSes.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/transport/direct/ && git commit -m "$(cat <<'MSG'
feat(transport/direct): sessions that warm themselves and never share a nonce

A session is one identity: one jar, one fingerprint, one egress, one lock. The
lock is per session and not per process, and that is the whole scaling story of
this mode — two requests on one jar necessarily send the same nonce, which is
exactly what a nonce detects, while two requests on two jars share nothing.

Acquire warms the identity before handing it out. A fresh cookieless request
collects a 429 immediately, so a lazy warm-up would spend the caller's first
real request learning that.

The first 403 on a jar restored from disk is not counted towards "this address
is burnt". A laptop that suspended on one network and resumed on another
presents a full-history identity from nowhere, and conflating the two would
have every such laptop report its ISP as blacklisted.

RenewIdentity builds a fresh transport rather than reusing the old one: a
pooled connection carries the previous identity's TLS session, which would
undo the renewal quietly.

MSG
)"
```

---

### Task 11: The ladder — caused remedies, and a halt that does not clear itself

**Files:**
- Create: `internal/transport/ladder.go`, `internal/transport/ladder_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `type Fetcher struct{}`; `NewFetcher(Leaser, *Pacer, FetchConfig) *Fetcher`; `(*Fetcher).Fetch(ctx, req *http.Request, surface avito.Surface) (Result, error)`; `type Result struct { Page avito.Page; Verdict Verdict; Attempts int; Session, Label string; Body []byte }`; `type HaltError struct{ Verdict; URL string; At time.Time }`.

- [ ] **Step 1: Write the failing test**

Cover exactly these, against `fakeavito`:

```go
// TestFetchSucceedsOnAWarmSession — the ordinary path, one attempt, ClassOK.
//
// TestThrottledRotatesTheEgressAndNothingElseDoes — drive each class through a
// stub Lease that records which remedy was called, and assert that
// RotateEgress was reached from ClassThrottled and from no other class.
//
// TestBurnedRenewsTheIdentityAndNeverRotates — a 403 must call RenewIdentity
// and must not call RotateEgress. Rotating would destroy a clearance worth
// nineteen to twenty-three seconds to fix something rotation cannot fix.
//
// TestChallengeInBlankTrailModeRepeatsOnTheSamePort — Caps.SolvesChallenges
// true: three attempts, same session each time, roughly 25 s apart on the
// pacer's clock.
//
// TestChallengeInDirectModeHaltsAndStopsRequesting — Caps.SolvesChallenges
// false: Fetch returns a *HaltError, and fakeavito.Requests() does not move
// for the following simulated hour however many times Fetch is called.
//
// TestShapeIsNotRetriedAtAll — ClassShape returns immediately with zero
// retries and the body attached. Repeating a request whose payload we have
// stopped understanding spends the budget on our own bug.
//
// TestEmptyIsNotAFailure — ClassEmpty returns the page, no remedy, one attempt.
//
// TestNothingReadsRetryAfter — fakeavito never sends one; assert the ladder's
// waits come from its own table by checking the elapsed pacer clock against
// 25/60/200 rather than against any header.
//
// TestLadderResetsOnTheFirstOK — after a throttle and a success, the next
// throttle waits 25 s again rather than 60.
```

Write each with the reasoning above as its doc comment.

- [ ] **Step 2: Run and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/ -run 'TestFetch|TestThrottled|TestBurned|TestChallenge|TestShape|TestEmpty|TestNothingReads|TestLadder' 2>&1 | head -5
```

Expected: FAIL — `undefined: NewFetcher`.

- [ ] **Step 3: Write the ladder**

The shape, with the reasoning that must appear as comments:

```go
// Fetch runs one request to a verdict, applying the remedy each class asks for
// and no other.
//
// The backoffs are 25 s, 60 s and 200 s, seeded from the measurements — about
// 23 s for a first offence and about 200 s after ninety requests in ten
// minutes — and they reset on the first ClassOK. Nothing here reads
// Retry-After: this target never sends one, so a ladder that waited for one
// would wait for a header that never comes.
//
// A challenge is the one branch that depends on the mode, and it depends on
// Caps rather than on Mode: with a solver, repeat on the SAME identity three
// times about twenty-five seconds apart, because rotating would throw away the
// solve in progress. Without one, halt — POST /web/N/firewallCaptcha/verify
// needs an X-Cube header carrying a WebGL proof of work, and no amount of
// retrying produces one.
//
// Halting is a state and not an incident. Fetch returns a *HaltError carrying
// the status, the URL, the marker and the time, and the caller records it; the
// program never clears it. A monitor that decided on its own that the wall had
// gone away is a monitor that resumes into it all night.
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./internal/transport/... 2>&1 | tail -4 && go vet ./...
```

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/transport/ && git commit -m "$(cat <<'MSG'
feat(transport): a ladder whose remedies are caused, never scheduled

Each class asks for one remedy and the ladder applies that one. Rotation is
reachable from ClassThrottled and from nowhere else, enforced by a test that
drives every class through a recording lease: a Qrator clearance costs
nineteen to twenty-three seconds and dies on any upstream change, so rotating
on a 403 throws away something that was working to fix something rotation
cannot fix.

The backoffs are ours — 25, 60, 200 seconds, seeded from the measured recovery
— and reset on the first ClassOK. Nothing reads Retry-After, because this
target never sends one.

A challenge branches on Caps rather than on Mode. With a solver: repeat on the
same identity, three times, about twenty-five seconds apart, because rotating
throws away the solve in progress. Without one: halt, and the test asserts the
fake origin's request counter does not move for the following simulated hour.

Halting is a state, not an incident, and the program never clears it. A monitor
that decides on its own that the wall has gone away is a monitor that resumes
into it all night.

MSG
)"
```

---

### Task 12: `avitomon fetch`

**Files:**
- Create: `cmd/avitomon/main.go`

**Interfaces:**
- Consumes: `transport.*`, `bt.*`, `direct.*`, `avito.*`.
- Produces: the binary, and the increment's demonstrable behaviour.

- [ ] **Step 1: Write it**

`avitomon fetch [-mode direct|blanktrail] [-surface mobile|desktop] [-control URL] [-key K] [-ports N] [-proxies FILE] [-state FILE] <url>` prints one line of `class`, `rule`, `path`, `listings`, `total`, `session`, `label`, `attempts`, and then the first few listings — the same shape `avitoparse` prints, so the two are comparable at a glance.

Two rules the command must follow:

- **The API key never reaches a file this program writes.** It comes from `-key` or from `AVITOMON_BLANKTRAIL_KEY`, and it is never echoed, never logged and never included in an error. The spec puts it in a settings column the panel does not display; here it is an argument that goes nowhere.
- **A halt exits non-zero and prints the boundary message**, naming the status, the marker, the URL and the fact that BlankTrail is what passes it. That message is the product's main sales moment and it should read like an explanation rather than an advertisement.

- [ ] **Step 2: The demonstrable behaviour, run**

```bash
cd S:/Projects/Avito_Go && go build ./... && go run ./cmd/avitomon fetch -mode direct \
  -surface mobile "https://m.avito.ru/all?q=iphone+15&s=104"
```

Expected, and either outcome is informative: a `class=ok` line with thirty listings, or a halt naming the marker. **This is the first live answer to whether a utls client is served `m.avito.ru` at all** — every mobile page measured so far arrived through a BlankTrail iOS port. Record which it was in `docs/measurements`, whichever it is.

```bash
cd S:/Projects/Avito_Go && AVITOMON_BLANKTRAIL_KEY=<key> go run ./cmd/avitomon fetch \
  -mode blanktrail -surface mobile -ports 2 \
  "https://m.avito.ru/all?q=iphone+15&s=104"
```

Expected: `class=ok`, thirty listings, `label=blanktrail:200NN`.

- [ ] **Step 3: Commit**

```bash
cd S:/Projects/Avito_Go && git add cmd/avitomon/ docs/measurements/ && git commit -m "$(cat <<'MSG'
feat(cmd): avitomon fetch, and the first live answer for direct mode

One command, two modes, one line of output shaped like avitoparse's so the two
are comparable at a glance. A halt exits non-zero and prints the boundary
message naming the status, the marker and the URL — that message is the
product's main sales moment and it should read like an explanation rather than
an advertisement.

The API key comes from a flag or the environment and reaches no file this
program writes, is never logged and never appears in an error.

Running it against the mobile host in direct mode is the first live test of the
one thing every measurement so far assumed: whether a utls client sending a
mobile header set is served m.avito.ru at all, or redirected to www. Every
mobile page measured to date arrived through a BlankTrail iOS port. Whichever
way it went is recorded in docs/measurements, because if it went the wrong way
direct mode is desktop-only, which doubles its request cost and halves the
watch figure the panel quotes.

MSG
)"
```

---

### Task 13: The soak run — replace the extrapolation with a number

**Files:**
- Create: `cmd/avitosoak/main.go`, `docs/measurements/2026-08-XX-soak.md`

**Interfaces:**
- Consumes: `transport.Fetcher` (Task 11), both Leasers, `avito.Extract`.
- Produces: the `avitosoak` binary, a JSONL record per request, and — the actual deliverable — `docs/measurements/2026-08-XX-soak.md` carrying three numbers that currently do not exist.

This is the task the whole increment exists to make possible, and the one the README is waiting on. Three questions, and none of them currently has an answer that is not an extrapolation from forty requests.

- [ ] **Step 1: Write the harness**

`avitosoak -mode direct|blanktrail -requests N -spacing D -arm NAME` walks one query's page one repeatedly through the real ladder, and records per request: the timestamp, the class, the status, the session, the egress label, the extraction rule, the listing count and the elapsed time. It writes JSONL, and a summary with the ban rate per block of ten requests.

It stops early on the first `ClassChallenge` in direct mode and says so. It never runs unattended: the operator watches it.

- [ ] **Step 2: Run both arms and write down what happened**

Run the direct arm first, so BlankTrail works from an address the direct arm has already annoyed — the same discipline `Avito_Parser/docs/blanktrail/RESULTS.md` used, and for the same reason: it makes the comparison harder for the paid mode rather than easier.

Answer these three, in `docs/measurements/2026-08-XX-soak.md`, with the numbers rather than adjectives:

1. **Does a warm utls session hold at 3.5 s spacing, and for how many requests?** The floor, the ceiling and the thirty-watch figure the panel quotes are all extrapolated from one forty-request arm.
2. **Do several warm jars on one address beat one?** The design assumes so and says it is unmeasured. If they do not, the direct pool collapses to one jar per address and the panel's arithmetic changes.
3. **What is the honest number to put in the README in place of "300-700"?**

- [ ] **Step 3: Correct whatever the run contradicts**

Whatever the soak says, the spec and the plan are corrected to match it rather than the other way round. If 3.5 s does not hold, the floor moves and the budget arithmetic moves with it. If the mobile surface is not served to a utls client, `ChromeAndroid` is marked unusable and direct mode is desktop-only — which doubles its request cost and halves the watch figure, and the panel must say so.

- [ ] **Step 4: Commit the measurement, then the README line it earns**

```bash
cd S:/Projects/Avito_Go && git add docs/measurements/ && git commit -m "$(cat <<'MSG'
docs: the soak run, and the number that replaces the extrapolation

<Fill in with what actually happened. State the sample size in the first
sentence. If the result is worse than the design assumed, say so plainly and
name what changes because of it — that is what this run was for.>

MSG
)"
```

---

## Done when

- `go test ./...` passes and `go vet ./...` is clean.
- `TestPackageReachesNoNetwork` still passes: nothing in this increment reached into `avito/`.
- `blanktrail`'s own suite passes, `TestPackageNamesNoTarget` included, with `avito` and `avisc` on the banned list.
- `avitomon fetch -mode blanktrail -surface mobile <url>` prints `class=ok` with thirty listings.
- `avitomon fetch -mode direct` either prints `class=ok` or halts with the boundary message — and which it was is written down in `docs/measurements`.
- Driving a 439 through direct mode halts, and `fakeavito.Requests()` does not move afterwards.
- The soak run has happened and its numbers are committed. **No throughput figure appears anywhere in the repository that this run did not produce.**

## What increment 3 inherits

`transport.Fetcher.Fetch` returns a `Result` carrying the page, the verdict, the session and the label. Increment 3's `SavePass` writes exactly those into `passes`, and its `evidential` column is computed from `Verdict.Class` and `Page.Report.Path`. The pacer's `Snapshot`/`Restore` move from one file into `transport_sessions` and `egress_windows`, keyed the way the schema already says — the per-identity floor on the session, the per-address ceiling on the address, because a ceiling stored per session hands every identity sharing an address the whole budget again on the next start.

Nothing in this increment knows what a watch is, and nothing in it should learn.
