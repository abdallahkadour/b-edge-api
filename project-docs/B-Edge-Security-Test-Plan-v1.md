# B-Edge — Security Assessment &amp; Penetration Test Plan v1

> **Snapshot, not current state.** Figures below (domain counts, schema
> version, table counts) were true when written. Verified against code on
> 2026-09-02 the project has **32 migrations, 29 tables, 17 domains, 115
> route registrations, 570 tests**. Read this for its analysis, not its
> numbers; `CLAUDE.md` has the current figures.


> Written 2026-08-31. A threat matrix and executable test plan for the API
> border, authentication boundaries, and transaction workflows.
>
> **Companion to, not a replacement for,
> `B-Edge-Security-UI-Enterprise-Assessment-v1.md`** (Aug 15, 2026), which
> records what has already been hardened and why. That document is the
> "where are we" snapshot; this one is "how do we attack it, and what must
> hold." Read §1.2 of that document before testing — several classes here
> were already found and fixed once, and a regression is more likely than a
> novel finding.
>
> **No tests in this document have been executed.** Every row is a
> hypothesis to be proven or disproven by a human tester. Nothing below
> should be read as a statement that the system passes.

---

## 0. Read this before using the document

### 0.1 There is no edge layer

The prompt this document answers assumed an edge tier — CDN, API gateway,
edge compute. **B-Edge has none of that today.** Verified against the
running system:

- A single Go/Fiber binary listening on one port. No CloudFront, no
  Cloudflare, no Lambda@Edge, no Workers, no API gateway.
- Two Angular PWAs compiled to static bundles.
- No WAF, no bot management, no DDoS scrubbing.
- `B-Edge-INFRA.docx` plans Nginx + EC2 + Kubernetes; none of it is live,
  and `RELEASE-CHECKLIST.md` correctly lists production hosting as
  entirely unassessed.

Every "edge" attack class below is therefore split into two states:

| Marker | Meaning |
|---|---|
| **LIVE** | Testable against the current system, today. |
| **ANTICIPATORY** | Not testable yet. Becomes real the day a reverse proxy or CDN is introduced. Written now because these are cheap to design against and expensive to retrofit. |

Testing an ANTICIPATORY row today produces a false pass — the vulnerability
is absent because the component is absent, not because it is defended.

### 0.2 There is no payment gateway

B-Edge does not process card payments and cannot. Stripe does not operate
in Lebanon (see `B-Edge-Feature-Feasibility-Assessment-v1.md` §0.2). Money
moves by **manual OMT/Whish bank transfer**, with a human confirming
receipt.

This removes an entire class of threats (card testing, BIN enumeration,
chargeback abuse, PCI scope) and **replaces it with a different, less
familiar one**: fraud against a human confirmation step. §2.2 is where the
real financial risk lives, and it is unusual enough that a tester following
a generic e-commerce checklist will miss all of it.

### 0.3 The most valuable finding is a regression, not a novelty

Two prior security passes found real bugs of a specific shape:

- **Fiber route-group middleware leaking across domain boundaries** — a
  group scoped to the bare `/api/v1` prefix applied its auth requirement to
  every route registered afterwards, silently 401-ing a public endpoint.
- **Cross-tenant IDOR** — `RequireRole("artist")` proves the caller is *an*
  artist, never that they are *that* artist. Six booking endpoints shipped
  with this before it was found.

Both classes recur whenever a domain is added. Prioritise them.

---

## 1. Threat Matrix

Severity uses CVSS v3.1 qualitative bands. Ratings assume the **current**
deployment (single instance, no WAF, pre-launch traffic) and would change
under production load.

### 1.A Edge &amp; Infrastructure

| # | Vector | State | Sev | Description &amp; B-Edge-specific impact | Test vector |
|---|---|---|---|---|---|
| A1 | **Volumetric DDoS** | LIVE | High | No scrubbing, no CDN, one process. The in-process concurrency limiter sheds load with 503s rather than collapsing — a real mitigation for *thundering herd*, not for a distributed flood. One saturated uplink takes the whole product down. | Ramp K6/Locust against `GET /api/v1/discovery/artists` until 503s dominate; record the request rate at which p95 latency exceeds 2s. |
| A2 | **EDoS — Twilio credit exhaustion** | LIVE | **High** | Every OTP request queues a WhatsApp message costing ~$0.0141. The account is pay-as-you-go, **$20, auto-recharge disabled** — a genuine mitigation, but it caps *loss*, not *denial*: once drained, **no customer can log in**, because OTP is the only customer auth path. | Script `POST /customer-auth/request-otp` across many distinct phone numbers; measure spend per 1,000 requests and time-to-drain against the OTP rate limiter. |
| A3 | **Application-layer DoS via slot generation** | LIVE | Medium | `GetAvailableSlots` is the most computationally expensive public endpoint — hours resolution, exception lookup, booking-conflict scan, travel buffers. Unauthenticated and reachable by guests. | Concurrent requests across many `date` values for one artist; compare CPU against an equivalent volume of `/discovery/artists`. |
| A4 | **Cache poisoning / cache deception** | ANTICIPATORY | High | No cache exists. On introducing one, the danger is acute: `/api/v1/media/portfolio/:id` and `/discovery/*` are public, but `/billing/*` and `/artists/salon/*` are per-user. A cache keyed without `Authorization` would serve one artist's invoices to another. | Post-CDN: request an authed endpoint with a cache-buster extension (`/billing/invoices.css`); confirm it is not cached and served to an anonymous client. |
| A5 | **HTTP request smuggling** | ANTICIPATORY | High | Requires a front-end/back-end pair that disagree on message framing. Not possible with a single origin server. Becomes live with Nginx in front of Fiber. | Post-proxy: Burp Suite HTTP Request Smuggler, CL.TE and TE.CL variants against the proxy→Fiber hop. |
| A6 | **Host header injection** | LIVE | Medium | Password reset and review-token links are generated server-side. If any URL is built from the inbound `Host` header rather than configured origin, an attacker controls where a victim's reset link points. **`internal/share` reads `CLIENT_URL` from env, not the Host header** — verify every other link builder does the same. | `POST /auth/forgot-password` with `Host: evil.test`; inspect the queued `notifications` row for the emitted link. |
| A7 | **TLS downgrade / stripping** | ANTICIPATORY | High | No TLS termination exists locally. On deployment: no HSTS is emitted today (see D4), so a first-visit downgrade is unmitigated. | Post-deploy: `testssl.sh` against the domain; assert TLS ≥1.2, HSTS present with a meaningful max-age, no downgrade. |
| A8 | **SSRF** | LIVE | Medium | The server fetches outbound in two places: Cloudinary upload forwarding and Twilio. Neither takes a user-supplied URL — media upload receives a **file**, not a URL, which is the design that closes this. Verify no future endpoint accepts a URL to fetch. | Grep for outbound HTTP with user-controlled hosts; attempt `169.254.169.254` (cloud metadata) in any URL-accepting field. |

### 1.B Authentication &amp; Authorization

