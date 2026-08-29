-- 025_invoices.up.sql
--
-- One row per billing period per subscription. Append-mostly: a 'paid'
-- invoice is never edited, only transitioned to a terminal state or
-- corrected by voiding it and issuing a new one - the audit trail is the
-- point, matching the "rejected is terminal, not deleted" reasoning
-- already established for artists.status in migration 019.
CREATE TABLE IF NOT EXISTS invoices (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  subscription_id   UUID NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
  artist_id         UUID NOT NULL REFERENCES artists(id),

  invoice_number    SERIAL UNIQUE,     -- human-quotable over WhatsApp/OMT notes
  period_start      DATE NOT NULL,
  period_end        DATE NOT NULL,
  due_date          DATE NOT NULL,

  -- Snapshot at issue time - deliberately NOT recomputed from the
  -- subscription or joined from plans. If the artist's plan or seat count
  -- changes after this invoice is issued, this invoice must still say what
  -- was actually owed for the period it covers. plan_code here is a label
  -- for display only, with no FK - unlike subscriptions.plan_code, an old
  -- invoice must remain readable even if the plan it names is ever removed.
  amount            NUMERIC(10,2) NOT NULL CHECK (amount >= 0),
  currency          VARCHAR(3)   NOT NULL DEFAULT 'USD',
  seats_billed      INTEGER      NOT NULL,
  plan_code         VARCHAR(30)  NOT NULL,

  status            VARCHAR(25) NOT NULL DEFAULT 'issued'
                    CHECK (status IN ('issued', 'submitted', 'paid', 'void')),

  -- Artist-supplied, mirroring bookings.deposit_reference (migration 011):
  -- free text, not validated, an OMT/Whish transaction code or a note. A
  -- submitted reference is a CLAIM, not proof - only an admin's confirm
  -- action (below) treats money as real. See section 8 of the spec.
  payment_reference VARCHAR(255),
  submitted_at      TIMESTAMPTZ,

  -- Who actually verified the money arrived - never NULL once status='paid'.
  confirmed_by      UUID REFERENCES users(id),
  paid_at           TIMESTAMPTZ,

  void_reason       TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_invoices_artist ON invoices (artist_id);
CREATE INDEX IF NOT EXISTS idx_invoices_status ON invoices (status);

-- The load-bearing constraint for lazy invoice generation (see
-- internal/billing.Service.EnsureInvoicesUpTo): makes it safe to call from
-- ANY read path, any number of times, from concurrent requests, without
-- ever double-billing a period. Two simultaneous requests both deciding
-- "this month's invoice is missing" is the normal case when an artist and
-- an admin load their dashboards around the same time, not a hypothetical -
-- the second insert simply no-ops against this index instead of creating a
-- duplicate invoice.
CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_period
  ON invoices (subscription_id, period_start);

COMMENT ON COLUMN invoices.payment_reference IS
  'Artist-entered, free text, unvalidated - a claim that money was sent, '
  'not proof it arrived. Only confirmed_by/paid_at represent verified payment.';
