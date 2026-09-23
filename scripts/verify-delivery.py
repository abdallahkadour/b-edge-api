#!/usr/bin/env python3
"""Proves that a notification can actually reach a handset.

WHY THIS EXISTS

M1 on the reliability scorecard is the single most important number B-Edge
has, and on 2026-09-23 it read:

    92 queued · 23 sent · 69 dead · 0 delivered

Zero. Not one message has ever been confirmed delivered in the entire history
of the system, so the `delivered` branch of the reconciler has never executed.
Customer login is an OTP over WhatsApp - until one message lands, the product
does not work for a customer at all, and every feature downstream of delivery
is theory.

THIS SCRIPT IS EXPECTED TO FAIL TODAY. That is the point.

A check written after a fix, which has only ever been observed passing, is not
evidence of anything - it may be asserting nothing at all. Seven of this
project's false results came from a check that had never been seen to fail.
So this is written and committed NOW, while it still fails, and the failure is
recorded. The day TWILIO_SMS_FROM is provisioned or Meta verification clears,
this same script passing is a real measurement.

WHAT IT DOES

  1. Queues one notification through the real table the worker reads.
  2. Waits for the worker to pick it up and drive it to a terminal state.
  3. Reports which transport carried it - WhatsApp or the SMS fallback.
  4. Exits non-zero unless the row reaches `delivered`.

Step 3 matters on its own: the SMS fallback was written on 2026-09-16 and has
never executed, because TWILIO_SMS_FROM is unset. Untested fallback code is
not a fallback.

USAGE

    make verify-delivery PHONE=+9617xxxxxxx

The number must be one you can physically check. The script tells you what the
provider reported; only you can confirm the handset actually buzzed.
"""
import json
import os
import subprocess
import sys
import time
import uuid

PG = ["docker", "exec", "-i", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]
TERMINAL = {"delivered", "dead", "failed"}
TIMEOUT_S = 180
POLL_S = 5