| # | Vector | State | Sev | Description &amp; B-Edge-specific impact | Test vector |
|---|---|---|---|---|---|
| B1 | **BOLA / IDOR — cross-tenant** | LIVE | **Critical** | The highest-yield class here, with prior confirmed instances. `RequireRole("artist")` proves role, never ownership. Every artist-scoped read/write must resolve `user_id → artists.id` server-side and ignore any client-supplied ID. | For every artist endpoint taking an ID: authenticate as mkup1, substitute mkup2's booking/client/product/media/invoice/store ID. Expect 403 or 404 — never data. |
| B2 | **BOLA — enumeration via status code** | LIVE | Medium | Billing invoices and media tags deliberately return **404, not 403**, on a foreign object, so IDs cannot be probed. Inconsistency elsewhere leaks existence. | Compare responses for (a) a nonexistent UUID and (b) a real UUID owned by another artist, on every ID-taking endpoint. They must be byte-identical. |
| B3 | **BFLA — privilege escalation to admin** | LIVE | **Critical** | Admin capability is the crown jewel: approving artists, confirming money, voiding invoices. `/auth/register` rejects `role=admin` and admins are capped at 2 via an interactive seeder. | Attempt `role: "admin"` in registration; call every `/api/v1/admin/*` route with an artist token; attempt admin-only billing mutations as an artist. |
| B4 | **JWT forgery / algorithm confusion** | LIVE | **Critical** | HS256 with a 64-char secret. Classic failures: `alg: none`, HS/RS confusion, secret brute-force, unverified signature. | Strip the signature; set `alg: none`; swap to RS256 with an attacker key; tamper `role` and `user_id` in the payload; replay a token signed with a weak guessed secret. |
| B5 | **Token replay after logout** | LIVE | High | Logout must invalidate the refresh token server-side. A 15-minute access token remains valid by design — confirm that window is the *only* residual exposure and the refresh path is genuinely dead. | Capture access + refresh tokens, log out, then (a) reuse the access token and (b) call `/auth/refresh` with the old refresh cookie. |
| B6 | **Refresh token rotation / reuse detection** | LIVE | High | Rotation on every refresh is implemented. The question is whether a *stolen, already-used* refresh token is detected and the family revoked, or merely rejected. | Refresh once (obtaining token B), then replay the original token A. Expect rejection; ideally expect the whole session family invalidated. |
| B7 | **OTP brute force** | LIVE | **Critical** | Customer auth is phone + OTP with **no password**. A guessable or unlimited-attempt code is a full account takeover. A per-attempt counter exists (`verify otp: increment attempts`) — verify it locks out and that the code space is large enough. | Request an OTP, then attempt codes at volume. Record attempts-before-lockout, whether lockout is per-phone or per-IP, and whether requesting a new OTP resets the counter. |
| B8 | **Dev OTP bypass reaching production** | LIVE | **Critical** | `devBypassOTPCode = "326321"` in `customerauth/service.go` accepts **any phone number** when `APP_ENV == "development"`. The only thing standing between this and total customer-account compromise is one environment variable. A blank, misspelled, or mis-cased `APP_ENV` must fail closed. | On a staging build, set `APP_ENV` to `""`, `Development`, `dev`, and unset; attempt `326321` against a real number each time. **Any acceptance outside exact `development` is Critical.** |
| B9 | **Password reset token abuse** | LIVE | High | Token must be single-use, short-lived, unguessable, and invalidated on password change. Prior finding: reset delivery was a dead path — verify the token lifecycle itself, not just delivery. | Reuse a consumed token; use an expired one; request two resets and try the first; alter the token by one character. |
| B10 | **Review token abuse** | LIVE | Medium | `GET/POST /public/reviews/by-token/:token` is unauthenticated by design (guests review without accounts). A predictable or reusable token allows review forgery, which is reputational fraud. | Assess token entropy; submit twice with one token; enumerate adjacent values. |
| B11 | **Suspended-artist enforcement bypass** | LIVE | Medium | Enforcement is graduated (grace 0–21d, past_due 21–45d, suspended 45+). Bypassing it is revenue leakage, not data loss. Billing routes are *deliberately* reachable while suspended so an artist can pay. | As a suspended artist, attempt writes to `/artists/*`, `/products/*`, `/media/*`; confirm 403 and that `/billing/*` still works. |

### 1.C Injection &amp; Payload

| # | Vector | State | Sev | Description &amp; B-Edge-specific impact | Test vector |
|---|---|---|---|---|---|
| C1 | **SQL injection** | LIVE | **Critical** | All queries use pgx with `$1` placeholders — structurally resistant. The residual risk is **string-interpolated SQL fragments**: `subscriptionVisibleCond` is built with `fmt.Sprintf` and duplicated across discovery, artist and share. It interpolates a constant today, not user input, but it is the pattern to watch. | SQLmap against every parameterised endpoint (search `q`, `city`, `category`, date params, all path UUIDs). Manually review every `fmt.Sprintf` producing SQL. |
| C2 | **NoSQL / ORM injection** | LIVE | Low | No NoSQL store and no ORM query builder — pgx only. Included for completeness; expected N/A. | Confirm no MongoDB/Redis query construction from user input. |
| C3 | **Command injection** | LIVE | Low | No `os/exec` in request paths. Image processing happens in-process, not via a shell-invoked binary. | Grep `exec.Command`; fuzz filename and metadata fields on upload with shell metacharacters. |
| C4 | **SSTI** | LIVE | Medium | One genuine template surface exists and it is **new**: `internal/share` builds an HTML document by string concatenation, embedding the artist's **bio** — user-controlled free text. It is `html.EscapeString`-escaped with tests, but this is the single highest-risk injection point in the codebase because it is the only place user input reaches raw markup. | Set an artist bio to `{{7*7}}`, `${7*7}`, `</title><script>alert(1)</script>`, and polyglot payloads; `curl /a/:handle` and inspect raw bytes. |
| C5 | **XXE** | LIVE | Low | No XML parsing anywhere; all APIs are JSON. Expected N/A. | Submit `Content-Type: application/xml` with an external-entity payload to POST endpoints; expect rejection. |
| C6 | **XPath / LDAP injection** | LIVE | Low | No XPath, no directory service. N/A. | — |
| C7 | **Mass assignment** | LIVE | High | Request structs are explicit, but any field bound from JSON that maps to a privileged column is exploitable — e.g. injecting `role`, `is_verified`, `rating`, `status`, `salon_id`, or `plan_code` into a profile or store update. | Add unexpected privileged fields to every PATCH/PUT body; confirm they are ignored, not applied. |
| C8 | **Decimal/currency coercion** | LIVE | Medium | Money is `decimal.Decimal` parsed from strings, with `NUMERIC(10,2)` columns. **Known gap**: `parseNonNegativeDecimal` accepts arbitrary precision, so `10.999` passes Go and is silently rounded by Postgres. Test whether that rounding can ever favour the payer. | Submit prices/deposits with excess precision, scientific notation (`1e3`), leading `+`, `Infinity`, `NaN`, and values exceeding `NUMERIC(10,2)`. |

### 1.D Client-Side &amp; Cross-Domain

| # | Vector | State | Sev | Description &amp; B-Edge-specific impact | Test vector |
|---|---|---|---|---|---|
| D1 | **Stored XSS via UGC** | LIVE | **High** | Angular escapes interpolation by default, so the PWA is largely protected. The exception is anything rendered outside Angular — which now exists: `internal/share` emits the bio into raw HTML. UGC fields: artist bio, service names, store names, review comments, `special_requests`, delivery notes, product descriptions. | Inject a payload into each UGC field, then view it in (a) the customer PWA, (b) the artist dashboard, (c) `/a/:handle`, and (d) any `[innerHTML]` binding. |
| D2 | **CSRF on the refresh endpoint** | LIVE | **High** | CORS is set with `AllowCredentials: true` and the refresh token is an **httpOnly cookie**. Bearer-token endpoints are inherently CSRF-resistant, but `/auth/refresh` and `/customer-auth/refresh` authenticate *by cookie* — the classic exception. Verify `SameSite` and whether any state-changing route accepts cookie auth alone. | From an off-origin page, auto-submit a form and issue a `fetch(..., {credentials:'include'})` to `/auth/refresh`; confirm the browser does not attach the cookie or the server rejects it. |
| D3 | **CORS misconfiguration** | LIVE | High | Allowlist from `CLIENT_URL`, not wildcard — correct. Risks: origin-reflection, a trailing-comma parse producing an empty allowed origin, and `null` origin acceptance from sandboxed iframes. | Request with `Origin: https://evil.test`, `Origin: null`, and a prefix-match attempt (`https://bedge.app.evil.test`). Assert no permissive `Access-Control-Allow-Origin`. |
| D4 | **Missing security headers** | LIVE | **High — confirmed gap** | Grep found **no** helmet middleware and no `Content-Security-Policy`, `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`, or `Strict-Transport-Security` anywhere. This is not a hypothesis; it is a present, verified absence. Consequences: clickjacking of the dashboard, MIME sniffing, no defence-in-depth against XSS, referrer leakage of booking URLs. | `curl -I` any endpoint and enumerate headers. Frame `/dashboard/*` in an iframe from another origin and attempt a click-through on a destructive control. |
| D5 | **Subdomain takeover** | ANTICIPATORY | High | No DNS is under management yet. Becomes live the moment `bedge.app` gains subdomains, especially if any CNAME to a deprovisioned service. | Post-DNS: enumerate subdomains, check every CNAME resolves to a claimed resource. |
| D6 | **Open redirect** | LIVE | Medium | `internal/share` **always redirects** — currently only to `CLIENT_URL` + a slug, which is safe. Any future redirect taking a user-supplied `next`/`return` parameter becomes a phishing primitive, especially on an auth flow. | Attempt `//evil.test`, `https://evil.test`, and CRLF injection in any redirect-influencing parameter. |
| D7 | **Sensitive data in logs** | LIVE | Medium | Structured Zap logging on every request. OTP codes, JWTs, `Authorization` headers, payment references and phone numbers must never be logged. | Drive a full auth + booking + payment flow, then grep the log output for the OTP code, token substrings, and phone numbers. |

---

## 2. Fraud, Spam &amp; Financial Exfiltration

This section is where B-Edge diverges most from a standard checklist, because
money never touches the platform.

