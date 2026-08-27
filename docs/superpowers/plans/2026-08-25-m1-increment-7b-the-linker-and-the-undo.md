# Increment 7B: the linker and the undo — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recognise that a listing is a seller re-publishing something they had already posted, join the two into one thread, withhold the duplicate «new listing» alert — and let a person say «no it isn't», which splits the thread and replays the alert that was withheld.

**Architecture:** `internal/repost` holds the five gates and the ladder as pure functions over a candidate pair, so every refusal is testable without a database. `internal/store/repost.go` finds candidates by `repost_key`, writes `repost_links`, honours `repost_pins` and rebuilds `listings.thread_id` with a depth-capped recursive CTE. The window producer calls the linker on each arrival, inside the pass transaction, and emits `listing-reposted` instead of `listing-appeared` when a link is made. The panel's Перепубликации screen shows every link and every proposal with the ladder's own terms, and the undo is one button.

**Tech Stack:** Go 1.26, no CGO. `modernc.org/sqlite`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md`, §Identity and lifecycle («Repost linking is advisory, gated, and biased towards missing») and increment 7 in §M1, cut into increments.

**Measurements this plan argues from:**
- `docs/measurements/2026-08-25-what-the-repost-ladder-can-see.md` — what each surface carries, and the four co-existing duplicate pairs on one page.
- `docs/measurements/2026-08-25-blanktrail-opens-the-desktop-surface.md` — the per-surface fill rates the gates will actually see.

Do not quote a number these do not contain.

## Global Constraints

- **AGPL-3.0-or-later** header on every new file.
- **All user-facing copy is Russian.** Identifiers, comments and commit messages English.
- **No fragile regexes against Avito.**
- **The asymmetry is the design and it is never traded away:** a missed link announces a repost as a new listing, which is the product's headline feature working. A wrong link silently withholds an alert and looks like data rather than a bug. When in doubt, do not link.
- **A missing signal contributes nothing.** Never renormalise a score over the signals that happen to be present.
- **`gofmt -l ./cmd ./internal ./avito` prints nothing; `go vet ./...` clean; `go test ./...` passes.** Every task ends there.

---

## File Structure

**Created:**

| file | responsibility |
|---|---|
| `internal/repost/gates.go` | the five hard gates, pure |
| `internal/repost/gates_test.go` | one test per gate, and the pin |
| `internal/repost/ladder.go` | the scoring ladder and its two auto-link rungs |
| `internal/repost/ladder_test.go` | the ladder, including «a missing signal contributes nothing» |
| `internal/store/repost.go` | candidates, links, pins, `RecomputeThread` |
| `internal/store/repost_test.go` | the store half, including the thread rebuild |
| `internal/web/reposts.go` | Перепубликации, the evidence, the undo |
| `internal/web/reposts_test.go` | the screen |

**Modified:**

| file | change |
|---|---|
| `internal/store/watches.go` | `Watch` gains `RepostWindowSec` |
| `internal/store/window.go` | link on arrival; `listing-reposted`; retract on reappearance |
| `internal/web/server.go` | routes and a tab |
| `cmd/avitomon/run.go` | `Event.RepostOf` so the rule engine suppresses |

---

## Task 1: The five gates

**Files:**
- Create: `internal/repost/gates.go`, `internal/repost/gates_test.go`

**Interfaces:**
- Produces:
  ```go
  type Listing struct {
      ItemID        int64
      SellerKey     string
      SellerQuality string   // "slug", "profile", "heuristic", ""
      RepostKey     string
      Title         string   // already folded
      DescriptionSHA string
      ImageKey      string
      PriceMinor    int64    // 0 when no price was read
      Lat, Lng      *float64
      CoordsPrecise bool
      CategoryRootID int64
      State         string   // "active", "left-window", "vanished", "closed"
      FirstSeenAt   int64
      LastSeenAt    int64
  }

  type Gate string
  const (
      GatePinned      Gate = "pinned"
      GateCoexisted   Gate = "coexisted"
      GateNotEnded    Gate = "not-ended"
      GateWindow      Gate = "window"
      GateCategory    Gate = "category"
      GateSeller      Gate = "seller"
  )

  type GateResult struct {
      Passed       bool
      Undetermined bool   // seller only: neither side identified one
      Refused      Gate   // set when Passed is false
      Why          string // Russian, shown on the panel
  }

  func Gates(newer, older Listing, pinned bool, windowSec int64) GateResult
  ```

- [ ] **Step 1: Write the failing test**

```go
// TestAPairThatCoexistedIsTwoThingsAndNotOneThingTwice.
//
// The strongest gate and the one the donor lost. Thread-and-Window put this
// veto in pass membership and then thinned pass membership at seven days, so it
// quietly stopped working after week one. Here it reads listings' own
// observation interval, which is permanent and covers every watch.
//
// Measured: one ordinary page carried FOUR pairs of listings sharing a full
// repost key with both members alive at the same moment — same seller, same
// category, same place, the same title word for word. This gate is what refuses
// them, and it is not the edge case.
func TestAPairThatCoexistedIsTwoThingsAndNotOneThingTwice(t *testing.T) {
	older := aVanished(100, 1000, 2000)
	newer := anArrival(200, 1500) // first seen before the older was last seen

	got := Gates(newer, older, false, thirtyDays)
	if got.Passed {
		t.Error("two listings alive at the same moment were linked")
	}
	if got.Refused != GateCoexisted {
		t.Errorf("refused by %q, want %q", got.Refused, GateCoexisted)
	}
	// Strictly after, so touching intervals are still two things.
	touching := anArrival(200, 2000)
	if Gates(touching, older, false, thirtyDays).Passed {
		t.Error("intervals that touch were treated as disjoint")
	}
	if !Gates(anArrival(200, 2001), older, false, thirtyDays).Passed {
		t.Error("a genuinely later listing was refused")
	}
}

