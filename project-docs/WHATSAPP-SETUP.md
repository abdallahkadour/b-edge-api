# WhatsApp delivery setup

> Written 2026-08-22. The delivery pipeline is fully built and already
> running in production (`internal/notification/worker.go`, a supervised
> background goroutine started in `cmd/main.go`). It polls the
> `notifications` table and sends every queued message — OTP codes, booking
> confirmations/approvals, review requests, cancellations — via Twilio's
> WhatsApp API. **The only thing missing is a real Twilio account.** Once
> that exists, wiring it in is a 5-minute task: three environment variables,
> a restart, and one live check.

## What "not configured" looks like today

Right now, every message that gets queued sits in the `notifications` table
and is attempted, but `sendWhatsApp` (`internal/notification/worker.go:232`)
immediately returns:

```
twilio not configured (TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_WHATSAPP_FROM required)
```

No customer or artist ever receives anything — not an OTP code, not a
booking confirmation, nothing. This is the current state of every
environment, including whatever is running today.

## Step 1 — Get a Twilio account with WhatsApp access

This step has its own external approval timeline (it routes through Meta
business verification) — **start this well before launch week, not during
it.**

1. Create a Twilio account: https://www.twilio.com/try-twilio
2. From the Twilio Console, request WhatsApp Business API access (Twilio's
   onboarding flow walks through Meta Business verification and WhatsApp
   Sender approval). This is the part that can take real calendar days.
3. Once approved, Twilio issues you an approved WhatsApp `From` number
   (distinct from a normal Twilio phone number).

## Step 2 — Get the three credential values

| Variable | Where to find it |
|---|---|
| `TWILIO_ACCOUNT_SID` | Twilio Console dashboard root — labeled "Account SID" |
| `TWILIO_AUTH_TOKEN` | Same page, directly below the Account SID — click "show" to reveal it |
| `TWILIO_WHATSAPP_FROM` | Console → Messaging → Senders → WhatsApp senders — the approved number from Step 1, in `+<countrycode><number>` format (no `whatsapp:` prefix — the worker adds that itself, see `worker.go:246`) |

## Step 3 — Set them

Add all three to `b-edge-api/.env` (already templated in `.env.example`):

```
TWILIO_ACCOUNT_SID=...
TWILIO_AUTH_TOKEN=...
TWILIO_WHATSAPP_FROM=...
```

Then restart the API process so it picks up the new environment variables.

## Step 4 — Verify it's actually working (don't just assume from a 200)

Matching the verification discipline used throughout this project: check
the real database state, don't trust a success response alone.

1. Trigger any real flow that already enqueues a WhatsApp message — the
   simplest is requesting an OTP:
   ```
   curl -X POST http://localhost:3000/api/v1/customer-auth/request-otp \
     -H "Content-Type: application/json" \
     -d '{"phone":"<a real WhatsApp-enabled number>"}'
   ```
2. Within a few seconds (the worker's poll interval), check the
   notification's actual delivery status — do not just assume the `200` from
   step 1 means it was delivered, that response only means it was queued.
   The real columns (verified against the live schema, not assumed) are
   `status`, `attempts`, and `error_message`:
   ```sql
   SELECT id, template_name, status, attempts, error_message, created_at
   FROM notifications
   ORDER BY created_at DESC
   LIMIT 5;
   ```
   `status` is one of `pending` / `sent` / `failed` / `dead`:
   - `status = 'sent'` with no `error_message` → delivery is genuinely working.
   - `status` still `'pending'` after ~10s, or `error_message` mentions
     "twilio not configured" → the env vars didn't take (check the restart
     happened, check for typos).
   - `status = 'failed'` (will retry) or `'dead'` (gave up after repeated
     failures) with some other `error_message` → a real Twilio-side error
     (unapproved recipient number, template/session-window issue, etc. —
     WhatsApp Business API has specific rules about messaging outside a 24h
     customer session window that are worth reading Twilio's own docs on
     before assuming a code bug).
3. Confirm the message actually arrived on a real phone, not just that the
   DB row says `sent` — the two have been known to diverge on WhatsApp
   Business API specifically because of the session-window rules above.

## Notes for whoever does this

- This only needs doing once per environment (dev/staging/prod each need
  their own `.env`, but the runbook is identical).
- The dev-bypass OTP code (`326321`, see `customerauth/service.go`'s
  `devBypassOTPCode`) continues to work regardless of Twilio being
  configured — it's a separate code path, not something this setup
  interacts with or breaks.
- Once real credentials are in place anywhere logs/notifications are
  monitored, watch the `notifications` table's `status` distribution for a
  while after the first real launch traffic — a sudden spike in `failed`
  rows is the fastest signal something's wrong with the WhatsApp
  integration specifically (as opposed to the app itself).