### 2.1 Account Takeover

| # | Vector | Sev | B-Edge specifics | Test vector |
|---|---|---|---|---|
| F1 | **Credential stuffing (artist)** | High | Artist login is email + bcrypt password. Global limit is 100 req/15 min **per IP** — trivially defeated by rotating IPs, and punishing to a salon behind shared WiFi. No CAPTCHA, no device fingerprinting, no breach-password check. | Distributed low-and-slow login attempts from rotating source IPs; measure detection and lockout. |
| F2 | **OTP interception (customer)** | **Critical** | OTP over WhatsApp is the *only* customer auth factor. Possession of the phone number is the entire identity. SIM-swap and WhatsApp-account compromise are out of our control — but code lifetime, single-use enforcement, and attempt caps are not. | Verify code TTL, single-use, and that a new request invalidates the previous code rather than allowing several valid codes concurrently. |
| F3 | **Account enumeration** | Medium | Login is documented as enumeration-safe. **OTP request is the weaker surface**: if it responds differently for a registered vs unregistered phone, it becomes a customer-database oracle. | Compare status, body and *timing* for `request-otp` on known vs unknown numbers. |
| F4 | **Password reset takeover** | High | See B9. Additionally: does changing the password invalidate existing sessions and refresh tokens? | Reset the password from session A while session B is active; confirm B is dead. |

### 2.2 Financial &amp; Business-Logic Exploits

> The threat model here is **not** card fraud. It is fraud against a human
> who clicks "confirm payment received."

| # | Vector | Sev | B-Edge specifics | Test vector |
|---|---|---|---|---|
| F5 | **Fabricated payment reference** | **Critical** | An artist submits a free-text OMT/Whish reference to claim an invoice is paid. It is explicitly *a claim, not proof* — but if an admin confirms without checking the bank, service is extended for money never received. This is the primary revenue-loss vector, and it is **procedural, not technical**. | Submit an invoice with a plausible fabricated reference. The test is whether the admin workflow *requires* out-of-band verification. Recommend a mandatory "verified against statement" checkbox. |
| F6 | **Double-confirm / period extension** | High | `ConfirmInvoice` extends `current_period_end` and marks paid in one transaction; re-confirming returns 409 rather than extending twice. Verify under **concurrency**, not just sequentially. | Fire N concurrent confirms on one invoice; assert exactly one succeeds and the period advanced once. |
| F7 | **Booking hold race / double-booking** | **Critical** | A GIST exclusion constraint is the final atomic guard. Prior testing proved the guarded-`UPDATE` pattern safe under 6 concurrent races — extend that. | Concurrent `POST /bookings/guest/hold` for the identical artist+store+slot from many clients; assert exactly one succeeds. |
| F8 | **Deposit amount tampering** | **Critical** | If deposit or price is taken from the request rather than re-read from `services` server-side, a customer sets their own deposit. | Submit a booking with `deposit_amount: 0.01` and a mismatched `price`; assert server-side values win. |
| F9 | **Product stock oversell** | High | Stock caps were previously tested under rapid-click abuse and a direct over-limit call. Re-test as a **race**, not a sequence. | N concurrent orders for the last unit; assert exactly one succeeds and stock never goes negative. |
| F10 | **Order total manipulation** | **Critical** | Cart totals must be recomputed server-side from product IDs and quantities. Any client-supplied total or unit price is a money bug. | Submit an order with tampered `total_amount`, negative quantity, and a quantity causing integer overflow. |
| F11 | **Coupon/discount abuse** | N/A *(today)* | **No discount capability exists** — nothing in `services.price`, `deposit`, `products` or `invoices` supports a reduction. This entire row becomes Critical the day Sprint 9 lands, and the race/stacking cases must be written *before* the feature. | Deferred. Add on discount-engine delivery. |
| F12 | **Currency arbitrage** | Low | Migration 026 pins every money column to USD with a `CHECK` constraint, and the service rejects non-USD. Structurally closed. | Attempt plan creation with `LBP`/`EUR`; expect 400 and a constraint violation on any direct write. |
| F13 | **Refund/chargeback abuse** | N/A | No card rails, so no chargebacks. Refunds are out-of-band bank transfers. Not a platform vector. | — |

### 2.3 Spam, Scraping &amp; Resource Exhaustion

| # | Vector | Sev | B-Edge specifics | Test vector |
|---|---|---|---|---|
| F14 | **PII scraping of Discover** | **High** | `/discovery/artists` is unauthenticated by design and returns name, category, city, rating and handle. `/a/:handle` now adds a machine-readable profile card. The whole artist roster — a competitor's prospecting list — is one loop away. There is no bot management. | Paginate the full artist list; measure records-per-minute before any control triggers. |
| F15 | **Store/phone harvesting** | High | `discovery.StoreCard` now exposes **`phone`, `address` and precise coordinates** publicly. Legitimate for customers; also a ready-made cold-call list. Confirm this is an accepted trade-off. | Enumerate all artist profiles and extract every phone/coordinate pair. |
| F16 | **OTP / notification bombing** | **High** | Two victims: the **target**, harassed with repeated WhatsApp codes, and **B-Edge**, whose Twilio balance funds it. The $20 cap with auto-recharge off bounds the cost but converts the attack into a **login outage for every customer** (see A2). | Repeatedly request OTPs for one number, then for many numbers; verify per-phone cooldown *and* a global send ceiling. |
| F17 | **Phishing links in UGC** | **High** | Free-text fields render to other users: artist bio, service/store names, review comments, `special_requests`, delivery notes. **The bio now also lands in Open Graph tags** on `/a/:handle`, so a malicious link is rendered by WhatsApp itself when the profile is shared — high-credibility phishing. No URL filtering or link rewriting exists. | Place `http://evil.test` in each UGC field; observe rendering in the PWA, the dashboard, and a WhatsApp link preview. |
| F18 | **Fake artist registration** | Medium | Self-service onboarding exists, gated by admin approval before an artist is publicly visible. The gate is the control — verify nothing is reachable pre-approval. | Register, skip approval, and attempt to appear in Discover, take a booking, and render a share preview. |
| F19 | **Guest booking spam** | Medium | Guest booking requires no account — the deliberate funnel design. Mass fake holds could occupy an artist's entire calendar. Holds expire lazily, which bounds but does not prevent it. | Create many guest holds across an artist's availability; measure how long the calendar stays blocked. |
| F20 | **Review bombing** | Medium | Reviews require a booking-derived token, which is a strong control. Verify a token cannot be minted without a completed booking. | Attempt review submission without a token, with a forged token, and with another booking's token. |
| F21 | **Waitlist queue poisoning** | Low | Entries need no account. A flooded waitlist starves genuine customers, and a `notified` entry currently blocks its queue indefinitely (the sweep is unbuilt). | Flood one artist's waitlist; confirm ordering and that expiry cascades. |

---

## 3. Formal Security Test Plan

### 3.1 Objectives

1. Prove or disprove every LIVE row in §1 and §2 against a staging build.
2. Regression-test the two previously-fixed classes (route-group leakage,
   cross-tenant IDOR) across **every** domain, including those added since
   the last pass: `billing`, `share`, `openinghours`, `subscription`.
3. Produce a ranked, reproducible finding list with remediation guidance.

### 3.2 Scope

**In scope**
- All `/api/v1/*` endpoints — **142 documented operations** across 23 domains
  as of 2026-09-21, counted from `docs/swagger.json` rather than by grepping
  for `.Get(`, which also matches `c.Get("Authorization")` and inflates the
  figure by about 40%. `internal/membership` is new and adds a **public,
  unauthenticated endpoint** — see 3.4d.
- `GET /a/:handle` (non-versioned, HTML-emitting).
- `GET /invitations/:token` and `POST /invitations/:token/decline` — **public and
  unauthenticated**, the second of them **mutating**. Added 2026-09-21.
- Both PWAs as deployed static bundles.
- Auth boundaries: artist JWT, customer OTP, admin role, review tokens, **the
  salon owner/member capability boundary** (`internal/pkg/salonrole`), and
  **the invitation token as a bearer credential**.
- Business logic: booking lifecycle, product orders, billing invoices,
  **salon membership and per-artist availability**.
- The staging database.

**Out of scope**
- Third-party infrastructure: Cloudinary, Twilio, Postgres internals.
- Social engineering of real staff.
- Physical security.
- **Production.** All testing runs against staging with synthetic data.
- ANTICIPATORY rows (A4, A5, A7, D5) until the relevant component exists.

### 3.3 Environment &amp; prerequisites

