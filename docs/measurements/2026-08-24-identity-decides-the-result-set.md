# The identity decides the result set

**Date:** 2026-08-24
**Increment:** M1 / 3 (the store), task 9, and the follow-up before increment 4
**Tool:** `cmd/avitoslice`, which holds ONE pool open across every pass
**Query:** `https://m.avito.ru/all?q=iphone+15&s=104`, mobile, date-sorted

The store's whole volume argument is that a second identical pass writes no
snapshot rows. The first two live passes wrote thirty each and reported thirty
arrivals on the second — a complete turnover of a date-sorted page in about two
minutes, which is not a thing markets do.

---

## §1 Direct mode: the identity decides, and it is exact

Six passes through one pool holding two persisted identities, alternating.

| | mean overlap | pairs |
|---|---|---|
| same identity | **100%** | 6 of 6 |
| different identity | **23%** | 9 of 9 |

Not "high" — a hundred percent, every pair, in the same order. Passes 1, 3 and 5
returned an identical list; so did 2, 4 and 6; and the two lists share seven
listings out of thirty.

The earlier pairwise check agrees from the other end: two passes on two *fresh*
identities shared **0 of 30**, and two on one persisted identity shared **30 of
30**.

**In direct mode the result set is a function of `srv_id`, and it is stable
inside a session.** Nothing in the measured surface said so and nothing in the
design assumed it.

## §2 BlankTrail: not the same picture, and not yet explained

The same six passes through a two-port BlankTrail pool:

```
overlap matrix
       1     2     3     4     5     6
 1     —  100%  100%   33%   93%   60%
 2  100%     —  100%   33%   93%   60%
 3  100%  100%     —   33%   93%   60%
```

Passes 1, 2 and 3 are identical — and 1 and 3 are port 20000 while 2 is port
20001. **Two different ports returned the same thirty listings.** The set then
drifts: 33%, 93%, 60%, without settling per port.

Same endpoint averaged 74% and different endpoints 62%. On six passes that
difference means nothing.

So the endpoint does **not** decide the slice here the way `srv_id` does in
direct mode. Whatever BlankTrail does to a session — it terminates the TLS,
solves the challenges and manages its own cookies — does not reproduce the
clean per-identity partition. Why is not established, and this document does
not guess.

**An earlier draft of this file said "Avito's result set depends on srv_id" as
a flat fact about the site.** That was over-claimed from the direct arm alone.
It holds in direct mode, exactly; through BlankTrail the same query returns an
unstable set that is not a function of the port.

## §3 What changed because of §1

**The identity is persisted by default.** `avitomon run` writes it beside the
database as `<db>.identities.json` unless `-state` says otherwise. It was an
optional flag; a monitor that mints a new identity per run sees a complete
turnover every tick, and every alert it sends is false. That is not a mistake a
user should be able to make by omission.

**And direct mode must hold exactly one identity per watch.** Two identities in
the pool are two markets 23% alike, and a watch taking whichever is rested
differences one against the other. This stacks with the soak's finding that
several jars on one address cost an order of magnitude in throughput
(`2026-08-23-soak.md` §4): the jar pool was already a bad trade for speed, and
it is a correctness fault for monitoring on top.

That is a constraint the collector in increment 4 has to carry, and it is not
yet enforced anywhere — `direct.Config.Sessions` still accepts any number.

## §4 What increment 4 has to do about it

**An identity change makes two passes incomparable.** The ladder renews on a
403 that three waits did not clear, and recycles the endpoint on a soft block.
Either hands the next pass a different slice — 23% alike, on this measurement.
Differenced against its predecessor, that reads as twenty-three listings
vanishing and twenty-three arriving, on schedule, for ever.

`passes.session` already records the identity, and exists for exactly this
question. The rule is therefore not only about a pass being admissible on its
own terms but about **two passes being comparable**:

> A pass may be differenced against the previous one only when both were read
> through the same session. A pass whose session differs from its predecessor's
> is evidence about the market it saw, and no evidence at all about what left.

No confirmation count and no elapsed span can filter this out, because the
second confirmation is as wrong as the first and arrives on time.

## §5 A defect the BlankTrail arm turned up on the way

Two BlankTrail passes minutes apart, from separate runs, recorded the same
session string `20000#0` and were not the same identity. `bt.Open` opens a fresh
pool, which opens a fresh port, which mints a fresh identity at the proxy — and
the SDK spells a session `port#counter` with the counter starting at zero on
every open.

That breaks the seam's contract outright: *Session changes exactly when the
identity does, and never otherwise.* Direct mode honours it, because its string
carries the real `srv_id`.

Fixed in `internal/transport/bt` rather than in the vendored SDK: the Leaser
mints a random origin at Open and prefixes it, so the strings now read
`8970dc4e4295/20000#0`. §4's comparability rule would have been unenforceable
against a string that repeats.

## §6 What is wrong with these numbers

- **One query, one hour, one machine.** Whether the partition holds for other
  queries, other sort orders or other times of day is not measured.
- **Six passes per arm.** Enough to see 100% against 23%, nowhere near enough
  to characterise the BlankTrail drift.
- **The desktop surface was not tested at all.** §1 is a statement about
  `m.avito.ru`.
- **§2 is a negative result, not an explanation.** It says the port does not
  determine the slice; it does not say what does.
