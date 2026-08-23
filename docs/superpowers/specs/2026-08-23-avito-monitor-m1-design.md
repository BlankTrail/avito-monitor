# Avito Monitor — M1 design

**Date:** 2026-08-23
**Status:** design, awaiting review
**Repository:** `S:/Projects/Avito_Go` — new, public, `github.com/BlankTrail/avito-monitor`
**Licence:** AGPL-3.0-or-later

An open-source Avito parser and monitor in Go: a web panel, a collection engine, change
tracking with rules, Telegram notifications, and exports. It works through BlankTrail Proxy
and it works without it, and it says plainly which of the two you are getting.

This supersedes `SSL_Fingerprint_RND/docs/superpowers/specs/2026-07-12-avito-parser-design.md`,
whose implementation is at `S:/Projects/avito-parser` (Go, MIT, CLI) and is now archived along
with `S:/Projects/Avito_Parser` (Python). Neither is modified. What is taken from them is
knowledge, not code: the challenge markers, the two-request search shape, and the A/B
benchmark method in `Avito_Parser/docs/blanktrail/RESULTS.md`.

---

## Decisions taken before the first file

These were settled with the owner and are not open. Each one closes off an architecture
rather than a preference.

**1. AGPL-3.0-or-later, and WB_GO code is lifted.** `S:/Projects/WB_GO` is the same author's
Wildberries monitor under the same licence, and roughly five thousand lines of it are tested
code this project would otherwise write twice: `internal/rules`, `internal/track`,
`internal/export`, `internal/chart`, `internal/events`, `internal/autostart`,
`internal/winres`, the outbox, the panel's shape and `store.Volume`. The earlier MIT plan is
therefore void — AGPL and MIT do not compose in that direction. The consequence is accepted:
anyone who runs this for others over a network owes them the source, and a closed-source fork
is not possible.

**2. Both transports are first class.** Mode (a) is BlankTrail; mode (b) is a genuine pure-Go
client. Mode (b) is not a demo — it is measured, tested against a fake origin that reproduces
every measured behaviour, and it stops loudly rather than degrading quietly.

**3. The first run asks.** `settings.transport_mode` starts empty and the panel's first screen
offers the choice with the arithmetic on it, and says where the arithmetic comes from: about
thirty page-one watches at five-minute resolution per egress address — derived from the
sixty-per-ten-minutes ceiling, which is itself set a third under the ninety-in-ten-minutes
hardening that was measured, and which no sustained run has yet confirmed — plus, in direct
mode only, a hard stop at the first captcha. BlankTrail removes the captcha stop outright and
raises the ceiling in proportion to the addresses in the pool; it does not abolish the ceiling,
because the ceiling belongs to the address and not to the client. Defaulting to direct would let a user conclude the product is broken when it is
working exactly as described; defaulting to BlankTrail would contradict decision 2.

**4. M1 is the new-listing monitor, price history and Telegram.** Sources are a search URL, a
seller and a single listing. Competitor analytics, ranking heatmaps, market intelligence,
reviews and any LLM feature are out. What is kept despite not being used in M1 is kept for one
reason only — its absence would force a destructive migration later — and is named where it
appears.

**5. `listing-vanished` notifies by default, prefixed «предположительно».** A buyer watching
one flat for a fortnight needs to know the option is gone more than they need protection from
an occasional false alarm. The prefix is what makes that trade honest, and the confirmation
sweeper upgrades the same thread to a stated fact later.

**6. MTProto is in M1, at increment 5.** `api.telegram.org` is blocked for part of the Russian
audience, which is this product's audience, and a user in mode (b) with that block otherwise
has no path at all. `gotd/td` is taken as a dependency for it.

**7. No fragile regular expressions.** Extraction goes through `golang.org/x/net/html` and
`encoding/json`, never a pattern over markup. This is an owner requirement and it is also
correct: every observation in the next section says the same thing from a different angle.

---

## What was measured

Everything here was observed on 2026-08-23 against the live site, through a licensed
BlankTrail instance and through a plain client, and the fixtures are captured. Numbers that
are *not* measured are marked as such wherever they appear; the largest of them is named at
the end.

### The premise

| Client | First request to a search URL |
|---|---|
| plain `curl`, direct | **HTTP 429** with wall markers in the body |
| the same through BlankTrail `socks5h://127.0.0.1:20000` | **HTTP 200**, clean |

The prior A/B in `Avito_Parser/docs/blanktrail/RESULTS.md` measured a 98.4% ban rate for a
`curl_cffi` baseline against 6.1% for BlankTrail, on one address with no proxy, with the
baseline run *first* so BlankTrail worked from an already-burnt IP. That run's samples were
six to twenty logical requests per arm. **It does not support a "300–700 requests" claim, and
neither does anything else yet.** The soak run that would is increment 2.

**The soak run has happened** — 2026-08-23/24, `docs/measurements/2026-08-23-soak.md` — and
its answer is that no request count is the right claim. Neither transport has a ceiling in
requests: direct ran 391 fetches from one address with every one clean, and was stopped by
the operator rather than by the site. What separates the two modes is the price of a refusal,
not the count before one. The figures that replace the claim are **about 250–260 clean
requests per hour direct** and **about 360 through BlankTrail**, both from a single address
and both carrying the confounds that document lists in its §8.

### The fetch shape

`/all?q=…` on the desktop host is a 386 KB SPA shell carrying zero listings. The canonical URL
sits inside it as pure JSON:

```
<script type="mime/invalid" data-mfe-state="true">
{"loaderData":{"data":{"status":{"code":301},"redirected":true,"url":"/all/telefony/…"}}}
```

Following it yields a 2.28 MB page whose single `data-mfe-state` block is 671 KB of JSON with
`loaderData.data.catalog.items[]` — fifty listings, `totalCount`, `catalog.pager.next`,
`filtersV2`. The same document *also* server-renders fifty microdata cards
(`data-marker="item"`, `data-item-id`, `itemprop="price"`), which is an independent second
extraction path over the same bytes.

Every listing carries `id`, `categoryId`, `microCategoryId`, `locationId`, `urlPath`, `title`,
`coords{lat,lng}`, `priceDetailed{value:int, wasLowered}`, `rating{score,summary}`,
`closedItemsText`, `contacts`, `userLogo{link}`, `imagesCount`, `isXl`, `isReMapPromotion`,
`isVerifiedItem`, `isReserved`, `isMarketplace`, `hasVideo`, `contacts{phone,delivery,message}`
and **`sortTimeStamp`/`allowTimeStamp` as absolute epoch milliseconds**. Twenty of the fifty additionally carry `iva`, which holds the full description
inline, the private/company label and the seller profile.

Item pages carry **no `ld+json` and no `itemprop="price"`**; their stable handles are
`data-marker` — including `item-view/total-views` and `item-view/today-views`, so view counts
are public at the cost of one request each. Seller pages (`/brands/<slug>`, 855 KB) use the
same single `data-mfe-state` block, so one extractor serves search and seller alike.

### The two surfaces

The surface is chosen by the **BlankTrail port profile**, not by the request's User-Agent: an
iPhone UA through a `Chrome_151_win` port was still redirected `m.avito.ru` → `www.avito.ru`,
because BlankTrail replaces the UA with its port profile's own. Opening a port with
`{"browser":"safari","os":"ios"}` made the mobile host answer directly. In mode (b) nothing rewrites our headers, so the header set is **expected** to be the surface — but that direction is unverified. Every mobile page in this measurement set arrived through a BlankTrail `{"browser":"safari","os":"ios"}` port, behind a real browser's TLS and h2 stack, and a direct fetch collects a 429 on request one, so no mobile page has ever been read outside BlankTrail. Whether a utls client sending a mobile header set is served `m.avito.ru` or redirected to `www.avito.ru` decides whether mode (b) gets the cheap surface at all, and it is the first thing increment 2 answers. **Answered 2026-08-23: served.** Status 200 from `m.avito.ru`, no redirect, h2 negotiated, a real `srv_id` minted by the warm-up with no solver involved, and `mobile.cards.v1` reading listings off the live page. Direct mode is not desktop-only, and the watch figure stands at the mobile cost rather than double it. Recorded in `docs/measurements/2026-08-23-direct-mode-first-contact.md`.
`Accept-Language: ru-RU,ru;q=0.9` is ours in both modes — BlankTrail passes it through
verbatim and Avito needs it.

|  | desktop `www` | mobile `m.` |
|---|---|---|
| requests for page one | 2 (shell → canonical) | **1** |
| bytes for page one | 386 KB + 2.28 MB | **1.17–1.25 MB** |
| listings per page | **50** | 30 |
| `data-mfe-state` | one block, 671 KB | **absent** |
| DOM cards | 50, microdata | 30, `item-wrapper(<id>)` |
| publication time | **epoch ms, exact** | relative text, ~1 h resolution, and **only under `s=104`** |

Mobile cards are otherwise complete — title, price, params, address, seller name and a
`<meta itemProp="description">`, all thirty of thirty, with the id in both
`item-wrapper(<id>)` and `data-marker-id`. An older note in the Python tester says mobile
carries no price; that is out of date.

The date column deserves its own line because it was got wrong once here. Without `s=104` the
mobile page has no date anywhere — `data-marker="sortTimeGrid"` is an empty `<div>` and there
is not one relative-time string on the page. With `s=104` all thirty carry one, but every one
of them read "1 час назад", so the resolution is about an hour and page one of a busy query
turns over inside it. **On the mobile surface `sort_ts_ms` is therefore NULL**, and every
lifecycle decision rests on our own observation time — which is what the design does anyway,
so nothing breaks; but any later analytic that assumes a real publication timestamp has no
data on the primary collection surface, and that has to be said out loud once rather than
discovered.

That reading bounds turnover rather than measuring it, and the error points the unsafe way. At
about one-hour resolution, thirty cards all reading «1 час назад» say the window spans **at
most** about an hour; it is equally consistent with the window spanning five minutes. So a
five-minute poll has an upper bound of roughly a tenfold margin on this query and no lower
bound at all. That is the whole reason the saturation metric exists: `items_on_page / arrivals`
is measured continuously against real arrivals, and it is the number to trust, not this one.

### Being seen as a bot

`loaderData.toggles` carries `runtime_catalog_drop_state_for_bots` and
`runtime_catalog_bots_drop_state`; the hydration data carries `buyerLocationProps.isBot`. Avito
can serve a page with its SSR state removed to a client it has classified. The DOM fallback is
therefore not redundancy, it is the other half of the parser — and *which path answered* is a
first-class metric **on the desktop surface**, because there a silent switch from `state` to
`dom` is the earliest available warning that this install has been flagged, earlier than a 429
and much earlier than a captcha.

On mobile that signal does not exist and cannot be manufactured: there is no hydration
catalogue to withhold, so `dom` is what a healthy pass has always answered. The mobile
equivalent is **card yield** — a watch's `items_seen` against the median of its last ten `ok`
passes, where a fall below half on a 200 with no wall marker and a non-zero
`__initialData__.search.count` means the page is being served to us differently. It needs ten
`ok` passes before it means anything, so until a watch has them the alarm is not armed and the
health screen says «набирается база» rather than showing a ratio against one sample — a
detector that fires on its own first reading teaches people to ignore it on the second day. It fires
`extraction-fell-back` by the same route and shows on the same panel line, so the health screen
reads the same on both surfaces even though the underlying tell is different. Saying this once
is what stops "the drift alarm" being quoted later as an unconditional property of the
product.

### The transport facts the ladder is built from

- Deep pagination is the loudest ban trigger; walking past page one or two escalates 429 to 403.
- A search caps at roughly 5000 results (~100 pages × 50).
- One warm cookie jar sustained forty of forty at three-second spacing on one address. A fresh
  cookieless session per request collects a 429 immediately. **The scarce resource is a rested
  identity.**
- `_avisc` is a sixty-second nonce refreshed on every response and echoed on the next request;
  `srv_id` is the long-lived identity and is what gets flagged; `v` lapses at thirty minutes.
- Recovery is about 23 s for a first offence and about 200 s after ninety requests in ten
  minutes, hardening from 429 to 403 along the way. **There is never a `Retry-After` header.**
- HTTP **439** is an Avito/Qrator status meaning challenge.
- A Qrator clearance is domain-wide, long-lived, costs 19–23 s to obtain, and is destroyed by
  any upstream change. Rotate the egress on 429 only.
- One solve per (port, host): parallel requests to one host on one port coalesce onto one
  solve. **Throughput scales with ports, never with goroutines.**
- Avito serves `br` and `zstd` and often omits `charset=`. A body that reaches a reader
  compressed produces no markers and no items, so a wall reads as a healthy 200 and a parse
  failure reads as an empty market. Both are silent; both have cost this codebase's ancestors a
  debugging session already.
- `POST /web/N/firewallCaptcha/verify` requires an `X-Cube` header carrying a WebGL proof of
  work — a hidden canvas, fifty frames, `gl.readPixels` timing. No HTTP client can produce it.
  **This is the capability boundary between the two modes**, and it is why mode (b) stops
  rather than retries.

### The BlankTrail control API, as it actually answered

`http://127.0.0.1:8891`, header `X-API-Key`. `GET /api/v1/health` → `{"status":"ok"}`.
`GET /api/v1/license/status` returns `activated`, `plan`, `pool` and `js_solver_max_procs` —
which must be read at startup, because a licence with no solver capacity still opens ports and
then relays every challenge page unchanged.

A live port's configuration reports several fields the WB_GO SDK's `openPortRequest` does not
carry: `captcha_action` (`"rotate_retry"`), `captcha_max_attempts`, `vdns_mode`,
`cache_enabled`, `cache_mode` and `auto_profile_from_ua`. Two more that a port reports —
`leak_guard` and `max_concurrent` — are already in the struct and already set by
`PortSpec.request`, and `effective_idle_seconds` is the server's echo of the `idle_seconds` the
SDK sends; naming them as gaps would have inflated the case for touching the SDK at all.
The captcha pair is exactly the policy this target needs, so the vendored SDK is extended with
it — additively, and with one deliberate edit to the guard that must not be made silently.
`neutrality_test.go` bans the substring `captcha` and scans every non-test `.go` file in the
package, so a `CaptchaAction` field fails on the commit that introduces it. `captcha` therefore
comes off the banned list, and the test's own comment records why: it is the name of a control
the proxy exposes for any target, not the name of a target or of a defence vendor, and the list
exists to stop the package learning about *sites*. `avito` and `avisc` go **on** the list in the
same commit, so the guard ends up stricter about the thing it was written to prevent. Editing a
guard to let a change through is exactly how guards die, which is why the reasoning is in the
file rather than in a commit message.

---


# The design

## What this is, and where it came from

The spine is **Thread-and-Window**: WB_GO's proven shape — a stable half, an append-only fingerprinted volatile half with anchor rows, `track.Diff` into named changes, `rules`, `notify_outbox` — plus the two things Avito forces and nothing else. It was chosen because its lift claims survive contact with the donor. I checked them: `internal/rules/rules.go`, `predicate.go` and `suppress.go` import `internal/track` and nothing else, so deleting WB_GO's second catalogue (`wb.EventKind`, `events`, `event_changes`) removes work rather than adding it; `thinSnapshotsSQL` runs unchanged against this schema with `(item_id, surface)` substituted for `(nm_id, dest, app_type)`, verified by executing it; and the one BlankTrail extension it needs is reachable from code the SDK already ships.

Three things are grafted in, and each closes a hole the spine has.

From **Offer Ledger**: `passes.evidential`. A pass may be used as evidence that something is *gone* only when it passes every admissibility test at once. Without that column, a soft block — HTTP 200, no catalogue, no wall marker — is differenced as a real reading and fires a removal alert for every listing in the watch. That is the failure this whole target is shaped around, and it is the one Thread-and-Window walks into unaided. Its two-gate absence rule (a confirmation count **and** an elapsed span), its seed-pass suppression, and its saturation metric come with it.

