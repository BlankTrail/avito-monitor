# Avito surface — verified live, 2026-08-23

Method: real Chrome + curl through BlankTrail (socks5h://127.0.0.1:20000, js_solver port).
Everything below was **observed**, not inferred.

## 1. Transport (the product premise, proven)

| Client | Result |
|---|---|
| plain curl, direct, 1st request | **HTTP 429 + wall markers** |
| curl through BlankTrail :20000 | **HTTP 200**, clean body, no wall |

BlankTrail control API live: `http://127.0.0.1:8891`, `X-API-Key` header.
`GET /api/v1/license/status` → `{"activated":true,"plan":"enterprise","pool":true,
"js_solver_max_procs":10,"js_solver_procs":10,"js_solver_live_procs":1}`
`GET /api/v1/health` → `{"status":"ok"}`; `GET /api/v1/ports/suggest` → `{"port":20002}`

Live port config exposes fields **absent from the WB_GO `blanktrail` SDK's `openPortRequest`**:
`captcha_action:"rotate_retry"`, `captcha_max_attempts:3`, `vdns_mode`, `cache_enabled`,
`cache_mode`, `cache_normalize_ids`, `auto_profile_from_ua`, `leak_guard`, `debug_capture`,
`effective_idle_seconds:1800`, `max_concurrent:64`.
→ the vendored SDK copy must be **extended** with the captcha-policy knobs for Avito.

## 2. Fetch shape: a search costs TWO requests

`/all?q=…` is an **SPA shell** (386 KB, 0 listings). It carries the canonical URL inside a
`<script type="mime/invalid" data-mfe-state="true">` block of pure JSON:

```json
{"loaderData":{"data":{"status":{"code":301},"redirected":true,"url":"/all/telefony/…"}}}
```

Follow it → canonical page (2.28 MB) with the results. Pagination `&p=N`, **max 100 pages**
(`pagination-button/page(100)`), 50 per page → **hard ceiling ~5000 results per search**.
Full category sweeps therefore MUST be sliced (price bands / regions / date windows).

## 3. Parsing — no fragile regex needed

Canonical page carries exactly **one** `data-mfe-state="true"` script = 671 KB of JSON.
Path: `loaderData.data`.

Counts: `count`, `totalCount` (34692), `totalElements`, `mainCount`, `itemsOnPage` (50).
Listings: `loaderData.data.catalog.items[]` (50). Pager: `catalog.pager.next`.
Also present: `filtersV2` (facet tree), `searchCore`, `seo`, `seoTags`, `rubricators`,
`changeLocations`, `geoMap`, `subscription`, `searchHash`, `context`.

### Fields on ALL 50 listings
`id`, `categoryId`, `microCategoryId`, `locationId`, `urlPath`, `title`,
`category{id,name,slug,rootId}`, `location{id,name,isRegion}`, `addressDetailed{locationName}`,
`geo{formattedAddress}`, **`coords{lat,lng,precision,address_user}`**,
**`sortTimeStamp` / `allowTimeStamp` — absolute epoch ms** (no "5 минут назад" parsing),
`priceDetailed{value:int, string, fullString, wasLowered:bool, exponent}`,
`priceWithoutDiscount`, `discountPercent`, `normalizedPrice`,
`rating{score:float, summary}`, `closedItemsText` ("362 завершённых объявления"),
`contacts{phone,delivery,message}`, `userLogo{link:/brands/<slug>}`, `imagesCount`,
**`isXl`** (XL promotion), **`isReMapPromotion`**, `isVerifiedItem`, `isNew`, `isReserved`,
`isMarketplace`, `hasVideo`, `closestAddressId`, `type`.

### Fields on only 20/50
`images{208x208…636x636}` and `iva` — the render-step payloads:
`iva.DescriptionStep[].payload.description` (**full description inline in search results**),
`iva.SecondLineStep[].payload.value` ("Компания" → private vs company),
`iva.UserInfoStep[].payload.{profile,rating}`, `iva.AutoParamsStep[].payload.text` ("Новый"),
`iva.BadgesStep`, `iva.DateInfoStep[].payload.{absolute,relative}`.

### Item page (629 KB)
**No `ld+json`, no `itemprop="price"`.** Use `data-marker` attributes:
`item-view/item-id`, `item-view/item-date`, `item-view/item-price`, `item-view/item-params`,
`item-view/item-description`, **`item-view/total-views`**, **`item-view/today-views`**,
`item-map-wrapper`/`map-full`, `breadcrumbs`, `badge-<id>`, `seo-navigation/seo-tags`.
→ views ARE public.

### Search page also server-renders microdata
50 × `data-marker="item"` + `data-item-id` + `itemprop="price"` — an **independent second
extraction path** on the same document.

## 4. Why two paths are mandatory (not belt-and-braces)

`loaderData.toggles` contains feature flags including
**`runtime_catalog_drop_state_for_bots`**, `runtime_catalog_bots_drop_state`,
`disable_web_features_request_for_crawlers`, `enable_serp_image_ssr_for_bot`.
Avito can **drop the SSR state for clients it thinks are bots**. The hydration data also
exposes `buyerLocationProps.isBot: boolean`.
→ Parser design: **JSON state primary, DOM/microdata fallback**, with a metric on which path
was used. A silent switch to the fallback is an early warning of being flagged.

## 5. Encoding traps (already paid for once in the Python tester)

- Avito serves `br`/`zstd`. Body MUST be decompressed before matching/parsing.
- `Content-Type: text/html` often **without `charset=`** → must decode UTF-8 explicitly.
- Both failures are silent: wall reads as "HTTP 200", parse reads as "0 results".
- Log must carry wire bytes, decoded bytes AND declared encoding to tell these apart.

## 6. Wall / challenge markers (from the working tester, still valid)

`проблема с IP` (IP block) · `подключиться к другой сети` (hard IP block) ·
`Продолжить для решения` (slider) · `geetest` / `gcaptcha4` (Geetest slider) ·
`Доступ ограничен` · `qrator` (the WAF) · `Слишком много запросов` (rate limit).
Direct client hit 429 + wall on request #1.

## 7. No lighter JSON API

SPA pagination triggers a **full document navigation**, not an XHR. There is no internal
search JSON endpoint reachable that way. The HTML document is the API.
Other endpoints seen: `POST /js/recommendations`, `POST /clickstream/events/1/json`,
`mon.avito.ru/?r=<ts>` + `mon-check.js`, `remote-verification/HashVerificationSdk.js`.

## 8. Open probes (not yet verified)

- m.avito.ru weight/shape with a **mobile UA** (desktop UA redirects to www — my measurement
  was invalid).
- Seller/profile page and reviews page structure.
- Whether `iva` presence (20/50) is an A/B variant or depends on request shape.
- Sustained throughput per port before a wall (the 300–700 claim) — needs a soak run.

---

# ADDENDUM — verified 2026-08-23, second pass

## 9. The surface is chosen by the BlankTrail PORT PROFILE, not by the UA header

Sending an iPhone User-Agent through port 20000 (profile `Chrome_151_win`) still got redirected
`m.avito.ru` → `www.avito.ru`: BlankTrail **replaces** the UA with the port profile's own.
Opening a port with `{"browser":"safari","os":"ios"}` produced profile `Safari_26_ios_gcd264b17`
and `m.avito.ru` then answered **200 with no redirect**.

→ In mode (a) the surface is a `PortSpec{OS,Browser}` decision — exactly WB_GO's `wb/mode.go`
  `Mode.Spec(base blanktrail.PortSpec)` pattern, which transfers verbatim.
→ In mode (b) the surface IS the UA + header set, which is why the desktop/mobile header sets
  matter only there. `Accept-Language: ru-RU` must be sent in BOTH modes — BlankTrail passes it
  through verbatim and Avito needs it.

## 10. Desktop vs mobile — measured, and it settles the monitoring surface

|                     | desktop `www`            | mobile `m.` (iOS port)  |
|---------------------|--------------------------|-------------------------|
| requests for page 1 | 2 (shell → canonical)    | **1**                   |
| bytes page 1        | 386 KB + 2.28 MB         | **1.25 MB**             |
| items per page      | **50**                   | 30                      |
| `data-mfe-state`    | **1 block, 671 KB JSON** | **absent entirely**     |
| DOM cards           | 50 (`data-marker="item"`)| 30 (`item-wrapper(<id>)`)|
| publication time    | **`sortTimeStamp` epoch ms** | **NONE — see below** |

**Mobile carries no date at all.** `data-marker="sortTimeGrid"` is an empty `<div>`, and the page
has zero hits for relative-time text, «вчера/сегодня», or `datetime=`.
→ **Desktop is the monitoring surface.** Mobile is a cheap fallback where "new" can only mean
  "this id was not seen before".

Mobile cards ARE otherwise complete (this **contradicts** the older Python tester's comment that
price is missing on mobile — that is out of date). Verified 30/30 via a proper tree walk:
`titleLabelGrid` → title, `priceLabelGrid` → "49 490 ₽", `itemParamsGrid` → "Новый",
`addressLabel` → "Республика Татарстан, Казань", `sellerAndRatingSellerLabel` → "TorStore Kazan",
`itemProp="description"` on a `<meta>`, and the id in BOTH `item-wrapper(<id>)` and
`data-marker-id="<id>"` on the `item/link` anchor.

## 11. Seller pages use the same extraction path

`/brands/<slug>` = 855 KB, **one** `data-mfe-state="true"` block, 45 × `"urlPath"`,
15 × `data-item-id`. One extractor serves search AND seller.

## 12. Avito labels below-market prices itself

"Цена ниже рыночной" appears 45× on one mobile results page. A free signal for the
"выгодные предложения" scenario — no need to compute it from our own median first.

## 13. Method note that belongs in the code review checklist

An early probe here extracted "the next 400 characters after the data-marker" and got the
`class=` attribute instead of the text — the exact fragility the no-regex rule exists to prevent.
Every extraction MUST resolve an element and take its subtree text. Class names are
CSS-modules hashes (`_71fa931013cec915`) and are worthless as handles.

## 14. The mobile surface DOES have a state container — `window.__initialData__`

Corrects §10: mobile has no `data-mfe-state`, but it is not stateless. An attribute-less
`<script>` carries `window.__initialData__ = "<json text>"` — a JS string literal holding JSON,
so it is two `json.Unmarshal` calls after an escape-aware scan for the first string literal.
260 KB on an empty query, 832 KB with 30 results. Listings are NOT in it (`urlPath` and
`priceDetailed` appear zero times); they remain the server-rendered beduin cards.

What it does carry is exactly what the classifier needs:

| `__initialData__.search` | 30 results | empty query |
|---|---|---|
| `mainCount` / `count`    | 59959      | 0 |
| `emptySearch`            | absent     | "Ничего не найдено по запросу «…»" |

So the mobile classifier is a full four-way split, symmetric with desktop's:

- `__initialData__` present, cards > 0            → **ClassOK**
- `search.count == 0` / `emptySearch` present     → **ClassEmpty** (a real answer)
- `search.count > 0` but zero cards extracted     → **ClassShape** (our parser broke)
- no `__initialData__` at all                     → **ClassSoftBlock**

Two consequences beyond the classifier:

1. `evidential` must be defined per surface — "the surface's own canonical path succeeded",
   not the literal `path='state'`. On desktop that is the `data-mfe-state` catalogue; on mobile
   it is `__initialData__` parsed plus a card count consistent with `search.count`. Mobile DOM
   is a PRIMARY complete rendering (30/30), not the degraded subset that the desktop DOM
   fallback is, and the admissibility rule must not conflate the two.
2. `search.count` supplies the total on mobile, so the `too_broad` guard (> 4500) works there
   too. It is not desktop-only.

Note for the parser: `window.__initialData__` is also the name the older Python tester's
fixture used, so that name belonged to the mobile surface all along.

## 15. Correction: the mobile card has no description

§10 said the mobile card carries a description in `<meta itemProp="description">`. It does
not. Measured against `mobile-search-30.html.gz` while implementing the extractor: the page
has exactly 30 such elements, all 30 carry the literal string `...`, and no `content`
attribute anywhere on the page holds Russian text longer than 40 characters.

So the description is available on the desktop surface only, and there on about 20 rows in 50
(`iva.DescriptionStep[].payload.description`). On the **default** collection surface it does
not exist.

This is not cosmetic. The repost linker treats an identical description digest as one of its
two strong corroborators, and the other is the image key, which is itself unverified. On
mobile it therefore has neither, and an auto-link rests on the weaker
title-plus-coordinates-plus-price tier. The extractor reads the placeholder and discards it
rather than not reading it, so the day Avito fills it in there is one place to change.

## 16. Correction: the seller page is a different envelope

§11 said `/brands/<slug>` uses "the same single `data-mfe-state` block" as a search page. The
block is in the same place; what is inside it is not. Measured while implementing the
resolver:

| | search page | seller page |
|---|---|---|
| top-level keys | `loaderData`, … | `i18n`, `state`, `toggles` — **no `loaderData` at all** |
| items | `loaderData.data.catalog.items` (50) | `state.searchData.profileCatalog.items` (**15**) |
| count | `loaderData.data.totalCount` | `state.searchData.profileCatalog.totalCount` (772) |

The 45 `urlPath` occurrences counted in §11 were not 45 listings — the block holds other
objects carrying that key too.

The item objects underneath are identical in shape (`priceDetailed`, `sortTimeStamp`, `coords`),
so one decoder serves both and only the path to the array differs. That is what makes the
resolver an ordered table of NAMED rules rather than one rule plus a probe: two envelopes are
known, each is named, and the probe stays as the last resort for a third nobody has seen.

The desktop DOM fallback does **not** cover a seller page: it selects on `data-marker="item"`,
which a seller page does not use. The cross-path audit is therefore a search-page calibration
only, and on a seller page it reports every listing as state-only. That is honest rather than
broken, but it means a seller watch has no second opinion if Avito withholds the state block.
