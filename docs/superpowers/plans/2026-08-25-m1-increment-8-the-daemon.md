# Increment 8: the daemon — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the product collect by itself. One process a person starts once, running the scheduler and the background workers under supervision, so that «avitomon run --once» stops being how a pass happens.

**Architecture:** `internal/supervise` is the helper the spec names: it recovers a panic, writes it down as a `changes` row, and restarts the loop on a doubling backoff. The scheduler is one goroutine over `Store.DueWatches` sharing **one** leaser and **one** pacer across every watch, because the budget is per egress address and a pacer per watch is the arithmetic that earns a ban. `avitomon panel` starts all of it beside the web server, so the browser is finally the whole product.

**Tech Stack:** Go 1.26, no CGO. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md`, §Collection — «One scheduler goroutine, a lease pool, and one token bucket per egress address» and «Every long-lived goroutine is supervised, and a panic is a row».

**Why this is not in the increment list:** it never was. §Collection describes it in detail and increments 1–7 cut everything else; six measurement documents in a row record «планировщика нет» as debt. This is that debt.

## Global Constraints

- **AGPL-3.0-or-later** header on every new file.
- **All user-facing copy is Russian.** Identifiers, comments and commit messages English.
- **One pacer and one leaser for the whole process.** The ceiling is per address; anything per-watch multiplies it.
- **The BlankTrail key, the Telegram token and the MTProto session are never written to a file this program creates, never logged, never in an error.**
- **A halted watch stays halted.** Nothing in this increment clears one; that is a person's act and the panel already has the button.
- **`gofmt -l ./cmd ./internal ./avito` prints nothing; `go vet ./...` clean; `go test ./...` passes.**

---

## Task 1: `internal/supervise`

**Files:**
- Create: `internal/supervise/supervise.go`, `internal/supervise/supervise_test.go`

**Interfaces:**
```go
type Config struct {
    Name    string                       // goroutine name, written into the change row
    Run     func(context.Context) error  // one turn of the loop
    Every   time.Duration                // how long to wait between turns
    OnPanic func(name, stack string)     // records it; nil means only the log
    Now     func() time.Time
    Sleep   func(context.Context, time.Duration) error
    Log     func(format string, args ...any)
}

func Go(ctx context.Context, cfg Config) // starts the goroutine
func Run(ctx context.Context, cfg Config) // the loop, exported for tests
```

The behaviour the spec states, and nothing beyond it: recover a panic, write one row, log one structured line, restart after five seconds doubling to a five-minute ceiling. A turn that returns an error is logged and retried on the same backoff; a turn that returns nil resets the backoff to `Every`.

- [ ] **Step 1: Write the failing test**

The assertions that matter:

- **A panic does not take the process with it**, and the loop runs again.
- **A panic is recorded once per occurrence** with the goroutine's name and the first frames of the stack. Not the whole stack: a hundred frames in a `changes` row is a row nobody reads.
- **The backoff doubles and stops at five minutes**, asserted on the recorded sleeps rather than by waiting.
- **A clean turn resets the backoff.** Without that, one bad afternoon leaves the scheduler on a five-minute tick for ever after.
- **A cancelled context ends the loop**, promptly, and does not restart it.
- **`Run` never returns while the context is live**, whatever `Run` does — including returning nil immediately.

- [ ] **Step 2–5: implement, run, commit**

```bash
git commit -m "feat(supervise): a panic is a row, and the loop comes back"
```

---

## Task 2: The scheduler

**Files:**
- Create: `internal/collect/scheduler.go`, `internal/collect/scheduler_test.go`

**Interfaces:**
```go
type Passer interface {
    Pass(ctx context.Context, w store.Watch) error
}

type Scheduler struct {
    Store   *store.Store
    Passer  Passer
    Batch   int             // how many due watches to take per turn
    Now     func() time.Time
}

func (s *Scheduler) Turn(ctx context.Context) error
```

One turn takes the due watches, runs a pass for each **in sequence**, and returns. Sequential and not parallel, and the spec says why: on this target parallel requests to one host through one port coalesce onto a single challenge solve and the joiners simply wait, so throughput scales with ports and never with goroutines. The pool is the concurrency, and the pool is inside the leaser.

`Passer` is an interface so the test needs no network and `cmd/avitomon` keeps owning the transport wiring.

- [ ] **Step 1: Write the failing test**

The assertions that matter:

- **Only due, enabled, idle watches are taken** — `DueWatches` does that, and the test pins that the scheduler asks for nothing else.
- **A watch that halts mid-turn does not stop the others.** One blocked watch must not stop a second one that has its own address.
- **A pass error is not fatal to the turn.** Collection that stops because one watch failed once is collection that never recovers.
- **An empty turn is not an error**, and does not spin: nothing due means sleep, not a busy loop.
- **The batch is bounded.** A hundred watches due at once must not become a hundred requests in one turn with no pacing between turns — the pacer handles the rate, but a turn that never ends means the panel never sees it finish.

- [ ] **Step 2–5: implement, run, commit**

---

## Task 3: One transport for the whole process

**Files:**
- Create: `cmd/avitomon/collector.go`
- Modify: `cmd/avitomon/run.go` (extract the wiring `runCmd` currently does inline)

The single most important line in this increment: **one leaser and one pacer, built once, shared by every watch.**

`runCmd` today builds both per invocation for one watch. A scheduler that did the same would give each watch its own token bucket, and the ceiling that `internal/web/budget.go` computes — «N addresses is N times the ceiling» — would be a number the product itself disproves on the first tick.

```go
// collector owns the transport for the life of the process.
type collector struct {
    store  *store.Store
    leaser transport.Leaser
    pacer  *transport.Pacer
}

