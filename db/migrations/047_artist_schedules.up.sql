-- 047_artist_schedules.up.sql
--
-- Per-artist working hours.
--
-- business_hours and business_hours_exceptions are both keyed on store_id.
-- That was correct while every salon had exactly one artist: the store's
-- opening hours WERE the artist's working hours. The moment a salon has two
-- artists it stops being true, and there is no way to express that Rania
-- works Tuesday to Saturday while a new hire works evenings. artist_stores
-- is only (artist_id, store_id) - a membership link with no schedule on it.
--
-- ══ THE LOAD-BEARING RULE ════════════════════════════════════════════════
--
--   AN ARTIST WITH NO ROW HERE IS AVAILABLE FOR THE WHOLE STORE WINDOW.
--   ABSENCE MEANS "NO PERSONAL RESTRICTION", NOT "NEVER AVAILABLE".
--
-- Every artist on the platform today has zero rows in this table, so the
-- intersection internal/pkg/schedule performs must be an identity operation
-- for all of them. Invert this default and five live calendars empty
-- themselves silently. scripts/capture-slot-baseline.py exists to prove it
-- did not happen: slot output is captured before this migration and compared
-- byte for byte afterwards.
--
-- ── Why one window per day, and what that rules out ──────────────────────
--
-- UNIQUE (artist_id, store_id, day_of_week) permits exactly one working
-- window per day. A split shift - 09:00-13:00 then 17:00-21:00 - cannot be
-- expressed. That is a deliberate v1 limit, not an oversight: supporting it
-- means relaxing this constraint AND generalising schedule.Intersect from a
-- single Window to an interval set, which is the same generalisation
-- resources and split bookings will need (see the interval-algebra sprint in
-- B-Edge-Feature-Feasibility-Assessment-v1.md section 4). Doing it once,
-- deliberately, beats doing it three times by accident. Recorded here so the
-- next person meets the decision rather than the symptom.
--
-- ── Why this mirrors business_hours rather than inventing a shape ────────
--
-- Same columns, same semantics, one level down. artist_store_buffers already
-- set the precedent for overriding a store-level setting per artist.

CREATE TABLE artist_schedules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id   UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    store_id    UUID NOT NULL REFERENCES stores(id)  ON DELETE CASCADE,

    day_of_week SMALLINT NOT NULL CHECK (day_of_week BETWEEN 0 AND 6),
    start_time  TIME NOT NULL,
    end_time    TIME NOT NULL,

    -- A row with is_working = false says "I do not work this day at this
    -- store", which is different from having no row at all. Without it an
    -- artist could not opt out of a single day; they would have to enumerate
    -- every other day to say so by omission.
    is_working  BOOLEAN NOT NULL DEFAULT TRUE,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT artist_schedules_range_check CHECK (start_time < end_time),
    CONSTRAINT uq_artist_schedules UNIQUE (artist_id, store_id, day_of_week)
);

CREATE INDEX idx_artist_schedules_lookup ON artist_schedules (artist_id, store_id);

COMMENT ON TABLE artist_schedules IS
    'Per-artist weekly working hours, intersected with the store''s opening '
    'hours at slot generation. NO ROW MEANS AVAILABLE FOR THE WHOLE STORE '
    'WINDOW - absence is not unavailability.';


-- Personal date overrides: holiday, illness, a one-off late start. Distinct
-- from business_hours_exceptions, which closes the STORE for everyone.
CREATE TABLE artist_schedule_exceptions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id      UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,

    -- NULL means every store: "I am away on the 3rd" rather than "away from
    -- this one branch on the 3rd". The common case is the whole day off.
    store_id       UUID REFERENCES stores(id) ON DELETE CASCADE,

    exception_date DATE NOT NULL,
    is_unavailable BOOLEAN NOT NULL DEFAULT TRUE,
    start_time     TIME,
    end_time       TIME,
    reason         VARCHAR(255),

    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Away for the whole day carries no times; working different hours
    -- carries both and they must be ordered. Stops a row that says
    -- "unavailable, 09:00-17:00", which reads two opposite ways.
    CONSTRAINT artist_schedule_exceptions_times_check CHECK (
        (is_unavailable AND start_time IS NULL AND end_time IS NULL)
        OR (NOT is_unavailable AND start_time IS NOT NULL
            AND end_time IS NOT NULL AND start_time < end_time)
    ),

    CONSTRAINT uq_artist_schedule_exceptions
        UNIQUE (artist_id, store_id, exception_date)
);

CREATE INDEX idx_artist_schedule_exceptions_lookup
    ON artist_schedule_exceptions (artist_id, exception_date);

COMMENT ON TABLE artist_schedule_exceptions IS
    'One-off personal overrides of artist_schedules. store_id NULL means '
    'all stores. Distinct from business_hours_exceptions, which closes the '
    'store for everyone.';
