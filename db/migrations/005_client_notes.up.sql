CREATE TABLE IF NOT EXISTS client_notes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    salon_id    UUID NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
    artist_id   UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content     TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (artist_id, customer_id)
);

CREATE INDEX IF NOT EXISTS idx_client_notes_artist ON client_notes(artist_id);
CREATE INDEX IF NOT EXISTS idx_client_notes_salon ON client_notes(salon_id);
