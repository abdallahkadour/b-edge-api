# Workflow 1 · Customer Registration & Profile Onboarding

**Date:** 2026-09-21 · **Status:** verified against the running stack
**Panel:** Software Architect · Systems & Use Case Analyst · UML Specialist

> **Scope correction, up front.** Three of the four workflows in the brief
> assume a marketplace this is not. B-Edge has **no payment gateway**, **no
> payouts**, **no staff/resource model**, and **no separate salon
> registration**. Those are recorded in §6 as *Unimplemented / Unknown in
> Code* rather than documented as if they existed. This file covers
> Workflow 1 only.

---

## 1. Process Specification

### Actors

| | Actor | Role |
|---|---|---|
| Primary | **Guest / Customer** | Owns a phone number; wants to book |
| Secondary | **Twilio (WhatsApp)** | Delivers the one-time code |
| Secondary | **Rate limiter** | Caps requests per phone |
| — | *Admin* | **No role.** Customers are never approved or reviewed |

### Pre-conditions & assumptions

- The customer has a phone number reachable on WhatsApp.
- **No prior account is required.** There is no customer `register`
  endpoint — `POST /auth/register` is artist-only.
- Numbers are normalised to **E.164** before anything else happens.

### Trigger

`POST /api/v1/customer-auth/request-otp` with a phone number — either from
the login screen, or implicitly at the end of the guest booking funnel,
which creates a customer record without any login at all (see §2).

### Happy path

1. Customer submits a phone number.
2. Service **normalises to E.164** (`phone.Parse`, default ISO `LB`).
3. Rate-limit check, keyed on the **normalised** number.
4. Eligibility check — **read-only**.
5. A code is generated and a `notifications` row is queued.
6. The notification worker hands it to Twilio.
7. Customer submits phone + code to `verify-otp`.
8. **The `users` row is created here**, on first successful verification.
9. Access + refresh tokens are issued; refresh lasts **7 days**.

### Alternative flows

| | Flow | Behaviour in code |
|---|---|---|
| A1 | **Guest booking creates the account** | `SubmitGuestBooking` calls `CreateGuestUser` with a normalised phone. A customer can exist having never logged in. |
| A2 | Returning customer | `verify-otp` finds the existing row; no duplicate is created |
| A3 | Development bypass | Code `326321` skips verification entirely — see §5, this is a finding |

### Exception flows

| | Condition | Response |
|---|---|---|
| E1 | Unparseable phone | `422 VALIDATION_ERROR` |
| E2 | Too many requests for one number | `429 RATE_LIMITED` |
| E3 | Wrong or unknown code | `OTP_NOT_FOUND` |
| E4 | Expired / already-used code | `OTP_NOT_FOUND` |
| E5 | Invalid refresh token | `401 TOKEN_INVALID` |
| E6 | **WhatsApp never arrives** | **No error at all.** The API returns success; the code is queued and dies silently. This is the live state — see §5 |

### Post-conditions

- **Success:** a `users` row with role `customer` and an E.164 phone; tokens
  issued.
- **Failure before verify:** *no row is created.* Deliberate — see §5.

---

## 2. Use Case Model

```mermaid
flowchart LR
  Customer(["Customer"])
  Twilio(["Twilio / WhatsApp"])
  Worker(["Notification worker"])

  subgraph B["B-Edge system boundary"]
    UC1["Request a login code"]
    UC2["Verify the code"]
    UC3["Refresh a session"]
    UC4["Book as a guest"]
    N1["Normalise phone to E.164"]
    N2["Rate-limit by phone"]
    N3["Create customer record"]
    N4["Queue notification"]
    X1["Dev bypass code"]
  end

  Customer --> UC1
  Customer --> UC2
  Customer --> UC3
  Customer --> UC4

  UC1 -. "include" .-> N1
  UC1 -. "include" .-> N2
  UC1 -. "include" .-> N4
  UC2 -. "include" .-> N1
  UC2 -. "include" .-> N3
  UC4 -. "include" .-> N3
  X1 -. "extend" .-> UC2

  N4 --> Worker
  Worker --> Twilio
```

**Note the shape.** `Create customer record` is included by **both**
`Verify the code` and `Book as a guest`. Those are the only two ways a
customer comes into existence, and neither is a registration form.

