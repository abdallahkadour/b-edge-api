-- 030_user_notifications.up.sql
--
-- The in-app notification centre.
--
-- Why a new table rather than reusing `notifications`:
--   That table is an OUTBOUND DELIVERY QUEUE, keyed to send attempts -
--   status, attempts, last_attempted_at, error_message, recipient_phone.
--   It answers "did this message reach a phone". It has no concept of a
--   person having READ anything, and nothing but the worker ever selects
--   from it.
--
--   This table answers a different question: "what does this user still
--   need to know". Different lifecycle, different grain, different reader.
--   Overloading the first with read state would give one row two
--   contradictory meanings - the exact trap the monetization spec flagged
--   about reusing artists.status for billing.
--
-- Why this exists at all:
--   Outbound delivery fails, permanently and invisibly. At the time of
--   writing every notification ever queued is `dead` (58 of them, 37 of
--   which were customer login codes) because Twilio was never configured.
--   Nobody was told. An artist has no way to learn that a customer was
--   never reached, so the customer simply arrives at the wrong time.
--
--   This is the surface that makes such failures visible. It is also the
--   only notification channel that works with no external dependency, no
--   Meta template approval and no per-message cost.
--
-- Scope: users, not guests.
--   Keyed to users.id, so it serves artists today and logged-in customers
--   later without a rename. Guest bookings have no account and therefore
--   no inbox - for them WhatsApp remains the only reachable channel, which
--   is precisely why outbound delivery still matters.

CREATE TABLE IF NOT EXISTS user_notifications (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  -- Machine-readable category, e.g. 'delivery_failed', 'refund_due'.
  -- Deliberately not a CHECK constraint: new kinds are added by product
  -- work, and a migration per notification type would be friction with no
  -- integrity benefit - nothing branches on unknown values.
  kind        VARCHAR(50)  NOT NULL,

  -- How loudly to render it. action_required is the one that matters:
  -- it means a human must DO something (call a customer, send a refund),
  -- as opposed to merely being informed.
  level       VARCHAR(20)  NOT NULL DEFAULT 'info'
              CHECK (level IN ('info', 'warning', 'action_required')),

  title       VARCHAR(200) NOT NULL,
  body        TEXT,
  -- Relative in-app path, e.g. '/dashboard/bookings?id=...'. A notification
  -- that cannot be acted on from itself mostly gets ignored.
  link        VARCHAR(500),

  -- Bundling key. Several occurrences of the same thing collapse into one
  -- row while unread, rather than filling the feed - a bulk shift touching
  -- twelve bookings must not produce twelve rows. See the partial unique
  -- index below, which is what actually enforces it.
  group_key   VARCHAR(200),
  item_count  INTEGER NOT NULL DEFAULT 1 CHECK (item_count > 0),

  -- Three distinct states, per notification-centre convention: unread
  -- (both null), read (read_at set), archived (dismissed from the feed).
  -- Kept as nullable timestamps rather than booleans so "when" survives,
  -- which matters for any later question about response times.
  read_at     TIMESTAMPTZ,
  archived_at TIMESTAMPTZ,

  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Bundling: at most one UNREAD, UNARCHIVED row per (user, group_key).
-- A second occurrence bumps item_count on the existing row instead of
-- inserting. Once read, the row no longer participates, so a later
-- occurrence correctly starts a fresh notification rather than silently
-- re-using one the user has already dealt with.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_notifications_group_open
  ON user_notifications (user_id, group_key)
  WHERE group_key IS NOT NULL AND read_at IS NULL AND archived_at IS NULL;

-- The feed: newest first, excluding archived. Covers both the list and the
-- unread-count query, which is polled far more often than the list itself.
CREATE INDEX IF NOT EXISTS idx_user_notifications_feed
  ON user_notifications (user_id, created_at DESC)
  WHERE archived_at IS NULL;

COMMENT ON TABLE user_notifications IS
  'In-app notification centre. Distinct from `notifications`, which is the outbound WhatsApp delivery queue - see migration 030.';
COMMENT ON COLUMN user_notifications.group_key IS
  'Bundling key: repeats collapse into one unread row via idx_user_notifications_group_open.';
COMMENT ON COLUMN user_notifications.level IS
  'action_required means a human must do something, not merely be informed.';
