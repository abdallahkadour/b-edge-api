-- 046_salon_invitations.up.sql
--
-- How a second artist gets into a salon.
--
-- Today there is exactly one path that creates a salon, and it is inside
-- internal/onboarding's transaction: salon -> artist -> store, all three
-- together. A salon is therefore created BY its first artist and can never
-- gain a second one. Every salon on the platform has exactly one member,
-- and `artists.salon_id` - nullable since 001, precisely so an artist can be
-- attached to a salon - has never had a second row pointed at the same
-- salon. This table is the missing door.
--
-- ── Why a token hash, never the token ────────────────────────────────────
--
-- Same reason refresh_tokens stores a hash: a database dump must not yield
-- working invitation links. The raw token exists only in the link that goes
-- to the invitee.
--
-- ── Why phone is E.164 ───────────────────────────────────────────────────
--
-- The partial unique indexes below are what stop an owner creating five live
-- invitations for the same person by typing their number five ways. That
-- only works if the column is normalised before insert, because Postgres
-- sees '70555123' and '+96170555123' as different strings. The service layer
-- runs internal/pkg/phone over it first. This is the same equivalence that
-- security test FRAUD-09 pinned for deposit payer numbers.
--
-- ── Why there is no expiry job ───────────────────────────────────────────
--
-- 'expired' is computed on read and written back, exactly as booking holds
-- self-heal and as migration 016's waitlist entries do. B-Edge has no
-- scheduler by design; adding one to age out invitations would be undoing
-- that deliberately.

CREATE TABLE salon_invitations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    salon_id    UUID NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
    invited_by  UUID NOT NULL REFERENCES users(id),

    -- At least one of these. Phone is the realistic channel in Lebanon;
    -- email exists because WhatsApp delivery is still gated on Meta
    -- verification and an owner needs some way to reach someone today.
    phone       VARCHAR(20),
    email       VARCHAR(255),

    token_hash  TEXT NOT NULL,
    status      VARCHAR(20) NOT NULL DEFAULT 'pending',
    expires_at  TIMESTAMPTZ NOT NULL,

    accepted_by UUID REFERENCES users(id),
    accepted_at TIMESTAMPTZ,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT salon_invitations_status_check
        CHECK (status IN ('pending', 'accepted', 'declined', 'revoked', 'expired')),

    CONSTRAINT salon_invitations_contact_check
        CHECK (phone IS NOT NULL OR email IS NOT NULL),

    -- A biconditional, not two independent NOT NULLs: a row cannot claim it
    -- was accepted without recording who accepted it, and cannot record an
    -- acceptor while in any other state.
    CONSTRAINT salon_invitations_accepted_check
        CHECK ((status = 'accepted') = (accepted_by IS NOT NULL))
);

-- At most one LIVE invitation per contact per salon. Inviting the same
-- person twice resends rather than duplicating, and these indexes are what
-- make that a database guarantee instead of a service-layer intention.
-- Partial, so the history of declined and revoked invitations is kept.
CREATE UNIQUE INDEX uq_salon_invitations_live_phone
    ON salon_invitations (salon_id, phone)
    WHERE status = 'pending' AND phone IS NOT NULL;

CREATE UNIQUE INDEX uq_salon_invitations_live_email
    ON salon_invitations (salon_id, lower(email))
    WHERE status = 'pending' AND email IS NOT NULL;

-- Redeeming a link is a lookup by hash and must not scan.
CREATE INDEX idx_salon_invitations_token ON salon_invitations (token_hash);

-- The owner's roster screen lists a salon's invitations by state.
CREATE INDEX idx_salon_invitations_salon ON salon_invitations (salon_id, status);

COMMENT ON TABLE salon_invitations IS
    'Pending and historical invitations for an artist to join a salon. '
    'Expiry is lazy: computed on read, written back, no scheduler.';

COMMENT ON COLUMN salon_invitations.phone IS
    'E.164, normalised by internal/pkg/phone BEFORE insert. The partial '
    'unique index above is bypassable otherwise.';

COMMENT ON COLUMN salon_invitations.token_hash IS
    'SHA-256 of the invitation token. The raw token is never stored.';