| Requirement | Detail |
|---|---|
| Target | Dedicated staging with production-shaped config. **`APP_ENV` must be set exactly as production sets it** — otherwise B8 is untestable and the recover middleware leaks stack traces. |
| Data | Synthetic only. The 14-account roster (`user1`–`user10`, `mkup1`–`mkup4`) is the intended fixture. **Never test against real customer PII.** |
| Isolation | Twilio in test mode or a stubbed sender. F16 will otherwise send real WhatsApp messages and drain the real $20 balance. |
| Authorisation | Written sign-off before any load testing (A1–A3). Confirm the hosting provider's policy — unannounced load tests are frequently a terms violation. |
| Tools | Burp Suite Pro (proxy, Intruder, Turbo Intruder for races, HTTP Request Smuggler), OWASP ZAP, SQLmap, Postman/Newman, K6 or Locust, `testssl.sh`, `jwt_tool`, `ffuf`. |
| Rollback | A restorable DB snapshot. Several tests write data deliberately. |

### 3.4 Test cases

Severity is the rating **if the test fails**.

| Test ID | Vulnerability / Scenario | Category | Severity | Procedure | Expected secure behaviour | Remediation guidance |
|---|---|---|---|---|---|---|
| **AUTH-01** | Cross-tenant IDOR, artist resources | BOLA | Critical | Authenticate as mkup1. For each artist endpoint taking an ID, substitute mkup2's booking, client, product, media, store, invoice and subscription IDs. | 403 or 404 with no object data. Identical response for foreign vs nonexistent. | Resolve `user_id → artists.id` server-side; never trust a client ID. Follow `GetArtistIDByUserID`. |
| **AUTH-02** | Foreign-object status leak | BOLA | Medium | Compare responses for a nonexistent UUID vs a real foreign UUID, on every ID endpoint. | Byte-identical, including timing class. **Failed 2026-09-05; fixed** for status/code/message — timing class still untested. | Standardise on 404 for both, as billing invoices already do. |
| **AUTH-03** | Admin privilege escalation | BFLA | Critical | Call every `/api/v1/admin/*` route with an artist token; attempt `role:"admin"` at registration; attempt admin billing mutations as artist. | 403 on every admin route. Registration ignores `role`. | Keep `RequireRole("admin")` on the group; assert in tests, not by convention. |
| **AUTH-04** | JWT algorithm confusion &amp; tampering | Auth | Critical | `jwt_tool` against a valid token: `alg:none`, HS/RS confusion, blank signature, tampered `role`/`user_id`, expired token. | Every variant rejected 401. **Verified 2026-09-05: passes.** | Pin the expected algorithm explicitly on verification; never trust the header's `alg`. |
| **AUTH-05** | Token replay after logout | Auth | High | Capture tokens, log out, reuse access token and refresh cookie. | Refresh rejected immediately. Access token dies at ≤15 min. | Maintain a server-side revocation list for refresh tokens. |
| **AUTH-06** | Refresh reuse detection | Auth | High | Refresh to get token B, then replay token A. | A rejected; ideally the whole family revoked as theft evidence. | Implement refresh-token families with reuse detection. |
| **AUTH-07** | OTP brute force | Auth | Critical | Request an OTP, then attempt codes at volume from one and many IPs. | Lockout within a small attempt budget, keyed to phone not IP. | Cap attempts per code; invalidate the code on cap; add exponential backoff per phone. |
| **AUTH-08** | **Dev OTP bypass in non-dev env** | Auth | **Critical** | Set `APP_ENV` to `""`, `Development`, `dev`, `prod`, and unset. Attempt `326321` against a real number each time. | Accepted **only** when `APP_ENV == "development"` exactly. | Prefer removing the bypass from production builds entirely (build tag), rather than relying on a runtime string. |
| **AUTH-09** | Password reset token lifecycle | Auth | High | Reuse a consumed token; use an expired one; mutate one character; request two resets and use the first. | All rejected. Reset kills existing sessions. | Single-use, short TTL, cryptographically random, invalidated on use and on password change. |
| **AUTH-10** | Review token forgery | Auth | Medium | Assess entropy; reuse a token; enumerate neighbours; use another booking's token. | Single-use, unguessable, bound to its booking. | Ensure ≥128 bits of entropy and single-use enforcement. |
| **INJ-01** | SQL injection sweep | Injection | Critical | SQLmap across `q`, `city`, `category`, dates and every path UUID. Manually review each `fmt.Sprintf` building SQL. | No injection. All values parameterised. | Keep `$n` placeholders; never interpolate user input, even "safe-looking" values. |
| **INJ-02** | Mass assignment | Injection | High | Add `role`, `is_verified`, `rating`, `status`, `salon_id`, `plan_code`, `artist_id` to every PATCH/PUT body. | Silently ignored; no privileged field changes. | Bind to explicit request structs only; never decode into a DB model. |
| **INJ-03** | SSTI / raw-HTML injection in share preview | Injection | High | Set bio to `{{7*7}}`, `${7*7}`, `</title><script>alert(1)</script>`, `"><img src=x onerror=alert(1)>`. `curl /a/:handle`; inspect raw bytes. | All escaped. No template evaluation. No tag breakout. | Keep `html.EscapeString` on every interpolated value; prefer `html/template` if this document grows. |
| **INJ-04** | Money-value coercion | Injection | Medium | Submit prices with excess precision, `1e3`, `+5`, `Infinity`, `NaN`, and values over `NUMERIC(10,2)`. | 400 for all invalid forms; no silent rounding in the payer's favour. **Failed 2026-09-05; fixed** — `internal/pkg/money`. | Enforce 2-decimal scale in Go before the DB rounds it. |
| **CLIENT-01** | Stored XSS across UGC | XSS | High | Inject payloads into bio, service name, store name, review comment, `special_requests`, delivery notes, product description. View in both PWAs and `/a/:handle`. | Rendered as inert text everywhere. | Rely on Angular escaping; audit every `[innerHTML]`; escape at the share boundary. |
| **CLIENT-02** | CSRF on cookie-authenticated refresh | CSRF | High | From an off-origin page, `fetch('/api/v1/auth/refresh', {credentials:'include'})` and an auto-submitting form. | Rejected. Cookie not sent cross-site. | `SameSite=Strict` (or `Lax`) on refresh cookies; consider a double-submit token. |
| **CLIENT-03** | CORS bypass | CORS | High | `Origin: https://evil.test`, `Origin: null`, `https://bedge.app.evil.test`. | No `Access-Control-Allow-Origin` for any of them. | Exact-match the allowlist; never reflect the Origin header; reject `null`. |
| **CLIENT-04** | **Missing security headers** | Headers | High | `curl -I` on any endpoint. Frame `/dashboard/*` cross-origin and attempt a click-through. | CSP, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy`, HSTS all present. | **Fixed 2026-09-05** — `internal/middleware/secheaders.go`, enforcing rather than report-only. |
| **CLIENT-05** | Sensitive data in logs | Logging | Medium | Run a full auth + booking + payment flow; grep logs for OTP codes, tokens, phone numbers, payment references. | None present. | Redact `Authorization`, OTP and payment-reference fields at the logger. |
| **EDGE-01** | Volumetric load resilience | DoS | High | K6 ramp against `/discovery/artists` to saturation. *Requires written authorisation.* | Graceful 503 shedding; no data loss; recovery without restart. | Deploy behind a CDN/WAF before launch; keep the concurrency limiter. |
| **EDGE-02** | Slot-generation amplification | DoS | Medium | Concurrent `GET /bookings/slots` across many dates for one artist; compare CPU to a baseline endpoint. | No disproportionate cost. | Cache per (artist, store, date); consider auth or a tighter limit. |
| **EDGE-03** | Host header injection in generated links | Injection | Medium | `POST /auth/forgot-password` with `Host: evil.test`; inspect the queued notification. | Link built from configured origin, never the request header. | Build every outbound URL from `CLIENT_URL`, as `internal/share` does. |
| **EDGE-04** | SSRF via outbound fetch | SSRF | Medium | Attempt `169.254.169.254` and `file://` in any URL-accepting field; review outbound calls. | No user-controlled outbound fetch. | Never accept a URL to fetch; keep upload as file-transfer. |
| **FRAUD-01** | Booking hold race | Logic | Critical | Turbo Intruder: N concurrent guest holds for one slot. | Exactly one succeeds. | Keep the GIST exclusion constraint as the final guard. |
| **FRAUD-02** | Deposit/price tampering | Logic | Critical | Book with `deposit_amount: 0.01` and a mismatched price. | Server re-reads from `services`; client values ignored. | Never accept price or deposit from the client. |
| **FRAUD-03** | Order total manipulation | Logic | Critical | Tamper `total_amount`; negative quantity; overflow quantity. | Total recomputed server-side; negatives rejected. | Recompute from product IDs and quantities only. |
| **FRAUD-04** | Stock oversell race | Logic | High | N concurrent orders for the last unit. | Exactly one succeeds; stock never negative. | Atomic guarded `UPDATE ... WHERE stock >= qty`. |
| **FRAUD-05** | Invoice double-confirm race | Logic | High | N concurrent `POST /admin/billing/invoices/:id/confirm`. | One succeeds; period advances once; others 409. | Keep the guarded transition; add a concurrency test. |
| **FRAUD-06** | Fabricated payment reference | Process | Critical | Submit a plausible fake OMT reference; observe whether the admin flow demands bank verification. | Confirmation requires out-of-band proof. | Add a mandatory "verified against statement" acknowledgement, and audit-log the confirming admin. |
| **FRAUD-07** | Suspended-artist bypass | Logic | Medium | As a suspended artist, write to `/artists/*`, `/products/*`, `/media/*`. | 403 on all; `/billing/*` still reachable. | Single policy source: `subscription.Enforce`. |
| **SPAM-01** | Discover scraping | Scraping | High | Paginate the entire artist roster; measure throughput. | Rate limiting or bot detection engages. | Add bot management at the edge; consider auth for bulk paging. |
| **SPAM-02** | OTP bombing | Abuse | High | Repeat OTP requests for one number, then many. *Stub Twilio first.* | Per-phone cooldown and a global daily ceiling. | Per-phone cooldown, global send cap with alerting, CAPTCHA on repeat. |
| **SPAM-03** | Phishing links in UGC | Abuse | High | Put `http://evil.test` in every UGC field; check the WhatsApp preview of `/a/:handle`. | Links inert or clearly attributed; ideally filtered. | Strip or rewrite URLs in bios; moderate flagged content. |
| **SPAM-04** | Guest booking flood | Abuse | Medium | Create many guest holds across an artist's availability. | Holds expire; per-IP/per-phone caps apply. | Cap concurrent holds per phone; shorten the hold TTL. |
| **SPAM-05** | Pre-approval artist exposure | Logic | Medium | Register and, without approval, attempt Discover listing, bookings, and a share preview. | Invisible and unbookable until approved. | Keep the approval gate on every public surface, including `/a/:handle`. |