From **Observation Ledger**: the classifier takes the *extraction*, not just the body, and splits four ways where one way would do. On this target an empty query, a dropped hydration state and a payload we have stopped understanding are all HTTP 200 with no listings, and only the parse tells them apart. That split is also what repairs Offer Ledger's own defect — its `evidential` demanded `items_seen > 0`, so a watch whose result set legitimately emptied could never confirm the removal of its last listing. Here `ClassEmpty` means the surface's own result container was present and reported zero — `loaderData.data.catalog` with `totalCount == 0` on desktop, `__initialData__.search.count == 0` corroborated by `search.emptySearch` on mobile — which is a real answer, and it counts as evidence.

From the specialist work: displacement accounting, the regex-free hydration-state reader, and the utls transport.

Everything the milestone does not need is cut. There is no `offers` layer, no `job_runs`, no `sightings`/`presences` ledger, no rebuild-from-log deriver, no positions table, no reviews, no competitor grain. What is kept despite not being used in M1 is kept for one reason only: its absence would force a destructive migration later. That list is short and named as it appears.

---

## Identity and lifecycle

**Three identities, and only three.**

`listings.item_id` is Avito's own number, `INTEGER PRIMARY KEY`. It is stable for the life of one posting and never reused, so it is the key. It is not the identity of the thing being sold: a seller who closes and re-publishes starts a new number for the same intent, and a monitor that could not see that would report every repost as news.

`listings.thread_id` groups one advertisement's incarnations. It is `products.match_id` turned ninety degrees — WB's match id groups every *seller's* listing of one physical product, this groups one *listing's* lives over time — and it is denormalised so a price chart is one indexed scan rather than a recursive query per draw. It is the only written derived value in the schema, and it has a rebuild: `Store.RecomputeThread(itemID)` walks `repost_links` with a depth-capped recursive CTE. A wrong link is therefore repairable rather than permanent, which is the property Offer Ledger's four un-rebuildable denormalisations lacked.

`sellers.id` is a surrogate, with `seller_key` as a unique natural key beside it. What a search row actually carries is `userLogo.link` for a company or `iva.UserInfoStep[].payload.profile` for a private seller, and only about twenty rows in fifty carry the second. Those two spellings are not one identity, and a private seller who changes their profile path forks into two rows. With a surrogate the fork is repaired later by repointing `listings.seller_id`; with a TEXT primary key it could only be repaired by rewriting every table that names a seller. `key_quality` records how the key was obtained, and a heuristic key is never accepted as repost evidence — inventing an identity that silently merges two people is worse than declining to.

**Absence is graded, and the grade is structural.**

Page one of a date-sorted query is a window, not a census. A listing leaves it because it was removed, because a newer posting pushed it off the bottom, or because a paid promotion reshuffled the block, and the response says which of the three it was for none of them. So `state_source` is a column and only two of its three values may write a terminal state: `search` may write `left-window` and `vanished`, `item-page` and `user` may write `closed`. `state_confidence` carries the same distinction into every event, on the axis `wb.Event` already uses — 1.0 for a fact the item page stated, 0.3 for a conclusion drawn from something not being in a window.

**Displacement accounting removes most of the guessing.** In a date-sorted window of *k* slots, an item at rank *r* is pushed out by *k − r* arrivals. That is exact arithmetic over two integers this schema already stores (`watch_members.last_position`, `watch_members.window_size`) and one it counts per pass (`passes.arrivals`). An absence explained by arrivals is *displacement*: the member is marked `displaced`, its streak does not advance, and nothing is emitted. An absence with headroom to spare is a candidate disappearance. This costs no extra request and it is the difference between "gone" and "we only look at the first page".

A disappearance is then believed only when both gates pass: `missing_streak >= watches.absence_confirmations` (2) **and** `now - missing_since >= watches.absence_min_span_sec` (1200). Two passes thirty seconds apart is one ranking blip; the span gate is what makes the count mean elapsed time rather than poll count.

**A gap in collection is not evidence of anything, and the design has to say so, because for a laptop this is a weekly event.** When a watch's previous evidential pass is older than three times its interval, the next pass is a *re-seed*: its members are recorded, its differences are marked `backfill`, and it may not confirm a disappearance. Otherwise a machine that slept from Friday to Monday wakes up, sees a page one that has turned over completely, and reports every listing it was watching as gone — one weekend producing a hundred false removals is how a person learns to ignore the notifications. The same gate covers a laptop that changed networks, a BlankTrail restart and a paused install; none of them are evidence about Avito.

The counter is per `(watch, listing)` and never per listing. A listing belongs to several watches, and absence from one is not absence from the listing. The listing-level verdict is one query — `NOT EXISTS (SELECT 1 FROM watch_members WHERE item_id = ? AND present = 1)` — run at the end of each pass. That is an index seek, and it deliberately replaces Offer Ledger's second, cross-watch counter, whose "absent from every watch in that tick's window" was undefined the moment two watches had different intervals.

**Repost linking is advisory, gated, and biased towards missing.**

`repost_key` is `sha256` over seller key, micro-category, location and the folded title, computed once at insert and never updated — a title edited after a link was made must not move the key that link was made against. Folding is `unicode`-driven case folding, whitespace collapse and punctuation stripping through a `strings.Builder`; no regex, which is also correct rather than merely required, because Cyrillic case folding and `regexp` do not mix well.

Candidates come from one index seek on `idx_listings_repost_key`, then five hard gates, in order:

1. Not in `repost_pins` — a pair a person has said are not the same thing can never be re-linked, by any future change to the ladder.
2. **No observed co-existence**: `new.first_seen_at > old.last_seen_at`, strictly. If both were ever alive at once they are two things, not one thing twice. Thread-and-Window put this veto in pass membership and then thinned pass membership at seven days, so the veto quietly stopped working after week one. Here it reads `listings`' own observation interval, which is permanent, covers every watch rather than the ones whose pass rows survived, and needs no history at all.
3. The predecessor is `vanished` or `closed`. `left-window` is too weak to build a merge on.
4. The gap is inside `watches.repost_window_sec` (30 days), and the root category matches.
5. **Seller is a gate, not a score.** Both known and equal passes. Either unknown leaves the gate *undetermined*, which caps the outcome at a proposal that can never suppress an alert. Different sellers is a rejection — a listing copied verbatim by somebody else is a scam clone, and that is precisely the thing a person must be told about.

Then a small deterministic ladder, whose one rule is that **a missing signal contributes nothing**. A weighted average that renormalises over present signals lets a title-and-price-only pair reach the same score as a title-price-description-image pair, and since description and images appear on only twenty rows in fifty, that is a false-positive factory. So: an auto-link needs the folded title to match *and* one strong corroborator — a shared CDN image object, or an identical description digest — *and* exactly one surviving candidate. Title plus exact address-level coordinates plus a price within twenty percent, again with exactly one candidate, is the weaker auto-link. Anything else writes a `proposed` or `considered` row and the ordinary new-listing alert fires with a line attached.

The image key needs no knowledge of Avito's URL grammar: a photo arrives as roughly six size variants sharing one object id and differing in a trailing token, so the longest common prefix across the variants *is* the object id. Whether that id survives a re-upload is unverified, so it is one term among several and the fraction of confirmed links carrying it is a shipped metric — if it sits at zero the assumption is dead and the term retires.

The asymmetry is deliberate and stated: a missed link means a repost is announced as a new listing, which is the product's headline feature working; a wrong link silently withholds an alert and looks like data rather than like a bug. More than one candidate therefore links nothing. A dealer with eight identical phones is the common case on this site, not the edge case.

Undo is cheap because nothing was merged. Deleting the belief is the whole of it: the link goes to `rejected`, the pair is pinned, `RecomputeThread` splits the chain, and **the suppressed alert is replayed**, marked as a corrected suppression. Reappearance does the same thing automatically: if a predecessor we declared vanished is seen again by an evidential pass, it never died, so every active link from it retracts.

---

## Storage

One migration, twenty-one tables, all `STRICT`, all timestamps whole Unix seconds — with two exceptions named for what they are, `sort_ts_ms` and `allow_ts_ms`, which are Avito's own absolute epoch milliseconds kept as sent. Observation Ledger moved the whole schema to milliseconds and admitted the consequence: WB_GO's retention arithmetic is written in `86400` and `604800` literals and would need rescaling, and a wrongly rescaled bucket fails as a wrong chart, never as a compile error. Storing a site-supplied millisecond fact in its own column costs one word and no audit.

Money is integer minor units beside a currency, never a float. The single highest-consequence line in the codebase is the unit boundary: **Avito states `priceDetailed.value` in whole roubles**, Wildberries states its prices in kopecks, and code is being lifted from WB_GO. A hundredfold error still sorts correctly, still renders as a plausible number, and poisons the whole price history before anyone reads a chart. It is one function, `avitoRoublesToMinor`, with an overflow guard, and it is pinned by a golden fixture carrying both the integer and the source label.

`price_kind` is three-valued and that is the column WB_GO has no equivalent of. A null price is ambiguous between «Цена договорная», which is the seller's own statement and a real event, and a parse gap, which is our defect — and the microdata fallback cannot see `priceDetailed` at all. Collapsing both into null means that the first time Avito withholds the hydration state, thirty listings simultaneously "become negotiable". The differ therefore emits `price-withdrawn` and `price-appeared` across the negotiable boundary and refuses to emit anything at all when either side is `absent`.

`listing_snapshots.path` records which extractor produced the row, and it is required rather than diagnostic. `loaderData.toggles` carries `runtime_catalog_drop_state_for_bots`, so Avito can decide to serve a page with its state removed; a silent switch from `state` to `dom` is the earliest warning available that this install has been classified as a bot, earlier than a 429 and much earlier than a captcha.

Volume is bounded, and the bound is the reason Thread-and-Window is the spine. `listing_snapshots` is written only when the fingerprint moves, plus one anchor row per `AnchorEvery`, and is then collapsed by WB_GO's own windowed `DELETE` to one row per day past thirty days and one per week past a year. Growth is therefore sublinear in the age of a watch. Observation Ledger deletes nothing from its ledger by design — its rebuild depends on completeness — so its growth is monotone with no lever except deleting the file, which on a product that is one file somebody double-clicked is disqualifying.

Two retention rules are deliberately not the obvious ones.

**Failed passes are kept and successful ones are thinned.** Thread-and-Window proposed keeping only passes that produced a change, which preferentially destroys the evidence that collection *failed* while preserving the evidence that it worked, and makes "was collection healthy in early March" unanswerable three weeks later. Here every pass whose class is neither `ok` nor `empty` is kept, and only healthy no-change passes — which is those two classes, because a query that legitimately returned nothing is as healthy as one that returned fifty — are thinned, to one per watch per hour past seven days and one per day past thirty, which also leaves an unbiased daily rank sample in `pass_items` that a later ranking milestone can use. Reading the rule as "not `ok`" would keep every empty pass for ever, and a watch on a rare query would grow without bound precisely because nothing was happening on it.

**The body archive is actually collected, and it is bounded twice.** `passes.body_sha` is nullable with `ON DELETE SET NULL`, so a body can be deleted while the pass row that referred to it stays: the sweep loses the page and keeps the reading. Observation Ledger's blob table was referenced by two tables it never deleted from, so its stated garbage collector could not fire on a single row — and a collector that is merely *possible* is that same defect one step later, which is why the two bounds here are rules rather than intentions. Bodies are archived only for a pass that failed in a way worth reading later, which is also the only class of page nobody can debug from a row count. The retention sweeper then deletes any body older than thirty days, and, whenever the archive exceeds five hundred megabytes, the oldest bodies until it does not. Both numbers are settings; neither may be unset. An archive with an age rule and no size rule is unbounded on a broken install, which is exactly when it grows fastest.

**When the disk fills.** None of the three candidate designs said what happens, and WB_GO already solved it. `store.Volume` is carried over verbatim — its own comment is the argument: *nobody administers this product, and without a number they can look at, the first time they learn the history is growing is the day the disk fills.* The panel shows the file size and the per-table row counts. On top of that: a free-space precheck before each pass transaction, which pauses collection and raises a panel state rather than attempting a write. It has a number — collection stops below 256 MB free or below two percent of the volume, whichever is larger — and it writes that state where a person will meet it, as a persistent banner and a `collector-silent` change, rather than as a log line on a machine nobody has a terminal open to. `SQLITE_FULL` does the same thing from the other side: collection stops, the panel keeps serving, and the reason is on the screen.

The emergency reclaim is the part it is easy to get backwards, and one of the candidate designs did. There is no `ON DELETE RESTRICT` anywhere, so a cascade cannot deadlock on its own foreign keys — but the obvious reclaim, an age cut over `passes`, is wrong twice over. It deletes precisely the failed passes the rule two paragraphs above keeps for ever, and it deletes them at the worst possible moment, because a disk filling is *correlated* with collection being broken: a blocked install is what writes both the failure rows and the archived bodies, so the naive sweep destroys the health history exactly when it is the only thing that explains what happened. It also frees almost nothing, since `passes.body_sha` references `bodies` and not the reverse, so dropping a pass row orphans its body and leaves the bytes on disk. `Store.Reclaim` therefore runs in a fixed order and stops at the first step that reaches its target: archived bodies oldest-first, then thinnable `ok` and `empty` passes, then `listing_snapshots` past their retention windows. Failed passes are what it reaches last, and it says on the panel when it has had to.

**One writer, many readers, and the pragmas that hold that together.** The file is opened `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`. One connection writes and the panel's readers do not block it; without WAL a chart drawn while a pass is committing is a `SQLITE_BUSY` in the browser. `synchronous=NORMAL` is the WAL-appropriate setting and the trade is named: a power cut can lose the last transactions, which for a monitor means one pass, and a pass is re-collected on the next tick. WAL also means the reclaim above must checkpoint, or a `DELETE` grows the file it was run to shrink.

---

## Collection

One scheduler goroutine, a lease pool, and one token bucket per egress address. `Store.DueWatches` reads `idx_watches_due`; each work item takes a lease, fetches one page, and writes one pass in one transaction. Concurrency is bounded by the pool rather than by a worker count, and that is not a style choice: on this target parallel requests to one host through one port coalesce onto a single challenge solve and the joiners simply wait, so throughput scales with ports and never with goroutines.

**Every long-lived goroutine is supervised, and a panic is a row.** There are five, and naming them here is the point, because a list that lives only in the reader's head grows a sixth member nobody wraps: the scheduler, the outbox worker, the out-of-band series producer, the confirmation sweeper and the retention sweeper. Each is started through one helper in `internal/supervise`, which recovers a panic, writes one `changes` row of kind `worker-panicked` carrying the goroutine's name and the first frames of the stack, logs one structured line, and restarts the loop after five seconds, doubling to a five-minute ceiling. WB_GO has no panic recovery anywhere in its non-test code, and on a tray build that means the icon vanishes with an explanation in a file nobody has open — this is the one place this design deliberately does not copy it.

There is no `jobs`, `job_runs` or `job_items`. A watch *is* the saved task and a pass *is* the run, and per-item resume machinery for a run that fetches one page is paid for on every tick and never used. WB_GO's `internal/job/schedule.go` lifts for interval parsing; the rest of that package does not.

