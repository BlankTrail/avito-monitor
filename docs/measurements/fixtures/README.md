# Captured pages — read this before publishing the repository

Five pages retrieved from avito.ru on 2026-08-23, gzipped as served. They are what
the measurements in `../2026-08-23-avito-surface.md` were read from, and they are
what the extraction tests in `avito/testdata/` will be built from.

| file | what it is |
|---|---|
| `desktop-spa-shell.html.gz` | `www.avito.ru/all?q=iphone+15` — the 386 KB shell, zero listings, carrying only the canonical redirect |
| `desktop-canonical-p1.html.gz` | the canonical page it redirects to — 2.28 MB, one `data-mfe-state` block, 50 listings |
| `mobile-search-30.html.gz` | `m.avito.ru/all?q=iphone+15` through an iOS/Safari port — 30 `item-wrapper` cards, `window.__initialData__`, no `data-mfe-state` |
| `mobile-search-empty.html.gz` | the same with a query that matches nothing — `search.count == 0` and `search.emptySearch` |
| `desktop-seller.html.gz` | `/brands/<slug>` — 855 KB, the same single `data-mfe-state` block as a search page |

## They carry personal data

Seller display names, street addresses and coordinates precise to a building. This
repository is AGPL and intended to be public. **Scrub before publishing.**

The scrub is field-allowlisted and it must not touch element structure, attribute
names or `data-marker` values — a fixture whose markup has been altered stops
testing the thing it was captured to test, and an over-cautious scrub is as
damaging as a leak. Replace the *values* of seller names, addresses, coordinates
and image URLs; leave every tag, attribute and marker exactly as served.
