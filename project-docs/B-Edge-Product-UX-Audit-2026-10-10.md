# B-Edge — Product, UX and scheduling audit (2026-10-10)

Three questions, asked by the founder in a Fresha/Zenoti-style audit brief:
does the multi-location travel buffer hold, can an owner reassign a booking
when an artist is sick, and can the owner see the money split by artist and
location. Answered against the code at `api 58d8e00 / web b7e9bc1`, with the
scheduling claims **measured** on the live dev stack (a throwaway two-store
salon, torn down afterwards), not inferred.

**Corrections to the brief's premises**, so nothing below is read against
the wrong system:

- The stack is **Angular 21.2 and Go 1.26.3**, not Angular 17 / Go 1.22.
- `artist_store_buffers` **exists** (per-artist, per-pair override) but **no
  API or screen writes it** — every buffer in use today is a store default
  (`stores.weekday_buffer_min` 150, `weekend_buffer_min` 90; Lebanon's
  weekend is Friday–Sunday).
- In B-Edge the **platform admin is read-only by design** (Admin →
  Bookings, 2026-10-09). The person who reassigns is the **salon owner**,
  who since D30 (today) can act on every member's booking.
- **Reassignment does not exist** — no endpoint, no screen, nothing named
  `reassign` in either repository.

---

## 1. The travel buffer between stores

### What works

Slot generation (`internal/booking/slots.go`, step 5) adds travel time as
typed `KindTravel` intervals either side of every booking the artist has at
another store, and the slot list honours it. **Measured:** artist booked at
Store A (Beirut) 10:00–11:00 on a Tuesday → Store B (Tripoli) first offers
**13:30**, exactly 11:00 + 150 minutes. Rescheduling (`reschedule.go`) and
the whole-day shift (`shift.go`) both check travel too.

### What fails — measured 2026-10-10

| # | Probe | Result | Severity |
|---|---|---|---|
| **T1** | `POST /bookings/guest/hold` at Store B **11:30** (inside the 150-minute window) | **201 — held.** The artist is double-booked across locations. | **Critical** |
| T2 | Hold at Store A **03:00** (store closed) | **201** | High |
| T3 | Hold at **13:07** (off the 15-minute grid) | **201** | Low |
| **T4** | Store B's buffer 30, Store A's 150. A booked 10–11 → B offers 11:30? / B booked 11:30 → A offers 10:00? | **Yes / No.** The same pair of appointments is allowed or refused depending on **which was booked first**. | High |
| T5 | Booking at Store B **01:30–02:30** Beirut (the previous UTC day); Store A open 05:00 | A offers **05:00**, inside the 150 minutes after it | Medium (bridal 5am starts are real) |
| T6 | Code reading: travel after the other store's booking starts at its `end_time`, not after its cleanup (`blocked_until`) | Under-reserves by the cleanup minutes (e.g. 15) | Medium |

**Why T1–T3:** `HoldGuestSlot` and `CreateBooking` never ask whether the
time is one the slot generator would offer. They check the parties, the
horizon and "not in the past" (`validateBookingTime`), then insert. The only
guard at write time is the GIST exclusion constraint, which covers
`[start_time, blocked_until)` per artist — an appointment and its cleanup,
**never travel, hours, rota or notice**. So the travel buffer exists only in
the list the PWA draws. A stale page, a second tab, a direct API call, or two
customers holding Beirut 10:00 and Tripoli 11:30 in the same second all get
through. Test plan 3.5 checks the list, not the hold, which is why this was
never caught.

**Why T4:** step 5 uses the **requested** store's default and looks the
override up in **one direction** (`csb.StoreID → storeID`) for both the
"before" and "after" intervals. `shift.go` copies the same rule. Travel
between two places is one road; the rule must be symmetric.

**Why T5:** `GetArtistBookingsForDate` and `GetArtistCrossStoreBookings`
filter `DATE(start_time AT TIME ZONE 'UTC') = $date`. Beirut's day starts
three hours before UTC's (two in winter), so bookings in its first hours are
on the wrong side of the filter. `shift.go` already uses the store-local day;
`slots.go` does not.

### Fix — Go

**(a) One symmetric travel rule**, used by slots, shift and the write path.
New file `internal/booking/travel.go`:

