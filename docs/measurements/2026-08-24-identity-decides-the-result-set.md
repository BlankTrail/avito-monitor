# The identity decides the result set

**Date:** 2026-08-24
**Increment:** M1 / 3 (the store), task 9
**Found by:** the first two consecutive passes ever written to a database

The store's whole volume argument is that a second identical pass writes no
snapshot rows. The first two live passes wrote thirty each, and reported thirty
arrivals on the second — a complete turnover of a date-sorted page in about two
minutes, which is not a thing markets do.

---

## §1 The measurement

Same URL, `https://m.avito.ru/all?q=iphone+15&s=104`, mobile surface, direct
transport, two passes minutes apart. The only variable is whether the cookie jar
survived between them.

| | overlap | arrivals | snapshots written |
|---|---|---|---|
| a fresh identity each run | **0 of 30** | 30 | 30 |
| one persisted identity | **30 of 30** | 0 | 0 |

Sixty distinct `item_id`s across two passes in the first arm, thirty across two
in the second. The listings are not a moving window over one ordering — they are
**disjoint sets**.

## §2 What it means

**Avito's search results depend on `srv_id`.** Two visitors asking the same
date-sorted question at the same moment are shown different listings. Nothing in
the measured surface said so and nothing in the design assumed it.

The design assumed the opposite everywhere it reasoned about absence. Thread-and-
Window's whole displacement account — *in a window of k slots an item at rank r
is pushed out by k − r arrivals* — presumes that two consecutive readings are
two samples of one ordering. Between identities they are not samples of the same
thing at all.

## §3 What changed because of it

**The identity is persisted by default.** `avitomon run` writes it beside the
database as `<db>.identities.json` unless `-state` says otherwise. It was an
optional flag; a monitor that mints a new identity per run sees a complete
turnover every tick, and every alert it sends is false. That is not a
configuration mistake a user should be able to make by omission.

## §4 What increment 4 has to do about it, and has not yet

This is the part that is not fixed, and it is larger than the fix above.

**An identity renewal changes the result set.** The ladder renews on a 403 that
three waits did not clear, and it recycles the endpoint on a soft block. Either
one hands the next pass a different slice of the market. Differenced against the
pass before it, that reads as every listing vanishing and thirty new ones
arriving.

`passes.session` already records the identity, and it exists for exactly this
kind of question. The rule increment 4 needs is therefore not only about the
pass being admissible on its own terms — it is about **two passes being
comparable**:

> A pass may be differenced against the previous one only when both were read
> through the same session. A pass whose session differs from its predecessor's
> is evidence about the market it saw, and no evidence at all about what left.

Without that, the absence machinery inherits a false-positive source that no
confirmation count and no elapsed span can filter out, because the second
confirmation is as wrong as the first and arrives on schedule.

## §5 And a defect the BlankTrail arm turned up on the way

Two BlankTrail passes minutes apart shared **eight of thirty** listings — some
overlap, unlike direct mode's zero, but nothing like a stable identity's thirty.
Both passes recorded the same session string, `20000#0`.

They were not the same identity. `bt.Open` opens a fresh pool, which opens a
fresh port, which mints a fresh identity at the proxy — and the SDK spells a
session as `port#counter` with the counter starting at zero on every open. Two
different identities on port 20000 therefore both write `20000#0`.

That breaks the seam's contract outright: *Session changes exactly when the
identity does, and never otherwise.* Direct mode honours it, because its string
carries the actual `srv_id`. BlankTrail's did not, across restarts.

Fixed in `internal/transport/bt` rather than in the vendored SDK: the Leaser
mints a random origin at Open and prefixes it. A caller keying a visit on the
session string can now tell two identities apart, which is the whole basis of
the comparability rule §4 asks for — a rule that would have been unenforceable
against a string that repeats.

**Not measured, and worth measuring before increment 4 writes its gate:**

- Whether the disjointness is stable — does one identity keep seeing *its* slice
  for hours, or does the slice drift within a session too?
- Whether a two-port pool is two disjoint views of the market. §5 shows two
  *sequential* pools overlapping eight of thirty; it does not show what two
  ports alive at once see. If they differ, a watch must be pinned to a port
  rather than taking whichever is rested, and the pool stops being a throughput
  lever for monitoring.
- Whether the desktop surface behaves the same way.

The third arm of that list is cheap. The second one decides whether the pool is
usable for monitoring at all, or only for breadth-first collection where
disjointness is a feature.