### 3.4b Test cases added 2026-09-21 — features shipped since the second execution

Covering `deposit_payer_phone` (migration 044), delivery reconciliation
(045), the `APP_ENV` boot guard, subscription enforcement on slot
generation, the morning reminder, draft persistence, and the 90-day booking
horizon.

Two of these were **found while writing this table**, not by running it, and
are marked accordingly. A test plan that only lists hypotheses is worth less
than one that records what looking already turned up.

| Test ID | Vulnerability / Scenario | Category | Severity | Procedure | Expected secure behaviour | Remediation guidance |
|---|---|---|---|---|---|---|
| **AUTH-11** | **Development build reachable from the internet** | Config | **Critical** | Start the API with `APP_ENV=development` and each of `CLIENT_URL`, `API_PUBLIC_URL`, `ARTIST_DASHBOARD_URL` set to a tunnel hostname, a real domain, a public IP; then to `localhost`, `127.0.0.1`, a private range. Also try a public host **second** in a comma-separated `CLIENT_URL`. | Refuses to boot, non-zero exit, naming the offending host, for every public value. Boots normally for every local one. | **Found live 2026-09-21** — a development API was serving the launch artist over a public tunnel and the OTP bypass was exploitable from the internet. Guard added in `config.ValidateEnv`. Prefer also removing the bypass by build tag (see AUTH-08). |
| **AUTH-12** | Delivery-health flag as a registration oracle | Auth | High | Call `request-otp` for (a) an artist's number, (b) a customer's, (c) a number never seen. Compare `delivery_looks_broken` and the whole body across all three, while the channel is failing and while it is healthy. | Byte-identical bodies. The flag must describe the CHANNEL, never the number. | The check is deliberately platform-wide for this reason. A per-number variant is only answerable for numbers the system has messaged, which is itself the leak. Do not "improve" it to be per-number. |
| **AUTH-13** | Account creation for a number you do not control | Auth | High | Submit a guest booking with someone else's phone; check whether a `users` row is created and whether that number can then be claimed. Repeat at volume. | A guest row may be created — that is by design — but it must not block the real owner from later verifying by OTP, nor grant any session. | `request-otp` deliberately creates nothing (Aug 2026 fix). Confirm the guest path cannot be used to squat numbers or to deny OTP login to their owner. |
| **DATA-01** | Provider message id exposure | Info leak | Low | Fetch every booking and notification surface as artist, customer and admin; grep responses for `SM[0-9a-f]{32}`. | The Twilio SID never appears in any API response. | **Verified 2026-09-21: absent from every response model.** It is a join key to the provider and belongs in the database only. |
| **DATA-02** | Payer phone disclosure | Privacy | Medium | As a customer, read your own booking. As an artist, read a booking. Check `deposit_payer_phone` in both. Attempt to read another customer's booking. | The payer number is visible to the artist who must refund it and to nobody outside the booking. Cross-tenant reads still 404. | It may be a third party's number (a spouse, an OMT counter). It must never be messaged, and must never appear in discovery, share previews or reviews. |
| **INJ-05** | **Bidi override in an outbound WhatsApp body** | Injection | Medium | Set a service, store or artist name to contain U+202E. Trigger each notification template — reminder, morning reminder, approval, confirmation, review request. Inspect the queued body byte-for-byte. | No bidi control characters reach the recipient; ordinary Arabic is untouched. | **FOUND AND FIXED 2026-09-21.** `buildMessageBody` returned the pre-rendered message verbatim. Share cards, inbox notifications and `.ics` fields all honoured the bidi rule; the outbound WhatsApp body, the one that reaches a customer's phone, did not. Stripped at the single choke point rather than per template. |
| **INJ-06** | Newline / template injection in a notification body | Injection | Medium | Put `\n`, `%s`, `{{.}}`, and a fake "B-Edge:" prefix into a service name; inspect the rendered WhatsApp text. | No format-string evaluation; the attacker cannot forge what looks like a second, official message. | Bodies are built by SQL concatenation today. Treat any field that lands in a message as hostile. |
| **FRAUD-08** | Refund gate bypass | Logic | **Critical** | With a payer number different from the customer's, call `PATCH /bookings/:id/refunded` with `customer_contacted` absent, `false`, `"false"`, `0`, `null`, and as a string `"true"`. Then try a second refund after the first succeeds. | Refused with `REFUND_PAYER_MISMATCH` for everything except a real boolean `true`. A refunded booking cannot be refunded twice. | Enforced in the service, not only the UI. Money moves out of band over OMT/Whish with no chargeback. |
| **FRAUD-09** | Payer-mismatch guard silently disabled | Logic | High | Set a customer's phone to a local-format or unparseable value, then attempt a refund from a different number. | The guard should still refuse, or the system should refuse to proceed — it must not silently treat "cannot parse" as "no mismatch" for a bookable customer. | **Known design trade-off:** `depositPayerMismatch` returns false when either side will not parse, so an unnormalised phone turns the warning OFF. `verify-uc2` M11 fails if any customer *with a booking* is in that state. |
| **FRAUD-10** | Unsellable artist still sold | Logic | High | For each of cancelled, past_due, suspended: check discovery listing, `GET /bookings/slots`, guest hold, and the share preview `/a/:handle`. | Invisible and unbookable at **every** surface, not just the one that takes money. | **Found 2026-09-20**: `Enforce(StatusCancelled)` said hidden while the discovery SQL said visible, and slot generation consulted no gate at all — 33 slots offered, hold refused at the last step. Both fixed; `verify-uc6` covers it. |
| **CLIENT-06** | PII left in browser storage | Privacy | Medium | Fill the booking funnel, abandon it, then inspect `sessionStorage` and `localStorage`. Close the tab and reopen. Complete a booking and re-inspect. | Name and phone live in `sessionStorage` only, keyed per artist, and are cleared when the booking completes or is abandoned. Nothing in `localStorage`. Nothing survives closing the tab. | Deliberate scope: a draft must survive a reload and nothing more. `localStorage` would outlive the session and the device being handed to someone else. |
| **EDGE-05** | Slot-generation amplification, 90-day horizon | DoS | Medium | Request slots for all 90 offered dates across several artists concurrently; compare cost to a single-date baseline and to the old 28-day window. | No disproportionate cost; the limiter engages before the database does. | The horizon tripled on 2026-09-19. Extends EDGE-02 rather than replacing it — re-baseline before assuming the old numbers hold. |
| **SPAM-06** | Discount code enumeration and redemption race | Abuse | High | Brute-force `discount_code` at checkout and measure whether valid and invalid codes differ in status, body or timing. Then redeem one code concurrently N times, including a `first_time_only` code. | Indistinguishable responses for valid-but-inapplicable vs nonexistent. Exactly one redemption per constraint. | `discount_redemptions` uniqueness is the guard; assert it under concurrency, not by reading the schema. |
| **SPAM-07** | Bulk hours as a mass-mutation vector | Abuse | Low | Call the per-day hours endpoint at volume via "Apply to all days"; attempt it for another artist's store. | Cross-tenant writes 404. Own-store writes are bounded by the rate limiter. | Seven sequential per-day calls, not a bulk endpoint. Worth confirming it stays that way if a bulk endpoint is ever added. |

