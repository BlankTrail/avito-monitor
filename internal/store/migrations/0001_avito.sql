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