// TestAPinCanNeverBeUndone.
//
// A pair a person has said are not the same thing can never be re-linked, by
// any future change to the ladder. That is what makes correcting this product
// feel like correcting it rather than arguing with it.
func TestAPinCanNeverBeUndone(t *testing.T) {
	got := Gates(anArrival(200, 3000), aVanished(100, 1000, 2000), true, thirtyDays)
	if got.Passed {
		t.Error("a pinned pair was linked")
	}
	if got.Refused != GatePinned {
		t.Errorf("refused by %q", got.Refused)
	}
}

// TestOnlyAnEndedListingCanHaveASuccessor.
//
// left-window is too weak to build a merge on: it means we stopped seeing it on
// page one, which happens to every listing on a busy query within the hour.
func TestOnlyAnEndedListingCanHaveASuccessor(t *testing.T) {
	for _, c := range []struct {
		state string
		want  bool
	}{
		{"vanished", true},
		{"closed", true},
		{"left-window", false},
		{"active", false},
	} {
		older := aVanished(100, 1000, 2000)
		older.State = c.state
		got := Gates(anArrival(200, 3000), older, false, thirtyDays)
		if got.Passed != c.want {
			t.Errorf("predecessor %q: passed=%v, want %v", c.state, got.Passed, c.want)
		}
		if !c.want && got.Refused != GateNotEnded {
			t.Errorf("predecessor %q refused by %q", c.state, got.Refused)
		}
	}
}

// TestTheGapMustBeInsideTheRepostWindow, so a listing from March is not linked
// to one in August on the strength of a title.
func TestTheGapMustBeInsideTheRepostWindow(t *testing.T) {
	older := aVanished(100, 1000, 2000)
	inside := anArrival(200, 2000+thirtyDays-1)
	outside := anArrival(200, 2000+thirtyDays+1)

	if !Gates(inside, older, false, thirtyDays).Passed {
		t.Error("a gap inside the window was refused")
	}
	got := Gates(outside, older, false, thirtyDays)
	if got.Passed {
		t.Error("a gap outside the window was allowed")
	}
	if got.Refused != GateWindow {
		t.Errorf("refused by %q", got.Refused)
	}
}

// TestDifferentSellersIsARejectionAndNotADoubt.
//
// A listing copied verbatim by somebody else is a scam clone, and that is
// precisely the thing a person must be told about — so it must produce an
// ordinary new-listing alert, not a withheld one.
func TestDifferentSellersIsARejectionAndNotADoubt(t *testing.T) {
	older := aVanished(100, 1000, 2000)
	older.SellerKey, older.SellerQuality = "brand:one", "slug"
	newer := anArrival(200, 3000)
	newer.SellerKey, newer.SellerQuality = "brand:two", "slug"

	got := Gates(newer, older, false, thirtyDays)
	if got.Passed || got.Undetermined {
		t.Errorf("two different sellers: %+v", got)
	}
	if got.Refused != GateSeller {
		t.Errorf("refused by %q", got.Refused)
	}
}