def sql(query):
    """Runs SQL, raising on error rather than returning an empty string.

    psql exits 0 on a failed statement unless ON_ERROR_STOP is set, so a typo'd
    column name returns "" - which reads exactly like a legitimate zero. That
    mistake produced a false 'residual: 0' in this project as recently as
    today.
    """
    p = subprocess.run(PG + ["-v", "ON_ERROR_STOP=1", "-c", query],
                       capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError(f"SQL failed: {p.stderr.strip()[:300]}")
    return p.stdout.strip()


def env_flag(key):
    """Reads a variable from .env without printing its value.

    Tokens and provider identifiers must never be echoed - the security plan
    requires auth material to be redacted whenever it is surfaced.
    """
    try:
        with open(".env") as fh:
            for line in fh:
                if line.startswith(key + "="):
                    return bool(line.split("=", 1)[1].strip())
    except FileNotFoundError:
        pass
    return bool(os.getenv(key))


def main():
    phone = os.getenv("PHONE") or (sys.argv[1] if len(sys.argv) > 1 else "")
    if not phone.startswith("+"):
        print("  usage: make verify-delivery PHONE=+9617xxxxxxx")
        print("  the number must be E.164 and one you can physically check")
        return 2

    # A FABRICATED NUMBER MAKES THIS CHECK USELESS, and worse, misleading.
    # Twilio answers a nonexistent recipient with 63024 (invalid recipient),
    # which looks like a delivery failure but says nothing about whether the
    # pipeline works - it is the provider correctly refusing to message a
    # number that does not exist. Only a real handset can distinguish "the
    # pipeline is blocked" from "that number isn't real".
    if phone.startswith(("+9617600", "+9617000")):
        print(f"  {phone} looks like a placeholder from the test roster.")
        print("  Use a real handset - a fake number returns 63024 (invalid")
        print("  recipient), which proves nothing about the pipeline.")
        return 2

    print("\n  ── delivery verification ──\n")

    # Report the gates BEFORE sending, so a failure below can be attributed
    # rather than guessed at.
    wa = env_flag("TWILIO_WHATSAPP_FROM")
    sms = env_flag("TWILIO_SMS_FROM")
    print(f"  TWILIO_WHATSAPP_FROM  {'set' if wa else 'UNSET'}")
    print(f"  TWILIO_SMS_FROM       {'set' if sms else 'UNSET  <- blocks the fallback'}")
    if not wa and not sms:
        print("\n  Neither transport is configured. Nothing can be delivered.")
        print("  This is procurement, not engineering - see W0.1 of the reliability plan.\n")
        return 1

    # Historical context, so the result is read against what came before.
    hist = sql("""SELECT count(*) FILTER (WHERE status='delivered'),
                         count(*) FILTER (WHERE status='sent'),
                         count(*) FILTER (WHERE status='dead'),
                         count(*) FROM notifications""")
    d, s, dead, total = [int(x) for x in hist.split("|")]
    print(f"\n  before this run: {total} queued, {s} sent, {dead} dead, {d} delivered")

    template = f"delivery_probe_{uuid.uuid4().hex[:8]}"
    body = "B-Edge delivery check. If you are reading this, the pipeline works."

    # $1::text, not $1 - Postgres cannot infer a parameter's type inside
    # jsonb_build_object and answers 42P18. That cost a debugging session once.
    sql(f"""INSERT INTO notifications (template_name, channel, payload, recipient_phone, status)
            VALUES ('{template}', 'whatsapp',
                    jsonb_build_object('message', '{body}'::text),
                    '{phone}', 'pending')""")
    print(f"  queued {template} -> {phone[:5]}****{phone[-2:]}")

    deadline = time.time() + TIMEOUT_S
    status = last = None
    while time.time() < deadline:
        row = sql(f"""SELECT status, channel, attempts, coalesce(error_message,''),
                             coalesce(delivery_status,'')
                        FROM notifications WHERE template_name='{template}'""")
        if not row:
            raise RuntimeError("the probe row vanished")
        status, channel, attempts, errmsg, delivery = row.split("|")
        if status != last:
            print(f"  [{int(time.time() - (deadline - TIMEOUT_S)):>3}s] {status:<10} "
                  f"via {channel:<9} attempts={attempts}"
                  + (f" provider={delivery}" if delivery else ""))
            last = status
        if status in TERMINAL:
            break
        time.sleep(POLL_S)

    row = sql(f"""SELECT status, channel, coalesce(error_message,''),
                         coalesce(delivery_status,'')
                    FROM notifications WHERE template_name='{template}'""")
    status, channel, errmsg, delivery = row.split("|")

    print()
    if status == "delivered":
        print(f"  PASS  delivered via {channel}")
        print(f"        provider delivery_status: {delivery or 'n/a'}")
        print("\n  This is the first time this check has passed. M1 is off zero.")
        print("  Confirm the handset actually received it before recording the metric.\n")
        return 0

    print(f"  FAIL  terminal status '{status}' via {channel}, never delivered")
    if errmsg:
        print(f"        provider said: {errmsg[:200]}")
    if "63024" in errmsg:
        print("\n        63024 is INVALID RECIPIENT, not the Meta block.")
        print("        That number is not a WhatsApp user. Re-run with a real one.")
    if "63051" in errmsg or "63016" in errmsg:
        print("\n        This is the business-verification block (Meta ticket 63051).")
    if status == "sent":
        print("\n        'sent' means Twilio ACCEPTED it, not that it arrived.")
        print("        The reconciler never saw a delivery receipt.")
    print("\n  Expected until WhatsApp business verification clears (Meta ticket")
    print("  63051) or TWILIO_SMS_FROM is provisioned. This failure is the")
    print("  positive control: the check can fail, so a future pass means")
    print("  something. Leave the row for the record.\n")
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # surfaced, never swallowed
        print(f"\n  HARNESS ERROR (not a product result): {exc}\n")
        sys.exit(3)
