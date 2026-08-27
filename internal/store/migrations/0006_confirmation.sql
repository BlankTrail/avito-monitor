-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- When the confirmation sweeper last looked at a listing.
--
-- The sweeper walks vanished listings oldest first and spends at most five
-- requests an hour. Without a record of what it has already tried, «oldest
-- first» puts the same unreadable listing at the head of the queue every hour
-- for ever, and the other four requests are all it ever gets to spend.
--
-- Separate from state_at, deliberately. state_at is when the lifecycle decided
-- something and is read by the absence gates; bumping it to mean «we looked»
-- would move a listing's whole history to make a queue work.
--
-- NULL means never looked at, which sorts first — a listing that has just
-- vanished is the one worth confirming, because the site still has its page.

ALTER TABLE listings ADD COLUMN confirmed_at INTEGER;

-- The sweeper's only query: vanished listings, least recently attempted first.
CREATE INDEX idx_listings_confirm ON listings(confirmed_at, state_at)
    WHERE state = 'vanished';