```go
package booking

// travelMinutes is the time the artist needs between a booking at store a and
// one at store b on localDay. ONE rule for slot generation, the day shift and
// the write-path check - it was copied into slots.go and shift.go, and both
// read it in one direction only (audit T4).
//
// Symmetric: an artist override for the pair in EITHER direction wins (the
// larger, if both exist); otherwise the larger of the two stores' defaults
// for that day. Beirut->Tripoli and Tripoli->Beirut are the same road, so the
// answer no longer depends on which store is being booked.
func (s *Service) travelMinutes(ctx context.Context, artistID uuid.UUID, a, b *Store, localDay time.Time) (int, error) {
	weekend := !weekdays[localDay.Weekday()]
	pick := func(weekday, weekendMin int) int {
		if weekend {
			return weekendMin
		}
		return weekday
	}
	override := -1
	for _, dir := range [2][2]uuid.UUID{{a.ID, b.ID}, {b.ID, a.ID}} {
		o, err := s.repo.GetArtistStoreBuffer(ctx, artistID, dir[0], dir[1])
		if err != nil {
			return 0, fmt.Errorf("travel minutes: %w", err)
		}
		if o != nil {
			override = max(override, pick(o.WeekdayBufferMin, o.WeekendBufferMin))
		}
	}
	if override >= 0 {
		return override, nil
	}
	return max(pick(a.WeekdayBufferMin, a.WeekendBufferMin), pick(b.WeekdayBufferMin, b.WeekendBufferMin)), nil
}
```

**(b) Step 5 of `GetAvailableSlots`**, replacing the loop (also fixes T6 by
travelling from `BlockedUntil`):

```go
	// ── Step 5: Travel buffer for cross-store bookings ────────────────────
	travel := map[uuid.UUID]time.Duration{} // per other store, resolved once
	for _, csb := range crossStoreBookings {
		need, ok := travel[csb.StoreID]
		if !ok {
			other, err := s.repo.GetStore(ctx, csb.StoreID)
			if err != nil {
				return nil, fmt.Errorf("get available slots: other store: %w", err)
			}
			mins, err := s.travelMinutes(ctx, artistID, store, other, localDate)
			if err != nil {
				return nil, fmt.Errorf("get available slots: %w", err)
			}
			need = time.Duration(mins) * time.Minute
			travel[csb.StoreID] = need
		}
		// She leaves after her cleanup there, not when the client gets up.
		for _, tv := range TravelIntervals(csb.StartTime, csb.BlockedUntil, need) {
			occupied.Add(tv)
		}
	}
```

`shift.go`'s `resolveShift` builds its `buffers` map with the same call, and
`gapBetween` is given `BlockedUntil` instead of `EndTime` on both sides.

**(c) The store's day, not UTC's** (T5). Both repository queries take an
instant range instead of a date, padded by the longest travel so a booking
just outside the day still casts its buffer into it:

