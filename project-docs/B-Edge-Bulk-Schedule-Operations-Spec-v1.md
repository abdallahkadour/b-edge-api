# B-Edge — Bulk Schedule Operations: Design Spec v1

> Written 2026-08-31. Design for two artist-facing bulk actions requested by
> Rania — **shift every booking on a day forward by N minutes**, and **cancel
> every booking on a day** — each notifying affected customers over WhatsApp.
>
> **Nothing here is implemented yet.** This is the design to review before
> code. Section 8 lists the decisions that need Rania or the founder, not an
> engineer.

---

## 0. Three corrections to the brief, up front

The request came with an assumed architecture (Node/TypeScript, MongoDB,
Redis/BullMQ or SQS). None of it matches this system, and following it would
make the feature worse. Stated plainly so the reasoning is reviewable:

**1. Do not add a message queue. You already have one, and it is better
suited than Redis here.** `notifications` is a durable queue table —
`status` (pending/sent/failed/dead), `attempts`, `last_attempted_at`,
`error_message` — drained by a supervised worker
(`internal/notification/worker.go`) that already retries with a cap. Adding
BullMQ or SQS introduces a **dual-write problem**: booking rows commit to
Postgres while jobs go to Redis, so a crash between the two either loses
notifications or sends them for a shift that rolled back. Enqueuing into the
same transaction as the booking update makes that class of bug impossible.
Redis buys throughput this system will not need for years, at the cost of
correctness it needs today.

**2. The database work does not need to be asynchronous.** The brief assumes
bulk operations risk request timeouts. A salon day is roughly 5–25 bookings.
Shifting them is *one* `UPDATE` over a handful of rows — sub-millisecond.
Building a job runner for that is infrastructure with no problem to solve.
The correct split is: **synchronous transaction** for the schedule change and
notification enqueue, **existing asynchronous worker** for delivery.

**3. PostgreSQL, not MongoDB** — and the specific Postgres feature in play
below is the reason the naive implementation of this feature fails.

---

## 1. The finding that dictates the design

`bookings` carries a GIST exclusion constraint preventing two active bookings
for one artist from overlapping. It is **not deferrable** (verified:
`condeferrable = f`).

Exclusion constraints are enforced **row by row, mid-statement**. So the
obvious implementation fails:

```sql
UPDATE bookings
SET start_time = start_time + INTERVAL '10 minutes',
    end_time   = end_time   + INTERVAL '10 minutes'
WHERE ...;
```

Tested against the real database with two adjacent bookings (09:00–10:00 and
10:00–11:00):

```
ERROR: conflicting key value violates exclusion constraint
       "bookings_artist_id_tstzrange_excl"
DETAIL: Key ...["2099-01-01 09:10:00+00","2099-01-01 10:10:00+00")
        conflicts with existing ...["2099-01-01 10:00:00+00","2099-01-01 11:00:00+00")
```

The first booking is shifted into the second's *old* slot. The final state
is perfectly valid; the intermediate state is not. **Every ordering fails** —
descending order breaks the same way when shifting backwards, and a
booking-at-a-time loop fails identically.

### The fix, also verified

Make the constraint `DEFERRABLE INITIALLY IMMEDIATE`, then defer it for the
duration of the bulk transaction only:

```sql
SET CONSTRAINTS bookings_artist_id_tstzrange_excl DEFERRED;
UPDATE bookings SET ... ;   -- succeeds
-- constraint evaluated at COMMIT, against the final state
```

Proven in a rolled-back transaction: the shift succeeds, **and a genuine
overlap introduced afterwards is still rejected** at check time. The safety
guarantee survives; only its timing moves.

> `INITIALLY IMMEDIATE` matters. Every existing write keeps failing fast at
> statement time exactly as today. Only a transaction that explicitly opts in
> with `SET CONSTRAINTS ... DEFERRED` gets the relaxed timing, so this
> changes nothing for the booking, hold and reschedule paths.

**Migration cost:** exclusion constraints cannot be altered in place
(`ALTER CONSTRAINT` is foreign-key only), so this is a DROP + ADD, which
rebuilds the GIST index under an `ACCESS EXCLUSIVE` lock. Trivial at current
volume, worth scheduling once the table is large.

