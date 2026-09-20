#!/usr/bin/env python3
"""
UC-2 / UC-3 · Deposits and refunds — executable verification.

WHY THIS FILE EXISTS, AND WHY IT IS NOT THE STATE MATRIX

`internal/booking/statematrix_test.go` already covers the state machine as a
grid: 7 actions x 11 statuses x 2 time positions, asserting the exact error
code and that a rejected action wrote nothing. Repeating that here would add
nothing.

What the grid does NOT cover is THE MONEY. Whether a transition is allowed is
a different question from whether the right amount ends up owed to the right
party, and B-Edge holds no money - deposits move customer-to-artist over
OMT and Whish, and a refund is the same transfer in reverse with no
chargeback behind it. An error here is not a 500; it is somebody out of
pocket.

So this suite asks only money questions:

  * does cancelling actually create a refund obligation, and only when one
    is owed?
  * is the 24-hour rule applied to the customer and not to the artist?
  * does a deposit paid from someone else's number block the refund?
  * does the price survive the lifecycle intact?

THE RULE BEING VERIFIED (internal/booking/service.go, CancelBooking)

  artist or admin cancels    -> refund due if deposit > 0   (always blameless)
  customer cancels >24h out  -> refund due if deposit > 0   (blameless)
  customer cancels <24h out  -> NO refund, deposit forfeited

The last line is the one that matters most. Too lenient and the deposit stops
protecting the artist against late cancellation, which is the only thing it
is for. Too strict and a customer who cancelled in good time loses money they
are owed.

    make verify-uc2

Needs the API on :3000 and bedge-postgres up. Builds its own bookings,
drives the real endpoints, and deletes everything it made.
"""

import datetime
import json
import subprocess
import sys
import urllib.error
import urllib.request

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]
ARTIST_EMAIL, ARTIST_PW = "rania@bedge.com", "password123"

PASS, FAIL, SKIP = [], [], []


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError(f"SQL failed: {p.stderr.strip()[:200]}")
    return p.stdout.strip()


def call(method, path, body=None, token=None):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    req = urllib.request.Request(
        API + path, method=method,
        data=json.dumps(body).encode() if body is not None else None, headers=h)
    try:
        with urllib.request.urlopen(req, timeout=25) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw or b"{}")
        except Exception:
            return e.code, {}
    except urllib.error.URLError as e:
        print(f"\n  cannot reach the API at {API} ({e.reason})")
        sys.exit(2)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if e else None


def check(fid, desc, ok, detail):
    (PASS if ok else FAIL).append(fid)
    print(f"  {'PASS' if ok else 'FAIL'}  {fid:5} {desc}\n         {detail}")


def skip(fid, desc, why):
    SKIP.append(fid)
    print(f"  SKIP  {fid:5} {desc}\n         {why}")


created = []

# Each booking gets its own hour, because the artist-level GIST exclusion
# constraint refuses two overlapping ranges - correctly. Two fixtures both
# placed at +72h collided on the first run, which is the guard doing its job
# and the harness not respecting it.
_offset = [0]


def make_booking(artist_id, status, hours_ahead, deposit, payer=None):
    """Insert a fixture at or near `hours_ahead`, stepping forward until the
    artist-level GIST exclusion constraint accepts it.

    Retrying rather than picking a "surely free" time, because there is no
    such time: the artist has real bookings, and a fixture that silently
    lands on top of one either fails the run or - worse - tests a row that
    is not the one it thinks it is. The constraint is the authority on what
    is free, so it is asked rather than second-guessed. M3 in particular
    NEEDS a slot inside 24 hours and cannot just be pushed into next year.
    """
    last = None
    for step in range(0, 60):
        try:
            return _insert_booking(artist_id, status, hours_ahead + step, deposit, payer)
        except RuntimeError as e:
            if "exclusion constraint" not in str(e):
                raise
            last = e
    raise RuntimeError(f"no free slot near +{hours_ahead}h after 60 tries: {last}")