```go
// GetArtistBookingsBetween returns the artist's blocking bookings that touch
// [from, to). The caller passes the store's local day widened by the longest
// travel time, so the slot loop sees everything that can reach into the day.
func (r *pgRepo) GetArtistBookingsBetween(ctx context.Context, artistID uuid.UUID, from, to time.Time) ([]*Booking, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM bookings
		WHERE artist_id = $1
		  AND start_time < $3 AND blocked_until > $2
		  AND status = ANY($4) AND deleted_at IS NULL
		ORDER BY start_time ASC`, bookingSelectCols),
		artistID, from, to, BlockingStatuses)
	if err != nil {
		return nil, fmt.Errorf("get artist bookings between: %w", err)
	}
	defer rows.Close()
	return scanBookings(rows)
}
```

In `GetAvailableSlots`: `dayStart := localDate` (already store-local),
`pad := 12 * time.Hour`, one call
`GetArtistBookingsBetween(ctx, artistID, dayStart.Add(-pad), dayStart.AddDate(0,0,1).Add(pad))`,
then split by `b.StoreID == storeID` for step 4 / step 5 — one query instead
of two, and `GetArtistCrossStoreBookings` retires.

**(d) The write path asks the same question as the list** (T1–T3). The
reschedule already does this (`slotOffered` over `GetAvailableSlots`); hold
and create adopt it:

```go
// assertOffered refuses a start time the slot generator would not offer:
// outside the store's or the artist's hours, inside same-day notice, off the
// grid, overlapping, or inside travel to another store. One source of truth -
// the list a customer picks from IS the rule (audit T1-T3).
func (s *Service) assertOffered(ctx context.Context, artistID, storeID, serviceID uuid.UUID, start time.Time, store *Store) error {
	day := start.In(storeLocation(store)).Format("2006-01-02")
	slots, err := s.GetAvailableSlots(ctx, GetAvailableSlotsRequest{
		ArtistID: artistID.String(), StoreID: storeID.String(), ServiceID: serviceID.String(), Date: day,
	})
	if err != nil {
		return err
	}
	if !slotOffered(slots, start) {
		return apperror.Conflict("SLOT_UNAVAILABLE", "This time is no longer available. Please choose another.")
	}
	return nil
}
```

Called in `HoldGuestSlot` and `CreateBooking` right after
`validateBookingParties`.

**(e) Closing the race (T1 under concurrency).** (d) is a read followed by
a write; two holds at different stores can both read "free" and both
insert, and the GIST constraint cannot see travel. Every writer of an
artist's time (hold, create, reschedule, shift, reassign) takes the same
per-artist transaction lock and re-checks travel **inside** the
transaction that inserts:

```go
// lockArtistAndCheckTravel serialises writers of one artist's diary and
// refuses a booking that leaves too little travel time to another store.
// Runs inside the inserting transaction, so the lock is held until commit -
// the second of two simultaneous holds waits, then sees the first.
func lockArtistAndCheckTravel(ctx context.Context, tx pgx.Tx, b *Booking, travel map[uuid.UUID]int) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('artist:' || $1::text, 0))`, b.ArtistID); err != nil {
		return fmt.Errorf("lock artist: %w", err)
	}
	stores, mins := make([]uuid.UUID, 0, len(travel)), make([]int32, 0, len(travel))
	for id, m := range travel {
		stores, mins = append(stores, id), append(mins, int32(m))
	}
	var clash bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM bookings o
		  JOIN unnest($2::uuid[], $3::int[]) AS t(store_id, mins) ON t.store_id = o.store_id
		  WHERE o.artist_id = $1 AND o.id <> $4
		    AND o.status = ANY($7) AND o.deleted_at IS NULL
		    AND o.start_time   < $6 + make_interval(mins => t.mins)
		    AND o.blocked_until > $5 - make_interval(mins => t.mins))`,
		b.ArtistID, stores, mins, b.ID, b.StartTime, b.BlockedUntil, BlockingStatuses).Scan(&clash)
	if err != nil {
		return fmt.Errorf("travel check: %w", err)
	}
	if clash {
		return ErrSlotUnavailable // mapped to 409 SLOT_UNAVAILABLE by every caller already
	}
	return nil
}
```

`travel` is computed before the transaction by a small service helper,
`travelFromStore(ctx, artistID, store, start)`: `travelMinutes` for each
other store the artist works at (`artist_stores`), on the store's local day. `insertBookingTx` calls
this first; the existing hold-count lock in `CreateGuestHold` stays.

**Tests that must exist before the fix ships:** a service test for each of
T1–T6 (watched failing first); a database test that runs two holds for the
same artist at two stores **concurrently** and asserts exactly one wins;
and E2E 3.5 extended from "not in the list" to "a hold there is 409".

---

## 2. Reassigning a booking when an artist is sick

### UX friction log — owner, today

1. **There is no reassign action.** With Artist A sick, the owner's only
   path is **Cancel** on the booking (now possible for a member's booking,
   D30). That marks it `refund_due`, puts the owner on the hook for a manual
   OMT/Whish refund, and sends the client back to the funnel — where the
   slot may already be gone.
2. **She cannot rebook on the client's behalf.** The dashboard has no "new
   booking" screen; walk-in and phone channels exist in the schema
   (`ChannelWalkIn`, `ChannelPhone`) and nowhere in the UI.
3. **Marking the sick day does not surface the day's bookings.** An
   exception in **My hours** (`SetMyScheduleException`) narrows future
   availability and says nothing about the three appointments already on
   that day. Nobody is told; the clients still expect Artist A.
4. **No "who could take this" view.** **Showing → Everyone** on Calendar
   (D30) shows who is busy, not who offers the service, works at that store
   and is free at 14:00 with travel accounted for.
5. **Prices differ per artist** (per-artist pricing, PP-*). Nothing says
   whether the client keeps her price when moved to a dearer artist.
6. **The client is not told.** No template exists for "your appointment is
   now with Maya", and WhatsApp delivery is blocked on Meta verification, so
   even a template would queue undelivered — the owner must call.

### Rules the backend has to enforce

The deposit was paid to the **salon's** account (`salon_payment_methods`),
never to the artist — so a reassignment moves **attribution, not money**:
the booking's `artist_id` changes, Earnings follow it, and the activity log
records both artists and the deposit amount. Eligibility of the new artist
is the same as for a fresh booking, which `validateBookingParties` already
expresses (offers the service under PP-7, works at that store, same salon),
plus the slot check of 1(d) and the travel lock of 1(e). The atomic guard is
a single `UPDATE … WHERE artist_id = <old>`: the GIST constraint re-checks
the new artist's overlap in the same statement.

### Fix — Go (proposed)

```go
// ReassignRequest moves a booking to another artist of the same salon.
type ReassignRequest struct {
	ArtistID string `json:"artist_id" validate:"required,uuid"`
}

