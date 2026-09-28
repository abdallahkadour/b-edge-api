#!/usr/bin/env python3
"""E2E suite 27 - leaving a salon, letting a hold go, and the shop's money path.

Suite 27 of E2E-TEST-PLAN.md was checked by hand as each fix landed
(2026-09-26/27). This makes it repeatable, together with the security cases
the same fixes opened:

  27.1  a member who LEFT the salon disappears from Discover
  27.2  releasing a hold frees the time at once; a second release is 404
  27.3  one network holds at most 2 unfinished times per artist   (FRAUD-18)
  27.4  a retried checkout is one order, one stock deduction       (FRAUD-19)
  27.5  a code's end date is a day on the salon's calendar
  27.6  every read of an order carries its discount
  27.7  the cart previews a code before the order is placed        (FRAUD-20's surface)
  16.5  an expired hold frees its time and tells the waitlist with NOBODY
        reading availability - the expiry worker, not the lazy sweep
  FRAUD-21  a forged client-address header does not lift the hold limit
  FRAUD-22  a hold that is not an unfinished guest hold cannot be released
  FRAUD-20  (opt-in, --fraud20) the promo previews stop answering after 20
            codes per address in 10 minutes, across BOTH previews. Opt-in
            because it spends this machine's code budget for 10 minutes: a
            second run inside that window would see 27.7 refused.

Every check states its positive control first: a "not listed" or "not
freed" measured on a state that never had the thing in it is not a pass.

Builds its own salon (an owner, a member who leaves, products, codes) and
destroys it in a finally block, reporting the residual row count. The shared
harness helpers - register, onboard, join, cleanup - are imported from
chaos-booking.py rather than copied, so the joining flow stays one
implementation. Requires the API built with -tags devbypass (make dev).

  python3 scripts/e2e-suite27.py
  python3 scripts/e2e-suite27.py --fraud20     # also measure FRAUD-20, last
"""
import concurrent.futures as cf
import importlib.util
import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid
from datetime import date, datetime, timedelta, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("chaos", os.path.join(HERE, "chaos-booking.py"))
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)
c.TAG = "s27"           # every row this run creates carries it, for cleanup

API = c.API
RESULTS = []
FRAUD20_ARGS = []   # (salon, product) for the opt-in FRAUD-20 run, set by main
PHONE = "+9617636{:04d}"  # cleanup() clears customer_otps under +9617636%


def rec(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<5} {tid:<10} {detail}")


def call(method, path, body=None, token=None, headers=None):
    """chaos.call, plus arbitrary headers (FRAUD-21 forges one)."""
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    h.update(headers or {})
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(API + path, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw) if raw else {}
        except ValueError:
            return e.code, {}


def err(r):
    return ((r or {}).get("error") or {}).get("code")


def slots(artist, store, service, day):
    st, r = call("GET", f"/bookings/slots?artist_id={artist}&store_id={store}"
                        f"&service_id={service}&date={day}")
    data = (r or {}).get("data") or []
    if isinstance(data, dict):
        data = data.get("slots") or []
    return st, [s["start_time"] for s in data]


def hold(artist, store, service, start, headers=None):
    return call("POST", "/bookings/guest/hold", {"artist_id": artist, "store_id": store,
                "service_id": service, "start_time": start}, headers=headers)


def release(booking_id):
    return call("DELETE", f"/bookings/guest/hold/{booking_id}")


def spaced(times, n, minutes):
    """n start times at least `minutes` apart. Consecutive start times from
    the slots endpoint OVERLAP each other for any service longer than the
    slot step - the first run of this suite held two of them and read the
    server's correct 409 SLOT_UNAVAILABLE as a failed positive control."""
    out, last = [], None
    for t in times:
        at = datetime.fromisoformat(t.replace("Z", "+00:00"))
        if last is None or (at - last).total_seconds() >= minutes * 60:
            out.append(t)
            last = at
        if len(out) == n:
            break
    return out


def bid(r):
    return ((r or {}).get("data") or {}).get("booking_id")


def same_instant(a, b):
    pa = datetime.fromisoformat(a.replace("Z", "+00:00"))
    pb = datetime.fromisoformat(b.replace("Z", "+00:00"))
    return pa == pb