### 3.4d Test cases added 2026-09-21 — multi-artist salons

Migrations 046-048, `internal/membership`, `internal/pkg/salonrole`,
`internal/pkg/schedule` and the per-artist rota routes together add three
things this plan has never covered: **a new public endpoint**, **a
token-as-credential flow**, and **an authorisation boundary between two
people who are both legitimately signed in**.

`make e2e-suite22` already proves the feature works and that a member is
refused every owner-only write. **None of that is a security test.** It
exercises the intended path; everything below attacks it. Where a case
overlaps suite 22, it is noted so the two do not get confused for each other.

**Two of these are written from known properties of the implementation, not
from suspicion.** DATA-03 and AUTH-14 describe behaviour I can see in the
code and have not yet measured the consequences of. They may turn out to be
acceptable; they should not turn out to be surprises.

---

#### 1.E Salon membership and the role boundary

| ID | Case | Why it matters | Expected |
|---|---|---|---|
| **AUTH-14** | **The access token outlives removal.** Sign in as a member, capture the access token, have the owner remove them, then keep using the captured token for the full 15 minutes. | `RemoveMember` and `TransferOwnership` call `RevokeAllForUser`, which stamps `revoked_at` on **refresh** tokens only. The access token is self-contained, still carries `salon_id` and `salon_role`, and is not checked against anything. So a removed member keeps salon reads and their own booking writes for up to `accessTokenDuration`. **Measure the real window and what it permits** — then decide whether 15 minutes is acceptable or whether removal needs a token denylist. | Undecided. Document the window, enumerate what stays reachable, and record a decision. Do not report a pass without one. |
| **AUTH-15** | **Ownership transfer leaves two owners.** Transfer, then immediately use the former owner's captured access token against an owner-only write. | Same mechanism as AUTH-14 with higher stakes: for up to 15 minutes there can be one `salons.owner_id` and two tokens claiming `salon_role=owner`. Both can edit prices; both can invite. | Same as AUTH-14. The HLD names this as risk R2 and accepts it as the price of not querying a role per request — the test is what turns "accepted" into "measured". |
| **AUTH-16** | **Accept someone else's invitation.** Obtain a token issued to number A, sign in as unrelated account B, POST accept. | The token IS the authorisation — nothing binds it to the invited phone number. Whoever holds the link joins the salon. | Decide deliberately. Binding acceptance to the invited contact would break the common case (invitee signs up with a different email), so this is likely by design — but it must be a decision, and the owner must be told who actually joined. |
| **AUTH-17** | **Privilege escalation through re-invitation.** As a removed member, accept a still-live invitation to the same salon. Then: does `artists.status` come back as `active` or `pending`? | `CompleteIntoExistingSalon` refuses when an artist row already exists (`ErrAlreadyOnboarded`). A removed member's artist row survives with `salon_id = NULL`, so this path is untested. | Either a clean refusal or a clean rejoin at `pending`. Never a silent rejoin at `active`. |
| **AUTH-18** | **`salon_id` in a forged token.** Mint a token with a valid signature but another salon's `salon_id` — or replay a token from an account that has since changed salons. | Every salon-scoped handler trusts `SalonIDFromContext` completely. | Signature verification is the whole defence; confirm it holds and that no route reads `salon_id` from a body or query parameter. |

#### 1.F The invitation as a credential

| ID | Case | Why it matters | Expected |
|---|---|---|---|
| **DATA-03** | **The raw token is stored in plaintext next to its own hash.** `salon_invitations.token_hash` stores only a SHA-256 — but `membership.QueueSalonInvitation` writes the full link, token and all, into `notifications.payload`. Query that table and you have working invitation links. | The hash design exists so a database dump yields no usable links. The notification row **defeats it completely**. The plaintext has to exist somewhere until Twilio sends it, so this is not automatically a bug — but right now nothing removes it afterwards, and every notification the platform has ever queued is still `dead`, so they accumulate forever. | Decide: redact `payload` once `status='sent'`, shorten the TTL, or accept and document. Also confirm the token never reaches a log line — check `middleware/logger.go` output for the accept and preview routes, which carry it in the **path**. |
| **SPAM-08** | **Invitation flooding.** As an owner, issue invitations to 200 distinct numbers in a loop. | Nothing limits invitations per salon per period. Each one writes a `salon_invitations` row and queues a WhatsApp message — so once Meta verification clears, this is a free SMS cannon pointed at arbitrary numbers, sent from B-Edge's verified sender. The reputational damage lands on the platform, not the salon. | A per-salon rate limit, or a cap on live invitations. Currently expected to **FAIL**; this is the case most likely to matter the day WhatsApp starts working. |
| **SPAM-09** | **Token brute force.** Hammer `GET /invitations/:token` with random 43-character tokens and confirm the 600-per-5-minute limiter is the only thing in the way. | The token is 32 bytes of `crypto/rand`, so guessing is not the risk — the endpoint being an unauthenticated, unthrottled lookup is. | Refused by the global limiter; no per-token lockout needed at 256 bits. Confirm the limiter actually applies to this route rather than being assumed. |
| **FRAUD-11** | **Public decline as denial of service.** `POST /invitations/:token/decline` needs **no authentication**. Anyone who sees the link — a forwarded WhatsApp message, a shoulder-surfed screen, a shared phone — can kill the invitation. | Declining is destructive and irreversible: the invitation moves to a terminal state and the owner must notice and reissue. The token is the credential, which is defensible, but the asymmetry is not: **reading** the link is harmless, **burning** it is not, and both need only the same thing. | Decide whether decline should require the invitee to be signed in, as accept does. Recommend yes — the invitee needs an account to accept anyway, so requiring one to decline costs nothing. |
| **FRAUD-12** | **Invitation phone-equivalence.** Invite `70555123`, then try to invite `+96170555123`, ` 70 555 123 ` and `0070555123` into the same salon. | The partial unique index on `(salon_id, phone) WHERE status='pending'` only works because the service normalises to E.164 first. Bypass normalisation and one person can hold five live invitations. Same equivalence FRAUD-09 pinned for deposit payers. | All four collapse to one number; the second attempt returns `INVITATION_EXISTS`. |

#### 1.G Per-artist schedules

| ID | Case | Why it matters | Expected |
|---|---|---|---|
| **SPAM-10** | **Write a rota for another salon's store.** As a member of salon A, `PUT /artists/me/schedule` with a `store_id` belonging to salon B. | `SetMyRota` checks `artist_stores` before writing, so this should be refused — but the check is service-layer, not a constraint, and `artist_schedules` has no salon column to contradict a bad write. | `404 STORE_NOT_FOUND`, indistinguishable from a store that does not exist. No row written — **verify by re-reading the table, not by the status code.** |
| **SPAM-11** | **Rota as an availability oracle.** Set a rota, then have an unauthenticated client read the slots endpoint and infer the artist's personal working pattern across the 90-day horizon. | Availability is necessarily public — it is how booking works. But a 90-day sweep now discloses one identifiable person's weekly schedule and their days off, which is different from disclosing when a shop is open. | Accepted by design, recorded explicitly. Worth a line in the artist-facing terms: setting a rota publishes when you personally work. |
| **EDGE-06** | **Rota amplification.** 90 dates × a rota and an exception per date, at concurrency, measured against the pre-rota baseline. | Step 1.5 adds **two queries per slot request**. EDGE-05 measured 1 ms per date before this; a regression here is paid by every customer on every date they look at. | Within the EDGE-05 envelope. Regression here is a performance defect, not a security one, but it is measured here because the harness already exists. |
| **INJ-07** | **Time values through the rota writer.** `PUT /artists/me/schedule` with `start_time` of `"25:00"`, `"09:00'; DROP TABLE"`, `""`, `null`, and a 10,000-character string. | The handler casts to `::time` in SQL with a parameter, so injection should be impossible — but the service compares times as **strings** (`d.StartTime >= d.EndTime`) before the cast, and a value that sorts correctly as a string but is invalid as a time reaches Postgres. | `422` or `400` with a field error. Never a 500, and never a partially written week. |

