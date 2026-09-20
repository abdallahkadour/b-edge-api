#!/usr/bin/env python3
"""
UC-1 · Guest books an appointment — executable verification.

WHY THIS FILE EXISTS

On 2026-09-19 this path was verified by hand: 17 flows, 0 defects, including
six concurrent holds on one slot resolving to exactly one winner. That proved
the logic worked *that afternoon*. A one-off script proves nothing the day
after, and the whole reason defects kept reaching the launch artist is that
nothing that got fixed stayed verified.

So the verification is committed, runnable, and exits non-zero. It is the
difference between having tested something and being able to keep testing it.

WHAT IT COVERS

Every flow of UC-1 that can be exercised through the API: the basic path,
three alternate flows (early-bird surcharge, no surcharge, duration affecting
the last bookable start) and thirteen exception flows, including the
concurrency race that decides whether two customers can be sold one slot.

WHAT IT NEEDS

A running API on :3000 and the dev database in Docker. It creates its own
fixtures, finds its own free slots, and deletes everything it made - including
the guest users, which the application itself does not clean up.

    make verify-uc1

LESSONS BAKED IN, because each one produced a false result first time:

  * SQL runs through a helper that SURFACES stderr. The original swallowed it,
    so an INSERT into a table that does not exist read as a product defect.
  * The slots endpoint returns ONLY bookable slots. There is no `available`
    field; filtering for one yields zero and looks like "no availability".
  * The exceptions table is `business_hours_exceptions` / `exception_date`.
    There is no `special_hours`.
  * Fixtures are DISCOVERED, never hardcoded. The first run picked a service
    that happened to be `is_active = false` and read the correct empty result
    as a bug.
  * A test that cannot run says SKIP. It never silently passes. The same-day
    notice check ran at 23:51 once and "passed" against zero slots, which
    proved nothing at all.
"""

import concurrent.futures as cf
import datetime
import json
import subprocess
import sys
import urllib.error
import urllib.request
import uuid

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]

PASS, FAIL, SKIP = [], [], []


def sql(q):
    """Run SQL. Raises on error rather than returning an empty string, because
    a silently-failed statement is how a test comes to verify nothing."""
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError(f"SQL failed: {p.stderr.strip()[:200]}")
    return p.stdout.strip()


def call(method, path, body=None):
    req = urllib.request.Request(
        API + path,
        method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json"},
    )
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
        print(f"\n  cannot reach the API at {API} — is it running? ({e.reason})")
        sys.exit(2)


def err(resp):
    e = (resp or {}).get("error")
    return e.get("code") if e else None


def check(fid, desc, ok, detail):
    (PASS if ok else FAIL).append(fid)
    print(f"  {'PASS' if ok else 'FAIL'}  {fid:5} {desc}\n         {detail}")


def skip(fid, desc, why):
    SKIP.append(fid)
    print(f"  SKIP  {fid:5} {desc}\n         {why}")


# ── fixtures, discovered rather than hardcoded ───────────────────────────────

def fixtures():
    row = sql("""
        SELECT a.id, st.id, sv.id, sv.duration_min, sv.price
          FROM artists a
          JOIN salons sa ON sa.id = a.salon_id
          JOIN stores st ON st.salon_id = sa.id AND st.is_active
          JOIN services sv ON sv.salon_id = sa.id AND sv.is_active
         WHERE EXISTS (SELECT 1 FROM business_hours bh
                        WHERE bh.store_id = st.id AND bh.is_open)
         ORDER BY sv.duration_min ASC
         LIMIT 1;""")
    if not row:
        print("  no active artist/store/service with open hours — cannot run")
        sys.exit(2)
    a, s, sv, dur, price = row.split("|")
    return a, s, sv, int(dur), price


def slots(artist, store, svc, date):
    _, r = call("GET", f"/bookings/slots?artist_id={artist}&store_id={store}"
                       f"&service_id={svc}&date={date}")
    return r.get("data") or []


def find_day_with_slots(artist, store, svc, start=2, span=12):
    for n in range(start, start + span):
        d = (datetime.datetime.now(datetime.timezone.utc)
             + datetime.timedelta(days=n)).strftime("%Y-%m-%d")
        s = slots(artist, store, svc, d)
        if len(s) > 6:
            return d, s
    return None, []