func (c *collector) Pass(ctx context.Context, w store.Watch) error {
    f := transport.NewFetcher(c.leaser, c.pacer,
        transport.FetchConfig{Surface: w.Surface})
    return runOnce(ctx, c.store, f, w)
}
```

A Fetcher per pass is cheap — it holds no state of its own, only the two shared things — and it is what carries the watch's surface.

- [ ] **Step 1: Write the failing test**

- **Two watches share one pacer.** Assert that the second watch's pass waits for the first's spacing rather than starting immediately — the whole point.
- **The leaser is closed once**, when the process stops, and not once per pass.

- [ ] **Step 2–5: implement, run, commit**

---

## Task 4: The panel collects

**Files:**
- Modify: `cmd/avitomon/panel.go`, `internal/store/panel.go` (a `collect_enabled` setting), `internal/web/watches.go` (say whether it is running)

`avitomon panel` starts the scheduler and the three background workers that already exist as one-shot calls, each under `supervise.Go`:

| goroutine | what it already is | how often |
|---|---|---|
| scheduler | Task 2 | every 15 s |
| outbox worker | `notify.Worker.Run` | every 10 s |
| series producer | `store.ProduceSeries` | every 60 s |
| retention sweeper | `store.Reclaim` | every 6 h |

The confirmation sweeper — the fifth the spec names — is **not** started here; see below.

Collection can be paused from Настройки, and the Наблюдения screen says which it is. A monitor that silently stopped collecting is the failure this whole product is arranged against, so «сбор идёт» is on the screen where the watches are, not buried in settings.

- [ ] **Step 1: Write the failing test**

- **The panel refuses to collect before a transport is chosen** — decision 3, and the banner already says so; the scheduler must not start and must not error in a loop about it.
- **Pausing stops it and the screen says so.**
- **A worker's panic reaches the panel as a row**, so «сборщик упал» is visible rather than only in a log.

- [ ] **Step 2–5: implement, run, commit**

---

## Task 5: The demonstrable — nobody touches a terminal

**Files:**
- Create: `docs/measurements/2026-08-XX-the-daemon.md`

Start `avitomon panel` on a fresh database and do nothing else. Add a watch from the browser, wait, and watch passes appear without a command being typed. Record: how long until the first pass, how many passes in an hour, what the budget line said and what actually happened, and whether the two agree.

This is increment 6's demonstrable finally being true. Say so, and say what it cost — including that a desktop watch through BlankTrail took 7m41s for one pass, measured, which at a 300-second interval means a watch that can never keep its own schedule.

That last number matters and must not be buried: **if a pass takes longer than the interval, the schedule is a fiction**, and the panel's budget arithmetic — which counts requests and not seconds — does not know it. Whether that is a defect to fix or a fact to display is a decision for whoever reads the measurement.

---

## Done when

- A panic in any supervised loop is a `changes` row of kind `worker-panicked`, carrying the name and the first frames, and the loop comes back.
- The backoff doubles from five seconds to a five-minute ceiling and resets on a clean turn.
- The scheduler takes due watches in sequence and one failure does not stop the turn.
- One leaser and one pacer serve every watch in the process, asserted by a test.
- `avitomon panel` collects with no other command, and Наблюдения says whether it is.
- Collection can be paused and resumed from the panel.
- The demonstrable is performed on live data and written up, including whether a pass fits inside its own interval.

## What this plan deliberately does not build

**The confirmation sweeper.** It is the fifth goroutine the spec names, and it is the only one that spends requests on its own initiative: one item-page fetch per listing we believe vanished, to turn a 0.3 guess into a stated fact. That is a second consumer of the same budget with its own priority question against the scheduler, and it deserves its own increment rather than being started in the same commit as the machinery it would compete with.

**The tray binary and the service units.** Named as debt in four plans now. This increment makes the daemon exist; packaging it as something Windows starts is a different problem with a different failure mode.

**Any change to the pacer or the leaser.** They already do what the spec describes. This increment shares them; it does not touch them.

**A scheduler that skips a watch whose pass takes longer than its interval.** Measured at 7m41s per desktop pass through BlankTrail against a 300-second interval, that is a real case — but «what should a watch do when it cannot keep up» is a product question with at least three defensible answers, and inventing one inside a scheduler is how it gets decided by accident. The demonstrable measures it and names it.