// Candidate is one artist the owner may move a booking to, or why not.
type Candidate struct {
	ArtistID uuid.UUID       `json:"artist_id"`
	Name     string          `json:"name"`
	Eligible bool            `json:"eligible"`
	// Reason is NOT_OFFERED, NOT_AT_STORE, NOT_WORKING, BUSY or TRAVEL when
	// Eligible is false - shown to the owner, never hidden.
	Reason   string          `json:"reason,omitempty"`
	Price    decimal.Decimal `json:"price"` // her price for this service
}

var reassignableStatuses = map[string]bool{
	StatusPending: true, StatusApproved: true, StatusDepositPaid: true, StatusConfirmed: true,
}

// ReassignBooking hands a booking to another artist in the salon. Owner only
// (salonrole.BookingsAnyWrite, from the request's caller - not actsForArtist:
// an artist does not give her own client away). The client keeps the price
// she agreed; the deposit stays where it was paid, the salon's account.
func (s *Service) ReassignBooking(ctx context.Context, bookingID, requesterUserID uuid.UUID, req ReassignRequest) (*BookingResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}
	to := uuid.MustParse(req.ArtistID)

	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("reassign: get booking: %w", err)
	}
	if !salonHolds(ctx, requesterUserID, b.SalonID, salonrole.BookingsAnyWrite) {
		return nil, errBookingNotFound()
	}
	if !reassignableStatuses[b.Status] || !b.StartTime.After(time.Now()) {
		return nil, apperror.Conflict("BOOKING_NOT_REASSIGNABLE", "Only an upcoming booking can be moved to another artist")
	}
	if to == b.ArtistID {
		return nil, apperror.BadRequest("SAME_ARTIST", "The booking is already with this artist")
	}

	// The same eligibility as a fresh booking: offers this service, works at
	// this store, same salon (PP-7) - then free at this exact time, travel
	// included.
	_, store, err := s.validateBookingParties(ctx, to, b.StoreID, b.ServiceID)
	if err != nil {
		return nil, apperror.Conflict("ARTIST_NOT_ELIGIBLE", "That artist does not offer this service at this location")
	}
	if err := s.assertOffered(ctx, to, b.StoreID, b.ServiceID, b.StartTime, store); err != nil {
		return nil, apperror.Conflict("ARTIST_NOT_FREE", "That artist is not free at this time")
	}

	// Travel from this store to each other store the new artist works at,
	// by the one rule (travelMinutes), for the in-transaction re-check.
	travel, err := s.travelFromStore(ctx, to, store, b.StartTime)
	if err != nil {
		return nil, fmt.Errorf("reassign: travel: %w", err)
	}
	from := b.ArtistID
	if err := s.repo.ReassignBooking(ctx, b, to, travel); err != nil { // locks `to`, travel re-check, UPDATE ... WHERE artist_id = from
		if errors.Is(err, ErrSlotUnavailable) {
			return nil, apperror.Conflict("ARTIST_NOT_FREE", "That artist was just booked at this time")
		}
		return nil, fmt.Errorf("reassign: %w", err)
	}
	b.ArtistID = to

	salon := b.SalonID
	audit.Record(ctx, s.activity, audit.Event{
		SalonID: &salon, EntityType: audit.EntityBooking, EntityID: b.ID, Action: "booking.reassign",
		OldValues: map[string]any{"artist_id": from},
		NewValues: bookingFacts(b), // includes deposit_amount: whose earnings it now counts under
	})
	s.notifyReassigned(ctx, b, from) // client, old artist, new artist; best-effort like every notification
	return toResponse(b), nil
}
```

Repository: one statement, guarded by the old artist so a concurrent
reassignment loses cleanly, and the GIST constraint re-checks the new
artist's diary atomically:

```go
func (r *pgRepo) ReassignBooking(ctx context.Context, b *Booking, to uuid.UUID, travel map[uuid.UUID]int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	moved := *b
	moved.ArtistID = to
	if err := lockArtistAndCheckTravel(ctx, tx, &moved, travel); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE bookings SET artist_id = $3, calendar_sequence = calendar_sequence + 1, updated_at = NOW()
		 WHERE id = $1 AND artist_id = $2
		   AND status IN ('pending','approved','deposit_paid','confirmed')`, b.ID, b.ArtistID, to)
	if isExclusionViolation(err) {
		return ErrSlotUnavailable
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrBookingNotFound
	}
	return tx.Commit(ctx)
}
```

Routes (owner only — the guard is in the service, as for every booking
action): `GET /bookings/:id/reassign-candidates` → `[]Candidate` (every
active member, eligible or with a reason) and `PATCH /bookings/:id/reassign`.

### Fix — Angular (proposed)

A **Move to another artist** button on the booking card, shown when
`team() !== 'mine'` and the booking is upcoming; it opens this sheet.
Ineligible artists are **listed with the reason**, not hidden — an owner who
cannot see Maya will assume a bug, not that Maya does not do bridal.

```ts
import { ChangeDetectionStrategy, Component, OnInit, inject, input, output, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';

import { BookingDataService, ButtonComponent, extractApiErrorMessage } from '@bedge/shared';
import type { EnrichedBooking, ReassignCandidate } from '@bedge/shared';

const WHY: Record<string, string> = {
  NOT_OFFERED: "doesn't offer this service",
  NOT_AT_STORE: "doesn't work at this location",
  NOT_WORKING: 'is off at this time',
  BUSY: 'has another appointment',
  TRAVEL: 'would not have time to travel here',
};

@Component({
  selector: 'bedge-reassign-sheet',
  standalone: true,
  imports: [ButtonComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div role="dialog" aria-modal="true" aria-labelledby="reassign-title" class="p-5">
      <h2 id="reassign-title" class="text-base font-bold text-ink">Move to another artist</h2>
      <p class="text-sm text-gray-500 mt-1">
        {{ booking().customer_name }} · {{ booking().service_name }} · now with {{ booking().artist_name }}
      </p>
      @if (loading()) {
        <p class="text-sm text-gray-400 py-6">Checking who is free…</p>
      } @else {
        <ul class="mt-4 divide-y divide-gray-100 rounded-lg border border-gray-200" role="radiogroup">
          @for (c of candidates(); track c.artist_id) {
            <li>
              <label class="flex items-center gap-3 px-4 py-3" [class.opacity-50]="!c.eligible">
                <input type="radio" name="to" [value]="c.artist_id" [disabled]="!c.eligible"
                  [checked]="picked() === c.artist_id" (change)="picked.set(c.artist_id)" />
                <span class="flex-1 text-sm">
                  <span class="font-semibold text-ink">{{ c.name }}</span>
                  @if (!c.eligible) { <span class="text-gray-500"> — {{ why(c.reason) }}</span> }
                </span>
                @if (c.eligible && c.price !== booking().final_price) {
                  <span class="text-xs text-gray-500">her price \${{ c.price }}</span>
                }
              </label>
            </li>
          } @empty {
            <li class="px-4 py-6 text-sm text-gray-400">Nobody else in the salon can take this.</li>
          }
        </ul>
        <p class="text-xs text-gray-500 mt-3">
          The client keeps her price of \${{ booking().final_price }}. Any deposit stays where it was paid.
          They are told by message; please call them too.
        </p>
      }
      @if (error(); as e) { <p role="alert" class="mt-3 text-sm text-danger-dark">{{ e }}</p> }
      <div class="mt-4 flex gap-2 justify-end">
        <bedge-button variant="secondary" (click)="closed.emit()">Keep it</bedge-button>
        <bedge-button [disabled]="!picked() || saving()" (click)="move()">{{ saving() ? 'Moving…' : 'Move booking' }}</bedge-button>
      </div>
    </div>
  `,
})
export class ReassignSheetComponent implements OnInit {
  private readonly bookings = inject(BookingDataService);
  readonly booking = input.required<EnrichedBooking>();
  readonly moved = output<void>();
  readonly closed = output<void>();