**Mobile, page one, sorted by date.** `m.avito.ru/all?q=…&s=104&p=1` returns 200 with results inline, about thirty items, roughly 1.17 MB, one request. Desktop `/all?q=…` is a 386 KB SPA shell carrying zero listings whose only useful content is the canonical URL inside `data-mfe-state`, so desktop costs two requests and 2.7 MB for the same page-one answer. Under a budget whose ceiling is sixty requests per ten minutes, halving the request count is the largest single lever there is: it is the difference between fifteen watches and thirty. Desktop is wired, defaults off, and is used for exactly three things — item pages, where the verified `data-marker` set was measured; one diagnostic attempt when two consecutive mobile passes come back `ClassSoftBlock` — no `__initialData__` at all, the one mobile outcome that is genuinely ambiguous — to separate "Avito changed the mobile surface" from "we are soft-blocked". `ClassEmpty` is never the trigger: on mobile it means the container was present and said zero, corroborated by the site's own «Ничего не найдено», so it is an answer and not a doubt, and a rare-query watch that sits legitimately empty must never spend two desktop requests and a clearance to be told so again. Nor is `ClassShape`, which has already proved the container intact and the block excluded; and seller watches, which have no measured mobile surface at all. Two requests to tell a code change from a ban is a bargain; two requests per page forever is not.

`s=104` is fixed and not user-configurable in M1. Page one of a date-sorted query is the only window in which "new listing" is a set difference rather than a guess.

**Seller watches are desktop watches, and the schema records it as one:** `surface='desktop'`, `max_pages=1`. A seller page costs one request rather than two, because `/brands/<slug>` is already canonical and needs no shell hop — the 855 KB measured is a single fetch carrying its own single `data-mfe-state` block, with forty-five `urlPath` entries in it, so `desktop.catalog.v1` reads it unchanged and the extractor needs nothing new. It is a desktop watch because the mobile spelling of a seller page was never measured, and an unmeasured claim in the one place the product promises to watch a competitor is not a saving. The known limit is stated rather than discovered: `/brands/<slug>` exists for companies, and a private seller is identified through `iva.UserInfoStep[].payload.profile`, which appears on only about twenty rows in fifty — so M1 supports company seller watches, the panel says so when a pasted profile URL has no brand slug, and private-seller watching is named as out of scope rather than silently broken.

**Deep pagination is refused in three independent places**, because one comment is not a control: `watches.max_pages` has `CHECK (BETWEEN 1 AND 2)`, the planner emits page two only while `seeded_at IS NULL`, and `avito.SearchURL` returns `ErrDeepPagination` above two regardless of who asked. Walking past the first page or two is what escalates a 429 into a 403, and a 403 costs the cookie jar and therefore the clearance the jar was carrying.

**`avito.SearchURL` is the whole of the entry point, and it is pure.** It turns a pasted URL into a parsed watch, and a parsed watch plus a surface plus a page number back into a URL to fetch; it takes a clock and no network. Parsing: the host is discarded, the path is split into a region slug and a category slug path (`/moskva/telefony/…`, with `/all/…` meaning no region), and the query is read for `q`, `pmin`, `pmax`, `s`, `p`, `f` and the small flags. `pmin`/`pmax` are whole roubles as Avito states them and pass through `avitoRoublesToMinor` on the way in and back out again — the same function and the same golden fixture as a listing price, because a price band wrong by a hundredfold silently returns the wrong market and still looks like a market. `f`, `user`, `cd`, `radius` and `localPriority` are stored verbatim in `params` and re-emitted verbatim: the `f` blob is passed through and never interpreted, and a pasted URL carrying one that we do not understand is still fetched exactly as pasted. Emitting: the region and category slugs are carried onto the mobile host as path — `m.avito.ru/<region>/<category-path>?q=…&s=104&p=1` — and not dropped, because dropping them silently widens a Moscow watch to the whole country. Only `m.avito.ru/all?q=…&s=104&p=1` was actually measured, so carrying the path is a design decision rather than a measurement, and increment 2's first task is to fetch one region-scoped and one category-scoped mobile URL and pin both as fixtures before anything is built on top of them.

**The five-thousand-result cap is handled by refusing to sweep, and both surfaces can see the number.** A desktop pass reads `catalog.totalCount` out of the hydration state; a mobile pass reads `__initialData__.search.count`, which was measured at 59959 on a populated query and 0 on an empty one. Both land in `passes.total_count`, so the guard is uniform: above 4500 the watch sets `too_broad` and the panel says to add a price band, naming `pmin` and `pmax`. Where a surface ever fails to report a count the column is NULL, the guard does not fire, and the collapse test in `evidential` is skipped rather than being read as a collapse to zero — a missing number must never be arithmetic on zero. Nothing slices automatically: a monitor that multiplies its own request budget without being asked is a monitor that earns a 403.

**Two budgets, because identities and addresses are exhausted by different things.**

A fresh cookieless request collects a 429 immediately; one warm jar sustained forty of forty at three-second spacing. The scarce resource is a *rested identity*, and identities are what a pool of ports multiplies.

The SDK being copied already implements the exact policy this needs, and it is worth stating because it is easy to reimplement badly. `Pool.take()` skips quarantined and leased ports; among those whose idle time has passed `Cooldown` it returns **the one used longest ago**; and when none is cool it returns the precise remaining wait so `Acquire` sleeps exactly that long and not a tick more. That is least-recently-used selection over a cooldown floor with no idling while any port is ready — requests leave at the aggregate rate while each identity rests N times longer than it would alone. `DeriveCooldown(PortsPerThread, DelayMin, DelayMax)` is `PortsPerThread × the midpoint of the delay range`, which is the arithmetic that makes those two sentences the same sentence. `PoolConfig`'s own comment records why ports are shared across threads rather than pinned to one: *"a fast thread never idles on its own cold port while another thread's ports sit free."*

**Every port carries its own address.** Ports are opened over `Channels` and spread by `Mixer` — a proxy list from a file or a URL, a rotating proxy with its change link, a BlankTrail gateway, or the host's own address, in any mix — so a pool of N ports is N identities on N egresses, not N cookie jars behind one IP. This is the recommended configuration and the one the budget arithmetic below assumes. `Mixer` also decays the weight of a channel that starts producing blocks, so a dead proxy in a supplied list stops being handed out without anyone editing the file. `blanktrail/upstream.go` parses six line formats and refreshes a list from its source on an interval; `internal/store/channels.go` persists the mix. All of it lifts.

The degenerate configuration still has to behave. N ports sharing one egress are N identities and one address, and Avito's reported /24-level aggregation means the address has a ceiling that more identities do not raise. So the second budget is a token bucket **keyed by egress address**, which lives in `internal/collect`: with one address per port it never binds, and with a pool run direct it is the thing that stops a larger pool from looking like a larger budget. The panel shows both numbers, because "I added ports and it got banned faster" is otherwise an unanswerable support question.

Every port carries `KeepSessions`, `JSSolver` and `Decompress`, and `RenewAfterRequests`/`RenewAfterInterval` are **zero** — proactive renewal is disabled outright, because renewing an identity discards a clearance that cost nineteen to twenty-three seconds and is domain-wide and long-lived.

Mode (b) gets the same shape behind the same interface: a set of persistent jars, each its own `srv_id` and each its own upstream where the user supplied a list, leased most-rested-first, with one request in flight per jar (the Transport section gives the reason, which is the `_avisc` nonce). Whether several warm jars behind *one* address are treated better than one is **not measured**; it is the second question the increment-2 soak run settles, and until it does mode (b) ships with one jar per address and the machinery already able to widen.

Pacing has a floor and a ceiling, and they are enforced at different scopes. The floor is 3.5 seconds between two requests on the **same identity**, with ±20% jitter — not 3.0, because the measurement is the edge of what worked and a default should not sit on an edge. The ceiling is a rolling ten-minute cap of sixty requests **per egress address**, a third under the ninety that was observed to harden a 429 into a 403. With one address per port the floor is what binds and the pool sets the pace; with several identities on one address the ceiling binds instead. Both windows survive a restart, each in the table that owns its scope: the per-identity floor in `transport_sessions.window_json`, the per-address ceiling in `egress_windows.window_json` keyed by the address. A ceiling stored per session would hand every identity sharing an address the whole budget again on the next start, which is exactly the restart cheat the pacer exists to prevent.

**And the same mistake was made in memory, where the schema had guarded against it on disk.** `Pacer.Wait` takes an identity and an address, and the ladder passed `Lease.Label` as the address. `Label` names the *endpoint* — `direct:chrome-133-android#0` through `#3`, or `blanktrail:20000` and `:20011` — so four jars sharing one IP were given four separate ten-minute windows and four times the budget they were entitled to. `Budget()` reported one address the whole time, which is what kept it invisible: the published figure was correct and the pacing beneath it was not, and the four-jar soak arm blew through the address limit exactly as that arithmetic predicts. The seam now carries `Lease.Egress()` distinct from `Label()`, and endpoints sharing an address must return the same string from it; with no proxies configured every direct session and every BlankTrail port reports `"direct"`.

`Pacer.Budget()` publishes the arithmetic for the configuration actually in use, because the answer moves with it. Sixty requests per ten minutes per address is 360 an hour; one page-one pass is one request; a five-minute interval is twelve passes an hour — so **one egress address sustains about thirty page-one watches at five-minute resolution**, and a pool of N addresses sustains about thirty N. A schedule that exceeds what is configured is refused when it is saved, with the sum shown and both remedies named: another address, or BlankTrail. That refusal is the most useful "you need the paid mode" moment in the product, because it arrives before anything breaks — it is the difference between a user concluding they were banned and a user learning they asked for more than they had configured.

**Saturation is measured and reported.** Page one catches every arrival only while the poll interval is shorter than the page's turnover time, and that "only while" is a number: `items_on_page / arrivals`, computed over the last ten evidential passes rather than over one, because a single pass with no arrivals makes it undefined and a single busy pass makes it alarming. Below two the watch is losing listings between polls, the core promise is quietly broken, and a `watch-undersampled` change fires. Zero arrivals across the whole window is not a division by zero and not an alarm: it is a quiet query, and it is reported as "no arrivals to measure" rather than as a ratio, because a metric that invents a number where it has no data is worse than one that says it has none. This is the only honest way to sell new-listing monitoring; the alternative is a product that misses arrivals and never says which.

**Item pages** are fetched for three reasons and no others: a `listing`-kind watch, a person asking, and the confirmation sweeper, which walks `idx_listings_state` for `vanished` listings and spends at most five requests an hour, oldest first, to turn a windowed guess into a stated fact. The cap is a constant, not a setting.

---

## Transport

Two first-class modes behind one seam. `internal/transport` knows about HTTP and about being blocked; `avito` knows about Avito; neither imports the other's concerns.

```go
type Leaser interface {
    Acquire(ctx context.Context) (Lease, error)
    Mode() Mode
    Caps() Caps
}

type Lease interface {
    Do(*http.Request) (*http.Response, error)
    Session() string            // changes exactly when the identity does
    Label() string              // "blanktrail:20003" | "direct:chrome-131-android"
    Profile() Profile           // who owns which header
    Egress() string             // the exit ADDRESS, not the endpoint — see the correction below
    RotateEgress(ctx) error     // reachable from ONE branch: 429
    RenewIdentity(ctx) error    // fallback for a 403 that waiting has not cleared
    Recycle(ctx) error          // a new endpoint entirely, same address: soft block
    Release()
}
```

`*blanktrail.Lease` already satisfies `Do`, `Session`, `RotateEgress`, `RenewIdentity` and `Release` — the whole seam bar `Mode`, `Caps`, `Label` and `Profile`, which are ours. `Profile` is the header-ownership split made into a type rather than a convention: BlankTrail overrides `User-Agent` and `sec-ch-ua` with whatever its solver browser presents, and a second opinion from here would contradict the TLS handshake underneath — WB_GO's `headers.go` states exactly that rule. But BlankTrail does **not** override `Accept-Language`, so that header is ours in both modes, must be `ru-RU,ru;q=0.9,…` in both, and gets its own constant and its own equality test.

**One BlankTrail SDK extension, and it is target-neutral.** `captcha_action` and `captcha_max_attempts` join `openPortRequest` and `PortSpec`, because a live port reports them and the SDK cannot set them. That is the whole of it.

~~An earlier draft of this section specified a second extension — a `Lease.Recycle` that would extract `renewIfDue`'s close-and-reopen path — on the reasoning that a 403 needs a fresh cookie jar and no control-API endpoint clears one. Reading the SDK before copying it showed the remedy already ships. `Lease.RenewIdentity` rotates the port's profile, closes its idle connections, bumps the session and resets the failure count while keeping the address... `RenewIdentity` is what the ladder calls for `ClassBurned` and `ClassSoftBlock`; its price is the same re-solve either way.~~

**Refuted by the soak run, and this paragraph is left standing so the reasoning that failed is visible.** The last sentence of it was right — *"whether rotation really does discard the jar is the vendor's own claim, and the soak run is where it gets checked rather than trusted"* — and the check went against the claim.

`Lease.RenewIdentity` rotates the **profile**. Whatever the proxy holds for that port survives it. Measured 2026-08-24: BlankTrail port 20000 stopped returning data and was renewed six times inside a single fetch — twenty-seven minutes — answering with an empty page every time, three fetches running, while port 20011 **on the same exit address** served in under two seconds throughout. So `Recycle` is a real extension after all: `Pool.recycle` lifts the close-and-reopen out of `renewIfDue` so a caller can ask for it instead of waiting for a schedule or a failed renewal. Two SDK extensions, not one.

The correction that matters more is what it implies about the table below: renewing an identity is a *weaker* act than the design assumed, and three of the four classes that asked for it did not want it.

Two more things are wiring, not extensions. `PoolConfig.CountFailure` is set to `func(status int) bool { return status == 429 }`, which encodes "rotate the egress on 429 only" at the layer that owns rotation, so the pool physically cannot rotate on a 403 or a 439 no matter what the ladder above does. **The soak run confirmed this one rather than refuting it, and for a better reason than the design had**: a 429 is not a rate limit at all — see the corrected table — and a different address is the only thing that clears it. And `neutrality_test.go`'s banned-word list gains `avito`, `geetest` and `avisc`, and loses `captcha`, in the same commit that copies the SDK in — `qrator` is already on it, inherited from WB_GO, and `wildberries` and `wbaas` stay on it rather than being swapped out, because a guard that only forbids this project's own target stops being a guard the moment the package is copied again — the guard exists precisely so the first well-meaning commit cannot quietly end the package's neutrality.

**WB_GO's `DefaultAttemptsPerEgress = 3` is deleted, not retuned.** It lives in `wb.RetryPolicy` rather than in the SDK, so "deleted" here means the field is simply never written into this project's own policy. It rotates on a *schedule*, every third attempt through one address, and on this target that throws away a nineteen-to-twenty-three-second clearance that was working. Rotation here is caused, never scheduled.

**Classification takes the extraction.** `wb.Classify(status, body)` is the right shape and the wrong table, and it is missing the status that matters:

| observed | class | remedy |
|---|---|---|
| 439, or a captcha marker (`firewallCaptcha`, `gcaptcha4`, `geetest`) at any status | `ClassChallenge` | BlankTrail: repeat on the **same** port, three times, ~25 s apart. Direct: **stop.** |
| 429 carrying wall markers on an identity that has never completed a warm-up | `ClassCold` | Direct: one root navigation for `srv_id`/`_avisc`, then one retry on the same jar. A second cold wall on an already-warm jar is `ClassChallenge`. BlankTrail: as `ClassThrottled`. |
| 429 | `ClassThrottled` | the only class that rotates the egress. **One turn per configured address, then halt** — no backoff ladder |
| 403 | `ClassBurned` | **wait 240 s. Do not renew.** Renewal is a fallback past three waits |
| 2xx, items present | `ClassOK` | — |
| 2xx, result container present, zero results | `ClassEmpty` | none — a real answer |
| 2xx, result container present, both extractors empty | `ClassShape` | **none, and zero retries** |
| 2xx, no result container for this surface | `ClassSoftBlock` | **`Recycle`** — a new endpoint, same address. Twice, then report |
| body will not decompress or is not UTF-8 | `ClassMangled` | one re-dial |