// TestAnUnknownSellerLeavesTheGateUndeterminedRatherThanOpen.
//
// Undetermined caps the outcome at a proposal that can never suppress an alert.
// Treating it as a pass would let the mobile surface — which identifies no
// seller at all, 0 of 30 — auto-link on a title, which is the false-positive
// factory this whole design is arranged to avoid.
func TestAnUnknownSellerLeavesTheGateUndeterminedRatherThanOpen(t *testing.T) {
	for _, c := range []struct{ older, newer string }{
		{"", "brand:one"}, {"brand:one", ""}, {"", ""},
	} {
		older := aVanished(100, 1000, 2000)
		older.SellerKey = c.older
		newer := anArrival(200, 3000)
		newer.SellerKey = c.newer

		got := Gates(newer, older, false, thirtyDays)
		if !got.Undetermined {
			t.Errorf("(%q, %q): Undetermined=false", c.older, c.newer)
		}
		if !got.Passed {
			t.Errorf("(%q, %q): an undetermined seller refused the pair outright",
				c.older, c.newer)
		}
	}
}

// TestAHeuristicKeyIsNeverEvidence.
//
// The schema records key_quality precisely so this rule can be enforced.
// Inventing an identity that silently merges two people is worse than declining
// to identify either, so a made-up key is treated as no key at all.
func TestAHeuristicKeyIsNeverEvidence(t *testing.T) {
	older := aVanished(100, 1000, 2000)
	older.SellerKey, older.SellerQuality = "brand:one", "heuristic"
	newer := anArrival(200, 3000)
	newer.SellerKey, newer.SellerQuality = "brand:one", "slug"

	if got := Gates(newer, older, false, thirtyDays); !got.Undetermined {
		t.Errorf("a heuristic key was accepted as a seller match: %+v", got)
	}
}

// TestTheRootCategoryMustMatch, so a phone is never a successor to a sofa.
func TestTheRootCategoryMustMatch(t *testing.T) {
	older := aVanished(100, 1000, 2000)
	older.CategoryRootID = 9
	newer := anArrival(200, 3000)
	newer.CategoryRootID = 4

	got := Gates(newer, older, false, thirtyDays)
	if got.Passed {
		t.Error("two root categories were linked")
	}
	if got.Refused != GateCategory {
		t.Errorf("refused by %q", got.Refused)
	}
}

// TestEveryRefusalSaysWhyInRussian, because the panel shows it verbatim and
// «coexisted» is an answer only to whoever wrote this code.
func TestEveryRefusalSaysWhyInRussian(t *testing.T) {
	// One case per gate, so a new gate with no wording fails here rather than
	// on the screen.
	...
}
```

Write the helpers `aVanished(id, firstSeen, lastSeen)` and `anArrival(id, firstSeen)` at the top of the file, and `const thirtyDays = 30 * 24 * 60 * 60`.

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/repost/ -v
```

- [ ] **Step 3: Write `internal/repost/gates.go`**

The order matters and is the spec's: pinned, co-existence, predecessor ended, window and category, seller. Cheapest and most decisive first; the seller gate is last because it is the only one with three outcomes.

Every refusal carries a Russian `Why`. The undetermined seller sets `Passed: true, Undetermined: true` — it does not refuse, it caps.

- [ ] **Step 4: Run the tests, then commit**

```bash
git add internal/repost/ && git commit -m "feat(repost): the five gates, and the one that has three answers"
```

---

## Task 2: The ladder

**Files:**
- Create: `internal/repost/ladder.go`, `internal/repost/ladder_test.go`

**Interfaces:**
- Consumes: `Listing` from Task 1.
- Produces:
  ```go
  type Basis string
  const (
      BasisStrong Basis = "strong"
      BasisWeak   Basis = "weak"
      BasisManual Basis = "manual"
  )

  type Verdict struct {
      State    string  // "linked", "proposed", "considered", "rejected"
      Basis    Basis
      Score    float64
      Evidence []Term  // shown verbatim on the panel
  }

  type Term struct {
      Name    string  // Russian
      Present bool
      Detail  string
  }

  func Judge(newer, older Listing, gate GateResult, candidates int) Verdict
  ```

The rungs, from the spec and nothing invented:

1. **Strong auto-link:** folded titles equal **and** one strong corroborator — an identical `ImageKey` or an identical `DescriptionSHA` — **and** exactly one surviving candidate **and** the seller gate determined.
2. **Weak auto-link:** folded titles equal **and** both sides carry address-level coordinates within ~100 m **and** a price within twenty percent **and** exactly one surviving candidate **and** the seller gate determined.
3. Anything else with the gates passed: `proposed` when the seller gate is determined, `considered` when it is undetermined.

