# B-Edge — Gap Analysis and Competitive Review

**Date:** 2026-09-07 · **Method:** full code audit of `b-edge-api` + `b-edge-web`,
then a web survey of what competing products ship today.

---

## 1. Health of the codebase

Good, and better than the feature gaps below might suggest.

| Check | Result |
|---|---|
| `go build ./...` | clean |
| `go test ./...` | 27 packages, all pass |
| Frontend builds | both apps clean |
| Uncommitted work | none, both repos |
| Swagger | current (discount routes present) |
| Auth on routes | correct — public groups are explicit, everything else behind `RequireAuth` |
| `TODO`/`FIXME` | 3, all recorded decisions, none accidental |

**Correction to an earlier note in this session:** the waitlist auto-fill worker
*is* built (`internal/booking/waitlist_worker.go`, 5-minute sweep, cascades to
the next person when a confirm window lapses). An earlier check used a bad glob
and reported it missing.

---

## 2. What the audit found missing

### 2.1 Untested security-critical code

809 lines across seven leaf packages have **zero tests**, and everything else
depends on them:

| Package | Lines | Why it matters |
|---|---|---|
| `pkg/jwt` | 168 | Token signing and verification. The whole auth model rests on it. |
| `pkg/validation` | 197 | Every request body passes through it. |
| `pkg/apperror` | 178 | Decides what status code a failure becomes. |
| `pkg/httpcache` | 100 | Sets cache headers — a wrong `Public` leaks a private response to a CDN. |
| `pkg/response` | 67 | Envelope shape the whole frontend parses. |
| `pkg/clientip` | 58 | Feeds the rate limiter and the audit log. |
| `pkg/hash` | 41 | Password hashing. |

`jwt` and `hash` are the two files where a silent regression is worst, and
neither has a single test. The domain packages are well covered (700+ tests);
this is specifically a hole in the foundation.

### 2.2 Frontend test coverage is thin

6 spec files, 33 tests, for two applications. `star-rating`, `money.util`,
`cloudinary-image-loader` and `theme.store` are covered. `button`, `badge`,
`card`, `input`, `empty-state`, `skeleton`, `location-map`, `help-guide` and
`theme-toggle` are not.

### 2.3 Appointment reminders are structurally impossible

The `notifications` table has **no `scheduled_at` column**, and the worker only
drains `pending`/`failed` rows immediately. There is no way to say "send this
tomorrow at 09:00". The code says so itself, in
`pkg/subscription/status.go`: *"Revisit these numbers once reminders actually
send."*

This is the single most common no-show tool in every competing product.
B-Edge's deposit mechanism is the compensating control, and it is a blunter
one — it protects the artist's income but does nothing to actually get the
client through the door.

### 2.4 Notification templates are still Phase 1

`buildMessageBody` reads a pre-rendered `message` string out of the payload.
There is no variable substitution and no locale selection, so **Arabic
notifications are not possible** without that work, independent of any UI
translation.

### 2.5 Customers cannot reschedule

A client can cancel, and an artist can shift a day. A client who needs to move
one appointment must cancel — losing the slot, and losing the deposit if they
are inside 24 hours — and book again. Every competitor surveyed offers
self-service rescheduling.

### 2.6 Sprint plan items not started

`service_templates` (S4), physical resources (S12), split bookings (S13), and
the Arabic UI (S11) are unbuilt. S11 is measurably at zero: no `@angular/localize`,
no Arabic strings anywhere, two files using RTL-safe logical properties.

---

## 3. What competitors ship

### 3.1 The one that matters: Hjezle

**Hjezle** is built in Beirut, for Lebanon, and runs 35+ teams across MENA. It
is the closest competitor by a wide margin, and it already ships most of what
section 2 lists as missing.