---

## 3. Formal UML

### 3.1 Sequence — request and verify

```mermaid
sequenceDiagram
    actor C as Customer
    participant API as Go API
    participant DB as PostgreSQL
    participant W as Notification worker
    participant T as Twilio

    C->>API: POST /customer-auth/request-otp {phone}
    API->>API: phone.Parse -> E.164
    Note over API: normalised BEFORE the rate-limit lookup,<br/>or one number has several buckets
    API->>DB: rate-limit check (by normalised phone)
    alt over the cap
        API-->>C: 429 RATE_LIMITED
    else allowed
        API->>DB: eligibility check (READ ONLY - no row created)
        API->>DB: INSERT otp + INSERT notification (pending)
        API-->>C: 200 "Verification code sent"
    end

    W->>DB: claim pending notification
    W->>T: POST /Messages {To, From, Body}
    T-->>W: 201 {sid}
    W->>DB: status='sent', provider_message_id=sid
    Note over W,DB: 'sent' means ACCEPTED, not delivered

    W->>T: GET /Messages/{sid} (reconciler, every 2 min)
    T-->>W: {status: undelivered, error_code: 63024}
    W->>DB: delivery_status='undelivered'
    Note over W: WARN "NOTHING is reaching recipients"

    C->>API: POST /customer-auth/verify-otp {phone, code}
    API->>DB: look up unexpired, unused otp
    alt no match
        API-->>C: OTP_NOT_FOUND
    else match
        API->>DB: find-or-create users row (role=customer)
        API-->>C: 200 {access_token, refresh_token}
    end
```

### 3.2 State machine — the customer identity

```mermaid
stateDiagram-v2
    [*] --> Anonymous

    Anonymous --> CodeRequested: request-otp (no row yet)
    CodeRequested --> Anonymous: expired / rate-limited
    CodeRequested --> Registered: verify-otp OK

    Anonymous --> Registered: guest booking submitted
    note right of Registered
        The users row is created at
        exactly these two moments,
        and nowhere else.
    end note

    Registered --> Authenticated: tokens issued
    Authenticated --> Registered: refresh expires (7 days)
    Authenticated --> Registered: logout
    Registered --> [*]: soft delete (deleted_at)
```

---

## 4. Code vs. Logic Gap Analysis

### Discrepancies

- **There is no customer profile onboarding.** The brief's "Profile
  Onboarding" does not exist: no avatar, no preferences, no profile
  completion step. A customer is a name and a phone number. Not a defect —
  but the workflow named in the brief is *half* implemented, and the missing
  half was never specified.
- **No email anywhere in the customer path.** Identity is the phone number.

### Concurrency

- Two simultaneous `verify-otp` calls for a new number could race on the
  insert. `CreateGuestUser` is a find-or-create; `users.phone` carries a
  **UNIQUE constraint**, so the loser gets a constraint violation rather
  than a duplicate. **Verified indirectly** — `users_phone_unique` fired
  during testing on 2026-09-19. Not load-tested at the auth path.

### Failure & rollback

- **The OTP row and the notification row are written, then Twilio is called
  by a separate worker.** This is correct: no external call sits inside the
  transaction, and a Twilio outage leaves a queued row rather than a
  half-registered customer.
- **The reverse risk is real and live:** the API answers *"Verification code
  sent"* whether or not anything is ever delivered. Since delivery is
  currently **0%**, every customer login attempt ends in a message that
  never arrives and an API that reported success.

### Validation & security

- Phone normalisation happens **before** the rate-limit lookup, so the
  per-number cap cannot be bypassed by reformatting. Correct, and commented
  as such.
- `request-otp` **deliberately creates no user row** — an unauthenticated
  endpoint that created accounts would let anyone pre-register strangers'
  numbers. Fixed in an August 2026 audit; the comment records it.

---

## 5. Findings

### F1 — Development OTP bypass was live on a public URL · **CRITICAL, fixed**

`devBypassOTPCode = "326321"` lets any code-holder authenticate as **any
phone number**, gated on `APP_ENV == "development"`.

The gate is written correctly and fails closed: unset, empty or misspelled
all leave it shut. The failure was not the code — it was that a
**development-mode API was being served over a public tunnel** to the launch
artist.