> **Three rows of that table were wrong, and the soak run of 2026-08-23/24
> replaced them.** Full working in
> `docs/measurements/2026-08-23-soak.md`; the short version:
>
> **A 429 is the captcha, not a rate limit.** Sixty-six consecutive 429s went
> through the old 25/60/200 s ladder across eleven fetches — about fifteen
> minutes apiece — and not one became a 200. The only two that recovered went
> through a 302, which is how a challenge flow ends. The body was then captured
> and kept as `internal/transport/testdata/live-429-captcha.html.gz`: 27 KB,
> titled «Доступ ограничен: проблема с IP», carrying `geetest`,
> `firewallCaptcha`, `initGeetest` and `x-cube`. It is a Geetest page. The
> classifier had been missing it because `Document.Wall` returns markers in
> table order, «проблема с ip» is listed before «geetest», and the captcha test
> read only that one returned label. `Document.Walls` reports every marker now.
> Waiting cannot solve a captcha, so the remedy is a different address or a
> halt — and reaching that halt in forty seconds instead of grinding for hours
> is the difference between the boundary message being the product's honest edge
> and being unreachable.
>
> **A 403 heals with time, and the identity is not what was refused.** Eighteen
> of eighteen cleared by waiting; none failed to. An arm whose `RenewIdentity`
> was replaced by a no-op recovered from four consecutive 403s on one unchanged
> `srv_id`, and again two fetches later; an arm that did renew was no faster,
> because recovery tracked the number of 240-second waits and nothing else.
> Renewing discarded a Qrator clearance worth 19–23 s to fix something the
> identity was not causing — the same mistake this table forbids in the other
> direction. It survives only as a fallback past three waits, where the
> measurement stops speaking.
>
> **A soft block is a stuck endpoint.** Three fetches exhausted all six attempts
> on an empty 200 — six renewals apiece, twenty-seven minutes apiece — every one
> of them on BlankTrail port 20000, while port 20011 on the same exit address
> answered in under two seconds throughout. Renewing rotates the profile and
> leaves the endpoint in place. `Recycle` closes it and opens a new one, keeping
> the address, because a stuck endpoint is not a refused address. Two recycles
> and a five-second settle; past that the endpoint is not what is wrong. Cost
> fell from twenty-seven minutes to about ten seconds.
>
> **Not measured, and therefore unchanged:** everything else in the table.

**"Result container" is a per-surface question, and it must be, because the two surfaces are built differently.** On desktop it is the `loaderData.data.catalog` object inside `data-mfe-state`, and zero results means `totalCount == 0`. On mobile there is no `data-mfe-state` block of any kind, so the container is `window.__initialData__` — an attribute-less `<script>` holding a JS string literal that holds JSON — and zero results means `search.count == 0`, corroborated by `search.emptySearch` carrying the site's own «Ничего не найдено по запросу «…»». Present with zero cards is `ClassEmpty`; `search.count > 0` with zero cards extracted is `ClassShape`, which is our bug and not a block; no `__initialData__` at all is `ClassSoftBlock`. A classifier that tested for a `catalog` object on a mobile page could never return `ClassEmpty` there — a query that legitimately emptied would fall through to `ClassSoftBlock`, `RenewIdentity` would fire on every healthy pass, and the design would throw away a nineteen-to-twenty-three-second clearance every tick. Both spellings are pinned by fixtures: an empty-result mobile page and an empty-result desktop page are captured in increment 1.

The cold-start row exists for the same class of reason. The measured direct baseline *is* that row — plain `curl`, no jar, HTTP 429 with wall markers on request one — and it is what an unwarmed identity always gets. It is the state the whole warm-jar design exists to leave, not a wall to stop at. Routing it to `ClassChallenge` would halt mode (b) permanently before it made a second request, because `halted_reason` is never cleared by the program. Only a wall on a jar that has already completed a warm-up is evidence of anything.

`ClassShape` is the branch Thread-and-Window lacked, and its absence was expensive: it routed a payload-shape change in our own parser to "drop the session", so a parser regression would burn the jar and re-pay a twenty-second solve, indefinitely, forever. Repeating a request whose payload we have stopped understanding spends the budget on our own bug. It archives the body and fires `collector-shape-unknown` instead.

There is never a `Retry-After` header on this target, so nothing waits for one; the ladder is our own, seeded from the measurements, and reset on the first `ClassOK`.

**Mode (b), concretely.** Go's stock ClientHello ships no GREASE, a cipher order no browser produces, and an extension order identical in every Go binary on earth. A perfect header set over it is a Go client wearing a hat. So direct mode dials with `refraction-networking/utls` at `HelloChrome_131`, and negotiates h2 through `http2.Transport.DialTLSContext` — not `ForceAttemptHTTP2`, which detects h2 by type-asserting the connection to `*tls.Conn`, silently fails that assertion for a `*utls.UConn`, and hands back HTTP/1.1 with a perfect Chrome handshake, a combination no Chrome produces.

Chrome and not Safari, despite BlankTrail presenting iOS successfully: BlankTrail drives a real browser and can. `x/net/http2` emits pseudo-headers in the order `:authority, :method, :path, :scheme`, which is Chrome's and neither Firefox's nor Safari's, and offers no hook to change it — so a Safari handshake over Chrome's framing is a self-contradiction, worse than an honest Chrome. One profile struct binds the hello id, the user agent, `sec-ch-ua`, the platform and `Accept-Encoding` together, and nothing may set one of them independently.

The gap is stated rather than hidden: `x/net/http2` exposes no hook for SETTINGS frame order, the initial WINDOW_UPDATE, or HPACK table sizing, and those are a second fingerprint. If Avito starts scoring it, mode (b) fails uniformly, and the remedy would be a forked `http2`, which the dependency budget refuses. It will fail as `ClassChallenge` on request one, which is the loud stop that was asked for.

The cookie jar is written rather than taken from `net/http/cookiejar`, for three reasons that are all load-bearing. `_avisc` is a sixty-second anti-bot nonce refreshed on every response and echoed on the next request; the standard jar honours `Max-Age` and simply stops returning a lapsed cookie, so a request made one second late goes out with *no* nonce, which is strictly worse than a one-second-stale one and fails as a 429 that looks like rate limiting. This jar serves the last value it saw, reports its age, and the session re-primes at fifty-five seconds. It also persists — `srv_id` is the long-lived identity and the expensive thing — and it is scoped to `avito.ru` at construction, so no cookie from this target can ever be sent anywhere else and `publicsuffix` is not needed.

One request in flight **per session**, enforced by a mutex — not one request in flight overall. Two concurrent requests on one jar necessarily send the same nonce, and nonce reuse is exactly what a nonce detects; two requests on two jars share nothing and are free. The lock is therefore per identity and costs nothing real, and mode (b) scales the same way mode (a) does: by holding more rested identities on more addresses, leased most-rested-first through the one seam both modes sit behind. What it cannot do is scale by concurrency on one identity, and the mutex is what makes that impossible rather than merely discouraged.

A restored jar's first 403 is treated as the exit address having changed — a laptop that suspended on one network and resumed on another presents a full-history identity from nowhere — and is deliberately **not** counted towards the consecutive-403 total that means "this address is burnt". Conflating them would have every laptop that changed networks overnight report its ISP as blacklisted.

**Where mode (b) stops, and how loudly.** `POST /web/N/firewallCaptcha/verify` requires an `X-Cube` header carrying a WebGL proof of work — a hidden canvas, fifty frames, `gl.readPixels` latency — which no HTTP client can produce. This is a capability boundary, not a rate limit, so 439 in direct mode is not retried, not rotated, not backed off. "Loudly" is defined operationally, because otherwise it is a wish: the affected watches enter `halted` and issue **no further requests**, which is asserted in tests against the fake server's request counter; the panel shows a persistent banner carrying the status, the URL, the matched marker and the timestamp, not a toast, because this is a state and not an incident; Telegram gets exactly one message, rate-limited to one per reason per six hours; the headless binary logs one structured line per halt and, when every enabled watch is halted, fails its own readiness — `/healthz` answers 503 and names the reasons — rather than exiting, because a process that exits is restarted by systemd into the same wall for ever, and because the panel is the only place the reason can be read; and `halted_reason` **is never cleared by the program**. A monitor that decides on its own that the wall has gone away is a monitor that resumes into it all night.

Clearing it is a person's act, and the control exists rather than being implied. A halted watch's row on **Наблюдения** carries a **Возобновить** button, and the headless binary has `avitomon watch resume <id>` and `avitomon watch resume --all`; both write `state='idle'`, `halted_reason=''`, `halted_at=NULL` and `next_due_at=now`. Neither re-tests anything and neither promises anything — if the wall is still there the next pass halts again with a fresh reason, which costs one request and buys a truthful answer. The resumption is recorded as a `changes` row carrying the reason that was cleared, marked `backfill` so the notifier skips it: an un-halt that sent "your install is blocked" through the global rule would be a lie in the one message that has to be trustworthy. Without this the most likely terminal state of a mode (b) install after a year — one captcha, one halted watch — would have no exit but editing the database by hand.

---

## Extraction

`avito/` imports no `net/http` and no `net`, takes a clock as a parameter, and is enforced by a `make check-no-net` target reading `go list -deps`. Every test is therefore offline by construction.

The hydration state is found by streaming `html.Tokenizer` with early exit, and the saving is real but smaller than it first looks: the block itself is 671 KB, so the tokenizer must still read that much plus whatever precedes it — roughly a third of the 2.28 MB page rather than a few hundred kilobytes. What early exit avoids is the remaining two thirds and, more to the point, the tree: `html.Parse` allocates a node per element for a document whose useful content is one script's text. The tokenizer also never has to hold the page and the tree at once, which on a 2.28 MB document is the difference that matters when thirty watches are collecting.

**Selection is on `data-mfe-state`, never on `type="mime/invalid"`.** That type is a deliberate non-MIME chosen so no browser executes the block; it is presentation trivia and the likeliest thing to be renamed, and requiring it would make a cosmetic change read as a total parse failure. It is recorded so a drift report can say it moved.

**Script text is never HTML-unescaped.** `x/net/html` implements the HTML5 script-data state: `Text()` returns the server's exact bytes for a raw tag. Passing them through `html.UnescapeString` rewrites a seller's literal `&amp;` inside a JSON string to `&` and `&lt;` to `<`, and HTML5's semicolon-optional entities make `&notin` decay to `¬in` — a corrupted title with no error anywhere. Attribute values are the mirror image: `TagAttr` already returns them unescaped, so unescaping an attribute-carried payload causes the identical corruption from the opposite direction. The rule is that `x/net/html` decides, this package never unescapes, and never unescapes twice. Every byte kept is copied, because the tokenizer's slices are documented as invalid after the next `Next()` — a bug that reproduces only on documents large enough for the buffer to be reused, which is to say only in production.

The items array is reached through an ordered table of named rules, and **which rule answered is recorded**. A last-resort shape probe matches on structure — an array of objects a majority of which carry a numeric `id` and a string `urlPath` — rather than on the name `items`, and it always sets a warning. A run where the probe starts answering instead of `desktop.catalog.v1` is a page whose structure moved, reported before anyone notices the counts are wrong.

Both extraction paths are real. The DOM fallback reads `data-marker`, `data-item-id` and `itemprop` through `golang.org/x/net/html` and **never a CSS class**. Avito ships CSS modules: every class is a stem plus a build hash, so exact names break on every deploy and stems break on ordinary refactors nobody announces. Both failures are silent in the worst possible way — a selector that matches nothing returns zero cards, zero cards reads as no listings, and no listings is exactly what a new-listing monitor reports on a quiet market. The monitor would go quiet and look healthy. `data-marker` is different in kind, not just in stability: it exists for Avito's own end-to-end tests, so renaming one breaks their CI before it breaks ours.

**The mobile extractor, `mobile.cards.v1`.** The mobile document carries no hydration catalogue, so on this surface `dom` is the primary path and not a fallback, and nothing about it is a degradation. Two readers run and both are required. The first parses `window.__initialData__`: find the attribute-less `<script>` whose text begins with the assignment, take the **first** double-quoted JS string literal with an escape-aware two-state scan — not a regex, and not `strings.Split`, because the block continues with further statements after the literal — then `json.Unmarshal` twice, since it is a JS string holding JSON text. From it come `search.count` (the total, which is what the `too_broad` guard reads on this surface) and `search.emptySearch`, which is what makes a legitimately empty result set distinguishable from a soft block. The second walks `x/net/html` for elements whose `data-marker` is `item-wrapper(<id>)`, takes the id from the parenthesised token and cross-checks it against the same element's `data-marker-id`, refusing the card if the two disagree. Within a card: title from `titleLabelGrid`, `url_path` from the `item/link` anchor's `href`, address from `addressLabel`, params from `itemParamsGrid`, seller name from `sellerAndRatingSellerLabel`, and **no description at all**: measured 2026-08-23, all thirty cards carry `<meta itemProp="description" content="...">` — the literal three dots — and no `content` attribute anywhere on the page holds one. The placeholder is read and discarded rather than left unread, so the day Avito fills it in there is exactly one place to change. The consequence is named where it bites: the repost linker's description digest, one of its two strong corroborators, is unavailable on the default collection surface, so an auto-link there rests on the image key or on the title-plus-coordinates-plus-price tier alone. Price comes from `priceLabelGrid`: `price_label` keeps the site's rendering verbatim, `price_minor` is the digits of that label under `unicode.IsDigit`, and `price_kind` is `negotiable` for «Цена договорная», `exact` when digits were read and `absent` when the node is missing. There is no `priceDetailed` here, so the advisory value-against-string cross-check does not run and its counter is not touched — a surface that cannot disagree with itself must not be recorded as agreeing. `window_size` is the number of cards parsed and never a constant: thirty is what was measured, not what is guaranteed.

**`sort_ts_ms` is NULL on the mobile surface, and that is a specified outcome rather than a gap to be filled later.** The measurement is unambiguous: without `s=104` the mobile page carries no date anywhere — `sortTimeGrid` is an empty `<div>` — and with `s=104` every one of the thirty cards read "1 час назад", so the field exists but its resolution is about an hour and page one of a busy query turns over inside it. Parsing that string would produce a timestamp precise enough to look trustworthy and wrong enough to order arrivals incorrectly, so it is not parsed. Every lifecycle decision uses `listings.first_seen_at`, our own observation time, which is what the design already does. The consequence is stated once here so it is never discovered: any later analytic that needs Avito's own publication time must run on the desktop surface, where `sortTimeStamp` is exact epoch milliseconds.

Precondition, enforced rather than assumed: the tokenizer's own documentation says the input is assumed UTF-8. `Document.Check` refuses an empty body, a body that is not valid UTF-8, and a valid body with no Cyrillic anywhere. A body still compressed with br or zstd produces no markers and no items and reads downstream as "the query matched nothing", which is the single most expensive silent failure in this system.

The fallback runs on every failure and, once every fifty pages plus the first page of each watch, alongside a successful state parse. A fallback that only runs when the primary fails has its first production execution during an incident, with no known-good disagreement baseline. That audit is also what calibrates the fifty-to-fifty agreement the golden fixture asserts.

The advisory price cross-check compares `priceDetailed.value` against the digits in `priceDetailed.string`, walked with **`unicode.IsDigit`, not `unicode.IsNumber`** — `IsNumber` accepts category No, which includes U+00B2, so `1 200 ₽ за м²` reads as 12002 rather than 1200, and per-square-metre pricing is ordinary on Avito's property categories. It increments a counter and never rejects a row; a sudden collapse in the agreement rate is the unit having changed.