- [ ] **Step 1: Write the failing test**

The assertions that matter:

- **A missing signal contributes nothing, ever.** A pair with title and price only must not reach the same verdict as one with title, price, description and image. Assert the score does not rise when a signal is absent and assert a title-only pair is never `linked`.
- **More than one candidate links nothing.** Two candidates, perfect evidence, and the verdict is `proposed` — the dealer with eight identical phones is the common case on this site.
- **An undetermined seller caps at `considered`** whatever the evidence.
- **The image key alone is not a link without the title.** Photos are re-used across a seller's listings.
- **Twenty percent is inclusive**, and a price of zero on either side is not a match at any percentage — an unread price is not a number.
- **Coordinates only count when both sides are address-level.** A city centroid is shared by half a district and is not evidence of anything.
- **Evidence is complete either way**: a term that was absent appears with `Present: false`, because the panel's whole job is answering «why was I not told about this listing».

- [ ] **Step 2–4: implement, run, commit**

```bash
git commit -m "feat(repost): the ladder, where a missing signal contributes nothing"
```

---

## Task 3: Candidates, links, pins and the thread rebuild

**Files:**
- Create: `internal/store/repost.go`, `internal/store/repost_test.go`
- Modify: `internal/store/watches.go` (`Watch.RepostWindowSec`, and `repost_window_sec` in `watchColumns`)

**Interfaces:**
- Produces:
  ```go
  func (s *Store) RepostCandidates(ctx context.Context, tx *sql.Tx, l repost.Listing, windowSec int64) ([]repost.Listing, error)
  func (s *Store) IsPinned(ctx context.Context, tx *sql.Tx, a, b int64) (bool, error)
  func (s *Store) SaveRepostLink(ctx context.Context, tx *sql.Tx, row RepostLinkRow) error
  func (s *Store) RecomputeThread(ctx context.Context, itemID int64) error
  func (s *Store) PinPair(ctx context.Context, a, b int64) error
  func (s *Store) RepostLinks(ctx context.Context, limit int) ([]RepostLinkRow, error)
  ```

`RecomputeThread` is the one that must not be got wrong. It walks `repost_links` with a **depth-capped** recursive CTE — the cap is what stops a cycle written by a future bug from hanging a pass — takes the oldest listing in the chain as the thread id, and writes it to every member.

- [ ] **Step 1: Write the failing test**

The assertions that matter:

- **A chain of three collapses to one thread**, and the thread id is the oldest listing's own id.
- **Splitting a link rebuilds both halves**, which is what makes a wrong link repairable rather than permanent — the property Offer Ledger's four un-rebuildable denormalisations lacked.
- **A cycle terminates.** Write one by hand and assert `RecomputeThread` returns rather than hanging.
- **The unique index refuses a second live successor** for one predecessor: `idx_repost_links_one_predecessor` exists for that, and the store must surface it as an error rather than a panic.
- **Candidates come from the key and exclude the listing itself.**
- **A candidate whose key is empty finds nothing** — the mobile surface's whole population.

- [ ] **Step 2–4: implement, run, commit**

---

## Task 4: The pass links, and the alert is withheld

**Files:**
- Modify: `internal/store/window.go`, `cmd/avitomon/run.go`

Inside the pass transaction, for each arrival whose `repost_key` is non-empty: find candidates, run the gates and the ladder, write the `repost_links` row whatever the verdict — including `rejected` — and, when the verdict is `linked`, emit `track.KindListingReposted` instead of `track.KindListingAppeared` and call `RecomputeThread`.

Then `cmd/avitomon/run.go` fills `rules.Event.RepostOf` from the link, which is what makes `suppress.Decide` return `ReasonRepost`. That field and that reason have existed since increment 5 with nothing to set them.

- [ ] **Step 1: Write the failing test**

- **A seller who closes and re-publishes one item produces `listing-reposted` and no `listing-appeared`.** The increment's own demonstrable, as a test.
- **A dealer with eight identical live phones produces zero links and eight ordinary alerts.** The co-existence gate does it, and this is the measured common case.
- **A rejected link is still written.** «Почему мне не сказали» has to be answerable for the pairs that were considered and refused, not only for the ones that were linked.
- **Linking never happens on a re-seed.** A backfill pass has nothing behind it to differ from, and a whole page arriving at once would look like a whole page of reposts.

