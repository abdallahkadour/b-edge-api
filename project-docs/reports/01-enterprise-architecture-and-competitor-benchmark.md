# 01 — Enterprise Architecture & Competitor Benchmark

**Author:** Marcus (Enterprise Business Architect)
**Date:** 2026-09-18
**Scope:** `b-edge-api` (Go 1.26 / Fiber), `b-edge-web` (Angular 21 monorepo), PostgreSQL 15

> Every figure below is measured from the repositories or the running stack on
> 2026-09-18. Nothing here is estimated.

---

## 1. Current state of the edge layer

B-Edge has **no edge layer in the CDN/WAF sense**. What it has is a set of
origin middlewares that do a competent job of the same work, in-process, on one
machine. That distinction matters for every recommendation below.

### 1.1 What exists, and it is more than expected

| Control | Implementation | Assessment |
|---|---|---|
| Per-IP rate limiting | `middleware/register.go`, sliding window | Working. Tighter limits on OTP and login. |
| Concurrency ceiling | `maxInFlightRequests = 300`, 503 shedding | Working. See §1.3 for the ratio problem. |
| Request deadline | `middleware/requestcontext.go`, 15s | Correct, and subtle — derived from the fasthttp ctx, not `context.Background()`. |
| Statement timeout | `statement_timeout = 5000ms` on the pool | Belt-and-braces with the above. Good. |
| Security headers | `middleware/secheaders.go` — CSP, XFO, nosniff, Referrer-Policy | Enforcing, not report-only. Verified live. |
| CORS | Exact-match allowlist | Verified: hostile Origins, `null`, and suffix-matching attacks are not reflected. |
| Trusted proxies | `config/proxy.go`, empty = trust nothing | Correct default. Undocumented until today. |
| HTTP caching | `pkg/httpcache` — Discovery 60s, Catalogue 5m, Reviews 2m, Share 10m | Deliberate per-surface windows. Better than most. |
| Audit logging | `internal/audit` on money and admin actions | Present and used. |

### 1.2 What does not exist

- **No CDN.** Every cacheable response is served from origin.
- **No WAF or bot management.** `SPAM-01` (discovery scraping) has no answer
  beyond the per-IP limiter.
- **No HTTPS origin.** This is why `AUTH-05` and `AUTH-06` (token replay,
  refresh-reuse detection) remain the largest untested security surface — it is
  an infrastructure gap, not a code gap.
- **No horizontal scale story.** The concurrency limiter is per-process.

### 1.3 The ratio that will fail first

```
in-flight ceiling      300 requests
database pool           20 connections
statement timeout        5 s
request timeout         15 s
```

**15:1.** Under saturation, 280 requests queue for a connection while holding a
request slot. The limiter sheds at 300 *arrivals*, not at pool exhaustion, so
the failure mode is latency collapse before it is rejection. The 5s statement
timeout is what stops this becoming an outage — it is doing more load-bearing
work than its size suggests.

This is acceptable for launch volume and is the first thing to change when
traffic is real. Raising the pool without raising `max_connections` moves the
failure to PostgreSQL; see report 03 §4.

---

## 2. Competitor benchmark

### 2.1 The competitor that actually matters

**Hjezle** — built in Beirut, for Lebanon, 35+ teams across MENA. Not a US
product that can be dismissed on market fit. It is the direct competitor and it
already ships most of what B-Edge lacks.

| Capability | Hjezle | B-Edge |
|---|---|---|
| Arabic + RTL | Full | **None** — zero Arabic strings, no i18n framework |
| LBP **and** USD | Both | USD only (decision D1) |
| Whish Pay deposits | Collected in-app | Manual transfer, artist confirms |
| WhatsApp reminders | Yes | **Structurally impossible** — no `scheduled_at` |
| Packages, memberships, loyalty | Yes | None |
| Intake forms | Yes | None |
| Recurring + group bookings | Yes | None |
| Commission tracking | Yes | None |
| Analytics (no-show, CLV, repeat) | Yes | Revenue only |
| Website embed | Yes | None |
| Calendar sync | Google/Outlook/Apple | iCal feed |
| Multi-location | Yes | Yes, **+ cross-store travel buffers** |
| Waitlist | Yes | Yes, **with auto-fill worker** |
| **Marketplace discovery** | No — per-business booking page | **Yes** |
| **Retail product shop** | No | **Yes** |
| **Dual-layer reviews** | No | **Yes** (artist + venue, independent) |
| **DB-level double-booking guard** | Not advertised | **Yes** — GIST exclusion constraint |