**The item extractor, `item.markers.v1`.** An item page yields `Kind == EnvelopeItem` and exactly one listing, and its handles are `data-marker` only — the measurement found no `ld+json` and no `itemprop="price"` on this surface. `item-view/item-id`, `item-view/item-date`, `item-view/item-price`, `item-view/item-params`, `item-view/item-description`, `item-view/total-views` and `item-view/today-views`, each read as the subtree text of the element that carries the marker. The price node's text is the only price source there is, so the digit walk over the rendered label *is* the reading — same `unicode.IsDigit` rule and same rouble-to-kopeck boundary as everywhere else — and the advisory cross-check does not run, because there is no second number to disagree with. A closed or archived posting is recognised from the site's own words in its warning node, which are stored verbatim and uninterpreted in `state_reason`; that, and only that, may write `state='closed'` with `state_source='item-page'` and `state_confidence=1.0`. This extractor is what turns decision 5's «предположительно» into a stated fact, so it is not optional and it is not later: it lands in increment 1 with the rest of extraction, and its fixture is captured then.

`Page.Ok()` asserts `Kind == EnvelopeCatalog && len(Listings) > 0`, and no caller may substitute a status check for it.

**Golden fixtures pin the whole `Page`, including the `ExtractionReport`.** The same fifty listings reached via `dom` instead of `state`, or via the probe instead of a named rule, is exactly the regression this design exists to catch, and a fixture that pinned only the items would let it pass green. Fixtures are captured whole and gzipped, with a frozen clock in their metadata so the timestamp validity window cannot make the suite fail on a day nobody changed anything, and mutation tests derived in-code from the real fixture prove the robustness claims rather than asserting them: rehash every class and nothing changes; rename the script type and nothing changes; inject `&amp;` into a title and it survives verbatim; delete the state block and the run must come back `dom` with card fidelity and a degradation warning.

---

## Events and rules

One catalogue. `internal/track` lifts as a package — `Reading`, `Change`, `Kind`, `Unit`, `Diff`, `PercentChange`, `ErrIdentityMismatch` — with its all-pointers discipline intact, which is exactly what «Цена договорная» needs and what makes `PercentChange` refuse an appearance instead of reporting an infinite rise. WB_GO's second catalogue is not ported: `wb.EventKind`, `events` and `event_changes` exist there and `internal/rules` never reads them.

`changes.was` and `changes.now` are **TEXT**, as WB_GO's `event_changes` already has them. An integer pair would have nowhere to put a title, a description digest, or a moderation reason, and widening the busiest table in the schema later is the one migration worth avoiding by writing one word differently today.

Three producers, and the fork is recorded rather than left to be deduced, which is why `changes.producer` admits three values and not two. The **window** producer runs inside the pass transaction, because appearance and disappearance are a set difference against the previous window that a concurrent watch must not observe half-written. The **series** producer runs out of band over two snapshots, exactly as WB_GO's tracker does. The **transport** producer is the one it is tempting to leave out of the prose and must not be: the seven health kinds — `extraction-fell-back`, `transport-blocked`, `collector-shape-unknown`, `collector-silent`, `worker-panicked`, `watch-undersampled`, `watch-too-broad` — are changes like any other so that a person can write a rule on them, but they are produced by the collector observing itself rather than by comparing two readings, and a reader who found only two producers named would have no way to place them.

`listing-appeared` requires membership in this pass, absence from the previous ones, **and** `listings.first_seen_at == this pass`. A listing that merely floated back into the window is `listing-returned`. That second clause immunises the new-listing alert against ranking churn and against a recovered soft block, and it is the cheapest correctness guard in the design.

Kinds M1 emits, and no others, for the reason `track.Kinds()` gives — a rule on a kind nothing can emit never fires, and a person whose rule never fires concludes nothing is changing:

- `listing-appeared`, `listing-returned`, `listing-left-window`, `listing-vanished`, `listing-closed`, `listing-reposted`, `repost-retracted`
- `price-changed`, `price-appeared`, `price-withdrawn`, `discount-changed`
- `title-changed`, `promotion-changed`, `reserved-changed`, `listing-bumped`
- `extraction-fell-back`, `transport-blocked`, `collector-shape-unknown`, `collector-silent`, `worker-panicked`, `watch-undersampled`, `watch-too-broad`

The last seven are health, not market, and they are rule-addressable on purpose: "tell me when my install is being throttled" is a real rule a person writes, and it is how they learn before the data quietly stops arriving. Three of them exist because a catalogue that only describes the market cannot describe the collector, and they say different things to whoever is holding the machine. `collector-shape-unknown` is what `ClassShape` raises: *our own parser stopped understanding a payload the site is still serving*, whose remedy is a code change, and it is deliberately not `extraction-fell-back`, whose remedy is waiting or another transport. `worker-panicked` is a supervised goroutine having crashed and restarted. **`collector-silent` is the one that answers the question the whole health screen exists for** — it fires when a watch has produced no pass at all for three times its interval, whatever the reason, including reasons that leave no other trace, because every other signal here is written *by* a pass and a collector that has stopped writing passes writes nothing at all. A quiet market and a dead collector look identical until something is watching for the absence of work rather than the content of it. Its `DedupKey` carries the watch and the kind and no numbers, so a watch that is down for a week produces one change and one message.

The price transition matrix is written out, because the temptation to compress it is what produces false storms:

```
exact      -> exact       price-changed, when the numbers differ
exact      -> negotiable  price-withdrawn   had_before=1 has_now=0
negotiable -> exact       price-appeared    had_before=0 has_now=1
negotiable -> negotiable  nothing
absent     -> anything    NOTHING, refused
anything   -> absent      NOTHING, refused
```

`rules` lifts whole — `predicate.go`'s tree, `suppress.go`'s cheapest-first ordering, `Scope.Covers`' unknown-scope-covers-nothing default. Three edits. `ScopeKind` becomes `listing | thread | seller | watch | filter`. `LastFired` takes a subject *pair* rather than one id, because the rate limit is per thread for a price rule and per listing for a lifecycle one — a repost must not reset a price rule's limit, and it must not silence the successor's appearance either. And `Filter` gains `IncludeUnpriced`, default false, keeping WB_GO's reasoning that a price band cannot include something whose price was not read, while acknowledging that here that is a large fraction of the catalogue rather than an edge case.

`DedupKey` keys price and title kinds on `thread_id`. This is a bug WB_GO cannot have: without it, a seller who reposts the same item at the same price every three days produces a fresh `price-changed` every time, because each incarnation is a new number with no history — and that is precisely the noise that gets a monitor switched off in its first week. For `listing-appeared` the key carries no numbers at all, so a listing can be announced as new **at most once, ever**, however many times it flaps in and out of the window.

`rule_events.suppressed_by` gains two reasons: `seed`, for a watch's first pass, and `repost`, for an alert an auto-link withheld. Both write the row. A monitor that silently drops notifications cannot be debugged by the person who stopped receiving them.

---

## Notifications

`notify_outbox` and its worker lift unchanged, including the two decisions worth keeping: `due_at` is an absolute moment so a restart does not reset a growing pause, and `rule_event_id` is nullable and cleared rather than cascaded so a message delayed by an outage survives its rule being edited.

Telegram is the Bot API over the same seam, reaching Telegram directly where that works and through a BlankTrail port where it does not, and the settings screen says which. **MTProto is in M1, at increment 5**, and `gotd/td` is taken as a dependency for it: `api.telegram.org` is blocked for part of the Russian audience, which is this product's audience, and a user in mode (b) behind that block otherwise has no path at all. WB_GO ships the same transport with its live exchange never verified against real Telegram, so it is lifted as a starting point and finished here rather than trusted. It is the reason the sender is an interface from the first commit rather than from the commit that needs two.

**The default set, enumerated, because "the default set" appearing once and never defined is how a headline feature goes missing.** Adding a watch creates four rules scoped to that watch — per watch, not one shared global copy, because a rule scoped to a watch that does not exist yet cannot be written in advance and a shared rule would make deleting one watch silence another. They are: `listing-appeared`, no threshold and no rate limit, which needs neither because its dedup key carries no numbers and fires at most once per listing ever; `price-changed`, keyed on `thread_id`, with a floor so a rounding move is not news; `listing-vanished`, enabled per decision 5 and prefixed «предположительно»; and `listing-closed`, which is the confirmation of the third and carries confidence 1.0. Five more are global and created on first run: `transport-blocked`, `extraction-fell-back`, `collector-shape-unknown`, `collector-silent` and `worker-panicked`, all rate-limited to one message per reason per six hours, because an install that has been flagged should say so once rather than either never or continuously. Every one of them is editable, disableable and deletable on the Правила screen like any rule a person wrote, and the panel says which rules came from the default set so that deleting one is an informed act.

Messages carry the evidence, not just the verdict. A `listing-vanished` at confidence 0.3 is prefixed «предположительно», says which watch stopped seeing it — the prefix is what makes an enabled-by-default removal alert honest rather than sloppy. When the confirmation sweeper later turns the guess into a stated fact, the second message says so and carries confidence 1.0. A `listing-reposted` names the old number and offers one inline button, «Это не перепубликация», which pins the pair and replays the withheld alert. A middling repost score never suppresses: the ordinary alert fires with one extra line attached.

---

## The panel

`127.0.0.1` by default, no password, refusing the LAN until the user sets one of their own — WB_GO's reasoning holds unchanged, and so does its wording. Windows tray binary built `-H windowsgui` plus a headless binary; systemd and launchd units for machines nobody logs into; data in the platform's own directory.

**The first screen asks which transport to use, and it asks with numbers.** `settings.transport_mode` starts empty and nothing collects until it is set. One side offers about thirty page-one watches per address at five-minute resolution, a stop at the first captcha, and the note that supplying a proxy list multiplies the first figure; the other offers BlankTrail with neither limit. Defaulting to direct would let a user conclude the product is broken when it is behaving exactly as documented; defaulting to BlankTrail would contradict mode (b) being first class. Asking once costs one screen and is the only honest option.

Eight screens, and three of them exist because of things this target does that Wildberries does not.

**Наблюдения** — the watches, each with its interval, its last pass, and the arithmetic of its cost against the budget. Adding a watch whose schedule exceeds the mode's ceiling is refused here, with the sum shown.

**Здоровье сбора** — the screen that makes the transport honest. The share of passes answered by each extraction path over the last day, the evidential fraction per watch, the saturation figure with its two-arrival alarm, the card-yield ratio for mobile watches against its own baseline, the age of each watch's last evidential pass — which is the one line that separates a quiet market from a dead collector — the block picture by class and hour with `ClassShape` labelled as ours rather than theirs, the database size and per-table row counts from `store.Volume`, and any halted watch as a persistent banner with the marker that halted it. A monitor being flagged, a monitor being throttled and a quiet market look identical from anywhere else in the product.

**История** — the price chart per thread, drawn without a plotting dependency as WB_GO's `internal/chart` already does, with a reading that has no price breaking the line rather than being drawn as a zero, and a stretch nobody collected breaking it too.

**Перепубликации** — every link ever made, with its evidence, searchable, with per-link and bulk undo. People often realise days later, and if the ladder is wrong for a watch it is wrong systematically, so one-at-a-time undo is not a remedy.

**Прокси** — where the addresses come from, and the screen the pool is worthless without. A list from a file or a URL, a rotating address with its change link, a BlankTrail gateway, and the host's own address, in any mix; ports are spread over whatever is ticked, and an entry that starts producing blocks loses weight on its own through `Mixer`. The screen states the consequence of the mix rather than leaving it to be inferred: N addresses is N times the ceiling, and one address is one ceiling however many ports are open.

Each entry has a test that costs nothing, and WB_GO's reasoning about what "nothing" means is kept exactly. A list is read and counted with the first bad line quoted. A gateway name is checked against the ones BlankTrail actually has. A rotating channel's change link is deliberately **not** pulled — the provider limits how often it may be, and a test that broke what it was testing would be worse than no test at all.

**Настройки** — the transport choice and its credentials, the Telegram token beside the MTProto pair, quiet hours, the panel password and the LAN tick, autostart, and the retention and free-space numbers with their current values rather than their names. It is the screen decision 3 lands on and the one every other decision quietly assumes, so it is enumerated rather than left implied.

**Правила** and **Результаты** are WB_GO's, with the field catalogue swapped: CSV, XLSX and JSON from one column selection, streaming, through the lifted `internal/export`.

---

## Dependencies

Eight direct, against WB_GO's four: three of the extra come from one decision and the fourth from another.

`modernc.org/sqlite` — house rule, the only SQLite without CGO. `gopkg.in/yaml.v3` — the pre-database configuration. `golang.org/x/sys` — the tray and the autostart entry. `golang.org/x/net` — `html` for the DOM fallback and `http2` for the utls h2 path; one module, two load-bearing uses, and the `html` half is what the no-regex requirement buys. `refraction-networking/utls` — mode (b) being real rather than nominal; pure Go, no build tags, already vendored in this author's other project. `andybalholm/brotli` and `klauspost/compress` — Avito serves `br` and `zstd`, and they are utls's own requirements, so the argument for advertising gzip alone to save two modules does not survive taking utls at all. Advertising Chrome's `gzip, deflate, br, zstd` and decoding all four is then both cheaper and more consistent than the alternative. `github.com/gotd/td` — MTProto, decision 6, and by a wide margin the largest line in this budget: some forty indirect modules for one delivery rung. It is carried knowingly rather than chosen cheaply. The reason is decision 6's and is not restated here. WB_GO already carries it, so this is a lift rather than a new liability — but its live exchange was never verified there, and it is verified here.


---

# Schema — migration 0001

-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0001_core.sql is the whole of M1: what we watch, what came back, what it
-- said, what moved, and who is told.
--
-- Every timestamp is whole Unix seconds UTC, as in WB_GO, so its retention
-- arithmetic lifts unchanged. The two exceptions are named for what they are:
-- sort_ts_ms and allow_ts_ms are Avito's own absolute epoch MILLISECONDS, kept
-- as sent, because rescaling a site-supplied fact on the way in is how a fact
-- gets lost. Money is integer minor units beside a currency, never a float.

-- ---------------------------------------------------------------- sellers --

-- sellers is who is selling. Its own table because a rating belongs to an
-- account and not to an advertisement: folding it into the listing would
-- rewrite four hundred rows when one review lands, and moving it out later
-- would be a rebuild of the largest table here.
--
-- id is a surrogate and seller_key is the natural key, deliberately in that
-- order. What a search row actually carries is userLogo.link (/brands/<slug>,
-- companies only) or iva.UserInfoStep[].payload.profile (about twenty rows in
-- fifty). Those two spellings are not one identity, and a private seller who
-- changes their profile path forks into two rows. With a surrogate id the fork
-- can be repaired later by repointing listings.seller_id; with a TEXT primary
-- key it could only be repaired by rewriting every table that names a seller.
--
-- key_quality records how the key was obtained. A heuristic key is never used
-- as evidence when linking a repost: inventing an identity that silently
-- merges two people is worse than declining to.
CREATE TABLE sellers (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    seller_key    TEXT    NOT NULL UNIQUE,
    key_quality   TEXT    NOT NULL DEFAULT 'heuristic'
                  CHECK (key_quality IN ('slug', 'profile', 'heuristic')),
    -- kind defaults to 'unknown' and not 'private'. «Компания» is carried in
    -- iva.SecondLineStep, which most search rows do not have, so the absence
    -- of the marker is the absence of iva and not evidence of a private
    -- seller.
    kind          TEXT    NOT NULL DEFAULT 'unknown'
                  CHECK (kind IN ('private', 'company', 'unknown')),
    display_name  TEXT    NOT NULL DEFAULT '',
    profile_path  TEXT    NOT NULL DEFAULT '',
    first_seen_at INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL
) STRICT;

