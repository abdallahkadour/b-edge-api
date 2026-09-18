# 02 — Release Readiness & Feature Completion Audit

**Author:** Elena (Release Manager & Delivery Auditor)
**Date:** 2026-09-18
**Verdict:** **CONDITIONAL GO** for a limited launch. Two blockers, both
infrastructure, neither in the code.

---

## 1. Build and test state

Measured on 2026-09-18, not reported from memory.

| Check | Result |
|---|---|
| `go vet ./...` | clean |
| `go test ./...` | **33 packages, 0 failures, 0 skips** |
| Go test functions | 715 declared, **710 run** (5 are `TestMain`, which emits no run line) |
| Go statement coverage | **33.0%** overall — see §1.1 |
| Angular builds | all three projects clean |
| Web tests | **33** across 6 spec files |
| Uncommitted work | none, both repositories |
| Swagger | current — 114 documented paths against 112 route registrations |

### 1.1 Coverage is bimodal, and the headline number misleads

| Pure logic | | Service layer (mock-tested) | |
|---|---|---|---|
| `pkg/subscription` | 100% | `earnings` | 52% |
| `pkg/bidi` | 100% | `booking` | 41% |
| `pkg/hash` | **100%** *(was 0%)* | `discovery` | 40% |
| `pkg/openinghours` | 97% | `customerauth` | 35% |
| `pkg/optional` | 97% | `middleware` | 34% |
| `pkg/discount` | 95% | `artist` / `admin` / `review` | ~25% |
| `pkg/money` | 92% | `billing` / `inbox` / `product` | ~22% |
| `pkg/jwt` | **85%** *(was 0%)* | `promo` | **11.5%** |
| `calendar` | 87% | | |

The low service numbers are **structural, not neglect** — those packages are
mostly handlers and SQL, and the tests exercise the service layer through
mocks, so repository code counts as uncovered by construction.

**`promo` at 11.5% is the genuine outlier** and it is the newest money-touching
feature. The resolver beneath it (`pkg/discount`) is at 95%, so the arithmetic
is sound; it is the service, repository and handler around it that are thin.

---

## 2. Completed features inventory

Verified present in code, not taken from documentation.

| Feature | Evidence |
|---|---|
| Artist onboarding + admin approval gate | `internal/onboarding`, `internal/admin` |
| Booking lifecycle with GIST double-booking guard | `bookings_artist_id_tstzrange_excl` — verified under 8-way concurrency |
| Guest booking + hold expiry | `internal/booking/holds.go` |
| Waitlist **with auto-fill worker** | `internal/booking/waitlist_worker.go`, 5-min sweep |
| Slot generation, opening hours, exceptions | `internal/booking/slots.go`, `pkg/openinghours` |
| Service buffer / cleanup time | migration 033, `Occupancy` interval type |
| Cross-store travel buffers | `artist_store_buffers` |
| Deposits + verification queue | `internal/booking`, deposit queue UI |
| **Deposit ≤ price, enforced at three layers** | migration 038 + service + form |
| Products, orders, stock | `internal/product` |
| Discount / promo codes | `internal/promo`, `pkg/discount` |
| Dual-layer reviews (artist + venue) | migration 035 |
| Subscriptions, invoices, admin billing console | `internal/billing` |
| iCal calendar feed | `internal/calendar` |
| OG share previews | `internal/share` |
| Notification worker + dead-letter alerting | `internal/notification` |
| **Payment destination of record** | migration 039, `internal/payout` |
| **Verified badge — grantable and audited** | `internal/admin` SetVerification |
| **Reports / recourse** | migration 040, `internal/report` |
| **Expired-credential reaper** | `internal/maintenance` |
| Dark mode (3-state, token-driven) | `shared/lib/styles/_theme.scss` |
| In-app help guides (artist, client, admin) | 35 topics, 146 steps |

---

## 3. In-progress, broken, or phantom features

### 3.1 Documented in the sprint plan, not built

| Item | Status |
|---|---|
| Service template library (S4) | **Not built.** `service_categories` exists in schema, seeded by nothing. `services.template_id` referenced by zero Go code. |
| Physical resources (S12) | **Not built.** Gated on demand evidence (D9). |
| Split bookings (S13) | **Not built.** `services.active_duration_min` is written and scanned but **read by nothing**. |
| Arabic UI (S11) | **Not built, and at absolute zero.** No `@angular/localize`, zero Arabic strings, 2 files using RTL-safe logical properties. `name_ar` columns exist and are surfaced by the API but nothing fills or displays them. |