### 2.2 The global field

| Product | Model | Relevance |
|---|---|---|
| Fresha | Free core, marketplace commission | Closest business model to B-Edge |
| Booksy | ~$70/mo + paid "Boost" placement | Marketplace, monetised twice |
| GlossGenius | Flat 2.6% processing | Solo-operator focus |
| Vagaro | ~$44/mo | Deepest features — POS, payroll, inventory |
| Blyssbook (Saudi) | WhatsApp **AI booking bot** | Where this market is heading |

**None of them solve Lebanon's constraint — no card rails.** That is why a
local field exists, and it is the whole reason B-Edge's trust work (payment
destination of record, verified badge, reports) is product surface rather than
something bought from Stripe.

### 2.3 Where B-Edge is genuinely ahead

1. **It is a marketplace, not a booking tool.** Hjezle gives a salon a booking
   page; B-Edge gives a client somewhere to *discover* artists. This is the only
   item on this page a competitor cannot add in a sprint.
2. **Double-booking cannot happen** — a Postgres exclusion constraint over a
   `tstzrange`, verified under real concurrency (8 simultaneous holds, ≤1 won).
3. **Dual-layer reviews**, unmatched in the field surveyed.
4. **Cross-store travel buffers**, which most multi-location products do not model.
5. **Payment destination of record** — a direct answer to a fraud vector the
   card-rail incumbents never have to think about.

---

## 3. Strategic feature roadmap

Ranked by enterprise value against technical complexity. Complexity is assessed
against the constraints in report 03, not in isolation.

| # | Feature | Value | Complexity | Verdict |
|---|---|---|---|---|
| 1 | **Scheduled notifications → reminders** | Very high | **Low** | One column, one worker branch. The worker pattern exists three times. |
| 2 | **Arabic + RTL, with notification templating** | Very high | High | The competitor leads with it in the same market. Must be sequenced with template work or Arabic messages stay impossible. |
| 3 | **Whish Pay integration** | High | Medium | Turns the deposit from a two-party manual chore into one tap. Largest funnel friction. |
| 4 | **Client-initiated reschedule** | High | Low | Slot machinery already exists. Removes the cancel-and-lose-your-deposit trap. |
| 5 | **Analytics: no-show, repeat rate, CLV** | High | Low–Medium | Artists choose software on these numbers. All three derivable from stored data. |
| 6 | **CDN + WAF in front of origin** | High | Low (procurement) | Blocked on the domain purchase, not on engineering. Also unblocks AUTH-05/06. |
| 7 | Packages / memberships | Medium | Medium | Retention and recurring revenue for the artist. |
| 8 | Intake forms | Medium | Medium | Patch tests and allergies for lashes/tint carry real liability. |
| 9 | Escrow | Very high | **Very high** | The endgame. Requires becoming a money transmitter — a regulatory project, not a sprint. Everything above is the bridge. |

### 3.1 Engineering pushback, recorded

**Daria (Go):** items 1 and 4 are genuinely cheap — the supervised worker
pattern and the slot generator both exist and are tested. Item 5 is the one to
watch: naive analytics queries against `bookings` will be the first thing to
blow the 5s statement timeout. It needs pre-aggregation, not a dashboard
running `COUNT(*)` over a growing table on every page load.

**Tarek (PostgreSQL):** item 3 adds an outbound dependency inside a request
path that currently has none — it must be a queued job, not a synchronous call,
or a slow Whish API becomes a B-Edge outage through pool exhaustion. Item 1 is
safe: a `scheduled_at` column plus a partial index on `WHERE sent_at IS NULL`
is a well-understood shape.

**Consensus:** build 1 and 4 now. They are low-complexity, high-value, and
neither threatens the constraints in report 03.