-- seller_snapshots is reputation over time, with the same fingerprint-and-
-- anchor rule the listing series uses: write when something moved, plus one
-- row per AnchorEvery so that "unchanged since March" and "unread since March"
-- are different rows.
--
-- rating is in hundredths of a point, matching track.UnitRatingHundredths, so
-- "fell by more than 0.2" is an integer comparison and goes through the same
-- arithmetic as a price in kopecks.
--
-- M1 writes this table and emits no events from it. The columns exist now
-- because the data is already parsed and free, and because adding the grain
-- later would mean deciding where a rating had been living in the meantime.
CREATE TABLE seller_snapshots (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    seller_id         INTEGER NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    ts                INTEGER NOT NULL,
    anchor            INTEGER NOT NULL DEFAULT 0,
    fingerprint       TEXT    NOT NULL,
    rating_hundredths INTEGER,
    review_count      INTEGER,
    rating_summary    TEXT    NOT NULL DEFAULT '',
    closed_items_text TEXT    NOT NULL DEFAULT ''
) STRICT;

-- The key and the only question are the same pair, so one unique index refuses
-- a duplicate reading and answers "how did this seller's rating move".
CREATE UNIQUE INDEX idx_seller_snapshots_seller_ts ON seller_snapshots(seller_id, ts);

-- --------------------------------------------------------------- listings --

-- listings is the stable half of an advertisement, plus its lifecycle.
--
-- item_id is Avito's own numeric id and nothing else. It is stable for the
-- life of one posting and is never reused, so it is the primary key — but it
-- is not the identity of the thing being sold, because a seller who closes and
-- re-publishes starts a new number for the same intent. That is what thread_id
-- is for.
--
-- thread_id is the item_id of the earliest known incarnation in the repost
-- chain, defaulting to the row's own item_id. It is denormalised so "the price
-- history of this thing" is one indexed scan rather than a recursive query on
-- every chart draw. It is the only derived value in this schema that is
-- written rather than computed, and it has a rebuild: Store.RecomputeThread
-- walks repost_links with a depth-capped recursive CTE, so a bad link is
-- repairable rather than permanent.
--
-- state is what we believe about the advertisement's life. state_source is why
-- we believe it, and it is load-bearing: absence from page one of a watch may
-- only ever write 'left-window' or 'vanished'. 'closed' may be written by an
-- item page, which read the site's own words, or by 'user', which is a person
-- saying so on the panel — and by nothing else. Page one is a window, and a
-- listing that fell off it may have been pushed by a newer one, reshuffled by
-- a paid promotion, or removed, and nothing in the response tells the three
-- apart. The CHECK admits all three sources; this comment is the reason the
-- third is there, so that a reader does not delete it as unreachable.
--
-- state_reason holds the site's own words for an ended listing verbatim.
-- Avito's vocabulary for that is not stable across surfaces, so the state
-- stays at 'closed' and the prose stays uninterpreted, the same decision
-- WB_GO makes for products.card_created.
--
-- first_seen_at and last_seen_at are also the co-existence interval the repost
-- linker vetoes on: two listings whose intervals overlap were alive at the
-- same time and cannot be two incarnations of one thing. Holding that on this
-- table rather than in pass membership is what keeps the veto working after
-- pass history has been thinned.
CREATE TABLE listings (
    item_id             INTEGER PRIMARY KEY,
    thread_id           INTEGER NOT NULL,
    url_path            TEXT    NOT NULL DEFAULT '',
    title               TEXT    NOT NULL DEFAULT '',
    title_norm          TEXT    NOT NULL DEFAULT '',
    category_id         INTEGER NOT NULL DEFAULT 0,
    category_root_id    INTEGER NOT NULL DEFAULT 0,
    micro_category_id   INTEGER NOT NULL DEFAULT 0,
    location_id         INTEGER NOT NULL DEFAULT 0,
    location_name       TEXT    NOT NULL DEFAULT '',
    geo_address         TEXT    NOT NULL DEFAULT '',
    lat                 REAL,
    lng                 REAL,
    -- coords_precise is coords.precision being address-level rather than a
    -- city centroid. A centroid shared by half a district is not evidence of
    -- anything, and the repost linker must be able to tell the two apart.
    coords_precise      INTEGER NOT NULL DEFAULT 0,
    seller_id           INTEGER REFERENCES sellers(id) ON DELETE SET NULL,

    -- Avito's own absolute epoch milliseconds, kept as sent. sortTimeStamp is
    -- a bump time and not a creation time, which is why a jump in it with
    -- nothing else moving is its own change kind rather than a new listing.
    sort_ts_ms          INTEGER,
    allow_ts_ms         INTEGER,

    -- repost_key is written once at insert and never updated: a title edited
    -- after a link was made must not move the key that link was made against.
    repost_key          TEXT    NOT NULL DEFAULT '',
    -- description_sha and image_key are the two strong repost signals, and
    -- both are absent from most search rows (only about twenty in fifty carry
    -- iva and images). Empty means "no row has ever carried it", not "the
    -- seller wrote none", and the linker contributes nothing at all from an
    -- absent signal rather than counting it against a match.
    description_sha     TEXT    NOT NULL DEFAULT '',
    image_key           TEXT    NOT NULL DEFAULT '',

    state               TEXT    NOT NULL DEFAULT 'active'
                        CHECK (state IN ('active', 'left-window', 'vanished', 'closed')),
    state_at            INTEGER NOT NULL,
    state_source        TEXT    NOT NULL DEFAULT 'search'
                        CHECK (state_source IN ('search', 'item-page', 'user')),
    state_reason        TEXT    NOT NULL DEFAULT '',
    -- confidence is the same axis wb.Event uses: 1.0 for a fact the item page
    -- stated, 0.3 for a conclusion drawn from something not being in a window.
    state_confidence    REAL    NOT NULL DEFAULT 1.0,

    first_seen_at       INTEGER NOT NULL,
    last_seen_at        INTEGER NOT NULL,
    -- discovered_seed marks a listing first seen by a watch's one-time seeding
    -- pass. Its appearance is not news, and the notifier must be able to say
    -- so without consulting the watch on every change.
    discovered_seed     INTEGER NOT NULL DEFAULT 0,
    first_watch_id      INTEGER
) STRICT;

-- The linker's only candidate query: rows sharing a key, oldest first.
CREATE INDEX idx_listings_repost_key ON listings(repost_key, first_seen_at)
    WHERE repost_key <> '';
-- Every chart and every export of one thing across its incarnations.
CREATE INDEX idx_listings_thread     ON listings(thread_id, first_seen_at);
CREATE INDEX idx_listings_seller     ON listings(seller_id, first_seen_at);
-- The confirmation sweeper: which listings have been gone long enough to be
-- worth one item-page request.
CREATE INDEX idx_listings_state      ON listings(state, state_at);

-- listing_snapshots is everything that moves. The shape, the fingerprint rule
-- and the anchor semantics are WB_GO's snapshots table; (dest, app_type)
-- becomes (surface) because that is what an Avito reading's context actually
-- is, and location travels on the listing rather than the reading because a
-- listing does not move between cities.
--
-- price_kind is the column WB_GO has no equivalent of and Avito forces. A null
-- price is ambiguous between «Цена договорная», which is the seller's own
-- statement and a real fact worth an event, and a parse gap, which is our
-- defect. Both give the differ a nil pointer and both correctly refuse
-- comparison, but a parser regression must not read back as a market that went
-- negotiable overnight, and the microdata fallback cannot see priceDetailed at
-- all.
--
-- path records which extractor produced this row. It is required rather than
-- diagnostic: loaderData.toggles carries runtime_catalog_drop_state_for_bots,
-- so Avito can serve a page with the hydration state removed, and a silent
-- switch from 'state' to 'dom' is the earliest warning available that this
-- install has been classified as a bot. A fact nobody records is a warning
-- nobody gets.
--
-- title lives here as well as on listings, unlike WB_GO's products.name. A
-- seller edits the title of a live advertisement and that is worth telling
-- somebody about; a schema that only kept the latest could not see it happen.
CREATE TABLE listing_snapshots (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id      INTEGER NOT NULL REFERENCES listings(item_id) ON DELETE CASCADE,
    surface      TEXT    NOT NULL CHECK (surface IN ('mobile', 'desktop')),
    ts           INTEGER NOT NULL,
    anchor       INTEGER NOT NULL DEFAULT 0,
    fingerprint  TEXT    NOT NULL,
    source       TEXT    NOT NULL CHECK (source IN ('search', 'item')),
    path         TEXT    NOT NULL CHECK (path IN ('state', 'dom')),

    price_kind   TEXT    NOT NULL CHECK (price_kind IN ('exact', 'negotiable', 'absent')),
    price_minor  INTEGER,
    currency     TEXT    NOT NULL DEFAULT 'RUB',
    price_without_discount_minor INTEGER,
    discount_pct INTEGER,
    was_lowered  INTEGER NOT NULL DEFAULT 0,
    -- The site's own rendering, kept verbatim. A parsed integer disagreeing
    -- with the string it came from is a bug that cannot be reconstructed once
    -- the string is gone, and it is how a change of unit would be noticed.
    price_label  TEXT    NOT NULL DEFAULT '',

    title        TEXT    NOT NULL DEFAULT '',
    -- Nullable, not zero-defaulted: the microdata path cannot see any of them,
    -- and a zero would be a claim rather than a silence.
    --
    -- M1 emits no change kind on is_marketplace, has_video or has_delivery, and
    -- they are captured anyway. The rule elsewhere in this design is to cut what
    -- the milestone does not need; the exception is narrow and it is this. Adding
    -- a column later is cheap, but the history before that day has no value for
    -- it and never will — and accumulated history is the whole asset. Each is one
    -- integer on a row that is only written when the fingerprint moves, they are
    -- read from the same JSON object as the fields beside them, and they cost one
    -- assignment each.
    images_count INTEGER,
    is_xl        INTEGER,
    is_remap     INTEGER,
    is_verified  INTEGER,
    is_reserved  INTEGER,
    is_marketplace INTEGER,
    has_video    INTEGER,
    has_delivery INTEGER,
    -- Item page only. Views are public but cost one request each, so most rows
    -- have none.
    views_total  INTEGER,
    views_today  INTEGER
) STRICT;

-- The index every history question needs: this listing, this surface, over
-- time. WB_GO's idx_snapshots_nm_dest_ts with the context column substituted.
CREATE INDEX idx_listing_snapshots_item_ts ON listing_snapshots(item_id, surface, ts);

-- ---------------------------------------------------------------- reposts --

-- repost_links is advisory and never merges anything.
--
-- A link changes exactly one thing: which thread a chart groups by. No
-- snapshot is moved, no price series is rewritten, no change row is edited. So
-- undo is deleting a belief, not recovering data, and that asymmetry is the
-- whole reason the linker is allowed to guess at all.
--
-- PRIMARY KEY (successor_id) plus the partial unique index below are what keep
-- a chain a chain: one predecessor, one successor, enforced by the schema
-- rather than trusted to the writer.
--
-- state 'considered' is a candidate the gates admitted and the ladder refused.
-- Those rows are the only evidence that could ever retune the ladder, and they
-- are unrecoverable once thrown away. decided_by 'user' is terminal: the
-- linker never overwrites, re-proposes or retracts a person's decision.
CREATE TABLE repost_links (
    successor_id   INTEGER PRIMARY KEY REFERENCES listings(item_id) ON DELETE CASCADE,
    predecessor_id INTEGER NOT NULL    REFERENCES listings(item_id) ON DELETE CASCADE,
    created_at     INTEGER NOT NULL,
    decided_at     INTEGER,
    basis          TEXT    NOT NULL CHECK (basis IN ('strong', 'weak', 'manual')),
    score          REAL    NOT NULL DEFAULT 0,
    state          TEXT    NOT NULL DEFAULT 'considered'
                   CHECK (state IN ('considered', 'proposed', 'linked', 'rejected', 'retracted')),
    decided_by     TEXT    NOT NULL DEFAULT 'auto'
                   CHECK (decided_by IN ('auto', 'user', 'reappeared')),
    candidates     INTEGER NOT NULL DEFAULT 1,
    gap_sec        INTEGER NOT NULL DEFAULT 0,
    -- evidence is the ladder's own terms as JSON, shown verbatim in the panel.
    -- "Why was I not told about this listing" is the question it answers.
    evidence       TEXT    NOT NULL DEFAULT '{}'
) STRICT;

CREATE UNIQUE INDEX idx_repost_links_one_predecessor
    ON repost_links(predecessor_id) WHERE state = 'linked';
CREATE INDEX idx_repost_links_pred  ON repost_links(predecessor_id);
CREATE INDEX idx_repost_links_state ON repost_links(state, created_at);

-- repost_pins are pairs a person said are not the same thing. They survive
-- every future change to the ladder, which is what makes correcting the
-- product feel like correcting it rather than arguing with it.
CREATE TABLE repost_pins (
    a         INTEGER NOT NULL,
    b         INTEGER NOT NULL,
    pinned_at INTEGER NOT NULL,
    PRIMARY KEY (a, b)
) STRICT;

-- ---------------------------------------------------------------- watches --

-- watches is one standing thing to monitor. It replaces WB_GO's jobs, and the
-- rename is the design: a job is an execution, a watch is a subscription that
-- does not end. There is no job_runs and no job_items, because the unit of
-- work is one page of one narrow query — too small to be worth resuming, and
-- the resume machinery would be paid for on every tick.
--
-- max_pages defaults to 1 and is CHECK-constrained to 2. Walking past the
-- first page or two is the loudest escalation trigger on this target: it is
-- what turns a 429 into a 403, and a 403 costs the cookie jar and therefore
-- the whole clearance the jar was carrying. This is a constraint and not a
-- default because the failure mode is somebody raising it "just for this one
-- query".
--
-- sort is fixed at '104', date order, and is not user-configurable in M1: page
-- one of a date-sorted query is the only window in which "new listing" is a
-- set difference rather than a guess.
--
-- absence_confirmations and absence_min_span_sec are both required before a
-- disappearance is believed, and they are per watch because a fast-churning
-- query needs a longer confirmation than a quiet one. Two passes thirty
-- seconds apart is one ranking blip; the span is what makes the count mean
-- elapsed time rather than poll count.
--
-- seeded_at is null until the one-time first pass has run. On a fresh watch
-- every listing is new, and a monitor that announces fifty of them is switched
-- off on the first day.
CREATE TABLE watches (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT    NOT NULL DEFAULT '',
    kind                  TEXT    NOT NULL CHECK (kind IN ('search', 'seller', 'listing')),
    url                   TEXT    NOT NULL DEFAULT '',
    query                 TEXT    NOT NULL DEFAULT '',
    category_id           INTEGER NOT NULL DEFAULT 0,
    location_id           INTEGER NOT NULL DEFAULT 0,
    price_min_minor       INTEGER NOT NULL DEFAULT 0,
    price_max_minor       INTEGER NOT NULL DEFAULT 0,
    -- The opaque f=<base64url> blob and the small flags (user=1, cd=1, radius,
    -- localPriority) as JSON, for the same reason WB_GO's jobs.params is JSON:
    -- it is the watch's own shape and has to round-trip so people can exchange
    -- ready-made configurations.
    params                TEXT    NOT NULL DEFAULT '{}',
    sort                  TEXT    NOT NULL DEFAULT '104',
    surface               TEXT    NOT NULL DEFAULT 'mobile'
                          CHECK (surface IN ('mobile', 'desktop')),
    max_pages             INTEGER NOT NULL DEFAULT 1 CHECK (max_pages BETWEEN 1 AND 2),
    interval_sec          INTEGER NOT NULL DEFAULT 300 CHECK (interval_sec >= 120),
    absence_confirmations INTEGER NOT NULL DEFAULT 2 CHECK (absence_confirmations >= 1),
    absence_min_span_sec  INTEGER NOT NULL DEFAULT 1200,
    repost_window_sec     INTEGER NOT NULL DEFAULT 2592000,

    state                 TEXT    NOT NULL DEFAULT 'idle'
                          CHECK (state IN ('idle', 'running', 'halted', 'disabled')),
    -- halted_reason is never cleared by the program. A monitor that decides on
    -- its own that the wall it hit has gone away is a monitor that resumes into
    -- the same wall all night.
    halted_reason         TEXT    NOT NULL DEFAULT '',
    halted_at             INTEGER,
    seeded_at             INTEGER,
    next_due_at           INTEGER NOT NULL DEFAULT 0,
    -- The last result count a pass was allowed to believe: catalog.totalCount
    -- on desktop, __initialData__.search.count on mobile, both landing here so
    -- the test is uniform. A collapse is a fall to below a quarter of it, which
    -- is far outside ordinary churn on a page of thirty to fifty and well
    -- inside what a truncated or partially served page looks like; the pass
    -- that saw it is refused as evidence of absence. NULL means no count was
    -- read, and then the test is skipped rather than treated as a collapse to
    -- zero -- a missing number must never become arithmetic.
    last_total_count      INTEGER,
    -- Set when totalCount is near the roughly five-thousand-result ceiling, so
    -- the watch is silently blind to its own tail. The panel says so and asks
    -- for a price band. Nothing slices automatically: a monitor that multiplies
    -- its own request budget without being asked is a monitor that earns a 403.
    too_broad             INTEGER NOT NULL DEFAULT 0,
    enabled               INTEGER NOT NULL DEFAULT 1,
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL
) STRICT;