---

## 2. Architecture &amp; workflow

```
Rania taps "Shift day by 10 min"
        │
        ▼
POST /api/v1/artists/schedule/shift        ← synchronous, artist-authenticated
        │
        ├─ resolve user_id → artists.id            (never trust a client ID)
        ├─ load affected bookings FOR UPDATE       (lock the day)
        ├─ validate: closing time, buffers, in-progress   → 409 with detail
        │
        ▼  ── single transaction ──────────────────────────────────
        │   SET CONSTRAINTS ... DEFERRED
        │   UPDATE bookings   (shift)
        │   INSERT notifications × N   (status='pending')
        │   INSERT audit_events × 1
        │  ── COMMIT ──────────────────────────────────────────────
        │
        ▼
   200 { affected: 7, notifications_queued: 7 }
                                                   ┌──────────────────────┐
   notifications table (durable queue) ───────────►│ notification worker  │
                                                   │ 5s poll, retry ≤3,   │
                                                   │ supervised restart   │
                                                   └──────────┬───────────┘
                                                              ▼
                                                        Twilio WhatsApp
```

**Why the transaction boundary sits there.** Booking rows and notification
rows commit together or not at all. Three failure modes disappear:

- Shift commits, enqueue fails → customers arrive at the old time.
- Enqueue commits, shift rolls back → customers told about a change that
  never happened.
- Partial shift → half the day moved.

**Rate limits are the worker's problem, not the endpoint's.** The worker
already batches and can be given a token-bucket. WhatsApp Business API
throughput is per-sender, so a hundred messages simply take longer to drain —
which is exactly what a queue is for.

---

## 3. Database logic

**Assumption: PostgreSQL 15**, as deployed.

### 3.1 Migration

```sql
-- 029_bookings_deferrable_overlap.up.sql
--
-- Bulk schedule operations shift many bookings at once. The exclusion
-- constraint is enforced per row, so shifting booking A into booking B's
-- OLD slot fails mid-statement even when the final state is valid. Verified
-- against the real schema: error 23P01 on a two-booking day.
--
-- INITIALLY IMMEDIATE keeps every existing write failing fast exactly as
-- before. Only a transaction that explicitly opts in with
-- SET CONSTRAINTS ... DEFERRED gets end-of-transaction checking.
--
-- Drop + re-add rather than ALTER CONSTRAINT, which is foreign-key only.
-- This rebuilds the GIST index under ACCESS EXCLUSIVE.

ALTER TABLE bookings DROP CONSTRAINT bookings_artist_id_tstzrange_excl;

ALTER TABLE bookings ADD CONSTRAINT bookings_artist_id_tstzrange_excl
  EXCLUDE USING gist (
    artist_id WITH =,
    tstzrange(start_time, end_time, '[)') WITH &&
  ) WHERE (status NOT IN ('cancelled','expired','no_show','refunded'))
  DEFERRABLE INITIALLY IMMEDIATE;
```

### 3.2 Selecting the affected bookings

Which bookings move is a correctness question, not a filter detail.

```sql
-- $1 artist_id, $2 store_id, $3 day start (store-local, resolved to UTC),
-- $4 day end, $5 now
SELECT b.id, b.start_time, b.end_time, b.status,
       u.phone, u.name, s.name AS service_name
FROM bookings b
JOIN users u    ON u.id = b.customer_id
JOIN services s ON s.id = b.service_id
WHERE b.artist_id = $1
  AND b.store_id  = $2
  AND b.start_time >= $3
  AND b.start_time <  $4
  AND b.deleted_at IS NULL
  -- Only bookings that still represent a future commitment.
  -- completed / no_show are history; cancelled / expired / refunded are
  -- already dead and are excluded from the constraint anyway.
  AND b.status IN ('pending','approved','held','deposit_pending','deposit_paid','confirmed')
  -- Never move something already under way.
  AND b.start_time > $5
ORDER BY b.start_time ASC
FOR UPDATE;                        -- lock the day against concurrent booking
```