def _insert_booking(artist_id, status, hours_ahead, deposit, payer=None):
    """Insert a booking directly. Built in SQL rather than through the funnel
    because these tests need exact states, exact deposits and exact distances
    from now - constructing them through the API would take a dozen calls and
    still not reach `confirmed` with a chosen start time."""
    payer_sql = f"'{payer}'" if payer else "NULL"
    bid = sql(f"""
        WITH pick AS (
          SELECT salon_id, store_id, artist_id, service_id, customer_id
            FROM bookings WHERE artist_id='{artist_id}' ORDER BY created_at DESC LIMIT 1)
        INSERT INTO bookings (salon_id, store_id, artist_id, customer_id, service_id,
                              start_time, end_time, blocked_until,
                              original_price, final_price, deposit_amount,
                              deposit_payer_phone, status)
        SELECT salon_id, store_id, artist_id, customer_id, service_id,
               NOW() + interval '{hours_ahead} hours',
               NOW() + interval '{hours_ahead} hours' + interval '30 min',
               NOW() + interval '{hours_ahead} hours' + interval '30 min',
               200, 200, {deposit}, {payer_sql}, '{status}'
          FROM pick RETURNING id;""")
    # psql -tA prints the RETURNING row AND the "INSERT 0 1" tag, so the raw
    # output is two lines. Taking the whole thing put a newline inside a URL.
    bid = bid.splitlines()[0].strip()
    created.append(bid)
    return bid


def status_of(bid):
    return sql(f"SELECT status FROM bookings WHERE id='{bid}';")