def main():
    admin_tok = c.login(c.ADMIN)
    if not admin_tok:
        rec("setup", "FAIL", "admin login failed")
        return

    # ── Topology: an owner with a salon, and a member who will leave ──────
    owner_email, member_email = "s27.owner@test.bedge.com", "s27.member@test.bedge.com"
    c.register("s27 Owner", owner_email, PHONE.format(8001))
    owner = c.onboard(owner_email, "s27 Salon", "s27-owner")
    c.approve_artist(owner, admin_tok)
    salon = c.sql(f"SELECT salon_id FROM artists WHERE id='{owner}'")
    store = c.sql(f"SELECT id FROM stores WHERE salon_id='{salon}' LIMIT 1")
    service = c.sql(f"SELECT id FROM services WHERE salon_id='{salon}' LIMIT 1")
    # Public surfaces show only an artist with a live plan. A plan the admin
    # approval did not create is the harness's job, not a finding.
    if c.sql(f"SELECT count(*) FROM subscriptions WHERE artist_id='{owner}'") == "0":
        c.sql(f"INSERT INTO subscriptions (artist_id, plan_code, monthly_price) VALUES ('{owner}','multi',0)")
    c.seat_plan(salon, "multi")
    owner_tok = c.login(owner_email)

    c.register("s27 Member", member_email, PHONE.format(8002))
    member = c.join(owner_tok, member_email, PHONE.format(8002), "s27-member", admin_tok)
    if c.sql(f"SELECT count(*) FROM subscriptions WHERE artist_id='{member}'") == "0":
        c.sql(f"INSERT INTO subscriptions (artist_id, plan_code, monthly_price) VALUES ('{member}','comped',0)")

    # A day with room: the first of the next ten with at least 8 open times.
    day, open_times = None, []
    for d in range(2, 12):
        cand = (date.today() + timedelta(days=d)).isoformat()
        st, times = slots(owner, store, service, cand)
        if st == 200 and len(times) >= 8:
            day, open_times = cand, times
            break
    if not day:
        rec("setup", "FAIL", "no day in the next ten has 8 open times for the owner")
        return
    gap = int(c.sql(f"SELECT duration_min + COALESCE(buffer_min,0) FROM services WHERE id='{service}'")) + 15

    def fresh(n):
        """n open times, re-read now, far enough apart not to overlap."""
        _, times = slots(owner, store, service, day)
        return spaced(times, n, gap)

    print(f"  setup: salon {salon[:8]}, day {day}, {len(open_times)} open times, spacing {gap} min\n")

    # ── 27.1 A member who left disappears from Discover ───────────────────
    def listed():
        st, r = call("GET", "/discovery/artists?q=s27%20Member")
        return st, [a.get("id") or a.get("artist_id") for a in ((r or {}).get("data") or [])]
    st, ids = listed()
    if member not in ids:
        rec("27.1", "FAIL", f"positive control: the member is not listed BEFORE leaving (status {st})")
    else:
        mt = c.login(member_email)
        st, r = call("POST", "/artists/salon/members/leave", {}, mt)
        _, ids_after = listed()
        if st >= 400:
            rec("27.1", "FAIL", f"leave refused: {st} {err(r)}")
        elif member in ids_after:
            rec("27.1", "FAIL", "still listed on Discover after leaving")
        else:
            rec("27.1", "PASS", "listed before leaving, gone after")

    # ── 27.2 Releasing a hold frees the time at once ──────────────────────
    t0 = fresh(1)[0]
    st, r = hold(owner, store, service, t0)
    b0 = ((r or {}).get("data") or {}).get("booking_id")
    _, during = slots(owner, store, service, day)
    if st != 201 or t0 in during:
        rec("27.2", "FAIL", f"positive control: hold {st} {err(r)}; time still offered: {t0 in during}")
    else:
        st1, _ = release(b0)
        _, after = slots(owner, store, service, day)
        st2, r2 = release(b0)
        ok = st1 in (200, 204) and t0 in after and st2 == 404
        rec("27.2", "PASS" if ok else "FAIL",
            f"held -> not offered; release {st1} -> offered again: {t0 in after}; second release {st2} {err(r2)}")

    # ── FRAUD-22 Only an unfinished guest hold can be released ────────────
    st, r = hold(owner, store, service, fresh(1)[0])
    b1 = bid(r)
    st, r = call("PATCH", f"/bookings/guest/{b1}/submit", {"name": "s27 Guest", "phone": PHONE.format(9950)})
    if st >= 400:
        rec("FRAUD-22", "FAIL", f"setup: submit refused {st} {err(r)}")
    else:
        before = c.sql(f"SELECT status FROM bookings WHERE id='{b1}'")
        st1, r1 = release(b1)
        after = c.sql(f"SELECT status FROM bookings WHERE id='{b1}'")
        st2, r2 = release(str(uuid.uuid4()))
        ok = st1 == 404 and before == after and st2 == 404 and err(r1) == err(r2)
        rec("FRAUD-22", "PASS" if ok else "FAIL",
            f"submitted booking: {st1} {err(r1)}, status {before} -> {after}; unknown id: {st2} {err(r2)} "
            f"(same answer, so the route cannot tell them apart)")

    # ── 27.3 Two unfinished holds per artist per network (FRAUD-18) ───────
    # FRAUD-21: with two held, a forged address header must not buy a third.
    t2, t3, t4, t5 = fresh(4)
    s2, r2 = hold(owner, store, service, t2)
    s3, r3 = hold(owner, store, service, t3)
    s4, r4 = hold(owner, store, service, t4)
    forged = {"CF-Connecting-IP": "203.0.113.9", "X-Forwarded-For": "203.0.113.9", "X-Real-IP": "203.0.113.9"}
    s5, r5 = hold(owner, store, service, t5, headers=forged)
    if s2 != 201 or s3 != 201:
        rec("27.3", "FAIL", f"positive control: the first two holds were refused ({s2} {err(r2)}, {s3} {err(r3)})")
        for rr in (r2, r3, r4, r5):
            if bid(rr):
                release(bid(rr))
    else:
        rec("27.3", "PASS" if (s4 == 429 and err(r4) == "TOO_MANY_HOLDS") else "FAIL",
            f"holds 1-2: 201, 201; hold 3: {s4} {err(r4)}")
        rec("FRAUD-21", "PASS" if (s5 == 429 and err(r5) == "TOO_MANY_HOLDS") else "FAIL",
            f"hold 3 with CF-Connecting-IP / X-Forwarded-For / X-Real-IP forged: {s5} {err(r5)} "
            f"(TRUSTED_PROXIES unset: no header is believed)")
        release(bid(r2))
        s6, r6 = hold(owner, store, service, t4)
        rec("27.3b", "PASS" if s6 == 201 else "FAIL", f"after releasing one, hold 3: {s6} {err(r6)}")
        for rr in (r3, r4, r5, r6):
            if bid(rr):
                release(bid(rr))

    # ── 16.5 The clock frees an abandoned hold, with nobody reading ───────
    t6 = fresh(1)[0]
    st, r = hold(owner, store, service, t6)
    b6 = bid(r)
    st_w, r_w = call("POST", "/bookings/waitlist", {"artist_id": owner, "store_id": store, "service_id": service,
                     "requested_date": day, "name": "s27 Waiter", "phone": PHONE.format(9951)})
    entry = ((r_w or {}).get("data") or {}).get("id") or ((r_w or {}).get("data") or {}).get("entry_id")
    if st != 201 or st_w >= 400:
        rec("16.5", "FAIL", f"setup: hold {st} {err(r)}, waitlist {st_w} {err(r_w)}")
    else:
        if not entry:
            entry = c.sql(f"""SELECT w.id FROM waitlist_entries w JOIN users u ON u.id=w.customer_id
                              WHERE u.phone='{PHONE.format(9951)}' ORDER BY w.created_at DESC LIMIT 1""")
        ctrl = (c.sql(f"SELECT status FROM bookings WHERE id='{b6}'"),
                c.sql(f"SELECT status FROM waitlist_entries WHERE id='{entry}'"))
        # Lapse the hold. From here NOTHING in this run reads availability.
        c.sql(f"UPDATE bookings SET held_until = NOW() - INTERVAL '1 minute' WHERE id='{b6}'")
        t_start, bst, wst = time.time(), None, None
        while time.time() - t_start < 90:
            bst = c.sql(f"SELECT status FROM bookings WHERE id='{b6}'")
            wst = c.sql(f"SELECT status FROM waitlist_entries WHERE id='{entry}'")
            if bst == "expired" and wst == "notified":
                break
            time.sleep(3)
        took = round(time.time() - t_start)
        ok = ctrl == ("held", "waiting") and bst == "expired" and wst == "notified"
        rec("16.5", "PASS" if ok else "FAIL",
            f"before: hold {ctrl[0]}, entry {ctrl[1]}; after {took}s with no reads: hold {bst}, entry {wst}")

    # ── Shop fixtures ─────────────────────────────────────────────────────
    p1 = c.sql(f"INSERT INTO products (salon_id,name,price,stock_quantity) VALUES ('{salon}','s27 serum',20.00,5) RETURNING id")
    p2 = c.sql(f"INSERT INTO products (salon_id,name,price,stock_quantity) VALUES ('{salon}','s27 cream',20.00,10) RETURNING id")
    other = c.sql(f"INSERT INTO salons (owner_id,name) VALUES ((SELECT user_id FROM artists WHERE id='{owner}'),'s27 other') RETURNING id")

    def order(body):
        base = {"salon_id": salon, "name": "s27 Buyer", "phone": PHONE.format(9952),
                "delivery_lat": 33.89, "delivery_lng": 35.50}
        base.update(body)
        return call("POST", "/orders", base)

    # ── 27.4 A retried checkout is one order (FRAUD-19) ───────────────────
    rid = str(uuid.uuid4())
    answers = [order({"request_id": rid, "items": [{"product_id": p1, "quantity": 2}]}) for _ in range(3)]
    ids = {((r or {}).get("data") or {}).get("id") for _, r in answers}
    codes = [st for st, _ in answers]
    stock = c.sql(f"SELECT stock_quantity FROM products WHERE id='{p1}'")
    rows = c.sql(f"SELECT count(*) FROM orders WHERE request_id='{rid}'")
    rec("27.4", "PASS" if (codes == [201] * 3 and len(ids) == 1 and stock == "3" and rows == "1") else "FAIL",
        f"3 sends -> {codes}, {len(ids)} distinct id(s), {rows} row(s), stock 5 -> {stock}")

    st, r = call("POST", "/orders", {"salon_id": other, "name": "s27 Buyer", "phone": PHONE.format(9952),
                 "delivery_lat": 33.89, "delivery_lng": 35.5, "request_id": rid,
                 "items": [{"product_id": p1, "quantity": 1}]})
    # The replay lookup runs before any pricing, so this must be the
    # request_id refusal itself, not a product error further down.
    rec("27.4b", "PASS" if (st == 409 and err(r) == "REQUEST_ID_REUSED") else "FAIL",
        f"same request_id for another salon: {st} {err(r)}")

    rid2 = str(uuid.uuid4())
    with cf.ThreadPoolExecutor(5) as ex:
        burst = list(ex.map(lambda _: order({"request_id": rid2, "items": [{"product_id": p1, "quantity": 1}]}), range(5)))
    bids = {((r or {}).get("data") or {}).get("id") for _, r in burst}
    rows = c.sql(f"SELECT count(*) FROM orders WHERE request_id='{rid2}'")
    stock = c.sql(f"SELECT stock_quantity FROM products WHERE id='{p1}'")
    rec("27.4c", "PASS" if ([s for s, _ in burst] == [201] * 5 and len(bids) == 1 and rows == "1" and stock == "2") else "FAIL",
        f"5 simultaneous -> {[s for s, _ in burst]}, {len(bids)} id(s), {rows} row(s), stock 3 -> {stock}")

    # ── 27.5 A code's end date is a salon-calendar day ────────────────────
    st, r = call("POST", "/artists/salon/discounts", {"code": "S27WINTER", "kind": "percentage", "value": "10",
                 "ends_on": "2026-12-01"}, owner_tok)
    d = (r or {}).get("data") or {}
    ok = st == 201 and d.get("ends_on") == "2026-12-01" and same_instant(d.get("ends_at", "1970-01-01T00:00:00Z"), "2026-12-01T22:00:00Z")
    rec("27.5", "PASS" if ok else "FAIL", f"ends_on 2026-12-01 -> {st}, ends_at {d.get('ends_at')}, ends_on {d.get('ends_on')}")
    st, r = call("POST", "/artists/salon/discounts", {"code": "S27SPRING", "kind": "percentage", "value": "10",
                 "ends_on": "2027-03-27"}, owner_tok)
    d = (r or {}).get("data") or {}
    rec("27.5b", "PASS" if (st == 201 and same_instant(d.get("ends_at", "1970-01-01T00:00:00Z"), "2027-03-27T22:00:00Z")) else "FAIL",
        f"the night clocks go forward: ends_at {d.get('ends_at')}")
    st, r = call("POST", "/artists/salon/discounts", {"code": "S27BOTH", "kind": "percentage", "value": "10",
                 "ends_on": "2026-12-01", "ends_at": "2026-12-05T10:00:00Z"}, owner_tok)
    rec("27.5c", "PASS" if st == 422 else "FAIL", f"ends_on and ends_at together: {st} {err(r)}")

    # ── 27.7 The cart previews a code first (public, writes nothing) ──────
    call("POST", "/artists/salon/discounts", {"code": "S27SAVE", "kind": "fixed", "value": "10"}, owner_tok)
    call("POST", "/artists/salon/discounts", {"code": "S27OLD", "kind": "fixed", "value": "5", "ends_on": "2020-01-01"}, owner_tok)
    items = [{"product_id": p2, "quantity": 2}]
    orders_before = c.sql(f"SELECT count(*) FROM orders WHERE salon_id='{salon}'")
    stock_before = c.sql(f"SELECT stock_quantity FROM products WHERE id='{p2}'")
    pv = {}
    for code in ("s27save", "S27OLD", "S27NOPE"):
        st, r = call("POST", "/orders/discount-preview", {"salon_id": salon, "code": code, "items": items})
        pv[code] = (st, (r or {}).get("data") or {})
    a, o, n = pv["s27save"][1], pv["S27OLD"][1], pv["S27NOPE"][1]
    ok = (a.get("valid") is True and a.get("subtotal") == "40.00" and a.get("final") == "30.00"
          and o.get("valid") is False and "expired" in (o.get("reason") or "")
          and n.get("valid") is False and "isn't valid" in (n.get("reason") or "")
          and c.sql(f"SELECT count(*) FROM orders WHERE salon_id='{salon}'") == orders_before
          and c.sql(f"SELECT stock_quantity FROM products WHERE id='{p2}'") == stock_before)
    rec("27.7", "PASS" if ok else "FAIL",
        f"no login; S27SAVE {a.get('subtotal')} -> {a.get('final')}; expired: \"{o.get('reason')}\"; "
        f"unknown: \"{n.get('reason')}\"; previews wrote no order, took no stock")

    # ── 27.6 Every read of an order carries its discount ──────────────────
    st, r = order({"discount_code": "S27SAVE", "items": items})
    placed = (r or {}).get("data") or {}
    st_q, r_q = call("GET", "/artists/salon/orders", None, owner_tok)
    mine = [x for x in ((r_q or {}).get("data") or []) if x.get("id") == placed.get("id")]
    q = mine[0] if mine else {}
    ok = (st == 201 and placed.get("discount_code") == "S27SAVE" and q.get("discount_code") == "S27SAVE"
          and str(q.get("discount_amount")) in ("10", "10.00") and str(q.get("total_amount")) in ("30", "30.00"))
    FRAUD20_ARGS.extend([salon, p2])
    rec("27.6", "PASS" if ok else "FAIL",
        f"placed: total {placed.get('total_amount')} code {placed.get('discount_code')}; "
        f"artist queue: items {[i.get('subtotal') for i in q.get('items', [])]}, "
        f"discount {q.get('discount_amount')} {q.get('discount_code')}, total {q.get('total_amount')}")