`FOR UPDATE` is load-bearing: without it a guest can hold a slot between the
validation read and the shift, and land inside the vacated window.

### 3.3 The shift

```sql
UPDATE bookings
SET start_time = start_time + make_interval(mins => $2),
    end_time   = end_time   + make_interval(mins => $2),
    updated_at = NOW()
WHERE id = ANY($1)                 -- exactly the IDs validated above
RETURNING id, start_time, end_time;
```

Driving off an explicit ID array rather than repeating the `WHERE` clause
means the rows updated are precisely the rows validated — no re-evaluation
against a schedule that may have changed.

### 3.4 The bulk cancel

```sql
UPDATE bookings
SET status = 'cancelled',
    cancelled_at = NOW(),
    cancellation_reason = $2,
    updated_at = NOW()
WHERE id = ANY($1)
  AND status IN ('pending','approved','held','deposit_pending','deposit_paid','confirmed')
RETURNING id, deposit_amount, status;
```

The status predicate repeated in the `UPDATE` is the same guarded-atomic
pattern used throughout this codebase: a booking that changed state between
read and write is skipped rather than clobbered. Compare `RETURNING` count
against the expected count and report the difference to Rania.

### 3.5 Enqueuing notifications in the same transaction

```sql
INSERT INTO notifications
  (booking_id, user_id, template_name, channel, recipient_phone, payload, status)
SELECT b.id, b.customer_id, $2, 'whatsapp', u.phone,
       jsonb_build_object(
         'customer_name', u.name,
         'service_name',  s.name,
         'old_time',      to_char(b.start_time - make_interval(mins => $3), 'HH24:MI'),
         'new_time',      to_char(b.start_time, 'HH24:MI'),
         'shift_minutes', $3
       ),
       'pending'
FROM bookings b
JOIN users u    ON u.id = b.customer_id
JOIN services s ON s.id = b.service_id
WHERE b.id = ANY($1);
```

---

## 4. Edge cases

| # | Case | Handling | Why |
|---|---|---|---|
| E1 | **Shift pushes past closing time** | **Reject the whole batch**, 409 with the offending bookings listed. | Silently scheduling work after closing produces a booking nobody can honour. Rania can shorten the shift or cancel the tail explicitly. Partial application would leave the day inconsistent. |
| E2 | **Cross-store travel buffer violated** | Reject, 409 naming the conflict. | Rania works multiple stores; `artist_store_buffers` reserves travel time. Shifting one store's day can silently make the next store's first booking unreachable. Generic designs miss this entirely — it is specific to B-Edge. |
| E3 | **Booking already started** | Excluded by `start_time > now` in §3.2. | Telling someone mid-appointment that they are delayed is nonsense. |
| E4 | **Booking already completed / no-show** | Excluded by status filter. | History is immutable. |
| E5 | **Shift creates an overlap with an untouched booking** | Caught by the deferred constraint at COMMIT; whole transaction rolls back. | The database remains the final guard — no application check replaces it. |
| E6 | **Negative shift (pull earlier)** | Support it, but validate against opening time and `now`. | Rania will ask. Same machinery; the additional rule is "never move a booking into the past". |
| E7 | **Concurrent guest booking during the shift** | `FOR UPDATE` on the day's rows. | Otherwise a new hold lands in the window being vacated. |
| E8 | **Rania double-taps** | Idempotency key on the request; replay returns the first result. | Without it, two taps shift the day 20 minutes and send two sets of messages. |
| E9 | **Customer has no phone number** | Shift proceeds; notification skipped and reported as undeliverable. | Guest bookings always capture a phone, but a null must not abort a schedule change. |
| E10 | **Deposits on bulk-cancelled bookings** | **Unresolved — see §8.** | Refunds are out-of-band bank transfers. Cancelling a day with paid deposits creates a real refund liability the system does not track. |

---

## 5. Backend implementation

**Go 1.26 / Fiber**, matching the codebase. Node/Python would mean a second
runtime for one feature.

### 5.1 Handler

