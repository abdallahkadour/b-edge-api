# 03 — Security Auditor

**2026-09-06.** OWASP Top 10, auth boundaries, input validation, edge posture.

## Scope & method

**This is not a fresh desk review.** The project's own
`B-Edge-Security-Test-Plan-v1.md` (33 numbered cases) was **executed live
against the running stack on 2026-09-05** — see its §5. This document reports
that execution, the fixes that followed, and what remains.

Every finding below was verified against the database, not inferred from a
status code. Where a case was not run, it says so rather than implying a pass.

---

## Result: 42 passed, 4 defects, all 4 fixed

### Fixed — INJ-04, unvalidated money reached SQL · was **Critical**

`PATCH /artists/salon/services/:id` and onboarding's first-service INSERT
passed `price` to SQL as a raw string. Postgres accepts `'NaN'::numeric`.

```
sent 'NaN'  -> PATCH 500 | DB NaN | every later read of that row 500s
```

The write **committed while returning 500**, `SUM(price)` over the salon
returned `NaN`, and the row could not be repaired through the API because the
API could no longer read it.

**Patch — `internal/pkg/money`, a whitelist not a rejection list:**

```go
// Enumerating rejections is precisely how NaN got in.
var canonical = regexp.MustCompile(`^\d{1,8}(\.\d{1,2})?$`)

func Parse(raw, field string) (decimal.Decimal, error) {
	if !canonical.MatchString(raw) {
		return decimal.Zero, invalid(field)
	}
	return decimal.NewFromString(raw)
}
```

Applied at 8 call sites; `billing.parseNonNegativeDecimal` now delegates to it.

**Defence in depth — migration 034:**

```sql
ALTER TABLE services ADD CONSTRAINT services_price_not_nan
  CHECK (price <> 'NaN'::numeric);   -- ×16 numeric columns
```

Two constraints that *look* right do nothing, and both were tested before
settling: `CHECK (price = round(price,2))` passes because PostgreSQL defines
`NaN = NaN` as **TRUE** for numeric; `CHECK (price >= 0)` passes because
PostgreSQL sorts `NaN` **above** all numbers. Scale cannot be defended at the
database level at all — the column coerces before any CHECK runs.

### Fixed — AUTH-02, 403-vs-404 existence oracle · was **Medium**

```
GET    /bookings/<id>               foreign=403  missing=404
PATCH  /artists/stores/<id>         foreign=403  missing=404
DELETE /artists/salon/services/<id> foreign=403  missing=404
```

Any artist token could enumerate live IDs by watching the status code.

**Patch — one constructor shared by both branches, per domain:**

```go
// It is a FUNCTION rather than two matching literals on purpose. The leak was
// possible because the not-found branch and the ownership branch were written
// separately and drifted.
func errBookingNotFound() error {
	return apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
}
```

27 sites converted. **Not a blanket change** — `NO_SALON`,
`ACCOUNT_SUSPENDED`, `SUBSCRIPTION_SUSPENDED`, `NOT_AN_ARTIST`,
`ARTIST_NOT_ACCEPTING_BOOKINGS`, the middleware role gates, and checks keyed on
a **public** artist ID correctly remain 403: they describe the caller, and a
public ID has nothing to enumerate.

Re-verified: `foreign=404 missing=404 bodies=identical` on all six families.

### Fixed — CLIENT-04, no security headers anywhere

The plan predicted this ("verified by grep, not inferred"). Now
`internal/middleware/secheaders.go`, registered **second** so a 429 from the
rate limiter and a 503 from the concurrency limiter carry them too.

```
Content-Security-Policy: default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: accelerometer=(), camera=(), geolocation=(), …
Cross-Origin-Opener-Policy: same-origin
Strict-Transport-Security: max-age=31536000; includeSubDomains   ← TLS only
```

`default-src 'none'` is *accurate* for JSON, not merely strict. The two routes
that serve HTML (`share`, `calendar`) narrow it by exactly one directive each
at the handler, so a future HTML endpoint fails closed.

HSTS is gated on `X-Forwarded-Proto: https` — sending it over plaintext would
pin a hostname to HTTPS in the browser cache for a year.

### Fixed — public services endpoint over-shared

`GET /artists/:id/services` is unauthenticated and returned `buffer_min`,
`salon_id`, `is_active`, `active_duration_min`. Migration 033 states the buffer
is never customer-facing. New `artist.PublicServiceResponse`.

---