-- The scheduler's only question.
CREATE INDEX idx_watches_due ON watches(enabled, state, next_due_at);

-- passes is one fetch of one page of one watch: what we asked, what came back,
-- and — the column the rest of this schema hangs off — whether the answer may
-- be used as evidence that something is GONE.
--
-- WB_GO needs no such column. A product missing from a Wildberries payload is
-- missing. Here a 200 can be a soft block, a hydration state can be withheld
-- from a client the site believes is a bot, and a collapsed totalCount can be
-- a filter that drifted. All three are indistinguishable from "the listing was
-- removed" if nothing records them, and each one would announce a whole page
-- of removals that never happened.
--
-- evidential is 1 only when all of: class is 'ok' or 'empty'; the pass was
-- answered by its SURFACE'S OWN primary extractor, with no degradation warning
-- on the ExtractionReport; the pass is not a seed; and the result count, on a
-- surface that supplies one, has not collapsed against this watch's last
-- evidential pass. Anything else is presence evidence only — a listing seen in
-- it is still alive, a listing absent from it proves nothing.
--
-- "Its surface's own primary extractor" is the clause that has to be read
-- carefully, and it is written this way because of a measurement. On desktop
-- the primary path is 'state', and a silent fall back to 'dom' is exactly the
-- bot-classification warning, so a desktop 'dom' pass yields presence evidence
-- only. On mobile there IS no data-mfe-state block — measured 2026-08-23,
-- thirty cards of thirty, no hydration payload on the page at all — so 'dom'
-- is not a fallback there, it is the whole reading, and it is fully
-- admissible. A rule that demanded path='state' on every surface would make
-- absence inadmissible on the DEFAULT surface, no disappearance could ever be
-- confirmed, and listing-vanished — which ships enabled — could never fire.
--
-- The collapse test is read per surface for the same reason. Desktop compares
-- total_count against watches.last_total_count, both supplied by the state
-- block. Mobile has no catalog object and therefore no totalCount; what it has
-- is __initialData__.search.count, which is stored in the same column, so the
-- test is uniform wherever a count was read and is skipped where total_count
-- is NULL. It is never a second, silent veto on the default surface.
--
-- 'empty' counts as evidential and that distinction is load-bearing: a query
-- whose catalog object is present and reports zero results is a real answer,
-- and a rule that demanded a non-empty page could never confirm the removal of
-- a watch's last listing.
--
-- degraded_why is the operator's answer to "why did my removal alerts stop".
CREATE TABLE passes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    watch_id     INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    started_at   INTEGER NOT NULL,
    finished_at  INTEGER,
    page         INTEGER NOT NULL DEFAULT 1,
    surface      TEXT    NOT NULL DEFAULT '',
    -- avito.Class.String(). No CHECK: the catalogue lives in Go and grows
    -- there, and a released database must not refuse a reading because a new
    -- class exists — WB_GO's reasoning for jobs.type and rules.event_kind.
    class        TEXT    NOT NULL DEFAULT '',
    http_status  INTEGER NOT NULL DEFAULT 0,
    path         TEXT    NOT NULL DEFAULT '' CHECK (path IN ('', 'state', 'dom')),
    mode         TEXT    NOT NULL DEFAULT '' CHECK (mode IN ('', 'blanktrail', 'direct')),
    port         INTEGER NOT NULL DEFAULT 0,
    session      TEXT    NOT NULL DEFAULT '',
    egress       TEXT    NOT NULL DEFAULT '',
    elapsed_ms   INTEGER NOT NULL DEFAULT 0,
    bytes        INTEGER NOT NULL DEFAULT 0,

    items_seen   INTEGER NOT NULL DEFAULT 0,
    total_count  INTEGER,
    -- arrivals is how many of this pass's listings were not in the previous
    -- one. It is what makes displacement computable, and with window size it
    -- is what says whether the poll interval is short enough to see everything
    -- the watch claims to see.
    arrivals     INTEGER NOT NULL DEFAULT 0,
    -- Rows whose item.type was neither a known advertisement type nor a known
    -- layout type. Counted rather than dropped: an unrecognised type silently
    -- discarded is listings the monitor stops reporting with no sign of it.
    unknown_types INTEGER NOT NULL DEFAULT 0,
    -- The site telling us, in its own hydration payload, what it thinks we
    -- are. Nullable, because absent is not the same as false.
    is_bot       INTEGER,
    drop_state   INTEGER NOT NULL DEFAULT 0,

    is_seed      INTEGER NOT NULL DEFAULT 0,
    evidential   INTEGER NOT NULL DEFAULT 0,
    degraded_why TEXT    NOT NULL DEFAULT '',
    error        TEXT    NOT NULL DEFAULT '',
    -- The archived response, for a pass that failed in a way worth reading
    -- later. Nullable and ON DELETE SET NULL so the body collector can
    -- actually run: a reference that can never be dropped is a garbage
    -- collector that can never fire.
    body_sha     TEXT    REFERENCES bodies(sha256) ON DELETE SET NULL
) STRICT;

CREATE INDEX idx_passes_watch_started ON passes(watch_id, started_at);
-- "Give me the last admissible reading of this watch", which is the absence
-- query, and "what does the block picture look like this hour".
CREATE INDEX idx_passes_evidential    ON passes(watch_id, evidential, started_at);
CREATE INDEX idx_passes_class_started ON passes(class, started_at);
-- The extraction-drift alarm, without a table scan.
CREATE INDEX idx_passes_path_started  ON passes(path, started_at);

-- pass_items is the membership set: which listings this fetch returned, and
-- where. It is the raw evidence behind every appearance and every
-- disappearance, and it is what makes "why was I told this was gone"
-- answerable a month later.
--
-- item_id carries no foreign key, for the reason WB_GO's 0002_signals.sql
-- gives for nm_id: a key would turn a row we cannot resolve yet into a failed
-- insert, which is the monitor losing a reading it already paid a request for.
CREATE TABLE pass_items (
    pass_id  INTEGER NOT NULL REFERENCES passes(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    item_id  INTEGER NOT NULL,
    PRIMARY KEY (pass_id, position)
) STRICT;

CREATE INDEX idx_pass_items_item ON pass_items(item_id, pass_id);

-- watch_members is what a watch currently believes is in its result set, with
-- its own absence counter.
--
-- The counter is per watch and not per listing, because a listing belongs to
-- several watches and absence from one is not absence from the listing. One
-- counter for both is the shortest path to announcing removals that never
-- happened. The listing-level verdict is a query over this table — "is this
-- still present in any watch" — run once per pass, which is an index seek and
-- needs no notion of a global tick.
--
-- last_position and displaced are the displacement account. In a date-sorted
-- window of k slots, an item at rank r is pushed out by k − r arrivals, so an
-- absence explained by arrivals is not evidence of anything and must not
-- advance the counter. That arithmetic is exact and costs no extra request; it
-- is the difference between "gone" and "we only look at the first page".
CREATE TABLE watch_members (
    watch_id       INTEGER NOT NULL REFERENCES watches(id)  ON DELETE CASCADE,
    item_id        INTEGER NOT NULL REFERENCES listings(item_id) ON DELETE CASCADE,
    first_seen_at  INTEGER NOT NULL,
    last_seen_at   INTEGER NOT NULL,
    last_position  INTEGER NOT NULL DEFAULT 0,
    window_size    INTEGER NOT NULL DEFAULT 0,
    missing_streak INTEGER NOT NULL DEFAULT 0,
    missing_since  INTEGER,
    displaced      INTEGER NOT NULL DEFAULT 0,
    present        INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (watch_id, item_id)
) STRICT;

CREATE INDEX idx_watch_members_present ON watch_members(watch_id, present, last_seen_at);
CREATE INDEX idx_watch_members_item    ON watch_members(item_id, present);

-- ---------------------------------------------------------------- changes --

-- changes is one thing that moved, and it is the only catalogue.
--
-- WB_GO carries two: wb.EventKind in events plus event_changes, and
-- track.Kind, which is the one internal/rules actually evaluates against —
-- rules.go, predicate.go and suppress.go import internal/track and nothing
-- else. Carrying that fork into a greenfield project would be lifting a known
-- wart, so this is track.Change persisted and nothing more.
--
-- was and now are TEXT, as WB_GO's event_changes already has them. An integer
-- pair would have nowhere to put a title, a description digest or a
-- moderation reason, and widening the busiest table in the schema later is the
-- one migration worth avoiding by writing one word differently today.
--
-- had_before and has_now are track.Change's own two flags. They are why a
-- listing with no price does not read as a listing priced at zero, and why a
-- percentage refuses an appearance instead of reporting an infinite rise.
--
-- ts is when the reading that revealed this was taken; saved_at is always the
-- store's clock. 0007_observation_provenance.sql's distinction, carried over:
-- without the pair, "read at 14:00" and "no time given, learned about it at
-- 14:00" are the same row.
--
-- backfill marks a change produced by a watch's seeding pass. The row is
-- written, because the history is real; the notifier skips it, so a fresh
-- watch produces a populated database and no messages.
CREATE TABLE changes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT    NOT NULL,
    item_id     INTEGER NOT NULL DEFAULT 0,
    thread_id   INTEGER NOT NULL DEFAULT 0,
    seller_id   INTEGER NOT NULL DEFAULT 0,
    watch_id    INTEGER NOT NULL DEFAULT 0,
    -- The evidence, with no foreign key and nullable, for
    -- notify_outbox.rule_event_id's reason: retention removes old passes and
    -- old snapshots, and a change must outlive the reading it rests on rather
    -- than be cascaded away with it.
    pass_id     INTEGER,
    snapshot_id INTEGER,
    ts          INTEGER NOT NULL,
    saved_at    INTEGER NOT NULL,
    subject     TEXT    NOT NULL DEFAULT '',
    field       TEXT    NOT NULL DEFAULT '',
    was         TEXT    NOT NULL DEFAULT '',
    now         TEXT    NOT NULL DEFAULT '',
    unit        TEXT    NOT NULL DEFAULT '',
    had_before  INTEGER NOT NULL DEFAULT 0,
    has_now     INTEGER NOT NULL DEFAULT 0,
    confidence  REAL    NOT NULL DEFAULT 1.0,
    backfill    INTEGER NOT NULL DEFAULT 0,
    -- Which producer wrote this. Membership differences are computed inside
    -- the pass transaction, because a set difference against the previous
    -- window must not be observed half-written; series differences are
    -- computed out of band from two snapshots; and the transport producer
    -- writes the health kinds, which come from the collector observing itself
    -- rather than from comparing two readings. Three producers into one table
    -- is a real fork, so it is recorded rather than left for a reader to
    -- deduce -- and the third is the one a reader would otherwise conclude was
    -- a typo in the CHECK.
    producer    TEXT    NOT NULL DEFAULT 'series'
                CHECK (producer IN ('window', 'series', 'transport'))
) STRICT;

CREATE INDEX idx_changes_item_ts   ON changes(item_id, ts);
CREATE INDEX idx_changes_kind_ts   ON changes(kind, ts);
CREATE INDEX idx_changes_thread_ts ON changes(thread_id, ts);
CREATE INDEX idx_changes_watch_ts  ON changes(watch_id, ts);

-- ----------------------------------------------------------------- bodies --

-- bodies is the archived response of a pass that went wrong, content-addressed
-- so that a hundred identical walls cost one copy.
--
-- Only a pass whose class is not 'ok' or 'empty' is archived, plus a pass
-- whose extraction shape we stopped understanding. That is deliberately the
-- opposite of a full ledger: the successful pages are reproducible from the
-- data they produced, and the failures are the ones nobody can debug from a
-- row count.
CREATE TABLE bodies (
    sha256        TEXT    PRIMARY KEY,
    algo          TEXT    NOT NULL CHECK (algo IN ('identity', 'flate')),
    size_raw      INTEGER NOT NULL,
    bytes         BLOB    NOT NULL,
    first_seen_at INTEGER NOT NULL
) STRICT;

-- ------------------------------------------------------------ egress, ops --

-- channels and proxies are WB_GO's 0005_ops.sql verbatim: one way out to the
-- network, and the parsed entries of a list. Both transport modes have an
-- egress, so the BlankTrail-versus-direct decision is a setting rather than a
-- channel kind.
CREATE TABLE channels (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    name                    TEXT    NOT NULL DEFAULT '',
    kind                    TEXT    NOT NULL CHECK (kind IN ('proxy-list', 'rotating', 'gateway', 'direct')),
    source                  TEXT    NOT NULL DEFAULT '',
    rotate_url              TEXT    NOT NULL DEFAULT '',
    rotate_min_interval_sec INTEGER NOT NULL DEFAULT 0,
    weight                  INTEGER NOT NULL DEFAULT 100,
    enabled                 INTEGER NOT NULL DEFAULT 1,
    created_at              INTEGER NOT NULL,
    updated_at              INTEGER NOT NULL
) STRICT;

CREATE TABLE proxies (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id   INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    scheme       TEXT    NOT NULL CHECK (scheme IN ('http', 'https', 'socks5', 'socks5h', 'socks4')),
    host         TEXT    NOT NULL,
    port         INTEGER NOT NULL,
    username     TEXT    NOT NULL DEFAULT '',
    password     TEXT    NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    last_ok_at   INTEGER,
    last_fail_at INTEGER,
    fail_streak  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (channel_id, scheme, host, port, username)
) STRICT;

-- transport_sessions is a warm cookie jar that survives a restart.
--
-- A warm session is the most expensive object in this program: a round trip, a
-- pause, and whatever reputation the identity cookie has accumulated since.
-- Discarding it on every restart means every restart looks like a brand new
-- visitor from an address that has been browsing all day, which is a worse
-- story than not restarting.
--
-- The nonce cookie lives sixty seconds and is refreshed on every response, so
-- it is always stale after a restart. That is not a reason to discard the
-- long-lived identity cookie beside it: one cheap request to the site root
-- refreshes the nonce and keeps everything else.
CREATE TABLE transport_sessions (
    session_key   TEXT    PRIMARY KEY,
    mode          TEXT    NOT NULL CHECK (mode IN ('blanktrail', 'direct')),
    profile       TEXT    NOT NULL DEFAULT '',
    opened_at     INTEGER NOT NULL,
    warmed_at     INTEGER,
    requests      INTEGER NOT NULL DEFAULT 0,
    window_json   TEXT    NOT NULL DEFAULT '[]',
    consecutive_403 INTEGER NOT NULL DEFAULT 0,
    cookies_json  TEXT    NOT NULL DEFAULT '[]',
    updated_at    INTEGER NOT NULL
) STRICT;

