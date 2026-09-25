-- 052_artist_services.up.sql
--
-- Which services an artist offers, and at what price.
--
-- ── The problem this fixes ───────────────────────────────────────────────
--
-- Services belong to the salon: one price, one deposit, and every artist in
-- the salon offers every service at that price. So in a salon with a makeup
-- artist and a nail artist, a customer can book the NAIL artist for Bridal
-- makeup, and the salon learns about it when the bride arrives. And a senior
-- artist cannot charge more than a junior one for the same service - the
-- founder's example is Rania's Bridal at $200 beside a colleague's at $100.
--
-- ── What this adds ───────────────────────────────────────────────────────
--
-- One row per artist per service she offers. NO ROW means she does not
-- offer it - there is deliberately no is_offered column, so "offered" can
-- never disagree with "has a row". A NULL price or deposit means "the
-- salon's"; the effective value is computed in ONE place,
-- internal/pkg/pricing, and nothing else may read services.price.
--
-- The deposit is per artist too, not only the price: bookings carry
-- CHECK (deposit_amount <= original_price), so a $25 junior price under a
-- $30 salon deposit would otherwise be unbookable. Where a blank deposit
-- follows the salon's above her price, the read CAPS it at the price.
--
-- ── Backfill ─────────────────────────────────────────────────────────────
--
-- Every existing artist gets a row for every service of her salon, ACTIVE
-- AND INACTIVE, prices NULL. Release day therefore changes nothing a
-- customer can see. Inactive services are included because the resolver
-- filters is_active already; leaving them out would mean a service
-- reactivated after release comes back offered by nobody.
--
-- Decisions: B-Edge-Per-Artist-Pricing-Spec-v1.md, PP-1 ... PP-9.

BEGIN;

CREATE TABLE artist_services (
    artist_id      UUID          NOT NULL REFERENCES artists(id)  ON DELETE CASCADE,
    service_id     UUID          NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    price          NUMERIC(10,2),
    deposit_amount NUMERIC(10,2),
    updated_by     UUID          REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (artist_id, service_id),
    CONSTRAINT artist_services_price_valid
        CHECK (price IS NULL OR (price >= 0 AND price <> 'NaN')),
    CONSTRAINT artist_services_deposit_valid
        CHECK (deposit_amount IS NULL OR (deposit_amount >= 0 AND deposit_amount <> 'NaN'))
);

-- The PK serves "this artist's services". This serves the reverse, used by
-- the cascade and by "who offers this service".
CREATE INDEX idx_artist_services_service ON artist_services (service_id);

INSERT INTO artist_services (artist_id, service_id)
SELECT a.id, s.id
  FROM artists a
  JOIN services s ON s.salon_id = a.salon_id
 WHERE a.salon_id IS NOT NULL
ON CONFLICT DO NOTHING;

COMMIT;