| | Hjezle | B-Edge |
|---|---|---|
| Arabic + RTL | ✅ full | ❌ zero strings |
| LBP **and** USD | ✅ | ❌ USD only (decision D1) |
| Whish Pay deposits | ✅ collected in-app | ⚠️ manual transfer, artist confirms |
| WhatsApp reminders | ✅ | ❌ not possible today |
| Packages, memberships, loyalty | ✅ | ❌ |
| Intake forms | ✅ | ❌ |
| Recurring + group bookings | ✅ | ❌ |
| Commission tracking | ✅ | ❌ |
| Analytics: no-show, CLV, repeat rate | ✅ | ❌ revenue only |
| Website embed | ✅ | ❌ |
| Calendar sync | ✅ Google/Outlook/Apple | ✅ iCal feed |
| Multi-location | ✅ | ✅ + cross-store travel buffers |
| Buffers | ✅ | ✅ |
| Waitlist | ✅ | ✅ auto-fill |
| **Marketplace discovery** | ❌ per-business booking page | ✅ |
| **Retail product shop** | ❌ | ✅ |
| **Dual-layer reviews** | ❌ | ✅ |
| **DB-level double-booking guarantee** | unknown | ✅ GIST exclusion |

### 3.2 The global field

- **Fresha** — free core, revenue from marketplace commission on new clients. The
  discovery model B-Edge is closest to.
- **Booksy** — ~$70/mo, marketplace plus paid "Boost" placement.
- **GlossGenius** — flat 2.6% processing, solo-operator focus.
- **Vagaro** — ~$44/mo, deepest feature set: POS, payroll, inventory, marketing.
- **Square Appointments** — the default if the business already takes Square cards.
- **Blyssbook** (Saudi) — a WhatsApp **AI booking bot**: the client messages
  "I want a haircut Saturday at 3pm" and the bot checks availability, books and
  reminds.

None of the global platforms solve Lebanon's constraint — no card rails — which
is why the local field exists at all.

---

## 4. Where B-Edge is genuinely ahead

Worth stating plainly, because the gap list is long:

1. **It is a marketplace, not a booking tool.** Hjezle gives a salon a booking
   page; B-Edge gives a client somewhere to *discover* artists. That is a
   different and more defensible business, and it is the Fresha/Booksy model.
2. **Double-booking cannot happen.** The guarantee is a Postgres exclusion
   constraint, not an application check. Most products cannot say this.
3. **Dual-layer reviews.** The artist and the venue are scored independently.
   No surveyed competitor offers it.
4. **Retail alongside appointments**, with the same money model.
5. **Cross-store travel buffers**, which most multi-location products do not model.
6. **Portfolio tagged to services** — browse the look, book the look.

---

## 5. Recommended order

Ranked by how much each closes a gap a Lebanese client would actually notice.

| # | Item | Why now |
|---|---|---|
| 1 | `scheduled_at` on notifications + a reminder sweep | Table stakes everywhere. Unblocks the highest-value message the product can send. Small: one column, one worker branch — the worker pattern already exists twice. |
| 2 | Tests for `pkg/jwt` and `pkg/hash` | Cheapest risk reduction in the repo. Pure functions, no DB. |
| 3 | Customer-initiated reschedule | Removes the cancel-and-lose-the-deposit trap. The slot machinery already exists. |
| 4 | Arabic + RTL | Hjezle leads with it in the same market. Also forces the notification-template work in §2.4, so sequence them together. |
| 5 | Whish Pay integration | Turns the deposit from a two-party manual chore into one tap. The largest friction point in the funnel. |
| 6 | Analytics: no-show rate, repeat rate, CLV | Artists choose software on these numbers. All three are derivable from data already stored. |
| 7 | Packages / memberships | Retention, and recurring revenue for the artist. |
| 8 | Intake forms | Patch tests and allergies for lashes and tint carry real liability. |

**Worth revisiting as a decision, not a task:** D1 fixed the platform to USD only.
The nearest competitor supports LBP and USD. That decision closed a real
retrofit risk and should not be reopened casually — but it was made without
knowing a direct competitor had gone the other way.
