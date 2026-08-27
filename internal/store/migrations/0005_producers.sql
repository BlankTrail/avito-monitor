-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Two producers the original CHECK could not have known about.
--
-- changes.producer was a closed list of three — window, series, transport — and
-- the comment in migration 0001 is right that three producers into one table is
-- a real fork worth recording. There are now five, and both new ones arrived
-- for the same reason: something other than a pass started writing changes.
--
-- 'user' is a change a person caused. Increment 7B's «Это не перепубликация»
-- replays the new-listing message that a repost link withheld, and that replay
-- is a changes row somebody pressed a button to create. Marking it 'window'
-- would put it among the rows a pass produced and lose the one fact that
-- distinguishes it: an appearance arriving hours late is a defect unless
-- somebody asked for it.
--
-- 'supervise' is a goroutine reporting its own crash. track.KindWorkerPanicked
-- has existed since increment 1 with nothing writing it; increment 8's
-- supervisor is its writer, and a crash belongs to no pass at all.
--
-- Found the way these things are found: the replay path was written in 7B with
-- a stub in its test, so nothing ever inserted the row, and the CHECK would
-- have refused it the first time a person pressed the button. The panic writer
-- hit the same wall one increment later and made it visible.
--
-- SQLite cannot alter a CHECK, so the table is rebuilt. It carries no foreign
-- keys pointing at it, which is what makes that safe here: rule_events
-- references changes(id) with ON DELETE SET NULL, and the ids are preserved.

PRAGMA foreign_keys = OFF;

CREATE TABLE changes_new (
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
    -- Which producer wrote this, and there are five now rather than three.
    --
    -- Membership differences are computed inside the pass transaction, because
    -- a set difference against the previous window must not be observed
    -- half-written; series differences are computed out of band from two
    -- snapshots; the transport producer writes the health kinds, which come
    -- from the collector observing itself rather than from comparing two
    -- readings. Those three were the original list.
    --
    -- 'user' is a change a person caused by pressing something — the replayed
    -- appearance behind «Это не перепубликация». 'supervise' is a goroutine
    -- reporting its own crash, and belongs to no pass at all.
    --
    -- Five producers into one table is a bigger fork than three, so the same
    -- reasoning applies more strongly: it is recorded here rather than left
    -- for a reader to deduce from the CHECK.
    producer    TEXT    NOT NULL DEFAULT 'series'
                CHECK (producer IN ('window', 'series', 'transport',
                                    'user', 'supervise'))
) STRICT;

INSERT INTO changes_new
SELECT id, kind, item_id, thread_id, seller_id, watch_id, pass_id, snapshot_id, ts, saved_at, subject, field, was, now, unit, had_before, has_now, confidence, backfill, producer
FROM changes;

DROP TABLE changes;
ALTER TABLE changes_new RENAME TO changes;

CREATE INDEX idx_changes_item_ts   ON changes(item_id, ts);
CREATE INDEX idx_changes_kind_ts   ON changes(kind, ts);
CREATE INDEX idx_changes_thread_ts ON changes(thread_id, ts);
CREATE INDEX idx_changes_watch_ts  ON changes(watch_id, ts);

PRAGMA foreign_keys = ON;