---

#### 1.I Section 3.4d executed — 2026-09-22

`make verify-security-salon` · **8 pass, 0 fail, 3 undecided, 1 informational.**

Three defects were found and fixed in the same run; one of them had been
concealing another.

| ID | Outcome |
|---|---|
| **DATA-03** | **FOUND, then FOUND AGAIN, then FIXED.** The first run reported PASS — and it was wrong. `salon_invitations` stores only a SHA-256, but `QueueSalonInvitation` writes the full link into `notifications.payload`, so the hash design is defeated by the row beside it. The case passed only because **no notification row was ever written**: the INSERT failed on a missing `::text` cast inside `jsonb_build_object` (Postgres 42P18), and `Invite` discarded the error with `_ =`. Queueing had therefore never once succeeded, and nothing said so. Both fixed. The token still has to be queued while the invitation is live — the message *is* the link — but it is now redacted on **all four** terminal transitions, including lazy expiry, which happens on a read and is the one most easily missed. Exposure is bounded to the invitation's own lifetime, which is the exposure the recipient's message thread already carries. |
| **SPAM-08** | **FOUND AND FIXED.** 30 invitations to 30 distinct numbers, no limit. Now capped at 20 live and 50 per day, checked *after* the duplicate refusal so an owner re-sending to one person is never told they have hit a limit. |
| **INJ-07** | **FOUND AND FIXED.** `25:00`, a SQL-injection string and a 5,000-character string all reached Postgres as `$n::time` and returned **500**. No injection succeeded — the table was intact and no partial week was written — but a 500 is the wrong contract twice over: the caller cannot tell a bad field from a broken server, and development mode returns a stack trace. The service compared times lexicographically, which is meaningless for anything that is not HH:MM. Format is now validated before the cast; all four values return 400 or 422. |

**Three results are UNDECIDED, and must not be read as passes.** Each measured
a real behaviour that needs a decision rather than a fix:

- **FRAUD-11** — an unauthenticated caller holding a link killed the invitation
  (204). Reading it is harmless, burning it is not, and both need only the
  link. Accept already requires an account; decline arguably should too.
- **AUTH-16** — an invitation addressed to one number was redeemed by an
  unrelated account. The token *is* the credential, so this may be intended,
  but it is a phishing primitive. The owner can at least see who joined.
- **AUTH-14** — a removed member's access token still reads the menu, roster
  and payment reference for up to 15 minutes. **No writes get through.**
  `RevokeAllForUser` revokes refresh tokens only, and the access token is
  self-contained.

**DATA-03b is informational and still open:** the token travels in the URL
**path** of `GET /invitations/:token` and `POST …/accept`, so any access log
or reverse-proxy log captures a working credential. Check before production.

**A note on method.** Two cases in this run initially reported the opposite of
the truth, both because a zero was read as a guard: DATA-03 passed on an empty
table, and an earlier probe read "0 slots" from a store with no opening hours.
Establishing the condition explicitly — a live subscription, a written
notification row — is what produced the real answers.

#### 1.H Found by execution, 2026-09-21

| ID | Case | Result |
|---|---|---|
| **FRAUD-13** | **An artist admin review has refused can still take a deposit.** `artists.status` is filtered by `internal/discovery`, by handle lookup, by UUID lookup and by the share preview — and by **nothing on the booking path**. `checkArtistAcceptsNewBookings` read the *subscription* and not the status. Measured with a live subscription: `pending` → HELD, **`rejected` → HELD**, `active` → HELD. | **FOUND AND FIXED** 2026-09-21 by E2E suite 23 case 23.7e. The gate now sits in `checkArtistAcceptsNewBookings`, which both the slots path and the hold path already call, and returns the same `ARTIST_NOT_ACCEPTING_BOOKINGS` as the billing branch so an artist's standing with the platform is not disclosed to anyone holding their ID. Re-measured live: pending refused, rejected refused, active held. |

**Why this one mattered.** Reaching an unapproved artist through the funnel
was already impossible — every lookup filters on status. But *unreachable*
was doing the work of *forbidden*, and an artist always knows their own ID.
On a platform where the customer pays a deposit out of band to a number the
artist controls, admin rejection is the only thing between a fraudulent
artist and real money, and it was enforced where people browse rather than
where they pay.

**Twice during this investigation a coincidental zero read as a guard.** The
first conclusion — "pending artists are refused, 0 slots offered" — came from
a store that had no business hours. The second — "the hold is refused, 403" —
came from a subscription my own cleanup had deleted. Neither had anything to
do with approval status. Establishing the condition explicitly (live
subscription, each status in turn) is what produced the real answer, and it
was the opposite of both.

**Not covered here, deliberately:** seat billing. `plans.seat_price` and
`plans.included_seats` are populated and feed no arithmetic, and
`InviteRequest.AcceptSeatCharge` is accepted and ignored. There is no
enforcement to attack yet. These cases get written with Phase 3.

### 3.4c How to run these so the results mean something

Added 2026-09-21. Every rule below exists because a test in this project
**reported a pass it had not earned**, and each one cost real time.

**1. Assert identity before measuring anything.** A UI sweep once reported
four pages clean; three of the URLs did not exist and it had measured the
404 page. A render check is not an identity check. Before recording any
result, assert you are on the thing you think you are.

**2. Verify the effect, never the status field.** `notifications.status =
'sent'` meant "Twilio returned 2xx". The real delivery rate was zero, for
six weeks, while every query in the system reported success. If a test can
ask the provider, the database or the row itself, it must — the
application's own report of its own success is not evidence.

**3. A test that cannot run must SKIP, loudly.** The same-day-notice check
once "passed" at 23:51 against zero slots. It proved nothing and looked
green. Two others silently never ran for weeks because they depended on the
test day happening to contain an early-bird slot. **Create the condition you
need rather than waiting for it** — set the cutoff, insert the exception,
then restore.

**4. Discover fixtures; never hardcode them.** A run once picked a service
that happened to be `is_active = false` and read the correct empty result as
a defect. Ask the database for something that satisfies your preconditions.

**5. Surface the errors of your own tooling.** A SQL helper that swallowed
stderr turned a failed INSERT into a non-existent table into a confident
false finding. If your harness cannot see its own failures it will report
the system's behaviour as its own bugs, and vice versa.

**6. Use the engine the user uses.** Three defects that reached the launch
artist were invisible in desktop Chrome: hour labels clipped to a single
letter, time values cut mid-character, an iOS-only banner with an
undersized control. Run UI security checks in **WebKit at 390px**, and set
an iPhone user-agent where a surface renders conditionally.

**7. Prove the negative as well as the positive.** A guard that refuses
everything passes every "it refuses" test. Each of AUTH-11, FRAUD-08 and
FRAUD-10 has an explicit *allowed* case for this reason — the boot guard
must still boot on localhost, the refund must still go through once
contact is confirmed, the artist in good standing must still be sold.

**8. Mutation-test the guard itself.** After adding a check, break the thing
it protects and confirm it fails. The column/scanner drift test was verified
by adding a column to the SELECT and watching it report
`34 columns but scanBooking scans 33 fields`. An assertion never seen to
fail is an assumption.

**9. Restore what you touched, in a `finally`.** Suites here mutate
subscriptions, business hours and store settings. One left a
deposit-exceeds-price row behind that was briefly mistaken for corrupt
production data.

**10. Distinguish defect, gap and decision.** "It does not do what it
should", "no requirement covers this", and "it behaves this way on purpose,
recorded here" call for three different responses. Filing the third as the
first is how a plan loses its reader.

### 3.5 Pass / fail criteria

A test **fails** if the expected secure behaviour is not observed, *or* if
the behaviour cannot be conclusively determined. Ambiguity is a fail —
"probably fine" is not a result.

| Severity | Definition | Fix SLA | Release gate |
|---|---|---|---|
| **Critical** | Full account takeover, cross-tenant data access, admin escalation, direct financial loss, or mass PII exfiltration. | **24 hours.** Halt release. | **Blocks launch.** |
| **High** | Significant data exposure, auth weakness needing a precondition, or a reliably exploitable business-logic flaw. | 7 days. | Blocks launch unless explicitly accepted in writing, with owner and date. |
| **Medium** | Limited exposure, needs an unlikely precondition, or defence-in-depth absent. | 30 days. | Does not block; must be tracked. |
| **Low** | Minimal impact, informational, or theoretical. | Backlog. | Does not block. |

**Reporting.** Every finding records: Test ID, severity with CVSS vector,
reproduction steps precise enough to re-run cold, evidence (request/response
or screenshot), impact in business terms, and remediation guidance.
Criticals are reported **immediately on discovery**, not at end of cycle.