-- egress_windows is the ten-minute ceiling, and it is keyed by address rather
-- than by session on purpose: several identities may share one egress, and a
-- window stored per session hands each of them the whole budget on the next
-- restart, which is precisely the restart cheat the pacer exists to prevent.
-- transport_sessions keeps the per-identity floor; this keeps the per-address
-- ceiling; neither can be derived from the other.
CREATE TABLE egress_windows (
    egress       TEXT    PRIMARY KEY,   -- '' is the host's own address
    window_json  TEXT    NOT NULL DEFAULT '[]',
    updated_at   INTEGER NOT NULL
) STRICT;

-- rules, rule_events, notify_targets and notify_outbox are WB_GO's 0005_ops.sql
-- and 0010_rule_events_subject.sql with the scope vocabulary changed and the
-- subject widened from a numeric id to a text key.
--
-- scope_kind gains 'thread' because a price rule is about the thing being
-- sold, not about the number it currently wears, and loses 'product' and 'job'
-- because neither exists here.
CREATE TABLE rules (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT    NOT NULL DEFAULT '',
    event_kind         TEXT    NOT NULL,
    condition          TEXT    NOT NULL DEFAULT '{}',
    scope_kind         TEXT    NOT NULL CHECK (scope_kind IN ('listing', 'thread', 'seller', 'watch', 'filter')),
    scope_id           INTEGER NOT NULL DEFAULT 0,
    scope_filter       TEXT    NOT NULL DEFAULT '{}',
    urgent             INTEGER NOT NULL DEFAULT 0,
    threshold_pct      INTEGER NOT NULL DEFAULT 0,
    threshold_minor    INTEGER NOT NULL DEFAULT 0,
    threshold_currency TEXT    NOT NULL DEFAULT '',
    min_interval_sec   INTEGER NOT NULL DEFAULT 0,
    aggregate          INTEGER NOT NULL DEFAULT 0,
    targets            TEXT    NOT NULL DEFAULT '[]',
    enabled            INTEGER NOT NULL DEFAULT 1,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
) STRICT;

-- suppressed_by is empty when the message went to the queue, and otherwise
-- names what stopped it — a quiet hour, a rate limit, a duplicate, a seeding
-- pass, or a repost link. "Why was I not told" is the question this column
-- exists to answer, and a product that silently drops notifications cannot be
-- debugged by the person who stopped receiving them.
--
-- subject_kind and subject_key are a pair rather than one id because the rate
-- limit is per thread for a price rule and per listing for a lifecycle one: a
-- repost must not reset a price rule's limit, and it must not silence the
-- successor's appearance either.
CREATE TABLE rule_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id       INTEGER NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
    change_id     INTEGER,
    fired_at      INTEGER NOT NULL,
    kind          TEXT    NOT NULL DEFAULT '',
    subject_kind  TEXT    NOT NULL DEFAULT '',
    subject_key   TEXT    NOT NULL DEFAULT '',
    field         TEXT    NOT NULL DEFAULT '',
    suppressed_by TEXT    NOT NULL DEFAULT '',
    dedup_key     TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_rule_events_rule_fired ON rule_events(rule_id, fired_at);
CREATE INDEX idx_rule_events_dedup      ON rule_events(dedup_key, fired_at);
CREATE INDEX idx_rule_events_subject    ON rule_events(rule_id, subject_kind, subject_key, fired_at);

CREATE TABLE notify_targets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL DEFAULT '',
    kind       TEXT    NOT NULL,
    address    TEXT    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- due_at is the growing pause stored as the next moment rather than as a
-- delay, so a restart does not reset it. rule_event_id is nullable and cleared
-- rather than cascaded: a queued message must survive its rule being edited
-- while Telegram was unreachable, and cascading would delete exactly the
-- messages an outage delayed.
CREATE TABLE notify_outbox (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    target_id     INTEGER NOT NULL REFERENCES notify_targets(id) ON DELETE CASCADE,
    rule_event_id INTEGER REFERENCES rule_events(id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL,
    due_at        INTEGER NOT NULL,
    attempts      INTEGER NOT NULL DEFAULT 0,
    state         TEXT    NOT NULL CHECK (state IN ('pending', 'sent', 'failed')),
    sent_at       INTEGER,
    last_error    TEXT    NOT NULL DEFAULT '',
    body          TEXT    NOT NULL,
    attachment    TEXT    NOT NULL DEFAULT ''
) STRICT;

-- The worker's only question: what is pending and ready to go.
CREATE INDEX idx_notify_outbox_due ON notify_outbox(state, due_at);

-- settings is one row, and it is deliberately NOT WB_GO's shape. WB_GO's
-- 0008_settings.sql is a key-value store — (key, value, type, secret,
-- updated_at) — on the stated grounds that a column per setting means a
-- migration per setting as milestones land them one at a time. That reasoning
-- does not hold here: the whole set is known at 0001, every reader wants a
-- typed value rather than a string plus a parse, and CHECK constraints on the
-- transport decision are worth more than the flexibility. The cost is accepted
-- and named: a setting added in M2 is a migration. Secrets keep WB_GO's
-- discipline of never being logged and never leaving the panel.
--
-- requests_per_minute defaults to 6, which is the ten-minute window cap of
-- sixty divided out, not the three-and-a-half-second floor: the window is the
-- binding constraint and quoting the looser of two limits is how a budget gets
-- overspent. The effective ceiling is the lower of this figure and the mode's
-- own, and a watch schedule that exceeds it is refused when it is saved, with
-- the arithmetic shown, rather than discovered later as a ban.
CREATE TABLE settings (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    transport_mode      TEXT    NOT NULL DEFAULT ''
                        CHECK (transport_mode IN ('', 'blanktrail', 'direct')),  -- '' = the first run has not chosen yet
    blanktrail_url      TEXT    NOT NULL DEFAULT '',
    blanktrail_key      TEXT    NOT NULL DEFAULT '',
    blanktrail_ports    INTEGER NOT NULL DEFAULT 2,
    requests_per_minute INTEGER NOT NULL DEFAULT 6,
    telegram_token      TEXT    NOT NULL DEFAULT '',
    -- MTProto's own pair, from my.telegram.org, and not a substitute for the
    -- bot token: the Bot API rung needs only the token, and MTProto's bot
    -- login needs the token AND an application identity. Increment 5 is inside
    -- M1, so these live here rather than in a later migration. The settings
    -- screen keeps them hidden until the Bot API rungs have actually failed —
    -- a machine where the direct route works is never sent to my.telegram.org.
    telegram_api_id     INTEGER NOT NULL DEFAULT 0,
    telegram_api_hash   TEXT    NOT NULL DEFAULT '',
    telegram_mtproto_session BLOB,
    quiet_from_min      INTEGER NOT NULL DEFAULT -1,
    quiet_to_min        INTEGER NOT NULL DEFAULT -1,
    panel_password_hash TEXT    NOT NULL DEFAULT '',
    panel_lan           INTEGER NOT NULL DEFAULT 0,
    updated_at          INTEGER NOT NULL DEFAULT 0
) STRICT;

INSERT INTO settings (id, updated_at) VALUES (1, 0);


---

# Packages

```
cmd/avitomon                 headless binary: panel, scheduler, notifier
cmd/avitomon-tray            same program, -H windowsgui, tray icon and menu (Windows only)
cmd/avitocapture             fetches one page through the real transport and freezes it as a golden fixture
cmd/avitoparse               reads a fixture from disk and prints Page + ExtractionReport; the offline debugging tool

blanktrail                   the SDK, copied in verbatim; two added PortSpec fields and a widened neutrality list. Stdlib-only, so copying it costs no dependency.

avito                        the domain. No net, no net/http, no clock, no database — enforced by `make check-no-net`
avito/testdata               captured pages, gzipped, with meta.json (frozen clock) and want.json per fixture

internal/transport           the Leaser/Lease seam, Profile, Caps, and the header-ownership split
internal/transport/bt        Leaser over *blanktrail.Pool
internal/transport/direct    utls dialler, h2 wiring, the avito.ru-scoped jar, the pacer, session persistence

internal/track               two readings in, named changes out; no clock, no database (lifted from WB_GO)
internal/lifecycle           membership + displacement accounting -> state transitions; pure
internal/repost              candidate gates and the evidence ladder; pure
internal/rules               scope, predicate tree, suppression (lifted from WB_GO)

internal/store               SQLite: migrations, the pass transaction, reads, retention, Volume, free-space guard
internal/collect             the scheduler: due watches, the budget, one pass end to end
internal/notify              the outbox worker
internal/telegram            the Sender interface, the Bot API rung, the MTProto rung (gotd/td), and the bot's commands
internal/web                 the panel: watches, health, history, reposts, proxies, rules, results, settings
internal/export              CSV, XLSX, JSON from one column selection (lifted from WB_GO)
internal/chart               the price chart, drawn without a plotting dependency (lifted from WB_GO)
internal/events              the in-process bus; a dropped event is counted, never silent (lifted from WB_GO)

internal/config              the YAML read before the database can be opened
internal/paths               platform data directories
internal/autostart           registry Run value, LaunchAgent, systemd user unit (lifted from WB_GO)
internal/winres              the icon, drawn in Go and committed as an object file (lifted from WB_GO)
```


---

# M1, cut into increments

**1. Extraction, entirely offline.**
`avito/` with the state-block reader, the resolver chain, the DOM/microdata fallback, `ExtractionReport`, the wall markers, `Page.Ok()`, the rouble-to-kopeck boundary and the price cross-check. `cmd/avitocapture` and `cmd/avitoparse`. Fixtures: the five already captured under `docs/measurements/fixtures/` — the desktop SPA shell, the desktop canonical page, a mobile page with thirty cards, a mobile page with an empty result set, and a seller page — move into `avito/testdata/` gzipped with their `meta.json` and `want.json`, and the ones still missing are captured here: an item page, a dropped-state desktop page, a soft block and each wall. Golden tests pin the whole `Page` including which path answered; mutation tests rehash every CSS class, rename the script type, inject `&amp;` into a title and delete the state block.
*Demonstrable:* `avitoparse testdata/fixtures/desktop-canonical-p1` prints 50 listings, `path=state`, `rule=desktop.catalog.v1`, and the same 50 ids from the microdata path. `make check-no-net` passes.

**2. Transport, both modes, no database.**
The `Leaser`/`Lease` seam, the classifier, `CountFailure` wired to 429 only, the widened neutrality list. The pool opened over `Channels` with one upstream per port, `blanktrail/upstream.go`'s six line formats and its file/URL refresh, and `Mixer`'s weight decay — the per-port-address claim is load-bearing for the whole budget and must be exercised here, not first met in the panel. Direct mode's utls dialler, jar, pacer, warm-up and session file. The `fakeavito` test server that enforces every measured fact: refuses a request with no nonce or a nonce over sixty seconds old, issues the identity cookie only on a root navigation, escalates 429 to 403 past ninety requests in ten minutes, never sends `Retry-After`, and has switches for dropped state, soft block and 439.
*Demonstrable:* `avitomon fetch -mode direct <url>` prints class, path, item count and session; the same with `-mode blanktrail`. Tripping 439 in direct mode halts and prints the boundary message, and the fake server's request counter does not move for the following simulated hour.

*Also in this increment:* **the soak run** — done, 2026-08-23/24, written up in `docs/measurements/2026-08-23-soak.md` with its raw JSONL alongside. Seven arms, about 1200 requests, one shared egress address.

It answered the three questions and refuted four things this spec believed. **Does a warm utls session hold at 3.5 s?** Yes, and there is no ceiling in requests: 391 fetches from one address, all clean, ten self-healing refusals, stopped by the operator on a steady state. **Do several jars on one address beat one?** No — 23 clean requests per hour against 252, an order of magnitude worse, because the refusal is bound to the address and more jars only reach its limit sooner. **What replaces "300–700"?** Not a request count, because neither transport has a ceiling in requests. The honest figures are about **250–260 clean requests per hour direct** and about **360 through BlankTrail**, both stopped by the operator rather than by the site, both from one address, both carrying the confounds §8 of that document lists. Direct's real cost is a 74% time tax: sixty-nine of ninety-three minutes spent waiting out ten refusals.

Nothing downstream may quote a throughput number this run did not produce, and nothing may quote one without the sample size and the single-address caveat attached.

**3. The store and one pass.**
The migration, `SavePass` in one transaction, `listings` and `listing_snapshots` with the lifted fingerprint and anchor rules, `pass_items`, `watch_members`, `store.Volume`, the free-space precheck, retention with the lifted windowed `DELETE`.
Decision 3 forbids a transport default, and increments 3, 4 and 5 all collect before the panel that asks exists — so `avitomon transport set blanktrail|direct` lands here, writing the same one column the first screen will write later. It is the command-line half of the same decision, not a default sneaking in by another door, and increment 6's screen replaces the need for it rather than the mechanism.

*Demonstrable:* `avitomon watch add "<url>"`, then `avitomon run --once` writes a pass and its listings; `avitomon volume` prints the file size and per-table row counts; running retention against a synthetic year leaves one row per day past thirty days and one per week past a year.

**4. Lifecycle and changes.**
`internal/lifecycle` with displacement accounting and the two absence gates, the `evidential` computation, the window producer inside the pass transaction and the series producer out of band, `internal/track` lifted, the change catalogue, seed suppression, the saturation metric.
*Demonstrable:* a scripted sequence of fixture pages produces exactly the right `changes` rows — the seeding pass writes rows and marks them `backfill`; a listing pushed off the bottom by arrivals produces nothing; a listing gone from two clean passes twenty minutes apart produces `listing-vanished` at confidence 0.3; and two consecutive dropped-state passes produce **no** vanished rows at all.

**5. Rules, the outbox and the first message.**
`internal/rules` lifted with the three edits, `DedupKey` on `thread_id` for price kinds and numberless for `listing-appeared`, the suppression reasons `seed` and `repost`, `notify_outbox` and its worker, the Telegram sender over the same seam.
*Demonstrable:* a new listing on a seeded watch produces one Telegram message; the same listing flapping in and out of the window produces no second one; a `rule_events` row exists for every suppression with its reason.

*Also in this increment:* **MTProto**, behind the same `Sender` interface as the Bot API, with `gotd/td` taken as a dependency and its live exchange verified against real Telegram rather than assumed — WB_GO's copy never was.

**6. The panel, the chart and export.**
Watches with the budget arithmetic, Здоровье сбора with the path shares and the evidential fraction, История with the price chart, Прокси with the mix, the per-entry tests and the ceiling arithmetic, Правила, Результаты with CSV/XLSX/JSON, Настройки with the BlankTrail check and the LAN refusal.
*Demonstrable:* the whole product is usable from a browser with no command line; a schedule over budget is refused at save time with the sum shown; a halted watch shows a banner naming the marker that halted it.

**7. Repost linking and undo.**
`internal/repost` with the gates and the ladder, `repost_links`, `repost_pins`, `RecomputeThread`, the retraction on reappearance, the Перепубликации screen and the inline Telegram button.
*Demonstrable:* a synthetic seller who closes and re-publishes one item produces `listing-reposted` and no `listing-appeared` message; a dealer with eight identical live phones produces zero links and eight ordinary alerts; pressing «Это не перепубликация» pins the pair, splits the thread, and replays the withheld alert.
