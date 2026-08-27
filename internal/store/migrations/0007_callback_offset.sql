-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Where the Telegram callback poller left off.
--
-- Telegram re-delivers an update until it is acknowledged by asking for the
-- next one, so the offset is the acknowledgement. Held only in memory, a
-- restart would replay every press somebody made since the last poll — and
-- «Это не перепубликация» acted on twice is harmless, but a whole afternoon of
-- them replayed at once is a person watching their corrections happen again
-- with no explanation.
--
-- Zero means «never polled», which is what getUpdates wants for a first call.

ALTER TABLE settings ADD COLUMN telegram_update_offset INTEGER NOT NULL DEFAULT 0;