**Re-test.** Every fix is re-tested against the original procedure and the
result recorded against the same Test ID. A fix is not closed on assertion.

### 3.6 Known-failing before testing starts

**Superseded 2026-09-05 by the first execution — see §5.** Items 1 and 2 were
predictions; both were confirmed and both are now fixed. Kept as written so the
predictions can be judged against what actually happened.

1. ~~**CLIENT-04 will fail.**~~ Confirmed, then **fixed** —
   `internal/middleware/secheaders.go`.
2. ~~**INJ-04 will likely fail** on excess precision.~~ Confirmed, and the real
   defect was **worse than predicted** — see §5.1. **Fixed** —
   `internal/pkg/money`.
3. **EDGE-01 has no real mitigation** to find. There is no CDN or WAF. The
   test measures the failure point rather than proving a defence. *Still true.*
4. **AUTH-08 is the single highest-value test in this document.** *Executed
   2026-09-05: **passes**. The check is an exact `== "development"` match and
   fails closed, and `APP_ENV` is in `requiredEnvVars` so the server will not
   boot without it. The same pattern holds in `hash.go` and
   `middleware/register.go`. Residual risk is operational, not code.*

---

## 5. First execution — 2026-09-05

Run against the local dev stack with the permanent roster. **42 checks passed,
4 defects found, all 4 fixed the same day.** The harness was hand-written
Python against the live API — no Burp, SQLmap or K6 available — and every
finding was verified against the database rather than inferred from a status
code.

### 5.1 What was found

| Test | Severity | Defect | Fix |
|---|---|---|---|
| **INJ-04** | **High** | `PATCH /artists/salon/services/:id` and onboarding's first-service INSERT passed `price` to SQL **unvalidated**. Postgres accepts `'NaN'::numeric`: the write committed while the response was a 500, every later read of that row then 500'd, `SUM(price)` returned `NaN`, and the row could not be repaired through the API. Create validated; update did not. | `internal/pkg/money` — one whitelist parser at all 8 money sites |
| **AUTH-02** | Medium | Foreign object → 403, nonexistent → 404 on bookings, stores and services: an oracle for enumerating live IDs. | One `errXNotFound()` per domain, shared by both branches — 27 sites |
| — | Low | `GET /artists/:id/services` (unauthenticated) returned `buffer_min`, `salon_id`, `is_active`; migration 033 says the buffer is never customer-facing. | `artist.PublicServiceResponse` |
| — | Low | `GET /bookings/slots` with a nonexistent `store_id` → **500** (unmapped pgx no-rows). Found while verifying the AUTH-02 fix. | `booking.ErrStoreNotFound` → 404 |

### 5.2 What passed

AUTH-01 (no foreign data from any endpoint tested), AUTH-03, AUTH-04 (7 forged
JWT variants including `alg:none` and HS/RS confusion — all 401), AUTH-08,
AUTH-09, INJ-01, INJ-02, INJ-03, CLIENT-02, CLIENT-03, EDGE-04, **FRAUD-01**
(8 concurrent holds on one slot → exactly one won, seven 409s: the GIST
exclusion constraint doing precisely what migration 001 promised),
**FRAUD-02** (client-supplied `price: 0.01` ignored; the server re-read 45.00),
SPAM-05.

### 5.3 Not executed — do not read these as passes

- **AUTH-05 / AUTH-06 — inconclusive, not failing.** The refresh cookie sets
  `Secure` unconditionally. Browsers exempt `localhost`; a scripted HTTP client
  does not, so the cookie is never returned and every refresh answers 401.
  **These cannot be tested over plain HTTP at all.** The flags themselves
  (`HttpOnly; Secure; SameSite=Strict`) are correct, and are what makes
  CLIENT-02 pass.
- **AUTH-07 / SPAM-02** — would send real messages through the live Twilio
  account. §3.3 already says to stub the sender first.
- **EDGE-01 / EDGE-02** — load testing needs the written authorisation §3.3
  requires.
- **FRAUD-04 / FRAUD-05** — need a stock-limited product and a pending invoice
  as fixtures; neither exists in the roster.
- **FRAUD-06** — a process control over admin behaviour, not a testable
  endpoint.
- **CLIENT-05** — log review; not swept.

### 5.4 For the next run

1. Run it over **HTTPS**, or AUTH-05/06 stay untestable — currently the largest
   genuine gap in this document's coverage.
2. Stub Twilio; AUTH-07 and SPAM-02 then become runnable.
3. Seed a stock-limited product and a pending invoice for FRAUD-04/05.
4. Treat INJ-04 and AUTH-02 as **regression** tests now, not discoveries. Both
   have unit-level guards: `internal/pkg/money`'s rejection table and
   `TestOwnershipAndAbsence_AreIndistinguishable`.

---

## 6. Second execution — 2026-09-18

Run against the live local stack (Postgres healthy, API :3000, customer-pwa
:4200, artist-dashboard :4300) using the permanent `mkup1`-`mkup4` roster.

### 6.1 Result

**21 security checks executed, 21 passed, 1 advisory observation.**
No security defect found.

Executed and passing: AUTH-01, AUTH-02, AUTH-03 (+ role-injection at
registration), AUTH-04, AUTH-07, INJ-01 (+ path-UUID), INJ-02, INJ-03,
INJ-04, CLIENT-03, CLIENT-04, EDGE-03, FRAUD-01, FRAUD-03, SPAM-02, SPAM-05.

Notable confirmations:

- **FRAUD-01** — eight concurrent guest holds on one slot; at most one won.
  The GIST exclusion constraint holds under real concurrency.
- **FRAUD-03** — `CreateOrderRequest` has **no total field at all**, so the
  total-tampering attack surface does not exist. Negative and absurd
  quantities are both rejected.
- **INJ-03** — `{{7*7}}`, `${7*7}`, `</title><script>` and an `onerror`
  attribute breakout were all escaped in the share preview.
- **Deposit ceiling** (new, migration 038) — a deposit above the price is
  rejected live. This was the fix made during the trust & safety review.

### 6.2 Advisory — not a vulnerability

The OTP request limiter engages correctly after exactly 3 requests per phone,
but returns **HTTP 400 with `RATE_LIMITED`** rather than **429**. The control
works; the status code is wrong for it. Standard client retry logic, proxies
and monitoring key on 429, and a 400 is indistinguishable from a validation
failure. Worth changing, but it is a correctness-of-signal issue and not a
hole.

A related cosmetic note: both PWAs fire a `401` on `auth/refresh` on every
cold signed-out load. Expected behaviour, but it makes real 401s harder to
spot in logs.

### 6.3 Not executed — do not read these as passes

AUTH-05, AUTH-06 (token replay / refresh reuse — still the largest genuine
coverage gap, and still blocked on a HTTPS origin), AUTH-08 (needs the API
restarted under other `APP_ENV` values), AUTH-09, AUTH-10, CLIENT-01 (only
partially covered via INJ-03), CLIENT-02, CLIENT-05, EDGE-01 (requires
written authorisation), EDGE-02, EDGE-04, FRAUD-04, FRAUD-05, FRAUD-06,
FRAUD-07, SPAM-01, SPAM-03, SPAM-04.

### 6.4 Note on method

The first UI pass reported six artist-dashboard screens as rendering when the
session had not finished establishing — every one was actually the login page,
which is long enough and error-free enough to satisfy a naive "page has
content" assertion. It was caught by opening a screenshot, not by the
assertion. The re-run waits on the URL and asserts the expected `h1`; all 14
screens then genuinely passed at both 1440×900 and 390×844.

Worth carrying forward: **a render check that does not assert identity will
happily pass on the login page.**

---

## 4. Recommended sequence

1. **Before any testing:** stub Twilio; take a DB snapshot; confirm staging's
   `APP_ENV` matches production exactly; obtain written load-test authorisation.
2. **Pass 1 — authorisation (highest yield).** AUTH-01 through AUTH-10. This
   is where prior passes found real bugs, and four new domains have shipped
   since the last one.
3. **Pass 2 — business logic and money.** FRAUD-01 to FRAUD-07. Races need
   Turbo Intruder, not sequential requests.
4. **Pass 3 — injection and client-side.** INJ-\*, CLIENT-\*.
5. **Pass 4 — abuse and scraping.** SPAM-\*. Requires the Twilio stub.
6. **Pass 5 — load.** EDGE-01, EDGE-02. Last, because it is disruptive.
7. **Deferred until infrastructure exists:** A4, A5, A7, D5. Re-open this
   document at that point rather than testing them prematurely.

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · August 31, 2026*
