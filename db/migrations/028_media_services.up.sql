-- 028_media_services.up.sql
--
-- Tags portfolio photos to the services they show.
--
-- Why this is needed:
--   A customer looking at a bridal-makeup photo has already decided what
--   they want; today the only path from that photo to a booking is to
--   scroll back up and guess which service menu item produced it. Tagging
--   turns the gallery into an entry point - "browse the look, book the
--   look" - which B-Edge-Feature-Feasibility-Assessment-v1.md scores as the
--   best value-per-effort item on the roadmap, precisely because both
--   halves already exist and only the link is missing.
--
-- Shape follows artist_stores (001_initial_schema.up.sql) exactly, which is
-- this codebase's house style for a join table: surrogate UUID primary key,
-- both sides cascading, created_at, and a UNIQUE pair so the same photo
-- cannot be tagged to the same service twice.
--
-- On the FK to media:
--   media is polymorphic (owner_type IN ('artist','salon','booking',
--   'product') + owner_id) and deliberately has NO foreign key on owner_id,
--   because owner_id points at four different tables. That does not stop a
--   real FK on media.id, which is an ordinary primary key - so this is the
--   first genuine referential link into that table, and it is sound.
--
-- What is deliberately NOT here:
--   An owner_type column. Only artist-owned media is taggable, and that
--   rule is enforced in the service layer where the artist's identity is
--   already resolved. Duplicating the discriminator here would create a
--   second copy to keep in sync with media.owner_type, for no added
--   integrity - a row can only exist if media_id resolves, and the service
--   is what decides which media_ids are eligible.
--
-- Cross-salon tagging is likewise a service-layer concern: services are
-- salon-scoped while portfolio media is artist-scoped, so "is this service
-- mine?" needs an artist -> salon resolution that SQL alone cannot express
-- as a constraint here.

CREATE TABLE IF NOT EXISTS media_services (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  media_id   UUID NOT NULL REFERENCES media(id)    ON DELETE CASCADE,
  service_id UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (media_id, service_id)
);

-- Reading "which services does this photo show" (portfolio render) and
-- "which photos show this service" (the customer's filter) are both hot
-- paths; the UNIQUE constraint already indexes (media_id, service_id), so
-- only the reverse direction needs its own index.
CREATE INDEX IF NOT EXISTS idx_media_services_service
  ON media_services (service_id);

COMMENT ON TABLE media_services IS
  'Links portfolio photos to the services they depict. Artist-owned media only; enforced in the service layer, not by a constraint here.';