- [ ] **Step 2–4: implement, run, commit**

---

## Task 5: Retraction on reappearance

**Files:**
- Modify: `internal/store/window.go`

If a predecessor we declared vanished is seen again by an evidential pass, it never died — so every active link from it retracts: the link goes to `retracted` with `decided_by = 'reappeared'`, `RecomputeThread` splits the chain, and `track.KindRepostRetracted` is emitted.

- [ ] **Step 1: Write the failing test**

- **A predecessor that comes back retracts its link**, and the successor gets its own thread back.
- **Only an evidential pass may retract.** A reappearance seen on a degraded pass is not evidence of anything, the same rule the absence gates keep.
- **The retraction is a change**, so the person who was told «выложено заново» is told it was wrong.

- [ ] **Step 2–4: implement, run, commit**

---

## Task 6: Перепубликации, and «Это не перепубликация»

**Files:**
- Create: `internal/web/reposts.go`, `internal/web/reposts_test.go`
- Modify: `internal/web/server.go`

The screen lists every link and proposal, newest first, each with the pair, the basis, the gap, and **the ladder's evidence verbatim** — including the terms that were absent. One button per row: «Это не перепубликация».

Pressing it pins the pair, sets the link to `rejected` with `decided_by = 'user'`, splits the thread, **and replays the withheld alert** marked as a corrected suppression.

- [ ] **Step 1: Write the failing test**

- **The screen shows why**, term by term, present and absent alike.
- **A proposal is visibly not a link**, because a proposal withheld nothing.
- **The undo pins, splits and replays** — all three, asserted separately.
- **The replayed alert says it is a correction** rather than arriving as a fresh new-listing message hours late with no explanation.
- **The pin survives**: after the undo, the same pair offered again is refused by `GatePinned`.
- **A title is escaped.**

- [ ] **Step 2–4: implement, run, commit**

The inline Telegram button the spec names is **not** in this task — see below.

---

## Task 7: The demonstrable

**Files:**
- Create: `docs/measurements/2026-08-XX-reposts.md`

The spec's own three, on real data through BlankTrail (direct mode cannot collect the desktop surface, measured):

1. A seller who closes and re-publishes produces `listing-reposted` and no duplicate alert.
2. A dealer with identical live phones produces zero links.
3. «Это не перепубликация» pins, splits and replays.

The first needs a real repost to occur, which cannot be scheduled. If none occurs in the observation window, **say so and demonstrate it on a scripted sequence instead** — and say that too, plainly, rather than presenting a synthetic run as a live one.

Record the link rate: how many arrivals produced a candidate at all, how many reached `proposed`, how many `linked`. And **the fraction of confirmed links carrying the image key**, which the spec names as a shipped metric: if it sits at zero the assumption behind that term is dead and the term retires.

---

## Done when

- Every gate refuses in Russian and a test walks all of them.
- Co-existence refuses a pair alive at the same moment, strictly.
- A pin can never be undone by any later change.
- An unknown seller caps at a proposal and never suppresses an alert; a different seller is a rejection.
- A heuristic key is never accepted as a seller match.
- More than one candidate links nothing.
- A missing signal contributes nothing, asserted directly.
- `RecomputeThread` collapses a chain, splits on removal, and terminates on a cycle.
- A repost produces `listing-reposted` and no `listing-appeared`.
- A rejected link is written, so the log can answer for it.
- A reappearing predecessor retracts its links, on an evidential pass only.
- The screen shows the evidence including its absences, and the undo pins, splits and replays.
- The demonstrable is performed and written up, including the image-key metric.

## What this plan deliberately does not build

**The inline Telegram button.** The spec names «Это не перепубликация» as a button on the message. That needs a callback endpoint, an update poller and a webhook decision — a second inbound path into this program, with its own authentication question — and none of it exists. The panel button does the whole job; the Telegram one is convenience on top, and it is named here as debt rather than half-built.

**Linking across surfaces.** A listing collected from mobile and enriched has `repost_key` built from seller and title with a zero category and location; one collected from desktop has all four terms. They land in different buckets and will not link to each other. Making them agree means deciding which terms are canonical, and that is a decision with no measurement behind it yet.

**A backfill of links over listings already collected.** The linker runs on arrival. Existing rows keep the threads they have.

**Private sellers on the item page.** 13 of 30 enrichment fetches named no seller, almost certainly because the marker read covers only `/brands/`. That is 7A's debt and it belongs there.