## Passed, with evidence

| Case | Evidence |
|---|---|
| **AUTH-01** BOLA | 8 endpoint families as mkup1 against mkup2's objects — no foreign data |
| **AUTH-03** BFLA | 7 admin routes with an artist token → all 401/403; `role:"admin"` at registration → 422 (`oneof=customer artist`) |
| **AUTH-04** JWT | 7 forgeries — `alg:none`, blank sig, HS/RS confusion, tampered `role`, tampered `user_id`, expired, garbage sig — **all 401** |
| **AUTH-08** dev OTP bypass | `os.Getenv("APP_ENV") == "development"` — exact match, fails closed, and `APP_ENV` is in `requiredEnvVars` so the server will not boot without it |
| **AUTH-09** reset tokens | forged token 400; identical 204 for known and unknown email |
| **INJ-01** SQLi | 5 payloads × 3 discovery filters + malformed path UUIDs — no injection, no driver error leaked |
| **INJ-02** mass assignment | `role`, `is_verified`, `rating`, `salon_id`, `id` in a PATCH body — all ignored |
| **INJ-03** SSTI/XSS | `{{7*7}}`, `${7*7}`, `"><img src=x onerror=…>` in bio → rendered `&#34;&gt;&lt;img …` |
| **CLIENT-02** CSRF | refresh cookie is `HttpOnly; Secure; SameSite=Strict` |
| **CLIENT-03** CORS | `evil.test`, `null`, `localhost:4200.evil.test` — none reflected |
| **FRAUD-01** hold race | 8 concurrent holds on one slot → **1×201, 7×409** |
| **FRAUD-02** price tampering | booked with `price: 0.01` → server stored `45.00` |
| **SPAM-05** pre-approval | unapproved artist hidden from Discover; token role is `artist` |

---

## Not executed — do not read these as passes

- **AUTH-05 / AUTH-06 — inconclusive, not failing.** The refresh cookie sets
  `Secure` unconditionally. Browsers exempt `localhost`; a scripted HTTP client
  does not, so the cookie is never returned and every refresh answers 401.
  **These cannot be tested over plain HTTP at all.** The flags themselves are
  correct. *This is the largest genuine gap in coverage.*
- **AUTH-07 / SPAM-02** (OTP brute force, OTP bombing) — would send real
  messages through the live Twilio account. Stub the sender first.
- **EDGE-01 / EDGE-02** (load, amplification) — need the written authorisation
  §3.3 requires.
- **FRAUD-04 / FRAUD-05** (stock oversell, invoice double-confirm) — need a
  stock-limited product and a pending invoice as fixtures.
- **FRAUD-06** — a process control over admin behaviour, not an endpoint.
- **CLIENT-05** — log review, not swept.

---

## Open risks

### R1 — No edge layer · **P1 (infra)**

No CDN, no WAF, no gateway. The in-process limiters are standing in for
infrastructure. `SPAM-01` (Discover scraping) has no real mitigation to find.

### R2 — One admin account confirms all money · **P1 (process)**

Recorded as decision **D19**. The entire revenue model assumes a human reliably
checks OMT/Whish transfers and clicks Confirm. `FRAUD-06` is the attack: a
plausible fake transfer reference. There is no out-of-band verification step in
the flow.

**Recommendation:** a mandatory "verified against statement" acknowledgement on
the confirm action, and audit-log the confirming admin. `internal/audit`
already exists.

### R3 — Timing-class equality untested · **P2**

AUTH-02 asks for byte-identical *and* timing-identical responses. Status, code
and message are now identical; timing is not measured. A DB round-trip
difference between "row exists but is foreign" and "no row" is theoretically
observable. Low practical severity over the public internet; recorded so it is
not assumed closed.

### R4 — Client-side money validation is UX, not a control · **P3**

`money.util.ts` mirrors the Go regex. Anyone can post directly to the API. The
server check is the real one and must never be relaxed because the client one
exists.

---

## Recommended action

| # | Action | Priority |
|---|---|---|
| R1 | CDN/WAF in front of the API before launch | **P1** |
| R2 | Out-of-band payment-verification acknowledgement + audit log | **P1** |
| — | Re-run AUTH-05/06 over HTTPS | **P1** |
| — | Stub Twilio, then run AUTH-07 / SPAM-02 | P2 |
| R3 | Measure timing class on ownership branches | P2 |
| — | Seed fixtures for FRAUD-04 / FRAUD-05 | P2 |