def main():
    ARTIST, STORE, SVC, DUR, PRICE = fixtures()
    print(f"\n  fixtures: artist={ARTIST[:8]} store={STORE[:8]} service={SVC[:8]} "
          f"({DUR} min, ${PRICE})")

    date, free = find_day_with_slots(ARTIST, STORE, SVC)
    if not free:
        print("  no day with free slots in the next two weeks — cannot run")
        sys.exit(2)
    print(f"  test date: {date}, {len(free)} slots\n")

    made_bookings, made_users = [], []

    def hold(start_time):
        st, r = call("POST", "/bookings/guest/hold",
                     {"artist_id": ARTIST, "store_id": STORE,
                      "service_id": SVC, "start_time": start_time})
        bid = (r.get("data") or {}).get("booking_id")
        if bid:
            made_bookings.append(bid)
        return st, r, bid

    try:
        # ── exception flows on the hold ──────────────────────────────────────
        slot = free[0]["start_time"]

        _, r = call("POST", "/bookings/guest/hold",
                    {"artist_id": "not-a-uuid", "store_id": STORE,
                     "service_id": SVC, "start_time": slot})
        check("E1", "malformed artist id refused", err(r) is not None, f"{err(r)}")

        _, r = call("POST", "/bookings/guest/hold",
                    {"artist_id": ARTIST, "store_id": STORE,
                     "service_id": SVC, "start_time": "15 June"})
        check("E2", "malformed start_time refused",
              err(r) == "INVALID_START_TIME", f"{err(r)}")

        past = (datetime.datetime.now(datetime.timezone.utc)
                - datetime.timedelta(days=1)).strftime("%Y-%m-%dT%H:%M:%SZ")
        _, r = call("POST", "/bookings/guest/hold",
                    {"artist_id": ARTIST, "store_id": STORE,
                     "service_id": SVC, "start_time": past})
        check("E3", "past start refused", err(r) == "BOOKING_IN_PAST", f"{err(r)}")

        _, r = call("POST", "/bookings/guest/hold",
                    {"artist_id": ARTIST, "store_id": STORE,
                     "service_id": str(uuid.uuid4()), "start_time": slot})
        check("E4", "unknown service refused",
              err(r) == "SERVICE_NOT_FOUND", f"{err(r)}")

        # ── basic flow ───────────────────────────────────────────────────────
        st, r, hold_id = hold(slot)
        check("B1", "a free slot can be held", st in (200, 201) and bool(hold_id),
              f"{st} id={(hold_id or '')[:8]}")

        st2, r2, _ = hold(slot)
        check("E5", "a second hold on the same slot is refused",
              err(r2) == "SLOT_UNAVAILABLE", f"{st2} {err(r2)}")

        if hold_id:
            _, r = call("PATCH", f"/bookings/guest/{hold_id}/submit",
                        {"name": "UC1 Verify", "phone": "abc"})
            check("E7", "invalid phone refused", err(r) is not None, f"{err(r)}")

            _, r = call("PATCH", f"/bookings/guest/{hold_id}/submit",
                        {"phone": "71555900"})
            check("E8", "missing name refused", err(r) is not None, f"{err(r)}")

            st, r = call("PATCH", f"/bookings/guest/{hold_id}/submit",
                         {"name": "UC1 Verify", "phone": "71555900"})
            d = r.get("data") or {}
            check("B2", "submit creates a pending booking",
                  d.get("status") == "pending", f"{st} status={d.get('status')}")
            if d.get("customer_id"):
                made_users.append(d["customer_id"])

            _, r = call("PATCH", f"/bookings/guest/{hold_id}/submit",
                        {"name": "UC1 Verify", "phone": "71555900"})
            check("E9", "re-submitting a used hold is refused",
                  err(r) == "HOLD_EXPIRED", f"{err(r)}")

        _, r = call("PATCH", f"/bookings/guest/{uuid.uuid4()}/submit",
                    {"name": "Valid Name", "phone": "71555901"})
        check("E10", "unknown hold id refused",
              err(r) in ("BOOKING_NOT_FOUND", "HOLD_EXPIRED"), f"{err(r)}")

        # ── the one that matters: real concurrency ───────────────────────────
        target = free[3]["start_time"]
        with cf.ThreadPoolExecutor(max_workers=6) as ex:
            out = list(ex.map(lambda _: hold(target), range(6)))
        won = [x for x in out if x[0] in (200, 201)]
        codes = {err(x[1]) for x in out if x[0] not in (200, 201)}
        check("E6", "six concurrent holds -> exactly one winner",
              len(won) == 1 and codes <= {"SLOT_UNAVAILABLE"},
              f"{len(won)} won, {len(out) - len(won)} refused with {codes or '{}'}")

        # ── hold expiry ──────────────────────────────────────────────────────
        _, _, exp_id = hold(free[5]["start_time"])
        if exp_id:
            sql(f"UPDATE bookings SET held_until = NOW() - interval '1 minute' "
                f"WHERE id = '{exp_id}';")
            _, r = call("PATCH", f"/bookings/guest/{exp_id}/submit",
                        {"name": "UC1 Expired", "phone": "71555902"})
            check("E11", "an expired hold cannot be submitted",
                  err(r) == "HOLD_EXPIRED", f"{err(r)}")

        # ── availability rules ───────────────────────────────────────────────
        dow = sql(f"SELECT EXTRACT(dow FROM DATE '{date}')::int;")
        close = sql(f"SELECT to_char(close_time,'HH24:MI') FROM business_hours "
                    f"WHERE store_id='{STORE}' AND day_of_week={dow};")
        last = free[-1]["start_time"][11:16]
        lh, lm = map(int, last.split(":"))
        ch, cm = map(int, close.split(":"))
        check("A3", "no start is offered that would run past closing",
              lh * 60 + lm + DUR <= ch * 60 + cm,
              f"last start {last} + {DUR}min vs close {close}")

        sql(f"UPDATE business_hours SET is_open=false "
            f"WHERE store_id='{STORE}' AND day_of_week={dow};")
        closed = slots(ARTIST, STORE, SVC, date)
        sql(f"UPDATE business_hours SET is_open=true "
            f"WHERE store_id='{STORE}' AND day_of_week={dow};")
        check("E12", "a closed weekday offers nothing", len(closed) == 0,
              f"{len(closed)} slots while closed")

        sql(f"INSERT INTO business_hours_exceptions "
            f"(store_id, exception_date, is_closed, reason) "
            f"VALUES ('{STORE}','{date}',true,'UC1 verify');")
        exc = slots(ARTIST, STORE, SVC, date)
        sql(f"DELETE FROM business_hours_exceptions WHERE reason='UC1 verify';")
        back = slots(ARTIST, STORE, SVC, date)
        check("E13", "an exception closure overrides the weekly pattern",
              len(exc) == 0 and len(back) > 0,
              f"{len(exc)} while closed, {len(back)} after removing it")

        sql(f"INSERT INTO business_hours_exceptions "
            f"(store_id, exception_date, is_closed, open_time, close_time, reason) "
            f"VALUES ('{STORE}','{date}',false,'14:00','16:00','UC1 verify');")
        cust = [s["start_time"][11:16] for s in slots(ARTIST, STORE, SVC, date)]
        sql(f"DELETE FROM business_hours_exceptions WHERE reason='UC1 verify';")
        check("A4", "custom exception hours narrow the day",
              bool(cust) and cust[0] >= "14:00" and cust[-1] < "16:00",
              f"{len(cust)} slots, {cust[0] if cust else '-'}..{cust[-1] if cust else '-'}"
              f" (expect within 14:00-16:00)")

        # ── early-bird surcharge ──────────────────────────────────────────────
        #
        # The condition is CREATED rather than waited for. Left to chance this
        # skipped on any day whose slots all start after the store's cutoff,
        # which is most of them - and a check that skips is a check that never
        # catches anything. The store's own setting is saved and restored.
        orig_cutoff = sql(f"SELECT coalesce(to_char(early_bird_cutoff,'HH24:MI'),'') "
                          f"FROM stores WHERE id='{STORE}';")
        orig_fee = sql(f"SELECT early_bird_fee FROM stores WHERE id='{STORE}';")

        # Derived from the slots that are STILL FREE at this point, not from
        # the list captured at the top - the holds placed above consumed the
        # earliest ones, and a cutoff computed from a stale list lands before
        # every remaining slot and qualifies nothing.
        free = slots(ARTIST, STORE, SVC, date)
        if not free:
            skip("A1", "early-bird surcharge is charged", "no slots left on the test day")
            skip("A5", "discount comes off the price including the surcharge",
                 "no slots left on the test day")
            eb, cutoff, fee = [], "", "0"
        else:
            hh, mm = (int(x) for x in free[0]["start_time"][11:16].split(":"))
            cutoff = f"{min(hh + 1, 23):02d}:{mm:02d}"   # an hour of qualifying slots
            sql(f"UPDATE stores SET early_bird_cutoff='{cutoff}', early_bird_fee=15 "
                f"WHERE id='{STORE}';")
            fee = "15.00"
            free = slots(ARTIST, STORE, SVC, date)
            eb = [s for s in free if s.get("is_early_bird")]
        if not cutoff or float(fee) <= 0:
            skip("A1", "early-bird surcharge is charged",
                 "this store has no early-bird cutoff or a zero fee")
        elif not eb:
            skip("A1", "early-bird surcharge is charged",
                 f"no early-bird slot on {date} (cutoff {cutoff})")
        else:
            _, _, ebid = hold(eb[0]["start_time"])
            if ebid:
                st, r = call("PATCH", f"/bookings/guest/{ebid}/submit",
                             {"name": "UC1 EarlyBird", "phone": "71555903"})
                if (r.get("data") or {}).get("customer_id"):
                    made_users.append(r["data"]["customer_id"])
                row = sql(f"SELECT original_price||'|'||final_price "
                          f"FROM bookings WHERE id='{ebid}';")
                orig, final = row.split("|")
                check("A1", "early-bird slot is charged the surcharge",
                      float(final) == float(orig) + float(fee),
                      f"{orig} + {fee} = {final}")

        # ── D3.4 end to end: surcharge applies BEFORE the discount ───────────
        # internal/pkg/discount tests this rule thoroughly at the resolver
        # (20% of 120, not of 100). What no test covered is the COUPLING:
        # applyDiscount passes `subtotal` as the base with a zero surcharge,
        # on the stated assumption that subtotal already includes the
        # early-bird fee. The resolver would stay correct and the booking
        # would be wrong if a caller ever passed the raw price instead, and
        # nothing would notice. This exercises the real path.
        if cutoff and float(fee) > 0 and eb:
            salon = sql(f"SELECT salon_id FROM stores WHERE id='{STORE}';")
            sql(f"""INSERT INTO discounts (salon_id, code, kind, value, is_active)
                    VALUES ('{salon}', 'UC1PCT20', 'percentage', 20, true)
                    ON CONFLICT DO NOTHING;""")
            try:
                _, _, did = hold(eb[-1]["start_time"])
                if did:
                    r = call("PATCH", f"/bookings/guest/{did}/submit",
                             {"name": "UC1 Discount", "phone": "71555905",
                              "discount_code": "UC1PCT20"})[1]
                    if (r.get("data") or {}).get("customer_id"):
                        made_users.append(r["data"]["customer_id"])
                    row = sql(f"""SELECT original_price||'|'||discount_amount||'|'||final_price
                                    FROM bookings WHERE id='{did}';""")
                    orig, disc, final = (float(x) for x in row.split("|"))
                    subtotal = orig + float(fee)          # surcharge first
                    expected_disc = round(subtotal * 0.20, 2)
                    check("A5", "discount is taken off the price INCLUDING the surcharge",
                          abs(disc - expected_disc) < 0.01
                          and abs(final - (subtotal - expected_disc)) < 0.01,
                          f"base {orig} + fee {fee} = {subtotal}; "
                          f"20% = {expected_disc}, got discount {disc}, final {final}")
            finally:
                sql("DELETE FROM discount_redemptions WHERE discount_id IN "
                    "(SELECT id FROM discounts WHERE code='UC1PCT20');")
                sql("DELETE FROM discounts WHERE code='UC1PCT20';")
        else:
            skip("A5", "discount is taken off the price including the surcharge",
                 "needs an early-bird slot on the test day")

        nb = [s for s in free if not s.get("is_early_bird")]
        if nb:
            _, _, nid = hold(nb[-1]["start_time"])
            if nid:
                r = call("PATCH", f"/bookings/guest/{nid}/submit",
                         {"name": "UC1 Normal", "phone": "71555904"})[1]
                if (r.get("data") or {}).get("customer_id"):
                    made_users.append(r["data"]["customer_id"])
                row = sql(f"SELECT original_price||'|'||final_price "
                          f"FROM bookings WHERE id='{nid}';")
                o, f = row.split("|")
                check("A2", "a normal slot carries no surcharge",
                      float(o) == float(f), f"original {o} / final {f}")
        else:
            skip("A2", "a normal slot carries no surcharge",
                 "every slot on the test day is early-bird")

    finally:
        # Guest users are cleaned up too. The application never deletes them,
        # so a suite that leaves them behind quietly grows the users table on
        # every run.
        for b in made_bookings:
            try:
                sql(f"DELETE FROM notifications WHERE booking_id='{b}';")
                sql(f"DELETE FROM bookings WHERE id='{b}';")
            except RuntimeError:
                pass
        for u in made_users:
            try:
                sql(f"DELETE FROM users WHERE id='{u}';")
            except RuntimeError:
                pass
        try:
            sql("DELETE FROM business_hours_exceptions WHERE reason='UC1 verify';")
        except RuntimeError:
            pass
        try:
            cut = f"'{orig_cutoff}'" if orig_cutoff else "NULL"
            sql(f"UPDATE stores SET early_bird_cutoff={cut}, "
                f"early_bird_fee={orig_fee or 0} WHERE id='{STORE}';")
        except (RuntimeError, NameError):
            pass

    print(f"\n  {len(PASS)} passed, {len(FAIL)} failed, {len(SKIP)} skipped")
    if FAIL:
        print(f"  FAILED: {', '.join(FAIL)}")
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