def fraud20(salon, product):
    """Guess codes through both previews, alternating, until refused.

    Before middleware.NewPromoCodeAttempts this answered 482 guesses in
    0.3 s. The budget is per address per 10-minute window and 27.7 has
    already spent some of it, so the count here is at most 20, not exactly.
    """
    import secrets
    answered, refusal = 0, None
    for i in range(40):
        code = "G" + secrets.token_hex(3).upper()
        if i % 2 == 0:
            st, r = call("POST", "/orders/discount-preview",
                         {"salon_id": salon, "code": code, "items": [{"product_id": product, "quantity": 1}]})
        else:
            st, r = call("POST", f"/bookings/{uuid.uuid4()}/discount-preview", {"code": code})
        if st == 429:
            refusal = (i, err(r))
            break
        answered += 1
    other, _ = call("POST", "/orders/discount-preview" if refusal and refusal[0] % 2 else
                    f"/bookings/{uuid.uuid4()}/discount-preview", {"code": "X"})
    rest, _ = call("GET", "/discovery/artists?q=zzzz")
    ok = (refusal is not None and refusal[1] == "TOO_MANY_CODE_ATTEMPTS" and answered <= 20
          and other == 429 and rest == 200)
    rec("FRAUD-20", "PASS" if ok else "FAIL",
        f"{answered} guesses answered across both previews, then {refusal}; the other preview "
        f"-> {other}; the rest of the API -> {rest}")


