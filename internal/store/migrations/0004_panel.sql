-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Retention, the free-space floor, and the autostart intention.
--
-- The spec's settings table has the panel's password and its LAN tick already,
-- because those were decisions taken before the first file. Retention was not:
-- store.SetRetention holds it in an atomic pointer, which is right for one
-- process and wrong for a product. A person who lowered their archive to a week
-- found it back at a year on the next start, and nothing told them.
--
-- Every column defaults to zero, and zero means "the built-in default" rather
-- than "no limit" — see StoredRetention. An archive with no limit is unbounded
-- on a broken install, which is exactly when it grows fastest.
--
-- Durations are seconds because that is what the rest of this schema stores and
-- what a settings form can render as a number of days without a parse.
ALTER TABLE settings ADD COLUMN retention_snapshot_daily_sec  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_snapshot_weekly_sec INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_pass_hourly_sec     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_pass_daily_sec      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_body_max_age_sec    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_body_max_bytes      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_anchor_sec          INTEGER NOT NULL DEFAULT 0;

-- The absolute free-space floor. Zero is unset and means the built-in, never
-- "no floor" — CheckSpace also enforces two percent of the volume, and the
-- larger of the two wins.
ALTER TABLE settings ADD COLUMN space_floor_bytes INTEGER NOT NULL DEFAULT 0;

-- Whether the user asked for start-at-login. Stored as an intention and never
-- read as the truth: internal/autostart is asked whether an entry actually
-- exists, because an entry can be removed by anything, and a checkbox showing a
-- stored intention would tell somebody their monitor starts at login when it
-- does not.
ALTER TABLE settings ADD COLUMN autostart INTEGER NOT NULL DEFAULT 0;