```go
// ShiftDay godoc
// @Summary  Shift every booking on a day by N minutes
// @Router   /artists/schedule/shift [post]
func (h *Handler) ShiftDay(c *fiber.Ctx) error {
	var req ShiftDayRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Invalid request body")
	}

	userID := middleware.UserIDFromContext(c)
	result, err := h.svc.ShiftDay(c.Context(), userID, req, c.IP())
	if err != nil {
		return err
	}
	return response.OK(c, result)
}
```

```go
// ShiftDayRequest is the body for POST /artists/schedule/shift.
type ShiftDayRequest struct {
	StoreID string `json:"store_id" validate:"required,uuid4"`
	// Date is the store-LOCAL calendar day, "YYYY-MM-DD". Resolved through
	// the store's IANA zone, not the server's - at 23:00 UTC it is already
	// tomorrow in Beirut.
	Date string `json:"date" validate:"required,datetime=2006-01-02"`
	// ShiftMinutes may be negative to pull a day earlier. Bounded so a
	// mistyped value cannot fling a day into next week.
	ShiftMinutes int `json:"shift_minutes" validate:"required,min=-240,max=240"`
	// IdempotencyKey makes a double-tap safe. Same key returns the first
	// result rather than shifting twice.
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=64"`
}
```

### 5.2 Service

```go
// ShiftDay moves every movable booking on one store-day by n minutes.
//
// Validation is all-or-nothing: if ANY booking would land past closing or
// violate a travel buffer, the whole batch is rejected with the offending
// bookings named. A partially-shifted day is worse than an unshifted one -
// half the customers get told about a change that did not happen to them.
func (s *Service) ShiftDay(ctx context.Context, userID uuid.UUID, req ShiftDayRequest, ip string) (*BulkResult, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	// Never trust a client-supplied artist ID - the IDOR pattern this
	// codebase settled on after six endpoints shipped without it.
	artistID, err := s.repo.GetArtistIDByUserID(ctx, userID)
	if err != nil {
		return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist profile not found")
	}

	if prev, found, err := s.repo.FindBulkOp(ctx, artistID, req.IdempotencyKey); err != nil {
		return nil, err
	} else if found {
		return prev, nil // replay
	}

	store, err := s.repo.GetStore(ctx, req.StoreID)
	if err != nil || store.ArtistCanAccess(artistID) == false {
		return nil, apperror.NotFound("STORE_NOT_FOUND", "Store not found")
	}

	// The store's own day, via the shared resolver - not the server's.
	loc := openinghours.Location(store.Timezone)
	dayStart, dayEnd, err := storeDayBounds(req.Date, loc)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_DATE", "Date must be YYYY-MM-DD")
	}

	return s.repo.ShiftDayTx(ctx, ShiftDayParams{
		ArtistID:       artistID,
		StoreID:        store.ID,
		DayStart:       dayStart,
		DayEnd:         dayEnd,
		ShiftMinutes:   req.ShiftMinutes,
		Now:            time.Now(),
		Location:       loc,
		IdempotencyKey: req.IdempotencyKey,
		ActorIP:        ip,
	})
}
```

### 5.3 Repository — the transaction

```go
// ShiftDayTx performs the whole operation atomically: validate, shift,
// enqueue notifications, audit.
//
// The exclusion constraint is DEFERRED for this transaction only (migration
// 029). Without it, shifting booking A into booking B's old slot violates
// the constraint mid-statement even though the committed state is valid.
func (r *pgRepo) ShiftDayTx(ctx context.Context, p ShiftDayParams) (*BulkResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("shift day: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Locks the day. A guest hold landing between validation and update
	// would otherwise slip into the window being vacated.
	rows, err := r.lockDayBookings(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &BulkResult{Affected: 0}, nil
	}

	// All-or-nothing validation, before anything is written.
	if conflicts := validateShift(rows, p); len(conflicts) > 0 {
		return nil, apperror.Conflict("SHIFT_NOT_POSSIBLE",
			"Some bookings cannot be moved").WithDetails(conflicts)
	}

	if _, err := tx.Exec(ctx,
		`SET CONSTRAINTS bookings_artist_id_tstzrange_excl DEFERRED`); err != nil {
		return nil, fmt.Errorf("shift day: defer constraint: %w", err)
	}

	ids := bookingIDs(rows)
	if _, err := tx.Exec(ctx, `
		UPDATE bookings
		SET start_time = start_time + make_interval(mins => $2),
		    end_time   = end_time   + make_interval(mins => $2),
		    updated_at = NOW()
		WHERE id = ANY($1)`, ids, p.ShiftMinutes,
	); err != nil {
		// 23P01 here means the shift collided with a booking OUTSIDE the
		// selected day - a real conflict the constraint correctly caught.
		if isExclusionViolation(err) {
			return nil, apperror.Conflict("SHIFT_CREATES_OVERLAP",
				"Shifting would overlap a booking outside this day")
		}
		return nil, fmt.Errorf("shift day: update: %w", err)
	}

	queued, err := r.enqueueShiftNotifications(ctx, tx, ids, p.ShiftMinutes)
	if err != nil {
		return nil, err
	}

	// Bulk schedule changes are exactly what an audit trail is for.
	_ = r.audit.LogTx(ctx, tx, audit.Event{
		ActorID: &p.ArtistID, ActorRole: "artist",
		EntityType: "schedule", EntityID: p.StoreID, Action: "bulk_shift",
		NewValues: map[string]any{"minutes": p.ShiftMinutes, "count": len(ids)},
		IPAddress: p.ActorIP,
	})

	if err := r.recordBulkOp(ctx, tx, p, len(ids)); err != nil {
		return nil, err
	}

	// The deferred constraint is evaluated HERE. A conflict rolls back
	// bookings AND notifications together.
	if err := tx.Commit(ctx); err != nil {
		if isExclusionViolation(err) {
			return nil, apperror.Conflict("SHIFT_CREATES_OVERLAP",
				"Shifting would create an overlapping booking")
		}
		return nil, fmt.Errorf("shift day: commit: %w", err)
	}

	return &BulkResult{Affected: len(ids), NotificationsQueued: queued}, nil
}
```

### 5.4 Worker — no new worker needed

`internal/notification/worker.go` already polls, sends and retries. It needs
only two new `template_name` values wired to approved WhatsApp templates.

The supervision is already generic: `superviseWorker(ctx, name, w, logger)`
takes a `backgroundWorker` interface, so a second worker (waitlist, dunning)
plugs in without touching `main.go`'s panic-recover-restart loop.

> ⚠️ **WhatsApp templates must be approved by Meta before either message can
> send.** A business-initiated message outside the 24-hour customer service
> window cannot be free text. Both messages here are **Utility** templates
> and need submitting well ahead of the feature — that approval has its own
> calendar time, and `TWILIO_WHATSAPP_FROM` is still unset, so nothing sends
> at all today.

Proposed templates:

```
bulk_shift_notice     "Hi {{1}}, your {{2}} appointment with Rania has moved
                       from {{3}} to {{4}} today. Sorry for the short notice."

bulk_cancel_notice    "Hi {{1}}, your {{2}} appointment with Rania on {{3}}
                       has been cancelled. She'll be in touch to rebook."
```

---

## 6. Error handling &amp; resilience

### 6.1 The scenario the brief asks about

> *The database updates succeed, but WhatsApp fails for specific numbers.*

This is the **expected** case, not the exceptional one — and it is why the
schedule change must not depend on delivery. The design already handles it:
the row sits at `status='failed'` with an `error_message` and a retry count,
and the worker retries to `maxAttempts` before marking it `dead`.

**What is missing is not retry logic — it is Rania knowing.** A customer who
never receives the message arrives at the old time. That is a real-world
failure with a person standing in a salon, and no amount of retry fixes it
once the appointment has passed.

### 6.2 Required: a delivery report

The bulk endpoint should return an operation ID, and the dashboard should
show, per bulk action:

| State | Meaning | What Rania does |
|---|---|---|
| Queued | Accepted, not yet sent | Nothing |
| Sent | Delivered to WhatsApp | Nothing |
| **Failed (retrying)** | Transient, will retry | Nothing yet |
| **Dead** | Gave up after `maxAttempts` | **Call this customer** |
| **No phone** | No number on the booking | **Call this customer** |

The two bold rows are the entire point. Anything less turns a delivery
failure into a customer standing outside a closed salon.

### 6.3 Retry semantics

- Existing worker: exponential backoff to `maxAttempts`, then `dead`.
- **Do not retry indefinitely.** A shift notice is worthless after the
  appointment. Add a hard expiry: stop attempting once
  `booking.start_time < now`, and mark `dead` with a reason.
- Twilio failures split in two, and the worker should distinguish them:
  **permanent** (invalid number, not on WhatsApp, opted out) → fail
  immediately, do not burn retries; **transient** (429, 5xx, network) →
  retry.

### 6.4 Alerting

At current scale a dashboard badge plus the audit log is proportionate. When
volume justifies it, the signal to watch is the `notifications.status`
distribution — a spike in `failed`/`dead` is the fastest indicator that
WhatsApp delivery has broken, as distinct from the app itself, and
`WHATSAPP-SETUP.md` already recommends monitoring exactly that.

---

## 7. Test plan additions

| ID | Scenario | Expected |
|---|---|---|
| BULK-01 | Shift a day of adjacent bookings by +10 | All shift; constraint satisfied at commit |
| BULK-02 | Shift past closing time | 409, **nothing** written |
| BULK-03 | Shift violating a cross-store travel buffer | 409 naming the conflict |
| BULK-04 | Shift with a booking in progress | In-progress booking untouched |
| BULK-05 | Concurrent guest hold during a shift | Serialised by `FOR UPDATE`; no overlap |
| BULK-06 | Double-tap with the same idempotency key | One shift, one set of notifications |
| BULK-07 | Notification enqueue fails | Booking shift rolls back too |
| BULK-08 | Customer with no phone | Shift succeeds; reported undeliverable |
| BULK-09 | Bulk cancel with paid deposits | Per §8 decision; refund liability surfaced |
| BULK-10 | Shift a day with zero bookings | 200, `affected: 0`, no notifications |
| BULK-11 | Shift crossing a DST boundary | Wall-clock offsets resolve per store zone |

---

## 8. Decisions needed before implementation

These need Rania or the founder, not an engineer.

1. **Deposits on bulk cancellation.** Cancelling a day with paid deposits
   creates a refund liability the platform does not track — refunds are
   out-of-band bank transfers. Does bulk cancel (a) refuse when any booking
   has a paid deposit, (b) proceed and produce a refund worklist, or (c)
   proceed silently and leave it to Rania's memory? **(c) is how you get a
   customer dispute.** Recommend (b).

2. **Shift past closing time.** Recommend hard reject (E1). Confirm Rania
   would not prefer "shift what fits, cancel the rest" — which is defensible
   but a materially bigger feature.

3. **Cancellation reason.** Free text from Rania, a fixed list, or nothing?
   It goes into the customer's WhatsApp message, so it is customer-facing
   copy, not an internal note.

4. **Scope of a bulk action.** Per store, or every store that day? A single
   artist working two branches could plausibly mean either. Spec above
   assumes **per store**; whole-day-across-stores makes travel buffers much
   harder.

5. **Who may do this?** Artist-only, or admin too? Admin-on-behalf-of needs
   a different audit actor.

6. **Notification opt-out.** Should Rania be able to shift *without*
   notifying — for example when she has already phoned everyone? Recommend
   yes, with the choice recorded in the audit event.

---

## 9. Build order

1. Migration 029 (deferrable constraint) + a test proving bulk shift works
   and real overlaps are still caught. **Independently shippable and
   valuable** — it unblocks any future multi-booking write.
2. Read-only preview endpoint: "what would shift, what would break". Lets
   Rania see the outcome before committing, and is the natural place to
   surface E1/E2 conflicts.
3. `ShiftDay` write path + audit + idempotency.
4. `CancelDay`, gated on decision 8.1.
5. Notification templates — **submit to Meta early**, approval is the long
   pole and is entirely outside our control.
6. Delivery report UI (§6.2). Not optional; without it a failed message is
   invisible.

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · August 31, 2026*