### 3.2 Half-implemented — the important category

| Item | What is there | What is missing |
|---|---|---|
| **Notification templates** | `buildMessageBody` reads a pre-rendered `message` string | **No variable substitution, no locale selection.** The code calls itself "Phase 1". Arabic notifications are impossible until this is done — independent of any UI translation. |
| ~~**Appointment reminders**~~ | **RESOLVED 2026-09-18** — migration 041 adds `scheduled_at`; `booking.ReminderWorker` reconciles the calendar in both directions every 15 minutes | Delivery still gated on Meta verification (blocker B1), like every other notification. The scheduling half is done and verified against the real database. |
| **Client reschedule** | Artist-side day shift exists | Client can only cancel — losing the slot and, inside 24h, the deposit. |
| **WhatsApp delivery** | Worker, templates, dead-letter alerting all built | Blocked on Meta business verification, which is blocked on a domain. Notifications queue correctly and never send. |

### 3.3 Phantom — documented or rendered but non-functional

| Item | Status |
|---|---|
| ~~Verified badge~~ | **RESOLVED 2026-09-18.** Was rendered on two customer surfaces and used as a discovery sort key while nothing in the codebase could set it. Now grantable, audited, and explained to clients. |
| **Rania's verified badge** | **Open data issue.** Set 2026-09-05 by direct DB edit, with no audit row, predating the feature. Either re-grant through the audited path or remove it. Owner decision. |

---

## 4. Documentation gap analysis

| Gap | Detail | Status |
|---|---|---|
| `TRUSTED_PROXIES`, `PROXY_HEADER` | Read by `config/proxy.go`, absent from `.env.example` | **FIXED 2026-09-18** |
| `.env.example` staleness | Last touched 2026-08-22; 13 migrations have shipped since | Partially addressed |
| `docs/` is gitignored | It is the generated swagger output directory. Nothing hand-written there is tracked. **Anything committed by hand must live in `project-docs/`.** | Recorded — this is why these reports are at `project-docs/reports/` |
| Swagger vs routes | 114 documented, 112 registered — no material drift | OK |
| `internal/share` routes | Not in swagger | Acceptable — HTML/OG endpoints, not a JSON API |

---

## 5. Go / No-Go checklist

### 5.1 Blocking — must clear before public launch

| # | Item | Owner | Why blocking |
|---|---|---|---|
| B1 | **Domain purchase** (~$10) | Founder | Blocks Meta verification → WhatsApp → every notification. Also blocks CDN/WAF and an HTTPS origin, which is the only reason AUTH-05/06 remain untested. Single highest-leverage item in the project. |
| B2 | **Lebanese business registration** | Founder | Meta requires it. Gates B1's downstream value. |

### 5.2 Should clear — not blocking, but launch is worse without

| # | Item | Status |
|---|---|---|
| ~~S1~~ | ~~Appointment reminders~~ | ✅ Built 2026-09-18. Queue correctly; send when B1 clears. |
| S2 | `promo` service coverage (11.5%) | Newest money path, thinnest tests. |
| S3 | Web test coverage (33 tests) | `button`, `badge`, `card`, `input`, `empty-state`, `skeleton`, `location-map`, `help-guide` have none. |
| S4 | CDN/WAF | Depends on B1. |
| S5 | `OFFSITE_CMD` for backups | Backup and restore-drill scripts exist; offsite copy is unconfigured. |

### 5.3 Cleared this session

- ✅ Deposit could exceed the price of the service it secured
- ✅ OTP rate limiting answered 400, so the shared rate-limit banner never fired and abuse was never logged
- ✅ `pkg/jwt` and `pkg/hash` had zero tests; `VerifyAccessToken` did not fail closed on an empty secret
- ✅ Verified badge was unobtainable dead code
- ✅ No recourse channel existed for a defrauded client
- ✅ Nothing reaped expired credentials — 94% of the largest table was dead
- ✅ Public review list was unbounded
- ✅ Integers with a floor and no ceiling returned 500 instead of 400
- ✅ Appointment reminders were structurally impossible (no `scheduled_at`)

### 5.4 Release verdict

**CONDITIONAL GO.** The codebase is in good shape: clean build, no failing
tests, correct auth on every route group, three TODOs all of which are recorded
decisions. The two blockers are procurement, not engineering, and no amount of
further development clears them.

A launch **without** B1 means no WhatsApp confirmations, no reminders, no CDN,
and an origin serving plain HTTP. That is a launch, but not a good one.