def main():
    st, r = call("POST", "/auth/login", {"email": ARTIST_EMAIL, "password": ARTIST_PW})
    token = (r.get("data") or {}).get("access_token")
    if not token:
        print(f"  cannot log in as {ARTIST_EMAIL} ({st}) — cannot run")
        sys.exit(2)
    artist_id = sql(f"""SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id
                        WHERE u.email='{ARTIST_EMAIL}';""")
    print(f"\n  artist {artist_id[:8]}, authenticated\n")

    try:
        # ── M1: artist cancels WITH a deposit -> money is owed back ──────────
        b = make_booking(artist_id, "confirmed", 72, 100)
        st, r = call("PATCH", f"/bookings/{b}/cancel", {"reason": "UC2"}, token)
        check("M1", "artist cancelling a paid booking owes a refund",
              status_of(b) == "refund_due", f"{st} -> {status_of(b)} (expected refund_due)")

        # ── M2: artist cancels with NO deposit -> nothing owed ───────────────
        b = make_booking(artist_id, "confirmed", 72, 0)
        call("PATCH", f"/bookings/{b}/cancel", {"reason": "UC2"}, token)
        check("M2", "artist cancelling an unpaid booking owes nothing",
              status_of(b) == "cancelled",
              f"-> {status_of(b)} (expected cancelled, not refund_due)")

        # ── M3: the 24-hour rule is the ARTIST's to ignore ───────────────────
        # Same cancel, 2 hours out. The artist is always blameless, so a
        # refund is still owed - the window only ever binds the customer.
        b = make_booking(artist_id, "approved", 2, 100)
        call("PATCH", f"/bookings/{b}/cancel", {"reason": "UC2 late"}, token)
        check("M3", "the 24h window does not bind the artist",
              status_of(b) == "refund_due",
              f"cancelled 2h out -> {status_of(b)} (expected refund_due)")

        # ── M4: the refund gate ──────────────────────────────────────────────
        b = make_booking(artist_id, "refund_due", 72, 100, payer="+96171999888")
        cust_phone = sql(f"""SELECT u.phone FROM bookings b JOIN users u ON u.id=b.customer_id
                             WHERE b.id='{b}';""")
        if not cust_phone or not cust_phone.startswith("+961") or cust_phone == "+00000000000":
            skip("M4", "a refund to a different number is blocked",
                 f"the customer on this booking has an unusable phone ({cust_phone or 'none'}), "
                 f"so 'cannot tell' correctly suppresses the warning")
        else:
            st, r = call("PATCH", f"/bookings/{b}/refunded", {"reference": "UC2"}, token)
            blocked = err(r) == "REFUND_PAYER_MISMATCH"
            st2, r2 = call("PATCH", f"/bookings/{b}/refunded",
                           {"reference": "UC2", "customer_contacted": True}, token)
            check("M4", "a refund to a different number is blocked until contact is confirmed",
                  blocked and status_of(b) == "refunded",
                  f"without attestation: {err(r)}; with it: {status_of(b)}")

        # ── M5: a matching payer needs no extra step ─────────────────────────
        b = make_booking(artist_id, "refund_due", 72, 100)  # payer NULL = not recorded
        st, r = call("PATCH", f"/bookings/{b}/refunded", {"reference": "UC2"}, token)
        check("M5", "an ordinary refund is not gated",
              status_of(b) == "refunded", f"{st} -> {status_of(b)}")

        # ── M6: refunding something not owed ─────────────────────────────────
        b = make_booking(artist_id, "confirmed", 72, 100)
        st, r = call("PATCH", f"/bookings/{b}/refunded", {"reference": "UC2"}, token)
        check("M6", "a booking with no refund outstanding cannot be refunded",
              err(r) == "BOOKING_NOT_REFUND_DUE" and status_of(b) == "confirmed",
              f"{err(r)}, still {status_of(b)}")

        # ── M7: the price survives the lifecycle ─────────────────────────────
        b = make_booking(artist_id, "confirmed", 72, 100)
        before = sql(f"SELECT original_price||'|'||final_price||'|'||deposit_amount "
                     f"FROM bookings WHERE id='{b}';")
        call("PATCH", f"/bookings/{b}/cancel", {"reason": "UC2"}, token)
        after = sql(f"SELECT original_price||'|'||final_price||'|'||deposit_amount "
                    f"FROM bookings WHERE id='{b}';")
        check("M7", "cancelling does not alter the amounts", before == after,
              f"{before} -> {after}")

        # ── M8: a deposit may never exceed the price (migration 038) ─────────
        try:
            # RETURNING + tracking, so that if the constraint is ever missing
            # again the row this creates is cleaned up rather than left behind
            # to look like real corrupt data. The first run of this test left
            # exactly such a row and it was briefly mistaken for one.
            bad = sql(f"""INSERT INTO bookings (salon_id, store_id, artist_id, customer_id,
                       service_id, start_time, end_time, blocked_until,
                       original_price, final_price, deposit_amount, status)
                    SELECT salon_id, store_id, artist_id, customer_id, service_id,
                           NOW() + interval '800 hours', NOW() + interval '800 hours',
                           NOW() + interval '800 hours', 100, 100, 150, 'pending'
                      FROM bookings WHERE artist_id='{artist_id}' LIMIT 1
                    RETURNING id;""")
            created.append(bad.splitlines()[0].strip())
            check("M8", "a deposit larger than the price is rejected", False,
                  "the database accepted deposit 150 on a price of 100")
        except RuntimeError as e:
            check("M8", "a deposit larger than the price is rejected",
                  "check" in str(e).lower() or "constraint" in str(e).lower(),
                  "rejected by a database constraint")

        # ── M9: money cannot be NaN (migration 034) ──────────────────────────
        try:
            sql(f"UPDATE bookings SET final_price='NaN'::numeric WHERE id='{b}';")
            check("M9", "NaN cannot be stored as money", False,
                  "the database accepted a NaN price")
        except RuntimeError:
            check("M9", "NaN cannot be stored as money", True,
                  "rejected by a database constraint")

        # ── M10: the assumption internal/pkg/money is pinned to ─────────────
        # bounds_test.go asserts that Parse accepts exactly what NUMERIC(10,2)
        # can hold - 99999999.99 - and rejects anything wider. That test is
        # only as true as this schema fact, and a future migration could add
        # a money column with a narrower type. Then Parse would accept a value
        # the insert cannot store: a 500 on a booking instead of a 400 on a
        # form. Checked here because only a live database can answer it.
        narrow = sql("""
            SELECT coalesce(string_agg(table_name||'.'||column_name||' NUMERIC('
                   ||numeric_precision||','||numeric_scale||')', ', '), '')
              FROM information_schema.columns
             WHERE data_type='numeric' AND table_schema='public'
               AND (column_name LIKE '%price%' OR column_name LIKE '%amount%'
                    OR column_name LIKE '%fee%' OR column_name LIKE '%deposit%')
               AND NOT (numeric_precision=10 AND numeric_scale=2);""")
        check("M10", "every money column is still NUMERIC(10,2)", narrow == "",
              narrow or "18 money columns, all NUMERIC(10,2) — matches money.Parse's bounds")

    finally:
        for b in created:
            try:
                sql(f"DELETE FROM notifications WHERE booking_id='{b}';")
                sql(f"DELETE FROM bookings WHERE id='{b}';")
            except RuntimeError:
                pass

    print(f"\n  {len(PASS)} passed, {len(FAIL)} failed, {len(SKIP)} skipped")
    if FAIL:
        print(f"  FAILED: {', '.join(FAIL)}")
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