  readonly candidates = signal<ReassignCandidate[]>([]);
  readonly picked = signal<string | null>(null);
  readonly loading = signal(true);
  readonly saving = signal(false);
  readonly error = signal<string | null>(null);

  protected why(reason?: string): string {
    return (reason && WHY[reason]) || 'is not available';
  }

  ngOnInit(): void {
    this.bookings.getReassignCandidates(this.booking().id).subscribe({
      next: (list) => { this.candidates.set(list); this.loading.set(false); },
      error: (e: HttpErrorResponse) => { this.loading.set(false); this.error.set(extractApiErrorMessage(e, 'Could not check who is free.')); },
    });
  }

  move(): void {
    const to = this.picked();
    if (!to) return;
    this.saving.set(true);
    this.error.set(null);
    this.bookings.reassign(this.booking().id, to).subscribe({
      next: () => { this.saving.set(false); this.moved.emit(); },
      // ARTIST_NOT_FREE here means someone took the time since the list
      // loaded: refresh the list rather than leave a stale "eligible".
      error: (e: HttpErrorResponse) => { this.saving.set(false); this.error.set(extractApiErrorMessage(e, 'Could not move the booking.')); this.ngOnInit(); },
    });
  }
}
```

Shared additions: `ReassignCandidate` (mirrors `Candidate`),
`BookingDataService.getReassignCandidates(id)` and `reassign(id, artistId)`.
And the sick-day gap (friction 3): when an exception makes an artist
unavailable on a day with bookings, `SetMyScheduleException` returns them,
and the screen says "You have 3 bookings that day" with a link to
**Showing → [artist]** for the owner to move them.

---

## 3. Reporting for the owner

### What exists

`GET /earnings/salon` (the **Salon** screen, D30) aggregates **across
locations correctly** — it scopes by `bookings.salon_id`, so every store and
every artist (including one who has left) is in it, and its total equals the
sum of the artists' own Earnings pages (database-tested). Per-artist rows,
no-shows and cancellations are there.

### Missing dimensions

| Gap | Today | Why it matters |
|---|---|---|
| **By location** | No store split, no store filter | "Yesterday, Beirut vs Tripoli" cannot be answered |
| **Yesterday / custom range** | UI presets are Today / This week / This month / Last month; the API accepts any range | The brief's own scenario needs "yesterday" |
| **Utilisation** | None | Booked minutes ÷ available minutes is the owner's capacity question |
| Cancellation **rate** | A count only | A count means nothing without the denominator |
| Discount cost, early-bird income | Not separated | Codes and the early-bird fee move the money silently |
| Deposits outstanding by age | Refunds owed is a total | An unpaid refund three weeks old is a complaint waiting |
| New vs returning clients, rebook rate, lead time, channel | None | The usual retention questions |

**A data-model caveat for utilisation, found by running the query below:**
an artist with **no rota** at a store is, by design, available for that
store's whole window (`internal/pkg/schedule`). Rania is placed at four
stores with no rota, so on 17 Aug the rollup credits her with
865 + 540 + 540 + 518 minutes — four places at once. Per-store utilisation
is meaningful; **salon-level capacity must count an artist once per day**
until rotas are set. The rollup reports per store × artist and the caveat
belongs on the screen.

### The Daily Salon Performance Rollup (PostgreSQL)

One row per **store × artist × store-local day**. **Verified** on the dev
database: September for Rania's salon returns earned **$100**, 1 completed,
1 cancelled — identical to `GET /earnings/salon` for the same range. The
money columns use the same rule as Earnings (completed + no-show at
`final_price`).

```sql
-- psql -v salon=<uuid> -v from=2026-10-09 -v to=2026-10-09 -f rollup.sql
WITH params AS (
  SELECT :'salon'::uuid AS salon_id, :'from'::date AS d_from, :'to'::date AS d_to
),
days AS (
  SELECT generate_series(p.d_from, p.d_to, interval '1 day')::date AS day FROM params p
),
-- Who could have worked where: today's placements, plus anyone who had a
-- booking at the salon in the window (an artist who has since left).
placements AS (
  SELECT ast.artist_id, ast.store_id
    FROM artist_stores ast JOIN stores st ON st.id = ast.store_id, params p
   WHERE st.salon_id = p.salon_id
  UNION
  SELECT b.artist_id, b.store_id FROM bookings b, params p
   WHERE b.salon_id = p.salon_id
     AND b.start_time >= p.d_from - 1 AND b.start_time < p.d_to + 2
),
grid AS (
  SELECT pl.artist_id, pl.store_id, st.name AS store_name, st.timezone AS tz, d.day,
         EXTRACT(DOW FROM d.day)::int AS dow
    FROM placements pl JOIN stores st ON st.id = pl.store_id CROSS JOIN days d
),
-- The store's window that day: an exception replaces the weekly hours.
store_window AS (
  SELECT g.*,
         CASE WHEN ex.id IS NOT NULL THEN NOT ex.is_closed ELSE COALESCE(bh.is_open, false) END AS store_open,
         COALESCE(CASE WHEN ex.id IS NOT NULL THEN ex.open_time END, bh.open_time)  AS s_open,
         COALESCE(CASE WHEN ex.id IS NOT NULL THEN ex.close_time END, bh.close_time) AS s_close
    FROM grid g
    LEFT JOIN business_hours bh ON bh.store_id = g.store_id AND bh.day_of_week = g.dow
    LEFT JOIN business_hours_exceptions ex ON ex.store_id = g.store_id AND ex.exception_date = g.day
),
-- The artist's rota narrows it; no rota row means the whole store window.
artist_window AS (
  SELECT w.*,
         CASE WHEN ae.id IS NOT NULL AND ae.is_unavailable THEN false
              WHEN ae.id IS NULL AND rs.id IS NOT NULL AND NOT rs.is_working THEN false
              ELSE w.store_open END AS working,
         GREATEST(w.s_open, COALESCE(CASE WHEN ae.id IS NOT NULL THEN ae.start_time END, rs.start_time, w.s_open)) AS a_open,
         LEAST(w.s_close,   COALESCE(CASE WHEN ae.id IS NOT NULL THEN ae.end_time END,   rs.end_time,   w.s_close)) AS a_close
    FROM store_window w
    LEFT JOIN artist_schedules rs ON rs.artist_id = w.artist_id AND rs.store_id = w.store_id AND rs.day_of_week = w.dow
    LEFT JOIN artist_schedule_exceptions ae ON ae.artist_id = w.artist_id AND ae.store_id = w.store_id AND ae.exception_date = w.day
),
booked AS (
  SELECT b.artist_id, b.store_id, (b.start_time AT TIME ZONE st.timezone)::date AS day,
         count(*) FILTER (WHERE b.status NOT IN ('held', 'expired'))                            AS appointments,
         count(*) FILTER (WHERE b.status = 'completed')                                        AS completed,
         count(*) FILTER (WHERE b.status = 'no_show')                                          AS no_shows,
         count(*) FILTER (WHERE b.status IN ('cancelled', 'refund_due', 'refunded'))           AS cancelled,
         COALESCE(sum(b.final_price)     FILTER (WHERE b.status IN ('completed', 'no_show')), 0) AS earned,
         COALESCE(sum(b.discount_amount) FILTER (WHERE b.status IN ('completed', 'no_show')), 0) AS discounts,
         COALESCE(sum(EXTRACT(EPOCH FROM (b.end_time - b.start_time)) / 60)
                  FILTER (WHERE b.status IN ('approved', 'deposit_paid', 'confirmed', 'completed', 'no_show')), 0) AS booked_min
    FROM bookings b JOIN stores st ON st.id = b.store_id, params p
   WHERE b.salon_id = p.salon_id AND b.deleted_at IS NULL
     AND (b.start_time AT TIME ZONE st.timezone)::date BETWEEN p.d_from AND p.d_to
   GROUP BY 1, 2, 3
)
SELECT aw.day, aw.store_name, u.name AS artist,
       CASE WHEN aw.working AND aw.a_close > aw.a_open
            THEN (EXTRACT(EPOCH FROM (aw.a_close - aw.a_open)) / 60)::int ELSE 0 END AS available_min,
       COALESCE(bk.booked_min, 0)::int AS booked_min,
       CASE WHEN aw.working AND aw.a_close > aw.a_open
            THEN round(100.0 * COALESCE(bk.booked_min, 0) / (EXTRACT(EPOCH FROM (aw.a_close - aw.a_open)) / 60), 1) END AS utilization_pct,
       COALESCE(bk.appointments, 0) AS appointments,
       COALESCE(bk.completed, 0)    AS completed,
       COALESCE(bk.no_shows, 0)     AS no_shows,
       COALESCE(bk.cancelled, 0)    AS cancelled,
       CASE WHEN COALESCE(bk.appointments, 0) > 0 THEN round(100.0 * bk.cancelled / bk.appointments, 1) END AS cancellation_pct,
       CASE WHEN COALESCE(bk.completed, 0) + COALESCE(bk.no_shows, 0) > 0
            THEN round(100.0 * bk.no_shows / (bk.completed + bk.no_shows), 1) END AS no_show_pct,
       COALESCE(bk.earned, 0)    AS earned,
       COALESCE(bk.discounts, 0) AS discounts_given
  FROM artist_window aw
  JOIN artists a ON a.id = aw.artist_id
  JOIN users u ON u.id = a.user_id
  LEFT JOIN booked bk ON bk.artist_id = aw.artist_id AND bk.store_id = aw.store_id AND bk.day = aw.day
 WHERE aw.working OR COALESCE(bk.appointments, 0) > 0
 ORDER BY aw.day, aw.store_name, earned DESC, artist;