def teardown():
    tagged_salons = "SELECT id FROM salons WHERE name LIKE 's27%'"
    # Only the guest customers (+961763699xx) are hard-deleted. The two
    # artists are soft-deleted by cleanup(), as every harness does, because
    # audit_events keep pointing at them.
    phones = "SELECT id FROM users WHERE phone LIKE '+961763699%'"
    for q in [
        f"DELETE FROM discount_redemptions WHERE discount_id IN (SELECT id FROM discounts WHERE salon_id IN ({tagged_salons}))",
        f"DELETE FROM order_items WHERE order_id IN (SELECT id FROM orders WHERE salon_id IN ({tagged_salons}))",
        f"DELETE FROM orders WHERE salon_id IN ({tagged_salons})",
        f"DELETE FROM products WHERE salon_id IN ({tagged_salons})",
        f"DELETE FROM discounts WHERE salon_id IN ({tagged_salons})",
        "DELETE FROM waitlist_entries WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE 's27.%')",
        f"DELETE FROM notifications WHERE user_id IN ({phones})",
        f"DELETE FROM bookings WHERE customer_id IN ({phones})",
    ]:
        try:
            c.sql(q)
        except RuntimeError as e:
            print(f"  cleanup: {e}")
    c.cleanup()   # salons, stores, services, artists, subscriptions, tagged users
    for q in [f"DELETE FROM refresh_tokens WHERE user_id IN ({phones})", f"DELETE FROM users WHERE id IN ({phones})"]:
        try:
            c.sql(q)
        except RuntimeError as e:
            print(f"  cleanup: {e}")
    left = c.sql(f"""SELECT (SELECT count(*) FROM salons WHERE name LIKE 's27%')
                   + (SELECT count(*) FROM users WHERE phone LIKE '+961763699%')
                   + (SELECT count(*) FROM users WHERE email LIKE 's27.%' AND deleted_at IS NULL)
                   + (SELECT count(*) FROM products WHERE name LIKE 's27%')
                   + (SELECT count(*) FROM discounts WHERE code LIKE 'S27%')""")
    print(f"  cleanup (suite 27 rows): {left} residual{'' if left == '0' else '   !! NOT CLEAN !!'}")


if __name__ == "__main__":
    print("\n  E2E suite 27 + 16.5 + FRAUD-21/22\n")
    try:
        main()
        if "--fraud20" in sys.argv and FRAUD20_ARGS:
            fraud20(*FRAUD20_ARGS)
    except Exception as e:  # a harness failure is reported, never hidden
        rec("harness", "FAIL", f"{type(e).__name__}: {e}")
    finally:
        teardown()
    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    print(f"\n  {p} pass, {f} fail\n")
    sys.exit(1 if f else 0)