**Verified exploitable**, against the public customer URL on 2026-09-21:

```
POST /api/v1/customer-auth/verify-otp {"phone":"70999888","code":"326321"}
-> 200, access token issued for a number that never requested a code
```

`APP_ENV` was set to `production` and the same request now returns
`OTP_NOT_FOUND`. The account the test created was deleted.

**This also closed a second exposure:** `EnableStackTrace` is gated on the
same variable, so the public API had been returning Go stack traces on panic.

**Residual risk:** `.env` is gitignored and machine-local. Nothing prevents
the next person running a public tunnel from a development configuration.
The durable fix is that a publicly-reachable deployment should not be reading
a developer's `.env` at all.

### F2 — Success is reported for a message that never arrives · **live**

`request-otp` returns *"Verification code sent"* on queueing. Delivery is
**0% and has always been** (91 notifications, none delivered). The customer
is told to check WhatsApp for a code that does not exist, and there is no
state in which the API tells them otherwise. Blocked on Meta business
verification, not on code.

### F3 — No customer profile onboarding exists · **gap, not defect**

Named in the brief, absent from the code and from any spec.

---

## 6. Unimplemented / Unknown in Code

Flagged rather than assumed, per instruction.

| Brief's workflow | Reality |
|---|---|
| **Salon Registration & Verification** | No such flow. A `salons` row is created inside artist **onboarding** (`onboarding/repository.go:63`). There is no salon-level registration, no verification queue for a business entity, and **no staff model at all** — zero tables matching staff/employee/resource. |
| **MUA banking / payout setup** | Does not exist. The `payout` package is **not payouts** — its own doc comment says it "owns where a salon's money should be sent", i.e. `salon_payment_methods`, the OMT/Whish account a *customer* sends a deposit to. **B-Edge holds no money**: no ledger, no settlement, no commission, no payout table. |
| **Payment gateway (Stripe et al.)** | No integration. Deposits move customer-to-artist out of band. Any diagram showing a payment gateway actor would be fiction. |
| **Cancellation insurance, promo extensions** | Promo codes exist (`internal/promo`); cancellation insurance does not. |

---

## 7. Tri-Expert Panel Verdict

### Systems & Use Case Analyst

Domain model is **complete for what it claims** and narrower than the brief
assumes. The two-path identity creation — verify-otp *or* guest booking — is
unusual and correct for a market where a customer should not need an account
to book. The genuine gap is **E6**: there is no flow, anywhere, for "the code
never arrived", and that is the single most likely thing to happen today.
**Missing edge case:** no resend, no fallback channel, no way for a customer
to report non-delivery.

### UML Specialist

Diagrams above are syntactically valid Mermaid and render. Two notation
compromises, stated rather than hidden: Mermaid has no native UML use-case
notation, so `<<include>>` / `<<extend>>` are expressed as labelled dotted
edges in a `flowchart`, which is the conventional substitute. The state
machine models the **customer identity**, not the session — modelling both in
one diagram would have required a concurrent region that Mermaid cannot
express faithfully.

### Software Architect — reliability **6 / 10**

Sound where it has been thought about; the deductions are for what surrounds
it.

**Strengths.** Normalisation before rate-limiting is a genuine security
detail, not a formality. Declining to create a row on an unauthenticated
endpoint is exactly right and was a deliberate fix. No external call inside
a transaction. Fail-closed environment gating.

**Critical failure points.**
1. *(was)* A universal login bypass reachable from the internet — F1, now
   closed, but it reached that state because nothing ties "publicly
   reachable" to "not development mode".
2. A 100% delivery failure that the API reports as success — F2.
3. `users.phone` UNIQUE is the only thing serialising concurrent
   registration. It holds, but by constraint violation rather than design.

**Recommendations, in order.**
1. Make the environment gate structural, not conventional: refuse to boot
   with `APP_ENV=development` when a public base URL is configured.
2. Give `request-otp` a truthful failure path — surface the reconciler's
   `delivery_status` so the UI can say "we could not reach your WhatsApp".
3. Add an explicit find-or-create on `users.phone` rather than relying on
   the unique index to arbitrate.