```

Limits stated plainly: business-hours **exceptions** are honoured, but a
store open past midnight is not modelled (no store is); `booked_min` counts
appointment time without cleanup; utilisation ignores travel time, which is
real unbookable time for a multi-store artist.

To ship it as a feature rather than a query: `GET /earnings/salon/daily`
(owner, `earnings:salon:read`) returning these rows, and on **Salon** a
**Yesterday** preset, a custom range, and a **By location** toggle on the
Team table.

---

## 4. UX friction log — client (PWA)

1. **The location is chosen silently.** The time picker preselects the
   artist's **first** store (`stores()[0]`) and puts **Select location** as
   a small toggle above the dates. A client near Tripoli sees Beirut's
   times and may book the wrong city; the store name is next repeated on
   the details and confirmation screens, after the time is held.
2. **"No availability on this date" with no way forward.** When the artist
   is at her other store that day, the empty state offers only the
   waitlist. It does not say "Available in Tripoli on this date" or "Next
   free: Thursday", though the API can answer both.
3. **The date strip gives no hint.** Every date looks the same; the client
   finds free days by tapping one at a time.
4. **The "slot taken" screen tells the wrong story.** Every hold failure
   except the hold limit — a network error, a 500, a suspended artist —
   and the client's own countdown expiring all show **"This slot was just
   taken — someone else booked this time while you were filling in your
   details"**, and **"Your details have been saved"** appears even when the
   hold failed at the picker, before any details were entered. After the
   travel fix (1d) a travel-refused hold also lands here, so the copy has to
   be honest: "That time is no longer available" for 409, "Your hold
   expired" for the countdown, "We couldn't reach the salon — try again" for
   network/5xx, and the save notice only when details exist.
5. **A booking the server should refuse is accepted** (T1–T3): the client
   sees a confirmation for a time the artist cannot keep. Fixed by 1(d)–(e);
   it is listed here because the client is who discovers it, on the day.

## 5. UX friction log — owner (dashboard)

1. No reassignment (section 2).
2. No booking on a client's behalf (walk-in / phone).
3. A sick day does not surface its bookings.
4. Reporting: no location split, no yesterday/custom range, no utilisation
   (section 3).
5. Travel times cannot be set per pair of stores — `artist_store_buffers`
   has no screen, so two branches on the same street pay the 150-minute
   default.
6. Activity history before 2026-10-10 is empty for bookings and money, and
   dev salons show rows written by test suites (suite 22 uses Rania's salon).

---

## Recommended order

1. **The travel/hours write-path fix (1d, 1e) with 1(a)–(c)** — a client can
   be confirmed for a time the artist cannot physically keep. Test-first,
   including the concurrent-hold database test.
2. **The PWA's error copy (4.4)** — small, and it becomes more visible once
   (1) makes the API refuse more.
3. **Reassignment (section 2)** — needs one founder decision first: does the
   client keep her price when moved to a dearer artist? (Proposed: yes.)
4. **Reporting: location split, Yesterday/custom range, the daily rollup.**
5. Pair travel times, sick-day warnings, owner-made bookings.

All code above is **proposed and not yet applied**; none of it changes
behaviour until it is built with its tests.
