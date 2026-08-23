# Direct mode, first contact with the live site

**Date:** 2026-08-23
**Increment:** M1 / 2 (transport), task 12
**Command:** `avitomon fetch -mode direct -surface mobile "https://m.avito.ru/all?q=iphone+15&s=104"`

Every mobile page measured before this one arrived through a BlankTrail iOS
port. This is the first request the direct transport — utls, our own jar, no
proxy — ever made against the real origin, and the increment was shaped around
one open question it answers.

---

## §1 The question the plan asked

> **This is the first live answer to whether a utls client is served
> `m.avito.ru` at all** — every mobile page measured so far arrived through a
> BlankTrail iOS port. Record which it was in `docs/measurements`, whichever it
> is.
>
> — plan, task 12, step 2

The concern was that a client presenting a mobile header set without a mobile
TLS fingerprint would be redirected to `www.avito.ru`. If it had been, direct
mode would be desktop-only, which roughly doubles its per-watch request cost
and halves the watch figure the panel is allowed to quote.

## §2 The answer: served, not redirected

```
mode=direct class=ok status=200 attempts=1
rule=mobile.cards.v1 path=dom listings=28 ok=true empty-declared=false total=41988
session=chrome-133-android#0/geXafu29itM746GE.p9LotVUC…mav label=direct:chrome-133-android#0
budget: 360 req/hour per address × 1 address(es) = about 30 watches at 5 min
  1  8331260843         40000.00  iPhone 15, 128 ГБ, SIM + eSIM
  …
```

Four separate things are confirmed by that one line:

| Question | Answer |
|---|---|
| Is `HelloChrome_133` + an Android header set served the mobile host? | **Yes.** 200, no redirect to `www`. |
| Does the h2-only requirement hold against the real origin? | **Yes.** `ErrNotH2` never fired. |
| Does the warm-up mint an identity without a solver? | **Yes.** A real `srv_id` came back on the root navigation. |
| Does the mobile extractor read a live page, not just a fixture? | **Yes.** `mobile.cards.v1`, 28 listings, `total=41988`. |

**Direct mode is not desktop-only.** The watch figure the panel may quote stands
at the mobile cost, not double it.

BlankTrail on the same query, same minute, for comparison:

```
mode=blanktrail class=ok status=200 attempts=1
rule=mobile.cards.v1 path=dom listings=28 ok=true total=41992
```

Same rule, same path, same count; `total` differs by 4 because the market moved
between the two requests. The two transports are interchangeable from the
extractor's point of view, which is what the seam was for.

**28 listings, not 30.** The spec says a mobile search page carries 30 cards.
Both transports returned 28 for this query, so it is the page and not the
parser. Not investigated further here — the count is data, and the extractor
reports what the page contains rather than what a document expected. Worth a
look before any code depends on a fixed page size.

## §3 What the first attempt actually returned, and why it matters

The run above is the second. The first was:

```
mode=direct class=mangled status=200 attempts=2
rule= path= listings=0 ok=false
detail: avito: body is not valid UTF-8
```

Status 200 with a minted `srv_id` — so §2's answer was already true — and an
unreadable body. The body arrived **compressed**.

Go's transports decompress gzip automatically only when they set
`Accept-Encoding` themselves. This client must set it explicitly, because
Chrome's exact set is part of the fingerprint, so Go handed back the raw bytes;
and it has never handled `br` or `zstd` at all. The two dependencies taken for
exactly this were vendored and never wired into the response path.

This is worth recording because of **how it surfaced**. An undecoded body
carries no wall markers and no listings. Read naively it is a healthy 200 over
an empty market — the exact silent failure this design was shaped around, and
the one the Python tester carried for a while as bug 998becc. What caught it was
`avito.Document.Check` refusing a body with no valid UTF-8, which the classifier
turned into `ClassMangled`.

The guard earned its place on the first live request of the package it guards.

## §4 A defect the same task exposed: an egress that was never dialled

`-proxies FILE` was in the plan's task 12 signature and had not been
implemented. Adding it surfaced something worse underneath.

`direct.Config.Upstreams` was accepted, assigned one per session, rotated
between by `RotateEgress`, and persisted to the state file — and **never
dialled through**. `newTransport` built a plain `net.Dialer`. Meanwhile
`Caps.Addresses` counts configured upstreams and `Pacer.Budget()` multiplies the
per-address hourly rate by it.

So a two-proxy install was shown twice the budget while every request left from
the host's own IP. The budget lay in the direction that gets an install banned.

Now measured working:

```
$ avitomon fetch -mode direct -proxies proxies.txt "https://m.avito.ru/all?q=…"
warn  unparsable proxy line: not-a-proxy
budget: 360 req/hour per address × 2 address(es) = about 60 watches at 5 min
error: direct: warm-up: Get "https://m.avito.ru/":
  socks connect tcp 203.0.113.10:1080->m.avito.ru:443: dial tcp …: i/o timeout
```

The addresses are TEST-NET-3, so the timeout is the expected outcome. The line
proves the two things that needed proving:

- the connection **goes through the proxy** rather than around it;
- the proxy is asked for `m.avito.ru:443` — the target name is resolved **at the
  proxy**, not here. Resolving it locally would leak the lookup from the host's
  own resolver and defeat the point of an exit address.

The TLS above the tunnel is still ours, so the fingerprint on the wire is the
one utls built. Both SOCKS5 and HTTP CONNECT are supported.

`RotateEgress` had the same shape of bug: it reassigned the field, but the
egress is captured in the transport's dial closure and pooled connections still
ran over the old address. It rebuilds the client now.

And `bt` gained `Config.Addresses`, bounding the count by the proxy pool — ten
ports over three proxies is three addresses, not ten.

## §5 What this does not answer

The soak run (task 13) is what produces the honest number to replace "300-700"
in the README. Nothing here says how long a warm utls session holds, or whether
several warm jars on one address beat one. One page is not a rate.

## §6 Environment note

`go test -race` does not run on this machine: the race detector needs cgo and
there is no gcc on PATH. The suite runs green without it locally; `-race` is
CI's job.
