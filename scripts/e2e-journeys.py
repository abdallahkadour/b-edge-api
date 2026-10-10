#!/usr/bin/env python3
"""E2E journeys 2-16 - the behaviour half, against the live API.

E2E-TEST-PLAN.md Suites 1-16 were written as manual journeys and were last
walked by hand before most of the product changed around them. This drives
what they assert about BEHAVIOUR through the real API; the screen half is
b-edge-web/scripts/e2e-journeys-ui.mjs. Each case records what it measured.

Builds its own salon under the tag below and tears it down with
chaos-booking.py's cleanup() - one teardown for every harness - plus the
rows that cleanup does not know about (products, orders, waitlist, reviews).

Customers sign in with the dev bypass code through verify-otp directly, so
no code is ever REQUESTED and nothing is queued to anyone's phone. Needs the
API built with -tags devbypass (make dev).

  python3 scripts/e2e-journeys.py            # every suite
  python3 scripts/e2e-journeys.py 2 3        # just these
"""
import importlib.util
import os
import sys
import time
from datetime import date, datetime, timedelta, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("chaos", os.path.join(HERE, "chaos-booking.py"))
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)
c.TAG = "j2"

RESULTS = []
CUSTOMER_PHONE = "+961763698{:02d}"   # cleanup() clears customer_otps under +9617636%


def rec(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<5} {tid:<14} {detail}")


_raw_call = c.call


def _paced_call(method, path_, body=None, token=None):
    """chaos.call, but it waits out the GENERAL rate limit instead of
    reporting it. Two full runs inside five minutes exceed the API's own
    600-requests-per-5-minutes budget for localhost, and every later check
    then fails in ways that look like defects (measured 2026-09-29: empty
    slot lists, a "saved" setting that read back 0). Only
    RATE_LIMIT_EXCEEDED is waited out; any other 429 - TOO_MANY_HOLDS,
    TOO_MANY_CODE_ATTEMPTS - is an answer the suites assert on."""
    for _ in range(12):
        st, r = _raw_call(method, path_, body, token)
        if st == 429 and c.err(r) == "RATE_LIMIT_EXCEEDED":
            print("    (request limit reached - waiting 30s for the window)")
            time.sleep(30)
            continue
        return st, r
    return st, r


c.call = _paced_call   # chaos helpers (register, onboard, ...) use it too
call, err, sql = c.call, c.err, c.sql


def data(r):
    return (r or {}).get("data") or {}


def customer_token(n):
    """A signed-in customer through the bypass code - no code is requested."""
    st, r = call("POST", "/customer-auth/verify-otp", {"phone": CUSTOMER_PHONE.format(n), "code": "000000"})
    if st >= 400:
        raise RuntimeError(f"customer sign-in: {st} {err(r)} (is the API built with -tags devbypass?)")
    return data(r).get("access_token")


def slot_times(artist, store, service, day):
    st, r = call("GET", f"/bookings/slots?artist_id={artist}&store_id={store}&service_id={service}&date={day}")
    d = (r or {}).get("data") or []
    if isinstance(d, dict):
        d = d.get("slots") or []
    return st, [s["start_time"] for s in d]


def next_weekday(start_offset=3):
    """A Monday-Friday date a few days out, so weekday rules (travel buffer
    150 min) apply."""
    d = date.today() + timedelta(days=start_offset)
    while d.weekday() >= 5:
        d += timedelta(days=1)
    return d


class World:
    """One owner, her salon, its first store and service, built through the
    real onboarding and admin-approval path."""

    def __init__(self):
        self.admin = c.login(c.ADMIN)
        self.email = "j2.owner@test.bedge.com"
        c.register("j2 Owner", self.email, "+96176368901")
        self.artist = c.onboard(self.email, "j2 Salon", "j2-owner")
        c.approve_artist(self.artist, self.admin)
        if sql(f"SELECT count(*) FROM subscriptions WHERE artist_id='{self.artist}'") == "0":
            sql(f"INSERT INTO subscriptions (artist_id, plan_code, monthly_price) VALUES ('{self.artist}','comped',0)")
        self.salon = sql(f"SELECT salon_id FROM artists WHERE id='{self.artist}'")
        self.store = sql(f"SELECT id FROM stores WHERE salon_id='{self.salon}' ORDER BY created_at LIMIT 1")
        self.service = sql(f"SELECT id FROM services WHERE salon_id='{self.salon}' LIMIT 1")
        self.tok = c.login(self.email)
        self.store_b = None


# ── Suite 2 — stores, hours, services ─────────────────────────────────────

def suite2(w):
    # 2.1 a second store
    st, r = call("POST", "/artists/salon/stores", {"name": "j2 Hamra", "city": "Beirut"}, w.tok)
    w.store_b = data(r).get("id")
    _, r2 = call("GET", "/artists/salon/stores", None, w.tok)
    names = [s.get("name") for s in (r2.get("data") or [])]
    rec("2.1", "PASS" if st == 201 and "j2 Hamra" in names and len(names) == 2 else "FAIL",
        f"add store -> {st}; the salon's stores: {names}")

    # 2.2 hours, and the two edge cases
    ok_days = []
    for store in (w.store, w.store_b):
        for dow in range(7):
            s_, r_ = call("POST", f"/artists/stores/{store}/hours",
                          {"day_of_week": dow, "open_time": "09:00:00", "close_time": "18:00:00", "is_open": dow != 0}, w.tok)
            ok_days.append(s_ < 400)
    st_bad, r_bad = call("POST", f"/artists/stores/{w.store}/hours",
                         {"day_of_week": 2, "open_time": "18:00:00", "close_time": "09:00:00", "is_open": True}, w.tok)
    st_short, r_short = call("POST", f"/artists/stores/{w.store}/hours",
                             {"day_of_week": 2, "open_time": "09:00", "close_time": "18:00", "is_open": True}, w.tok)
    stored = sql(f"SELECT open_time::text || '-' || close_time::text FROM business_hours WHERE store_id='{w.store}' AND day_of_week=2")
    rec("2.2", "PASS" if all(ok_days) else "FAIL", f"14 day rows set for 2 stores: {sum(ok_days)}/14")
    rec("2.2 open>close", "PASS" if st_bad in (400, 422) else "FAIL",
        f"open 18:00 close 09:00 -> {st_bad} {err(r_bad)}: \"{((r_bad or {}).get('error') or {}).get('message', '')[:70]}\"")
    rec("2.2 HH:MM", "PASS" if (st_short < 400 and stored == "09:00:00-18:00:00") or st_short in (400, 422) else "FAIL",
        f"'09:00' instead of '09:00:00' -> {st_short} {err(r_short)}; stored: {stored} (accepted and normalised, or refused - never stored malformed)")

    # 2.3 an exception closes the day, and deleting it reopens it
    day = next_weekday(4).isoformat()
    _, before = slot_times(w.artist, w.store, w.service, day)
    st_e, _ = call("POST", f"/artists/stores/{w.store}/exceptions", {"exception_date": day, "is_closed": True, "reason": "j2"}, w.tok)
    _, during = slot_times(w.artist, w.store, w.service, day)
    st_d, _ = call("DELETE", f"/artists/stores/{w.store}/exceptions/{day}", None, w.tok)
    _, after = slot_times(w.artist, w.store, w.service, day)
    rec("2.3", "PASS" if before and not during and after == before and st_e < 400 and st_d < 400 else "FAIL",
        f"{day}: {len(before)} times -> closed {len(during)} -> exception deleted {len(after)}")

    # 2.4 a service: add, edit, delete - on the dashboard and the public profile
    st, r = call("POST", "/artists/salon/services", {"name": "j2 Lashes", "duration_min": 45, "price": "80.00",
                 "deposit_amount": "20.00", "deposit_deadline_hours": 24}, w.tok)
    sid = data(r).get("id")

    def pub():
        return {s.get("name"): s for s in ((call("GET", f"/artists/{w.artist}/services")[1].get("data")) or [])}
    shown = pub().get("j2 Lashes") or {}
    st_p, _ = call("PATCH", f"/artists/salon/services/{sid}", {"price": "95.00", "duration_min": 60}, w.tok)
    edited = pub().get("j2 Lashes") or {}
    # history: a past completed booking on it must survive the delete
    cust = sql("SELECT id FROM users WHERE role='customer' AND deleted_at IS NULL ORDER BY created_at LIMIT 1")
    past = c.mkbooking(w.artist, w.salon, w.store, sid, cust, datetime.now(timezone.utc) - timedelta(days=10),
                       status="completed")
    st_del, r_del = call("DELETE", f"/artists/salon/services/{sid}", None, w.tok)
    gone = "j2 Lashes" not in pub()
    kept = sql(f"SELECT count(*) FROM bookings WHERE id='{past}' AND service_id='{sid}'")
    rec("2.4 add", "PASS" if st == 201 and shown else "FAIL",
        f"add -> {st}; on the public profile: {bool(shown)} (price {shown.get('price')})")
    rec("2.4 edit", "PASS" if st_p < 400 and str(edited.get("price")) in ("95", "95.00") and edited.get("duration_min") == 60 else "FAIL",
        f"edit -> {st_p}; public now {edited.get('price')} / {edited.get('duration_min')} min")
    rec("2.4 delete", "PASS" if st_del < 400 and gone and kept == "1" else "FAIL",
        f"delete -> {st_del} {err(r_del)}; gone from the profile: {gone}; past booking kept: {kept == '1'}")

    # edge: a service with a FUTURE confirmed booking
    st, r = call("POST", "/artists/salon/services", {"name": "j2 Brows", "duration_min": 30, "price": "40.00",
                 "deposit_amount": "0", "deposit_deadline_hours": 24}, w.tok)
    sid2 = data(r).get("id")
    future = c.mkbooking(w.artist, w.salon, w.store, sid2, cust, datetime.now(timezone.utc) + timedelta(days=9, hours=2))
    st_del2, r_del2 = call("DELETE", f"/artists/salon/services/{sid2}", None, w.tok)
    booking_ok = sql(f"SELECT status FROM bookings WHERE id='{future}'")
    defensible = (st_del2 >= 400) or (booking_ok == "confirmed")
    rec("2.4 future", "PASS" if defensible else "FAIL",
        f"delete with a future confirmed booking -> {st_del2} {err(r_del2)}; the booking is still '{booking_ok}'")
    sql(f"DELETE FROM bookings WHERE id IN ('{past}','{future}')")


# ── Suite 3 — guest books, end to end ─────────────────────────────────────

def suite3(w):
    # 3.1 discover by city and category, then the handle resolves
    _, r = call("GET", "/discovery/artists?city=Beirut&category=makeup&q=j2%20Owner")
    found = any(a.get("id") == w.artist for a in (r.get("data") or []))
    st_h, r_h = call("GET", "/artists/j2-owner")
    rec("3.1", "PASS" if found and st_h == 200 and data(r_h).get("id") == w.artist else "FAIL",
        f"Discover (city+category+name) lists her: {found}; /artists/j2-owner -> {st_h}")

    day = next_weekday(5).isoformat()
    _, times = slot_times(w.artist, w.store, w.service, day)
    if len(times) < 6:
        rec("3.x", "FAIL", f"setup: only {len(times)} open times on {day}")
        return
    t0 = times[0]

    # 3.2 a hold, and a second hold on the same time
    st, r = call("POST", "/bookings/guest/hold", {"artist_id": w.artist, "store_id": w.store, "service_id": w.service, "start_time": t0})
    b = data(r).get("booking_id")
    row = sql(f"SELECT status || '|' || round(extract(epoch FROM held_until - NOW())/60) FROM bookings WHERE id='{b}'") if b else ""
    st2, r2 = call("POST", "/bookings/guest/hold", {"artist_id": w.artist, "store_id": w.store, "service_id": w.service, "start_time": t0})
    rec("3.2", "PASS" if st == 201 and row.startswith("held|") and 8 <= int(row.split("|")[1]) <= 10 else "FAIL",
        f"hold -> {st}; row status|minutes left = {row}")
    rec("3.2 twice", "PASS" if st2 == 409 else "FAIL", f"same time held again -> {st2} {err(r2)}")

    # 3.3 details submitted -> pending; an expired hold cannot be submitted
    st, r = call("PATCH", f"/bookings/guest/{b}/submit", {"name": "j2 Guest", "phone": CUSTOMER_PHONE.format(1)})
    now_status = sql(f"SELECT status FROM bookings WHERE id='{b}'")
    rec("3.3", "PASS" if st < 400 and now_status == "pending" else "FAIL", f"submit -> {st} {err(r)}; status {now_status}")
    # The day's LAST start: consecutive starts overlap a 60-minute booking
    # (the same harness mistake e2e-suite27.py made first).
    st, r = call("POST", "/bookings/guest/hold", {"artist_id": w.artist, "store_id": w.store, "service_id": w.service, "start_time": times[-1]})
    bx = data(r).get("booking_id")
    if not bx:
        raise RuntimeError(f"3.3 setup: second hold {st} {err(r)}")
    sql(f"UPDATE bookings SET held_until = NOW() - interval '1 minute' WHERE id='{bx}'")
    st_x, r_x = call("PATCH", f"/bookings/guest/{bx}/submit", {"name": "j2 Late", "phone": CUSTOMER_PHONE.format(2)})
    rec("3.3 expired", "PASS" if st_x in (409, 410, 400) else "FAIL",
        f"submitting a lapsed hold -> {st_x} {err(r_x)} (the funnel turns this into \"choose again\")")

    # 3.4 approve (deposit deadline set), then the transfer lands
    st, r = call("PATCH", f"/bookings/{b}/approve", {}, w.tok)
    ap = sql(f"SELECT status || '|' || (deposit_deadline IS NOT NULL) || '|' || deposit_amount FROM bookings WHERE id='{b}'")
    st2, r2 = call("PATCH", f"/bookings/{b}/confirm-payment", {"reference": "OMT-J2-1"}, w.tok)
    fin = sql(f"SELECT status FROM bookings WHERE id='{b}'")
    deposit_needed = ap.split("|")[2] not in ("0", "0.00")
    rec("3.4", "PASS" if st < 400 and ap.startswith("approved") and (ap.split("|")[1] == "true" or not deposit_needed)
        and st2 < 400 and fin == "confirmed" else "FAIL",
        f"approve -> {st} (status|deadline set|deposit = {ap}); payment confirmed -> {st2} {err(r2)}, now {fin}")

    # 3.5 travel buffer between the salon's two stores (Suite 2 makes the
    # second store; run alone, this suite makes its own)
    if not w.store_b:
        st_s, r_s = call("POST", "/artists/salon/stores", {"name": "j2 Hamra", "city": "Beirut"}, w.tok)
        w.store_b = data(r_s).get("id")
        for dow in range(7):
            call("POST", f"/artists/stores/{w.store_b}/hours",
                 {"day_of_week": dow, "open_time": "09:00:00", "close_time": "18:00:00", "is_open": dow != 0}, w.tok)
    if not w.store_b:
        rec("3.5", "FAIL", "setup: could not create a second store")
    else:
        linked = sql(f"SELECT count(*) FROM artist_stores WHERE artist_id='{w.artist}' AND store_id='{w.store_b}'")
        if linked == "0":
            sql(f"INSERT INTO artist_stores (artist_id, store_id) VALUES ('{w.artist}','{w.store_b}')")
        # strictly after `day`: next_weekday(5) and next_weekday(6) can both
        # land on the same Monday, and the earlier booking there collided
        # and Monday-Thursday only: the product's weekend is Friday-Sunday
        # (Lebanon; booking/slots.go), when the buffer is 90 minutes, not 150.
        # Skipping only Sat/Sun failed this case on 2026-10-10, when the day
        # landed on a Friday and the product correctly used 90.
        bday = date.fromisoformat(day) + timedelta(days=1)
        while bday.weekday() >= 4:
            bday += timedelta(days=1)
        cust = sql("SELECT id FROM users WHERE role='customer' AND deleted_at IS NULL ORDER BY created_at LIMIT 1")
        offset = "+03:00" if 4 <= bday.month <= 10 else "+02:00"
        ba = c.mkbooking(w.artist, w.salon, w.store, w.service, cust, datetime.fromisoformat(f"{bday}T09:00:00{offset}"))
        _, b_times = slot_times(w.artist, w.store_b, w.service, bday.isoformat())
        end_a = sql(f"SELECT to_char(end_time AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS') FROM bookings WHERE id='{ba}'")
        end_dt = datetime.fromisoformat(end_a + "+00:00")
        inside = [t for t in b_times if datetime.fromisoformat(t.replace("Z", "+00:00")) < end_dt + timedelta(minutes=150)]
        outside = [t for t in b_times if datetime.fromisoformat(t.replace("Z", "+00:00")) >= end_dt + timedelta(minutes=150)]
        rec("3.5", "PASS" if not inside and outside else "FAIL",
            f"store A booked until {end_a}Z (store B linked by the harness: {linked == '0'}); store B offers "
            f"{len(inside)} times inside the 150-min travel window, {len(outside)} after it")
        sql(f"DELETE FROM bookings WHERE id='{ba}'")

    # 3.6 complete -> review request with a token -> review -> shown, rating moves
    sql(f"UPDATE bookings SET start_time = NOW() - interval '3 hours', end_time = NOW() - interval '2 hours', "
        f"blocked_until = NOW() - interval '2 hours' WHERE id='{b}'")
    st, r = call("PATCH", f"/bookings/{b}/complete", {}, w.tok)
    token = sql(f"SELECT review_token FROM bookings WHERE id='{b}'")
    queued = sql(f"SELECT count(*) FROM notifications WHERE booking_id='{b}' AND template_name ILIKE '%review%'")
    st_g, r_g = call("GET", f"/reviews/by-token/{token}")
    st_p, r_p = call("POST", f"/reviews/by-token/{token}", {"rating": 5, "comment": "j2 lovely"})
    _, pubr = call("GET", f"/public/reviews/artist/{w.artist}")
    listed = any((x.get("comment") == "j2 lovely") for x in (pubr.get("data") or []))
    rating = data(call("GET", f"/artists/{w.artist}")[1]).get("rating")
    st_again, r_again = call("GET", f"/reviews/by-token/{token}")
    st_post2, r_post2 = call("POST", f"/reviews/by-token/{token}", {"rating": 1})
    st_bad, r_bad = call("GET", "/reviews/by-token/not-a-real-token")
    rec("3.6", "PASS" if st < 400 and token and queued != "0" and st_g == 200 and st_p in (200, 201) and listed
        and str(rating) not in ("0", "0.0", "None") else "FAIL",
        f"complete -> {st}; token set: {bool(token)}; review request queued: {queued}; review -> {st_p} {err(r_p)}; "
        f"on the public list: {listed}; artist rating now {rating}")
    rec("3.6 again", "PASS" if st_post2 == 409 else "FAIL",
        f"reopen -> {st_again} (already_reviewed={data(r_again).get('already_reviewed')}); a second review -> {st_post2} {err(r_post2)}")
    rec("3.6 bad token", "PASS" if st_bad == 404 else "FAIL", f"garbage token -> {st_bad} {err(r_bad)}")

    # 3.7 cancellation both ways. The customer signs in by the bypass code.
    ctok = customer_token(3)
    cid = sql(f"SELECT id FROM users WHERE phone='{CUSTOMER_PHONE.format(3)}' AND deleted_at IS NULL")
    late = c.mkbooking(w.artist, w.salon, w.store, w.service, cid, datetime.now(timezone.utc) + timedelta(hours=10))
    sql(f"UPDATE bookings SET deposit_paid_at = NOW() - interval '1 day' WHERE id='{late}'")
    st, r = call("PATCH", f"/bookings/{late}/cancel", {"reason": "j2 late"}, ctok)
    late_status = sql(f"SELECT status FROM bookings WHERE id='{late}'")
    early = c.mkbooking(w.artist, w.salon, w.store, w.service, cid, datetime.now(timezone.utc) + timedelta(days=4))
    sql(f"UPDATE bookings SET deposit_paid_at = NOW() - interval '1 day' WHERE id='{early}'")
    st_a, r_a = call("PATCH", f"/bookings/{early}/cancel", {"reason": "j2 artist"}, w.tok)
    early_status = sql(f"SELECT status FROM bookings WHERE id='{early}'")
    rec("3.7 customer", "PASS" if st < 400 and late_status == "cancelled" else "FAIL",
        f"customer cancels a paid booking 10 h out (inside 24 h) -> {st} {err(r)}; status {late_status}")
    rec("3.7 artist", "PASS" if st_a < 400 and early_status == "refund_due" else "FAIL",
        f"artist cancels a paid booking -> {st_a}; status {early_status}")

    # 3.8 no-show on a past confirmed booking, visible to the customer
    gone = c.mkbooking(w.artist, w.salon, w.store, w.service, cid, datetime.now(timezone.utc) - timedelta(hours=5))
    st, r = call("PATCH", f"/bookings/{gone}/no-show", {}, w.tok)
    _, mine = call("GET", "/bookings/customer/me", None, ctok)
    seen = [x.get("status") for x in (mine.get("data") or []) if x.get("id") == gone]
    rec("3.8", "PASS" if st < 400 and seen == ["no_show"] else "FAIL",
        f"no-show -> {st} {err(r)}; in her My Bookings as: {seen}")

    # 3.9 the waitlist
    st, r = call("POST", "/bookings/waitlist", {"artist_id": w.artist, "store_id": w.store, "service_id": w.service,
                 "requested_date": day, "name": "j2 Waiter", "phone": CUSTOMER_PHONE.format(4)})
    _, wl = call("GET", f"/bookings/artist/{w.artist}/waitlist", None, w.tok)
    on_list = any(x.get("customer_name") == "j2 Waiter" for x in (wl.get("data") or []))
    rec("3.9", "PASS" if st == 201 and on_list else "FAIL", f"join -> {st}; on the artist's Waitlist: {on_list}")


def upload(path_, filename, content, content_type, token):
    """multipart/form-data by hand - urllib has no helper, and this is the
    one call in the harness that needs it."""
    import urllib.request
    import urllib.error
    import json as _json
    boundary = "----j2boundary"
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\n"
            f"Content-Type: {content_type}\r\n\r\n").encode() + content + f"\r\n--{boundary}--\r\n".encode()
    req = urllib.request.Request(c.API + path_, data=body, method="POST", headers={
        "Content-Type": f"multipart/form-data; boundary={boundary}", "Authorization": "Bearer " + token})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, _json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, _json.loads(raw or b"{}")
        except ValueError:
            return e.code, {}


# ── Suite 4 — product store, guest checkout to delivery ───────────────────

def suite4(w):
    # 4.1 a product, live in the shop; a disguised non-image is refused server-side
    st, r = call("POST", "/artists/salon/products", {"name": "j2 Serum", "category": "skincare", "price": "25.00",
                 "stock_quantity": 5}, w.tok)
    pid = data(r).get("id")
    shop = lambda: {p.get("id"): p for p in (call("GET", f"/salons/{w.salon}/products")[1].get("data") or [])}
    st_u, r_u = upload("/media/upload", "fake.jpg", b"#!/bin/sh\necho not an image\n", "image/jpeg", w.tok)
    rec("4.1", "PASS" if st == 201 and pid in shop() else "FAIL", f"add product -> {st}; live in the shop: {pid in shop()}")
    rec("4.1 fake jpg", "PASS" if 400 <= st_u < 500 and err(r_u) else "FAIL",
        f"a shell script named .jpg -> {st_u} {err(r_u)}: \"{((r_u or {}).get('error') or {}).get('message', '')[:80]}\" "
        f"(no real image is uploaded by this harness: that would go to Cloudinary)")

    order_body = lambda qty, phone_n=5: {"salon_id": w.salon, "name": "j2 Buyer", "phone": CUSTOMER_PHONE.format(phone_n),
                                         "delivery_lat": 33.8938, "delivery_lng": 35.5018, "items": [{"product_id": pid, "quantity": qty}]}
    # 4.2 sold out, and back
    call("PATCH", f"/artists/salon/products/{pid}", {"stock_quantity": 0}, w.tok)
    sold_out = shop().get(pid, {}).get("stock_quantity")
    st_o, r_o = call("POST", "/orders", order_body(1))
    call("PATCH", f"/artists/salon/products/{pid}", {"stock_quantity": 5}, w.tok)
    back = shop().get(pid, {}).get("stock_quantity")
    rec("4.2", "PASS" if sold_out == 0 and st_o == 409 and back == 5 else "FAIL",
        f"stock 0 -> shop shows {sold_out}, an order -> {st_o} {err(r_o)}; restocked -> {back}")

    # 4.3 deactivate: gone from the shop, still on the dashboard
    call("PATCH", f"/artists/salon/products/{pid}", {"is_active": False}, w.tok)
    in_shop = pid in shop()
    mine = {p.get("id"): p for p in (call("GET", "/artists/salon/products", None, w.tok)[1].get("data") or [])}
    call("PATCH", f"/artists/salon/products/{pid}", {"is_active": True}, w.tok)
    rec("4.3", "PASS" if not in_shop and pid in mine and mine[pid].get("is_active") is False else "FAIL",
        f"inactive: in the shop {in_shop}; on the dashboard {pid in mine} (is_active {mine.get(pid, {}).get('is_active')})")

    # 4.4 a guest order with a real pin
    st, r = call("POST", "/orders", order_body(2))
    oid = data(r).get("id")
    pin = sql(f"SELECT delivery_lat || ',' || delivery_lng FROM orders WHERE id='{oid}'") if oid else ""
    rec("4.4", "PASS" if st == 201 and pin == "33.8938,35.5018" else "FAIL", f"order -> {st}; stored pin {pin}")

    # 4.5 fulfilment, as the customer sees it after each step
    ctok = customer_token(5)
    def her_status():
        return [o.get("status") for o in (call("GET", "/orders/me", None, ctok)[1].get("data") or []) if o.get("id") == oid]
    steps = []
    for action in ("confirm-payment", "ship", "deliver"):
        s_, r_ = call("PATCH", f"/artists/salon/orders/{oid}/{action}", {}, w.tok)
        steps.append((action, s_, her_status()))
    rec("4.5", "PASS" if [x[2] for x in steps] == [["confirmed"], ["shipped"], ["delivered"]] and all(x[1] < 400 for x in steps) else "FAIL",
        f"what she sees after each step: {[(a, s, st_) for a, s, st_ in steps]}")

    # 4.6 she can cancel a placed order, not a shipped one
    _, r1 = call("POST", "/orders", order_body(1))
    placed = data(r1).get("id")
    st_c, r_c = call("PATCH", f"/orders/{placed}/cancel", {"reason": "j2 changed mind"}, ctok)
    _, r2 = call("POST", "/orders", order_body(1))
    shipped = data(r2).get("id")
    call("PATCH", f"/artists/salon/orders/{shipped}/confirm-payment", {}, w.tok)
    call("PATCH", f"/artists/salon/orders/{shipped}/ship", {}, w.tok)
    st_s, r_s = call("PATCH", f"/orders/{shipped}/cancel", {}, ctok)
    rec("4.6", "PASS" if st_c < 400 and her_status_of(ctok, placed) == "cancelled" and st_s >= 400 else "FAIL",
        f"cancel placed -> {st_c}, now {her_status_of(ctok, placed)}; cancel shipped -> {st_s} {err(r_s)}")


def her_status_of(ctok, oid):
    return next((o.get("status") for o in (call("GET", "/orders/me", None, ctok)[1].get("data") or []) if o.get("id") == oid), None)


# ── Suite 6 — client CRM ──────────────────────────────────────────────────

def suite6(w):
    a_id = sql(f"SELECT id FROM users WHERE phone='{CUSTOMER_PHONE.format(6)}' AND deleted_at IS NULL")
    if not a_id:
        customer_token(6)
        a_id = sql(f"SELECT id FROM users WHERE phone='{CUSTOMER_PHONE.format(6)}' AND deleted_at IS NULL")
    customer_token(7)
    b_id = sql(f"SELECT id FROM users WHERE phone='{CUSTOMER_PHONE.format(7)}' AND deleted_at IS NULL")
    ids = [c.mkbooking(w.artist, w.salon, w.store, w.service, a_id, datetime.now(timezone.utc) - timedelta(days=d), status="completed")
           for d in (20, 13)]
    ids.append(c.mkbooking(w.artist, w.salon, w.store, w.service, b_id, datetime.now(timezone.utc) - timedelta(days=16), status="completed"))
    _, r = call("GET", "/clients", None, w.tok)
    rows = {x.get("customer_id"): x for x in (r.get("data") or [])}
    rec("6.1", "PASS" if rows.get(a_id, {}).get("bookings_count") == 2 and rows.get(b_id, {}).get("bookings_count") == 1 else "FAIL",
        f"clients listed with visits: A {rows.get(a_id, {}).get('bookings_count')}, B {rows.get(b_id, {}).get('bookings_count')}")
    _, r = call("GET", f"/clients/{a_id}", None, w.tok)
    hist = data(r).get("history") or []
    hist_ids = {h.get("booking_id") or h.get("id") for h in hist}
    rec("6.2", "PASS" if hist_ids == set(ids[:2]) else "FAIL",
        f"A's history has {len(hist)} bookings, only hers: {hist_ids == set(ids[:2])}")
    st1, _ = call("PUT", f"/clients/{a_id}/notes", {"content": "j2 prefers mornings"}, w.tok)
    st2, _ = call("PUT", f"/clients/{a_id}/notes", {"content": "j2 allergic to latex"}, w.tok)
    note = data(call("GET", f"/clients/{a_id}", None, w.tok)[1]).get("note")
    rec("6.3", "PASS" if st1 < 400 and st2 < 400 and note == "j2 allergic to latex" else "FAIL",
        f"saved twice; reloaded note: \"{note}\" (replaced, not appended)")


# ── Suite 7 — earnings ────────────────────────────────────────────────────

def suite7(w):
    # A fixed past month, so nothing else of hers can land in it.
    cust = sql("SELECT id FROM users WHERE role='customer' AND deleted_at IS NULL ORDER BY created_at LIMIT 1")
    made = []
    for day_, status, price in ((3, "completed", "120.00"), (10, "completed", "80.50"), (17, "no_show", "60.00"), (24, "cancelled", "999.00")):
        b = c.mkbooking(w.artist, w.salon, w.store, w.service, cust, datetime(2025, 3, day_, 9, 0, tzinfo=timezone.utc), status=status)
        sql(f"UPDATE bookings SET final_price={price}, original_price={price} WHERE id='{b}'")
        made.append(b)
    _, r = call("GET", "/earnings/summary?from=2025-03-01&to=2025-03-31", None, w.tok)
    d = data(r)
    expected = sql(f"SELECT SUM(final_price) FROM bookings WHERE id IN ({','.join(repr(x) for x in made)}) AND status IN ('completed','no_show')")
    rec("7.1", "PASS" if str(d.get("total_revenue")) in (expected, expected.rstrip("0").rstrip(".")) and d.get("total_bookings") == 3 else "FAIL",
        f"March 2025: summary {d.get('total_revenue')} over {d.get('total_bookings')} bookings; hand sum of completed + no-show "
        f"= {expected} over 3 (the cancelled $999 must not count)")


# ── Suite 8 — billing and enforcement ─────────────────────────────────────

def suite8(w):
    sub = sql(f"SELECT id FROM subscriptions WHERE artist_id='{w.artist}' ORDER BY created_at LIMIT 1")
    # 8.1 is about a COMPED artist, the plan's "normal state for all current
    # artists". A newly approved artist starts on a Solo trial instead, so
    # make her comped first - the first run read her trial as a failure.
    first_plan = sql(f"SELECT plan_code FROM subscriptions WHERE id='{sub}'")
    sql(f"UPDATE subscriptions SET plan_code='comped', monthly_price=0 WHERE id='{sub}'")
    _, r = call("GET", "/billing/subscription", None, w.tok)
    rec("8.1", "PASS" if data(r).get("status") == "active" and data(r).get("plan_code") == "comped" else "FAIL",
        f"comped artist: status {data(r).get('status')}, plan {data(r).get('plan_name')} "
        f"(a newly approved artist starts on '{first_plan}')")
    _, r = call("GET", "/billing/plans")
    codes = sorted(p.get("code") for p in (r.get("data") or []))
    rec("8.2", "PASS" if codes == ["multi", "salon", "solo", "studio"] else "FAIL", f"public plans: {codes}")

    # 8.3-8.5, 8.10 - a paid plan whose period ran out generates an invoice
    sql(f"UPDATE subscriptions SET plan_code='solo', monthly_price=(SELECT monthly_price FROM plans WHERE code='solo'), "
        f"current_period_end=NOW() - interval '3 days', trial_ends_at=NULL WHERE id='{sub}'")
    call("GET", "/billing/subscription", None, w.tok)
    _, r = call("GET", "/billing/invoices", None, w.tok)
    inv = [i for i in (r.get("data") or []) if i.get("status") in ("open", "pending", "unpaid", "issued")] or (r.get("data") or [])
    if not inv:
        rec("8.3", "FAIL", "setup: no invoice was generated for an expired paid period")
        return
    iid = inv[0].get("id")
    st, r = call("POST", f"/billing/invoices/{iid}/submit", {"payment_reference": "OMT-J2-INV"}, w.tok)
    after_submit = sql(f"SELECT status FROM invoices WHERE id='{iid}'")
    rec("8.3", "PASS" if st < 400 and after_submit == "submitted" else "FAIL", f"submit reference -> {st} {err(r)}; invoice now {after_submit}")
    st, r = call("POST", f"/admin/billing/invoices/{iid}/confirm", {}, w.admin)
    paid = sql(f"SELECT status FROM invoices WHERE id='{iid}'")
    period = sql(f"SELECT current_period_end > NOW() FROM subscriptions WHERE id='{sub}'")
    rec("8.4", "PASS" if st < 400 and paid == "paid" and period == "t" else "FAIL",
        f"admin confirms -> {st} {err(r)}; invoice {paid}; period now runs into the future: {period == 't'}")
    # a second invoice to void
    sql(f"UPDATE subscriptions SET current_period_end=NOW() - interval '2 days' WHERE id='{sub}'")
    call("GET", "/billing/subscription", None, w.tok)
    open_ = sql(f"SELECT id FROM invoices WHERE subscription_id='{sub}' AND status NOT IN ('paid','void') ORDER BY created_at DESC LIMIT 1")
    st_v0, r_v0 = call("POST", f"/admin/billing/invoices/{open_}/void", {"reason": ""}, w.admin) if open_ else (0, {})
    st_v, r_v = call("POST", f"/admin/billing/invoices/{open_}/void", {"reason": "j2 test void"}, w.admin) if open_ else (0, {})
    voided = sql(f"SELECT status FROM invoices WHERE id='{open_}'") if open_ else ""
    rec("8.5", "PASS" if open_ and st_v0 in (400, 422) and st_v < 400 and voided == "void" else "FAIL",
        f"void without a reason -> {st_v0}; with one -> {st_v} {err(r_v)}; invoice {voided}")
    _, r = call("GET", "/billing/invoices", None, w.tok)
    hist = sorted(i.get("status") for i in (r.get("data") or []))
    rec("8.10", "PASS" if "paid" in hist and "void" in hist else "FAIL", f"her invoice history: {hist}")

    # 8.6 past_due: hidden from Discover. 8.7 suspended: cannot write.
    sql(f"UPDATE subscriptions SET current_period_end=NOW() - interval '30 days' WHERE id='{sub}'")
    _, r = call("GET", "/discovery/artists?q=j2%20Owner")
    hidden = not any(a.get("id") == w.artist for a in (r.get("data") or []))
    st_pd, _ = call("POST", "/artists/salon/services", {"name": "j2 PastDue", "duration_min": 30, "price": "10.00",
                    "deposit_amount": "0", "deposit_deadline_hours": 24}, w.tok)
    rec("8.6", "PASS" if hidden and st_pd < 400 else "FAIL",
        f"past_due (30 days): hidden from Discover {hidden}; can still edit her menu -> {st_pd}")
    sql(f"UPDATE subscriptions SET current_period_end=NOW() - interval '50 days' WHERE id='{sub}'")
    st_su, r_su = call("POST", "/artists/salon/services", {"name": "j2 Suspended", "duration_min": 30, "price": "10.00",
                       "deposit_amount": "0", "deposit_deadline_hours": 24}, w.tok)
    st_pr, r_pr = call("POST", "/artists/salon/products", {"name": "j2 Nope", "category": "x", "price": "5.00"}, w.tok)
    rec("8.7", "PASS" if st_su in (402, 403) and st_pr in (402, 403) else "FAIL",
        f"suspended (50 days): add service -> {st_su} {err(r_su)}; add product -> {st_pr} {err(r_pr)}")

    # 8.8 admin plans, 8.9 admin edits a subscription
    code = "j2tier"
    st_c, r_c = call("POST", "/admin/plans", {"code": code, "name": "j2 Tier", "monthly_price": "9.00", "included_seats": 1}, w.admin)
    st_e, r_e = call("PATCH", f"/admin/plans/{code}", {"name": "j2 Tier Renamed", "monthly_price": "11.00"}, w.admin)
    stored = sql(f"SELECT name || '|' || monthly_price FROM plans WHERE code='{code}'")
    rec("8.8", "PASS" if st_c == 201 and st_e < 400 and stored == "j2 Tier Renamed|11.00" else "FAIL",
        f"create -> {st_c} {err(r_c)}; edit -> {st_e} {err(r_e)}; stored {stored}")
    st_s, r_s = call("PATCH", f"/admin/billing/subscriptions/{sub}", {"plan_code": "comped"}, w.admin)
    now_plan = sql(f"SELECT plan_code FROM subscriptions WHERE id='{sub}'")
    _, r = call("GET", "/billing/subscription", None, w.tok)
    rec("8.9", "PASS" if st_s < 400 and now_plan == "comped" and data(r).get("status") == "active" else "FAIL",
        f"admin moves her to comped -> {st_s} {err(r_s)}; plan {now_plan}; her status {data(r).get('status')}")


# ── Suite 9 — open/closed status and map pins ─────────────────────────────

def store_card(w, store_id):
    _, r = call("GET", f"/discovery/artists/{w.artist}")
    return next((s for s in (data(r).get("stores") or []) if s.get("id") == store_id), {})


def set_today(w, store, open_t, close_t, is_open=True):
    return call("POST", f"/artists/stores/{store}/hours",
                {"day_of_week": w.today_dow, "open_time": open_t, "close_time": close_t, "is_open": is_open}, w.tok)


def suite9(w):
    from zoneinfo import ZoneInfo
    now = datetime.now(ZoneInfo("Asia/Beirut"))
    w.today_dow = (now.weekday() + 1) % 7          # Sunday = 0, as business_hours stores it
    hm = lambda dt: dt.strftime("%H:%M:00")

    # 9.1 open now / ended earlier today / opens later today
    set_today(w, w.store, "00:00:00", "23:59:00")
    os_ = store_card(w, w.store).get("open_status") or {}
    rec("9.1 open", "PASS" if os_.get("is_open") and os_.get("closes_at") else "FAIL", f"00:00-23:59 today -> {os_}")
    if now.hour >= 2:
        set_today(w, w.store, "00:00:00", hm(now - timedelta(hours=1)))
        os_ = store_card(w, w.store).get("open_status") or {}
        rec("9.1 ended", "PASS" if not os_.get("is_open") and os_.get("reason") == "outside_hours" and not os_.get("opens_at") else "FAIL",
            f"ended an hour ago -> {os_} (no 'opens' pointing at tomorrow)")
    else:
        rec("9.1 ended", "SKIP", f"it is {now:%H:%M} in Beirut: no window can have ended earlier today")
    if now.hour <= 21:
        set_today(w, w.store, hm(now + timedelta(hours=1)), "23:59:00")
        os_ = store_card(w, w.store).get("open_status") or {}
        rec("9.1 later", "PASS" if not os_.get("is_open") and os_.get("opens_at") else "FAIL", f"opens in an hour -> {os_}")
    else:
        rec("9.1 later", "SKIP", f"it is {now:%H:%M} in Beirut: no window can start later today")

    # 9.3 a holiday and a non-trading weekday look alike but say why
    set_today(w, w.store, "00:00:00", "23:59:00")
    today = now.date().isoformat()
    call("POST", f"/artists/stores/{w.store}/exceptions", {"exception_date": today, "is_closed": True, "reason": "j2"}, w.tok)
    holiday = (store_card(w, w.store).get("open_status") or {})
    call("DELETE", f"/artists/stores/{w.store}/exceptions/{today}", None, w.tok)
    set_today(w, w.store, "09:00:00", "18:00:00", is_open=False)
    weekday = (store_card(w, w.store).get("open_status") or {})
    set_today(w, w.store, "09:00:00", "18:00:00")
    rec("9.3", "PASS" if holiday.get("reason") == "holiday" and weekday.get("reason") == "closed_today"
        and not holiday.get("is_open") and not weekday.get("is_open") else "FAIL",
        f"exception -> {holiday.get('reason')}; weekday off -> {weekday.get('reason')}")

    # 9.4 no hours at all -> unknown, never "closed"
    st, r = call("POST", "/artists/salon/stores", {"name": "j2 NoHours", "city": "Beirut"}, w.tok)
    bare = data(r).get("id")
    sql(f"DELETE FROM business_hours WHERE store_id='{bare}'")
    card = store_card(w, bare)
    rec("9.4", "PASS" if (card.get("open_status") or {}).get("reason") == "unknown" else "FAIL",
        f"a store with no hours rows -> {card.get('open_status')} (the app shows no badge for 'unknown')")

    # 9.5 a failed hours read must not break the profile: the plan names the
    # unit test, because only query-level fault injection can reach it
    import subprocess
    p = subprocess.run(["go", "test", "./internal/discovery/", "-run", "TestBuildStoreCards_HoursReadFails", "-count=1", "-v"],
                       cwd=os.path.dirname(HERE), capture_output=True, text=True)
    ran = "--- PASS: TestBuildStoreCards_HoursReadFails" in p.stdout
    rec("9.5", "PASS" if ran and p.returncode == 0 else "FAIL", f"TestBuildStoreCards_HoursReadFails_ProfileStillRenders: {'PASS' if ran else p.stdout[-200:]}")

    # 9.6 the pin, and the half-pin guard
    s1, r1 = call("PATCH", f"/artists/stores/{w.store}", {"latitude": 33.9}, w.tok)
    s2, r2 = call("PATCH", f"/artists/stores/{w.store}", {"latitude": 33.9, "longitude": 35.5, "clear_location": True}, w.tok)
    s3, _ = call("PATCH", f"/artists/stores/{w.store}", {"latitude": 33.8961, "longitude": 35.4785}, w.tok)
    pinned = store_card(w, w.store)
    s4, _ = call("PATCH", f"/artists/stores/{w.store}", {"clear_location": True}, w.tok)
    cleared = store_card(w, w.store)
    rec("9.6", "PASS" if (s1, err(r1)) == (400, "INCOMPLETE_LOCATION") and (s2, err(r2)) == (400, "CONFLICTING_LOCATION")
        and s3 < 400 and pinned.get("latitude") == 33.8961 and s4 < 400 and cleared.get("latitude") is None else "FAIL",
        f"latitude only -> {s1} {err(r1)}; both + clear -> {s2} {err(r2)}; pin -> {s3}, profile lat {pinned.get('latitude')}; "
        f"clear -> {s4}, profile lat {cleared.get('latitude')}")

    # 9.7 the same 09:00 is 07:00Z in winter and 06:00Z in summer
    call("POST", f"/artists/stores/{w.store}/hours", {"day_of_week": 1, "open_time": "09:00:00", "close_time": "17:00:00", "is_open": True}, w.tok)
    firsts = {}
    for label, d in (("January", date(2027, 1, 11)), ("July", date(2027, 7, 12))):   # both Mondays
        _, t = slot_times(w.artist, w.store, w.service, d.isoformat())
        firsts[label] = t[0] if t else None
    # Compare INSTANTS: the API answers in the store's own offset
    # (09:00+02:00 in winter), which the first run read as a mismatch.
    utc = {k: datetime.fromisoformat(v.replace("Z", "+00:00")).astimezone(timezone.utc).strftime("%H:%MZ") if v else None
           for k, v in firsts.items()}
    rec("9.7", "PASS" if utc == {"January": "07:00Z", "July": "06:00Z"} else "FAIL",
        f"first 09:00 slot: {firsts} = {utc}")


# ── Suite 10 — portfolio tagged to services ───────────────────────────────

def suite10(w):
    c.register("j2 Other", "j2.other@test.bedge.com", "+96176368902")
    other = c.onboard("j2.other@test.bedge.com", "j2 Other Salon", "j2-other")
    other_service = sql(f"SELECT id FROM services WHERE salon_id=(SELECT salon_id FROM artists WHERE id='{other}') LIMIT 1")
    st, r = call("POST", "/artists/salon/services", {"name": "j2 Nails", "duration_min": 45, "price": "30.00",
                 "deposit_amount": "0", "deposit_deadline_hours": 24}, w.tok)
    s2 = data(r).get("id")
    url = "https://res.cloudinary.com/demo/image/upload/sample.jpg"   # never fetched by the API
    m = [sql(f"INSERT INTO media (owner_type, owner_id, url, type, display_order) VALUES ('artist','{w.artist}','{url}','photo',{i}) RETURNING id")
         for i in range(3)]
    theirs = sql(f"INSERT INTO media (owner_type, owner_id, url, type, display_order) VALUES ('artist','{other}','{url}','photo',0) RETURNING id")
    product_id = sql(f"INSERT INTO products (salon_id,name,price) VALUES ('{w.salon}','j2 Tagless',5.00) RETURNING id")
    prod_photo = sql(f"INSERT INTO media (owner_type, owner_id, url, type, display_order) VALUES ('product','{product_id}','{url}','photo',0) RETURNING id")

    def tags(mid):
        mine = data(call("GET", "/media/my", None, w.tok)[1]).get("photos") or []
        return sorted(next((x.get("service_ids") or [] for x in mine if x.get("id") == mid), None) or [])

    st, r = call("PUT", f"/media/{m[0]}/services", {"service_ids": [w.service, s2]}, w.tok)
    rec("10.1", "PASS" if st < 400 and tags(m[0]) == sorted([w.service, s2]) else "FAIL", f"tag two services -> {st}; stored {len(tags(m[0]))}")
    st, r = call("PUT", f"/media/{m[1]}/services", {"service_ids": [w.service]}, w.tok)
    st_c, _ = call("PUT", f"/media/{m[1]}/services", {"service_ids": []}, w.tok)
    rec("10.2", "PASS" if st_c < 400 and tags(m[1]) == [] else "FAIL", f"clear -> {st_c}; service_ids {tags(m[1])}")
    before = tags(m[0])
    st, r = call("PUT", f"/media/{m[0]}/services", {"service_ids": [w.service, other_service]}, w.tok)
    body = str(r)
    rec("10.3", "PASS" if (st, err(r)) == (400, "INVALID_SERVICE_ID") and other_service not in body and tags(m[0]) == before else "FAIL",
        f"another salon's service -> {st} {err(r)}; names the id: {other_service in body}; tags unchanged: {tags(m[0]) == before}")
    s_a, r_a = call("PUT", f"/media/{theirs}/services", {"service_ids": [w.service]}, w.tok)
    s_p, r_p = call("PUT", f"/media/{prod_photo}/services", {"service_ids": [w.service]}, w.tok)
    s_n, r_n = call("PUT", f"/media/{m[0][:-4]}0000/services", {"service_ids": [w.service]}, w.tok)
    rec("10.4", "PASS" if (s_a, err(r_a)) == (404, "MEDIA_NOT_FOUND") and (s_p, err(r_p)) == (404, "MEDIA_NOT_FOUND") and s_n == 404 else "FAIL",
        f"another artist's photo -> {s_a} {err(r_a)}; a product photo -> {s_p} {err(r_p)}; a missing one -> {s_n}")
    _, r = call("GET", f"/media/portfolio/{w.artist}")
    pub = {x.get("id"): x for x in (data(r).get("photos") or [])}
    rec("10.5 data", "PASS" if sorted(pub.get(m[0], {}).get("service_ids") or []) == sorted([w.service, s2]) else "FAIL",
        f"public portfolio carries the tags the chips are built from: {len(pub.get(m[0], {}).get('service_ids') or [])} on the tagged photo")
    st, r = call("PATCH", "/media/reorder", {"ids": [m[2], m[0], m[1]]}, w.tok)
    order = sql(f"SELECT string_agg(id::text, ',' ORDER BY display_order) FROM media WHERE owner_id='{w.artist}' AND owner_type='artist'")
    rec("10.7", "PASS" if st < 400 and order == ",".join([m[2], m[0], m[1]]) else "FAIL",
        f"reorder -> {st}; photo moved to the front is now the cover: {order.split(',')[0] == m[2]}")


# ── helpers for the raw-HTTP suites (share previews, calendar) ────────────

def raw_get(path_):
    """GET on the API host WITHOUT following redirects - 11.4 and 11.5 are
    about the redirect itself."""
    import http.client
    conn = http.client.HTTPConnection("localhost", 3000, timeout=20)
    conn.request("GET", path_)
    r = conn.getresponse()
    body = r.read().decode("utf-8", "replace")
    if r.status == 429 and "RATE_LIMIT_EXCEEDED" in body:
        time.sleep(30)          # same pacing as _paced_call, for the raw pages
        return raw_get(path_)
    return r.status, {k.lower(): v for k, v in r.getheaders()}, body


def sql_try(q):
    try:
        return True, c.sql(q)
    except RuntimeError as e:
        return False, str(e)


def local_dt(day, hhmm, tz="Asia/Beirut"):
    from zoneinfo import ZoneInfo
    h, m = map(int, hhmm.split(":"))
    return datetime(day.year, day.month, day.day, h, m, tzinfo=ZoneInfo(tz))


def insert_booking(w, start, minutes, status="confirmed", store=None, buffer_min=0):
    cust = sql("SELECT id FROM users WHERE role='customer' AND deleted_at IS NULL ORDER BY created_at LIMIT 1")
    end = start + timedelta(minutes=minutes)
    blocked = end + timedelta(minutes=buffer_min)
    return sql(f"""INSERT INTO bookings (salon_id, store_id, artist_id, customer_id, service_id, start_time, end_time,
                   blocked_until, original_price, final_price, deposit_amount, status, special_requests)
                   VALUES ('{w.salon}','{store or w.store}','{w.artist}','{cust}','{w.service}','{start.isoformat()}',
                   '{end.isoformat()}','{blocked.isoformat()}',100,100,0,'{status}','j2 journey') RETURNING id""")


# ── Suite 11 — share link previews ────────────────────────────────────────

def suite11(w):
    st, h, body = raw_get("/a/j2-owner")
    import re
    tags = dict(re.findall(r'<meta (?:property|name)="((?:og|twitter):[a-z_:]+)" content="([^"]*)"', body))
    need = ["og:title", "og:description", "og:image", "og:url", "og:site_name", "twitter:card"]
    rec("11.1", "PASS" if st == 200 and all(k in tags for k in need) and tags.get("twitter:card") == "summary_large_image"
        and ":3000" not in tags.get("og:url", ":3000") else "FAIL",
        f"{st}; missing tags: {[k for k in need if k not in tags]}; og:url {tags.get('og:url')}")
    st2, _, _ = raw_get(f"/a/{w.artist}")
    rec("11.2", "PASS" if st2 == 200 else "FAIL", f"/a/<handle> -> {st}, /a/<uuid> -> {st2}")
    img = tags.get("og:image", "")
    has_photo = sql(f"SELECT count(*) FROM media WHERE owner_type='artist' AND owner_id='{w.artist}'") != "0"
    rec("11.3", "PASS" if img and (("c_fill,g_auto,w_1200,h_630" in img) or not has_photo) else "FAIL",
        f"portfolio photo: {has_photo}; og:image {img[:100]}")
    sub = sql(f"SELECT id FROM subscriptions WHERE artist_id='{w.artist}' ORDER BY created_at LIMIT 1")
    # trial_ends_at too: a newly approved artist is on a live trial, which
    # keeps her visible whatever the period says (the first run forgot this).
    sql(f"UPDATE subscriptions SET plan_code='solo', trial_ends_at=NULL, current_period_end=NOW() - interval '60 days' WHERE id='{sub}'")
    st4, h4, _ = raw_get("/a/j2-owner")
    sql(f"UPDATE subscriptions SET plan_code='comped', current_period_end=NULL WHERE id='{sub}'")
    st5, h5, _ = raw_get("/a/no-such-artist-j2")
    rec("11.4", "PASS" if st4 == 302 else "FAIL", f"suspended artist's preview -> {st4} (to {h4.get('location')})")
    rec("11.5", "PASS" if st5 == 302 else "FAIL", f"unknown slug -> {st5} (to {h5.get('location')}); the database-outage variant is not staged")
    sql(f"UPDATE artists SET bio='</title><script>alert(1)</script>' WHERE id='{w.artist}'")
    _, _, body6 = raw_get("/a/j2-owner")
    sql(f"UPDATE artists SET bio=NULL WHERE id='{w.artist}'")
    rec("11.6", "PASS" if "<script>alert(1)" not in body6 and ("&lt;script&gt;" in body6 or "&lt;/title&gt;" in body6) else "FAIL",
        f"bio with </title><script>: live script in the page {('<script>alert(1)' in body6)}; escaped {'&lt;script&gt;' in body6}")
    rec("11.7", "SKIP", "a real WhatsApp/Instagram crawler - manual by definition")


# ── Suite 12 — bulk schedule shift preview ────────────────────────────────

def suite12(w):
    day = date.today() + timedelta(days=12)
    while day.weekday() >= 5:
        day += timedelta(days=1)
    dow = (day.weekday() + 1) % 7
    call("POST", f"/artists/stores/{w.store}/hours", {"day_of_week": dow, "open_time": "09:00:00", "close_time": "18:00:00", "is_open": True}, w.tok)
    preview = lambda mins, d=day.isoformat(), tok=w.tok, store=w.store: call("POST", "/bookings/schedule/shift-preview",
                                                                        {"store_id": store, "date": d, "shift_minutes": mins}, tok)

    # 12.1 the deferrable constraint, straight in SQL (rolled back)
    a = insert_booking(w, local_dt(day, "09:00"), 60)
    b = insert_booking(w, local_dt(day, "10:00"), 60)
    ok1, e1 = sql_try(f"BEGIN; UPDATE bookings SET start_time=start_time+interval '10 min', end_time=end_time+interval '10 min', "
                      f"blocked_until=blocked_until+interval '10 min' WHERE id IN ('{a}','{b}'); ROLLBACK;")
    ok2, e2 = sql_try(f"INSERT INTO bookings (salon_id,store_id,artist_id,customer_id,service_id,start_time,end_time,blocked_until,"
                      f"original_price,final_price,deposit_amount,status) SELECT salon_id,store_id,artist_id,customer_id,service_id,"
                      f"start_time+interval '30 min',end_time+interval '30 min',blocked_until+interval '30 min',100,100,0,'confirmed' FROM bookings WHERE id='{a}'")
    ok3, e3 = sql_try(f"BEGIN; SET CONSTRAINTS ALL DEFERRED; UPDATE bookings SET start_time=start_time+interval '30 min', "
                      f"end_time=end_time+interval '30 min', blocked_until=blocked_until+interval '30 min' WHERE id='{a}'; COMMIT;")
    ok4, e4 = sql_try(f"BEGIN; UPDATE bookings SET start_time=start_time+interval '10 min', end_time=end_time+interval '10 min', "
                      f"blocked_until=blocked_until+interval '10 min' WHERE id='{a}'; UPDATE bookings SET start_time=start_time+interval '10 min', "
                      f"end_time=end_time+interval '10 min', blocked_until=blocked_until+interval '10 min' WHERE id='{b}'; COMMIT;")
    rec("12.1", "PASS" if ok1 and not ok2 and ("23P01" in e2 or "exclusion" in e2) and not ok3 and not ok4 else "FAIL",
        f"adjacent pair shifted in one statement: {ok1}; a real overlap refused: {not ok2}; deferred overlap refused at commit: "
        f"{not ok3}; one-row-per-statement shift refused: {not ok4}")
    sql(f"DELETE FROM bookings WHERE id IN ('{a}','{b}')")

    # 12.2 boundaries on shift_minutes
    cases = {}
    for v in (0, 1, -1, 240, -240, 241, -241, 2147483648, "10", 10.5, None):
        body = {"store_id": w.store, "date": day.isoformat()}
        if v is not None:
            body["shift_minutes"] = v
        cases[repr(v)] = call("POST", "/bookings/schedule/shift-preview", body, w.tok)[0]
    ok = (all(cases[k] == 200 for k in ("1", "-1", "240", "-240")) and all(cases[k] in (400, 422) for k in ("241", "-241", "2147483648", "'10'", "None")))
    rec("12.2", "PASS" if ok and cases["0"] < 500 and cases["10.5"] < 500 else "FAIL",
        f"{cases} (0 and 10.5 pinned as measured)")

    # 12.3 hostile dates: all clean 4xx, never a 500
    bad = ["2026-9-16", "16-09-2026", "2026-02-30", "2026-02-29", "0000-01-01", "99999-01-01", "", None,
           "2026-09-16T10:00:00Z", (date.today() + timedelta(days=365 * 50)).isoformat()]
    got = {repr(d): call("POST", "/bookings/schedule/shift-preview", {"store_id": w.store, "date": d, "shift_minutes": 10}, w.tok)[0] for d in bad}
    rec("12.3", "PASS" if all(400 <= s < 500 for s in got.values()) else "FAIL", f"{got}")

    # 12.4 the trading edges (store 09:00-18:00)
    def verdict(start, mins, shift):
        bid = insert_booking(w, local_dt(day, start), mins)
        _, r = preview(shift)
        d = data(r)
        moved = [x.get("booking_id") for x in (d.get("movable") or [])]
        blocked = [x.get("booking_id") for x in (d.get("blockers") or [])]
        sql(f"DELETE FROM bookings WHERE id='{bid}'")
        # A blocked booking is ALSO listed under movable; the blockers list
        # and can_apply=false are what say it cannot go (first run misread this).
        return "blocked" if bid in blocked else "moves" if bid in moved and d.get("can_apply") else "absent"
    edges = {"ends at closing +1": verdict("17:00", 60, 1), "ends 1 min before closing +1": verdict("16:59", 60, 1),
             "starts at opening -1": verdict("09:00", 60, -1)}
    rec("12.4", "PASS" if edges == {"ends at closing +1": "blocked", "ends 1 min before closing +1": "moves", "starts at opening -1": "blocked"} else "FAIL",
        f"{edges} (the 'starts exactly now' cases need a same-day run at trading hours and are not staged)")

    # 12.5 a corrupted zone falls back without a 500; a +60 across the spring-forward night
    sql(f"UPDATE stores SET timezone='Not/AZone' WHERE id='{w.store}'")
    st_z, _ = preview(10)
    sql(f"UPDATE stores SET timezone='Asia/Beirut' WHERE id='{w.store}'")
    dst = date(2027, 3, 28)
    call("POST", f"/artists/stores/{w.store}/hours", {"day_of_week": 0, "open_time": "09:00:00", "close_time": "18:00:00", "is_open": True}, w.tok)
    bid = insert_booking(w, local_dt(dst, "10:00"), 60)
    _, r = preview(60, d=dst.isoformat())
    mv = next((x for x in (data(r).get("movable") or []) if x.get("booking_id") == bid), {})
    new_start = mv.get("new_start") or mv.get("new_start_time") or ""
    sql(f"DELETE FROM bookings WHERE id='{bid}'")
    call("POST", f"/artists/stores/{w.store}/hours", {"day_of_week": 0, "open_time": "09:00:00", "close_time": "18:00:00", "is_open": False}, w.tok)
    local_new = datetime.fromisoformat(new_start.replace("Z", "+00:00")).astimezone(local_dt(dst, "00:00").tzinfo).strftime("%H:%M") if new_start else None
    rec("12.5", "PASS" if st_z < 500 and local_new == "11:00" else "FAIL",
        f"'Not/AZone' -> {st_z}; 10:00 +60 on 2027-03-28 -> {local_new} local ({new_start})")

    # 12.6 fifty bookings in one day, under a second
    ids = [insert_booking(w, local_dt(day, "09:00") + timedelta(minutes=10 * i), 10) for i in range(50)]
    t0 = time.time()
    st6, r6 = preview(5)
    took = time.time() - t0
    counted = len(data(r6).get("movable") or []) + len(data(r6).get("skipped") or [])
    rec("12.6", "PASS" if st6 == 200 and took < 1.0 and counted == 50 else "FAIL", f"50 bookings -> {st6} in {took:.2f}s, {counted} accounted for")

    # 12.7 twenty at once agree
    import concurrent.futures as cf
    import json as _json
    with cf.ThreadPoolExecutor(20) as ex:
        outs = list(ex.map(lambda _: preview(5), range(20)))
    shapes = {_json.dumps(data(r), sort_keys=True) for _, r in outs}
    rec("12.7", "PASS" if all(s == 200 for s, _ in outs) and len(shapes) == 1 else "FAIL",
        f"20 concurrent previews: statuses {sorted({s for s, _ in outs})}, distinct answers {len(shapes)}")

    # 12.8 who may preview
    other_tok = c.login("j2.other@test.bedge.com") if sql("SELECT count(*) FROM users WHERE email='j2.other@test.bedge.com' AND deleted_at IS NULL") != "0" else None
    if not other_tok:
        c.register("j2 Other", "j2.other@test.bedge.com", "+96176368902")
        c.onboard("j2.other@test.bedge.com", "j2 Other Salon", "j2-other")
        other_tok = c.login("j2.other@test.bedge.com")
    s_o, r_o = preview(5, tok=other_tok)
    s_n, r_n = preview(5, store="00000000-0000-4000-8000-000000000000")
    s_x, _ = call("POST", "/bookings/schedule/shift-preview", {"store_id": w.store, "date": day.isoformat(), "shift_minutes": 5})
    s_c, _ = preview(5, tok=customer_token(8))
    leaked = len(data(r_o).get("movable") or []) + len(data(r_o).get("skipped") or [])
    rec("12.8", "PASS" if (s_o == 404 or (s_o == 200 and leaked == 0)) and s_n == 404 and s_x == 401 and s_c in (403, 404) else "FAIL",
        f"another artist -> {s_o} ({leaked} of our bookings shown); unknown store -> {s_n}; no token -> {s_x}; customer -> {s_c}")
    sql(f"DELETE FROM bookings WHERE id IN ({','.join(repr(x) for x in ids)})")


# ── Suite 13 — in-app notification centre ─────────────────────────────────

def feed_items(r):
    """The feed is an object with the list inside it (plus counts)."""
    d = (r or {}).get("data")
    if isinstance(d, list):
        return d
    return next((v for v in (d or {}).values() if isinstance(v, list)), [])


BUNDLE_SQL = """INSERT INTO user_notifications (user_id, kind, level, title, body, link, group_key)
  VALUES ('{u}', 'j2_test', 'info', '{t}', 'b', NULL, {g})
  ON CONFLICT (user_id, group_key) WHERE group_key IS NOT NULL AND read_at IS NULL AND archived_at IS NULL
  DO UPDATE SET item_count = user_notifications.item_count + 1, title = EXCLUDED.title, body = EXCLUDED.body,
                link = EXCLUDED.link RETURNING id"""


def suite13(w):
    u = sql(f"SELECT user_id FROM artists WHERE id='{w.artist}'")
    rows = lambda extra="": sql(f"SELECT count(*) || '|' || COALESCE(max(item_count),0) FROM user_notifications WHERE user_id='{u}' AND kind='j2_test' {extra}")
    # 13.1 bundling, through the repository's own statement (internal/inbox/repository.go Create)
    for i in range(100):
        sql(BUNDLE_SQL.format(u=u, t=f"j2 {i}", g="'j2-a'"))
    one = rows()
    _, badge = call("GET", "/notifications/unread-count", None, w.tok)
    import concurrent.futures as cf
    with cf.ThreadPoolExecutor(2) as ex:
        conc = list(ex.map(lambda _: sql_try(BUNDLE_SQL.format(u=u, t="j2 c", g="'j2-b'")), range(2)))
    two = rows("AND group_key='j2-b'")
    first = sql(f"SELECT id FROM user_notifications WHERE user_id='{u}' AND group_key='j2-a'")
    call("PATCH", f"/notifications/{first}/read", None, w.tok)
    sql(BUNDLE_SQL.format(u=u, t="j2 after read", g="'j2-a'"))
    after_read = sql(f"SELECT count(*) FROM user_notifications WHERE user_id='{u}' AND group_key='j2-a'")
    for _ in range(10):
        sql(BUNDLE_SQL.format(u=u, t="j2 null", g="NULL"))
    nulls = sql(f"SELECT count(*) FROM user_notifications WHERE user_id='{u}' AND kind='j2_test' AND group_key IS NULL")
    rec("13.1", "PASS" if one == "1|100" and data(badge).get("count", data(badge).get("unread")) in (1, None) and all(o for o, _ in conc)
        and two == "1|2" and after_read == "2" and nulls == "10" else "FAIL",
        f"100 same-key -> rows|count {one} (badge {data(badge)}); 2 concurrent -> {two}, errors {[e for o, e in conc if not o]}; "
        f"after reading, the next occurrence starts a new row: {after_read == '2'}; 10 without a key -> {nulls} rows")

    # 13.2 read / archive state machine through the API
    sql(f"DELETE FROM user_notifications WHERE user_id='{u}' AND kind='j2_test'")
    ids = [sql(BUNDLE_SQL.format(u=u, t=f"j2 s{i}", g="NULL")) for i in range(3)]
    s_r1 = call("PATCH", f"/notifications/{ids[0]}/read", None, w.tok)[0]
    s_r2 = call("PATCH", f"/notifications/{ids[0]}/read", None, w.tok)[0]
    s_a1 = call("PATCH", f"/notifications/{ids[1]}/archive", None, w.tok)[0]
    arch_read = sql(f"SELECT read_at IS NOT NULL FROM user_notifications WHERE id='{ids[1]}'")
    s_ar = call("PATCH", f"/notifications/{ids[1]}/read", None, w.tok)[0]
    s_a2 = call("PATCH", f"/notifications/{ids[1]}/archive", None, w.tok)[0]
    s_all = call("POST", "/notifications/read-all", None, w.tok)[0]
    left = sql(f"SELECT count(*) FROM user_notifications WHERE user_id='{u}' AND read_at IS NULL AND archived_at IS NULL")
    s_all2 = call("POST", "/notifications/read-all", None, w.tok)[0]
    rec("13.2", "PASS" if s_r1 < 300 and s_r2 < 300 and s_a1 < 300 and arch_read == "t" and s_a2 == 404 and s_all < 300
        and left == "0" and s_all2 < 300 else "FAIL",
        f"read {s_r1}, read again {s_r2}, archive {s_a1} (also read: {arch_read == 't'}), read an archived one {s_ar} (pinned), "
        f"archive again {s_a2}, read-all {s_all} -> unread left {left}, read-all with none {s_all2}")

    # 13.3 pagination clamps
    for i in range(55):
        sql(BUNDLE_SQL.format(u=u, t=f"j2 p{i}", g="NULL"))
    lims = {}
    for v in ("0", "-1", "1", "50", "51", "10000", "abc", None):
        q = "" if v is None else f"?limit={v}"
        st, r = call("GET", f"/notifications{q}", None, w.tok)
        lims[v] = (st, len(feed_items(r)))
    rec("13.3", "PASS" if all(st < 500 and 1 <= n <= 50 for st, n in lims.values() if st == 200) and lims["10000"][1] <= 50 else "FAIL",
        f"limit -> (status, rows): {lims}")

    # 13.4 another artist cannot touch hers, and read-all is per user
    other_tok = c.login("j2.other@test.bedge.com")
    mine = sql(BUNDLE_SQL.format(u=u, t="j2 private", g="NULL"))
    s_or, r_or = call("PATCH", f"/notifications/{mine}/read", None, other_tok)
    s_oa, _ = call("PATCH", f"/notifications/{mine}/archive", None, other_tok)
    call("POST", "/notifications/read-all", None, other_tok)
    still = sql(f"SELECT read_at IS NULL AND archived_at IS NULL FROM user_notifications WHERE id='{mine}'")
    _, feed = call("GET", "/notifications", None, other_tok)
    items = feed_items(feed)
    rec("13.4", "PASS" if s_or == 404 and s_oa == 404 and still == "t" and not any(x.get("id") == mine for x in (items or [])) else "FAIL",
        f"another artist reads -> {s_or} {err(r_or)}, archives -> {s_oa}; her read-all left mine unread: {still == 't'}")
    rec("13.5", "SKIP", "needs a forced Twilio failure through the running worker; covered instead by the Go database test for E2E 28.12 (internal/notification/worker_db_test.go)")

    # 13.6 long titles are refused by the column, not truncated
    ok, e = sql_try(BUNDLE_SQL.format(u=u, t="x" * 201, g="NULL"))
    rec("13.6 length", "PASS" if not ok and "too long" in e else "FAIL", f"a 201-character title -> {'refused' if not ok else 'accepted'}")

    # 13.7 cascade: deleting a user removes her notifications
    tmp = sql("INSERT INTO users (name,email,password_hash,role) VALUES ('j2 Tmp','j2.tmp@test.bedge.com','x','artist') RETURNING id")
    sql(BUNDLE_SQL.format(u=tmp, t="j2 orphan?", g="NULL"))
    sql(f"DELETE FROM users WHERE id='{tmp}'")
    orphans = sql(f"SELECT count(*) FROM user_notifications WHERE user_id='{tmp}'")
    rec("13.7 cascade", "PASS" if orphans == "0" else "FAIL", f"rows left after deleting the user: {orphans}")
    sql(f"DELETE FROM user_notifications WHERE user_id='{u}' AND kind='j2_test'")


# ── Suite 14 — "Add to calendar" links ────────────────────────────────────

def unfold(ics):
    return ics.replace("\r\n ", "").replace("\r\n\t", "")


def ics_props(ics):
    out = {}
    for line in unfold(ics).split("\r\n"):
        if ":" in line:
            k, v = line.split(":", 1)
            out.setdefault(k.split(";")[0], v)
    return out


def suite14(w):
    day = date.today() + timedelta(days=15)
    while day.weekday() >= 5:
        day += timedelta(days=1)
    _, times = slot_times(w.artist, w.store, w.service, day.isoformat())
    st, r = call("POST", "/bookings/guest/hold", {"artist_id": w.artist, "store_id": w.store, "service_id": w.service, "start_time": times[0]})
    b = data(r).get("booking_id")
    call("PATCH", f"/bookings/guest/{b}/submit", {"name": "j2 Cal", "phone": CUSTOMER_PHONE.format(9)})
    tok_pending = sql(f"SELECT COALESCE(calendar_token,'NULL') FROM bookings WHERE id='{b}'")
    guest_view = str(data(r))
    call("PATCH", f"/bookings/{b}/approve", {}, w.tok)
    tok = sql(f"SELECT calendar_token FROM bookings WHERE id='{b}'")
    approved_msg = sql(f"SELECT COALESCE(string_agg(payload::text, ' '),'') FROM notifications WHERE booking_id='{b}' AND template_name ILIKE '%approv%'")
    call("PATCH", f"/bookings/{b}/confirm-payment", {}, w.tok)
    confirmed_msg = sql(f"SELECT COALESCE(string_agg(payload::text, ' '),'') FROM notifications WHERE booking_id='{b}' AND template_name ILIKE '%confirm%'")
    import re
    rec("14.1", "PASS" if tok_pending == "NULL" and re.fullmatch(r"[0-9a-f]{64}", tok or "") and tok not in approved_msg
        and f"/c/{tok}" in confirmed_msg and tok not in guest_view else "FAIL",
        f"pending: {tok_pending}; approved: 64-hex token {bool(re.fullmatch(r'[0-9a-f]{64}', tok or ''))}; in the approved message: "
        f"{tok in approved_msg}; in the confirmed message: {f'/c/{tok}' in confirmed_msg}; in the guest funnel's response: {tok in guest_view}")

    # 14.2 RFC 5545 - Arabic, emoji and escapes in the names
    sql(f"UPDATE stores SET name='j2 متجر الجمال 💄 Hamra, Beirut; \\\\ line\nbreak' WHERE id='{w.store}'")
    sql(f"UPDATE services SET name='j2 Bridal, Glam; \\\\ 💍' WHERE id='{w.service}'")
    st, hdrs, ics = raw_get(f"/c/{tok}.ics")
    raw = ics.encode("utf-8")
    lines = raw.split(b"\r\n")
    bare_lf = raw.replace(b"\r\n", b"").count(b"\n")
    too_long = [len(l) for l in lines if len(l) > 75]
    split_utf8 = any((l[:1] and 0x80 <= l[0] <= 0xBF) for l in lines[1:] if l.startswith(b" "))
    folded_ok = True
    try:
        unfold(ics).encode("utf-8").decode("utf-8")
    except UnicodeError:
        folded_ok = False
    p = ics_props(ics)
    summ, loc = p.get("SUMMARY", ""), p.get("LOCATION", "")
    # The store name is in SUMMARY ("service at store"); LOCATION is the
    # address/city. The first run looked for the escaped newline there.
    rec("14.2", "PASS" if st == 200 and bare_lf == 0 and not too_long and folded_ok and "\\," in summ and "\\;" in summ
        and "\\\\" in summ and "\\n" in summ and "\n" not in summ else "FAIL",
        f"{st}; bare LF {bare_lf}; lines over 75 octets {too_long[:3]}; continuation starting mid-UTF-8: {split_utf8}; "
        f"SUMMARY {summ[:70]!r}; LOCATION {loc[:70]!r}")

    # 14.3 instants in UTC; an unloadable zone falls back to UTC
    start_db = sql(f"SELECT to_char(start_time AT TIME ZONE 'UTC','YYYYMMDD\"T\"HH24MISS\"Z\"') FROM bookings WHERE id='{b}'")
    sql(f"UPDATE stores SET timezone='Not/AZone' WHERE id='{w.store}'")
    st_bad, _, ics_bad = raw_get(f"/c/{tok}.ics")
    sql(f"UPDATE stores SET timezone='Asia/Beirut' WHERE id='{w.store}'")
    rec("14.3", "PASS" if p.get("DTSTART") == start_db and p.get("DTEND", "").endswith("Z") and st_bad == 200
        and ics_props(ics_bad).get("DTSTART") == start_db else "FAIL",
        f"DTSTART {p.get('DTSTART')} vs booked {start_db}; with a broken zone -> {st_bad}, DTSTART {ics_props(ics_bad).get('DTSTART')}")

    # 14.4 reschedule: same UID, higher SEQUENCE, moved DTSTART
    uid, seq = p.get("UID"), int(p.get("SEQUENCE", "0"))
    # Rescheduling is the CUSTOMER's action (the service answers 404 to anyone
    # else), so she does it - signed in by the bypass code, as always.
    st_rs, r_rs = call("PATCH", f"/bookings/{b}/reschedule", {"start_time": times[4]}, customer_token(9))
    _, _, ics2 = raw_get(f"/c/{tok}.ics")
    p2 = ics_props(ics2)
    rec("14.4", "PASS" if st_rs < 400 and p2.get("UID") == uid and int(p2.get("SEQUENCE", "0")) > seq and p2.get("DTSTART") != p.get("DTSTART") else "FAIL",
        f"reschedule -> {st_rs} {err(r_rs)}; UID same {p2.get('UID') == uid}; SEQUENCE {seq} -> {p2.get('SEQUENCE')}; DTSTART {p.get('DTSTART')} -> {p2.get('DTSTART')}")

    # 14.5 cancelled stays reachable and withdraws; completed is left alone
    call("PATCH", f"/bookings/{b}/cancel", {"reason": "j2"}, w.tok)
    st_c, _, ics_c = raw_get(f"/c/{tok}.ics")
    pc = ics_props(ics_c)
    rec("14.5", "PASS" if st_c == 200 and pc.get("METHOD") == "CANCEL" and pc.get("STATUS") == "CANCELLED" and pc.get("UID") == uid else "FAIL",
        f"cancelled -> {st_c}, METHOD {pc.get('METHOD')}, STATUS {pc.get('STATUS')}, same UID {pc.get('UID') == uid}")

    # 14.6 malformed tokens all answer the same 404; headers
    review_tok = sql("SELECT review_token FROM bookings WHERE review_token IS NOT NULL LIMIT 1") or "0" * 64
    probes = ["abc", tok.upper(), "z" * 64, "", "..%2F..%2Fetc%2Fpasswd", str(w.artist), review_tok, "0" * 64]
    answers = {pr[:12]: raw_get(f"/c/{pr}")[0] for pr in probes}
    st_p, hp, _ = raw_get(f"/c/{tok}")
    _, _, page0 = raw_get(f"/c/{tok}")
    # "The page sends noindex, nofollow": a meta tag in the page, or a header
    robots = hp.get("x-robots-tag", "") or ("noindex, nofollow" if '<meta name="robots" content="noindex, nofollow">' in page0 else "")
    cache = hp.get("cache-control", "")
    rec("14.6", "PASS" if all(v == 404 for k, v in answers.items() if k) and "noindex" in robots and "no-store" in cache else "FAIL",
        f"probes -> {answers}; X-Robots-Tag '{robots}'; Cache-Control '{cache}'")

    # 14.7 the landing page escapes names and strips bidi overrides
    sql(f"UPDATE stores SET name='j2 <script>x</script> ‮evil' WHERE id='{w.store}'")
    _, _, page = raw_get(f"/c/{tok}")
    _, _, ics3 = raw_get(f"/c/{tok}.ics")
    rec("14.7", "PASS" if "<script>x" not in page and "‮" not in page and "%E2%80%AE" not in page.upper() and "‮" not in ics3 else "FAIL",
        f"live <script> in the page: {'<script>x' in page}; bidi override in page/link/ics: "
        f"{'\\u202e' in page}/{'%E2%80%AE' in page.upper()}/{'\\u202e' in ics3}")
    rec("14.8", "SKIP", "importing into Apple Calendar, Google Calendar and Outlook - manual by definition")

    # 14.9 fifty concurrent fetches describe the same event. Identical apart
    # from DTSTAMP: the file carries METHOD, so RFC 5545 makes DTSTAMP the time
    # this copy was generated - request time is right. "Byte-identical" held
    # only while all fifty landed in one wall-clock second; on 2026-10-10 they
    # straddled one and the check failed a correct product.
    import concurrent.futures as cf
    import re as _re
    t0 = datetime.now(timezone.utc)
    with cf.ThreadPoolExecutor(50) as ex:
        bodies = list(ex.map(lambda _: raw_get(f"/c/{tok}.ics")[2], range(50)))
    t1 = datetime.now(timezone.utc)
    events = {_re.sub(r"DTSTAMP:\S+", "DTSTAMP:-", b_) for b_ in bodies}
    stamps = [datetime.strptime(m, "%Y%m%dT%H%M%SZ").replace(tzinfo=timezone.utc)
              for b_ in bodies for m in _re.findall(r"DTSTAMP:(\S+)", b_)]
    fresh = len(stamps) == 50 and all(t0 - timedelta(seconds=1) <= st_ <= t1 + timedelta(seconds=1) for st_ in stamps)
    rec("14.9", "PASS" if len(events) == 1 and fresh else "FAIL",
        f"50 concurrent fetches -> {len(events)} distinct event(s) once DTSTAMP is set aside; every DTSTAMP is the fetch time: {fresh}")
    sql(f"UPDATE stores SET name='j2-owner branch' WHERE id='{w.store}'")
    sql(f"UPDATE services SET name='j2 Bridal' WHERE id='{w.service}'")


# ── Suite 15 — service buffer / cleanup time ──────────────────────────────

def suite15(w):
    st, r = call("PATCH", f"/artists/salon/services/{w.service}", {"buffer_min": 30}, w.tok)
    stored = sql(f"SELECT buffer_min FROM services WHERE id='{w.service}'")
    day = date.today() + timedelta(days=18)
    while day.weekday() >= 5:
        day += timedelta(days=1)
    s_, r_ = call("GET", f"/bookings/slots?artist_id={w.artist}&store_id={w.store}&service_id={w.service}&date={day}")
    sl = (r_.get("data") or [])
    durations = {round((datetime.fromisoformat(x["end_time"].replace("Z", "+00:00")) - datetime.fromisoformat(x["start_time"].replace("Z", "+00:00"))).total_seconds() / 60)
                 for x in sl if x.get("end_time")}
    svc_min = int(sql(f"SELECT duration_min FROM services WHERE id='{w.service}'"))
    rec("15.1", "PASS" if st < 400 and stored == "30" and durations == {svc_min} else "FAIL",
        f"buffer 30 saved: {stored}; slots advertise {durations} min (service is {svc_min}, never +30)")

    # 15.2 it reserves time: book the first slot, the next start respects end+buffer
    t0 = sl[0]["start_time"]
    st_h, r_h = call("POST", "/bookings/guest/hold", {"artist_id": w.artist, "store_id": w.store, "service_id": w.service, "start_time": t0})
    b = data(r_h).get("booking_id")
    call("PATCH", f"/bookings/guest/{b}/submit", {"name": "j2 Buf", "phone": CUSTOMER_PHONE.format(10)})
    blocked = sql(f"SELECT to_char(blocked_until AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS') FROM bookings WHERE id='{b}'")
    _, after = slot_times(w.artist, w.store, w.service, day.isoformat())
    bdt = datetime.fromisoformat(blocked + "+00:00")
    first_after = next((t for t in after if datetime.fromisoformat(t.replace("Z", "+00:00")) >= datetime.fromisoformat(t0.replace("Z", "+00:00"))), None)
    rec("15.2", "PASS" if first_after and datetime.fromisoformat(first_after.replace("Z", "+00:00")) >= bdt else "FAIL",
        f"booked {t0}, blocked until {blocked}Z; next offered start {first_after}")

    # 15.3 the database refuses what the API would
    ok_in, e_in = sql_try(f"INSERT INTO bookings (salon_id,store_id,artist_id,customer_id,service_id,start_time,end_time,blocked_until,"
                          f"original_price,final_price,deposit_amount,status) SELECT salon_id,store_id,artist_id,customer_id,service_id,"
                          f"blocked_until - interval '10 min', blocked_until + interval '50 min', blocked_until + interval '50 min',100,100,0,'confirmed' FROM bookings WHERE id='{b}'")
    ok_at, e_at = sql_try(f"INSERT INTO bookings (salon_id,store_id,artist_id,customer_id,service_id,start_time,end_time,blocked_until,"
                          f"original_price,final_price,deposit_amount,status,special_requests) SELECT salon_id,store_id,artist_id,customer_id,service_id,"
                          f"blocked_until, blocked_until + interval '60 min', blocked_until + interval '60 min',100,100,0,'confirmed','j2 journey' FROM bookings WHERE id='{b}' RETURNING id")
    ok_inv, e_inv = sql_try(f"UPDATE bookings SET blocked_until = end_time - interval '1 min' WHERE id='{b}'")
    rec("15.3", "PASS" if not ok_in and ("23P01" in e_in or "exclusion" in e_in) and ok_at and not ok_inv else "FAIL",
        f"inside the cleanup window: refused {not ok_in}; exactly at its end: accepted {ok_at}; blocked_until before end_time: refused {not ok_inv}")
    if ok_at:
        sql(f"DELETE FROM bookings WHERE id='{e_at.splitlines()[0]}'")

    # 15.4 finishing early releases the buffer at once
    sql(f"UPDATE bookings SET status='confirmed', start_time=NOW() - interval '70 min', end_time=NOW() - interval '10 min', "
        f"blocked_until=NOW() + interval '20 min' WHERE id='{b}'")
    call("PATCH", f"/bookings/{b}/complete", {}, w.tok)
    after_c = sql(f"SELECT (blocked_until <= NOW() + interval '5 seconds') || '|' || (blocked_until >= end_time) FROM bookings WHERE id='{b}'")
    rec("15.4", "PASS" if after_c == "true|true" else "FAIL",
        f"completed 10 min after its end with 20 min of buffer left: blocked_until collapsed to now {after_c.split('|')[0]}, "
        f"never below the end {after_c.split('|')[-1]}")

    # 15.5 bounds, and a snapshot
    s_neg = call("PATCH", f"/artists/salon/services/{w.service}", {"buffer_min": -1}, w.tok)[0]
    s_big = call("PATCH", f"/artists/salon/services/{w.service}", {"buffer_min": 121}, w.tok)[0]
    snap_before = sql(f"SELECT blocked_until FROM bookings WHERE id='{b}'")
    s_zero = call("PATCH", f"/artists/salon/services/{w.service}", {"buffer_min": 0}, w.tok)[0]
    snap_after = sql(f"SELECT blocked_until FROM bookings WHERE id='{b}'")
    rec("15.5", "PASS" if s_neg in (400, 422) and s_big in (400, 422) and s_zero < 400 and snap_before == snap_after else "FAIL",
        f"-1 -> {s_neg}; 121 -> {s_big}; 0 -> {s_zero}; an existing booking's blocked_until unchanged by the edit: {snap_before == snap_after}")


# ── Suite 16 — waitlist cascade and sweep (16.5 lives in e2e-suite27) ─────

def suite16(w):
    day = date.today() + timedelta(days=20)
    while day.weekday() >= 5:
        day += timedelta(days=1)

    def entry(n, for_day=None):
        call("POST", "/bookings/waitlist", {"artist_id": w.artist, "store_id": w.store, "service_id": w.service,
             "requested_date": (for_day or day).isoformat(), "name": f"j2 W{n}", "phone": CUSTOMER_PHONE.format(20 + n)})
        return sql(f"SELECT w.id FROM waitlist_entries w JOIN users u ON u.id=w.customer_id WHERE u.phone='{CUSTOMER_PHONE.format(20 + n)}' "
                   f"ORDER BY w.created_at DESC LIMIT 1")

    status = lambda e: sql(f"SELECT status FROM waitlist_entries WHERE id='{e}'")
    results = {}
    # In a full run, Suite 15 left its own booking earlier today on this same
    # artist, and the no-overlap constraint (rightly) refused this suite's.
    # Everything in that window is harness data under the j2 salon.
    window = f"artist_id='{w.artist}' AND start_time > NOW() - interval '4 hours' AND start_time < NOW() + interval '1 hour'"
    sql(f"DELETE FROM notifications WHERE booking_id IN (SELECT id FROM bookings WHERE {window})")
    sql(f"DELETE FROM reviews WHERE booking_id IN (SELECT id FROM bookings WHERE {window})")
    sql(f"DELETE FROM bookings WHERE {window}")
    from zoneinfo import ZoneInfo
    for n, event in enumerate(("cancel", "no-show", "complete")):
        # A no-show and a completion need an appointment that has STARTED, so
        # those bookings are earlier today and the waiting customer is queued
        # for that same day - the cascade keys on the booking's date. (The
        # first run queued her for a future day and read "waiting" as a miss.)
        if event == "cancel":
            start, for_day = local_dt(day, "10:00"), day
        else:
            start = datetime.now(timezone.utc) - timedelta(hours=2)
            for_day = start.astimezone(ZoneInfo("Asia/Beirut")).date()
        sql(f"UPDATE waitlist_entries SET status='cancelled' WHERE artist_id='{w.artist}' AND requested_date='{for_day}' AND status IN ('waiting','notified')")
        bid = insert_booking(w, start, 60, buffer_min=30)
        e = entry(n, for_day)
        if event == "cancel":
            call("PATCH", f"/bookings/{bid}/cancel", {"reason": "j2"}, w.tok)
        else:
            call("PATCH", f"/bookings/{bid}/{event}", {}, w.tok)
        results[event] = status(e)
        sql(f"DELETE FROM notifications WHERE booking_id='{bid}'")
        sql(f"DELETE FROM bookings WHERE id='{bid}'")
    rec("16.1", "PASS" if set(results.values()) == {"notified"} else "FAIL",
        f"entry status after each slot-freeing event: {results} (hold and deposit expiry: e2e-suite27 16.5)")

    # 16.4 the message
    sql(f"UPDATE waitlist_entries SET status='cancelled' WHERE artist_id='{w.artist}' AND requested_date='{day}' AND status IN ('waiting','notified')")
    bid = insert_booking(w, local_dt(day, "11:00"), 60)
    e = entry(9)
    call("PATCH", f"/bookings/{bid}/cancel", {"reason": "j2"}, w.tok)
    msg = sql(f"SELECT COALESCE(payload::text,'') FROM notifications WHERE template_name='waitlist_slot_open' AND user_id="
              f"(SELECT customer_id FROM waitlist_entries WHERE id='{e}') ORDER BY created_at DESC LIMIT 1")
    rec("16.4", "PASS" if msg and (day.strftime("%-d") in msg or day.isoformat() in msg) and "min" in msg.lower() else "FAIL",
        f"waitlist_slot_open queued for her: {bool(msg)}; names the day and the minutes: {msg[:140]}")
    sql(f"DELETE FROM notifications WHERE booking_id='{bid}'")
    sql(f"DELETE FROM bookings WHERE id='{bid}'")
    # 16.2 the stall only the sweep can clear: A notified with a lapsed
    # confirm window, B waiting behind her, and NOTHING else happening. The
    # waitlist worker runs every 5 minutes, so this waits for one sweep.
    stall_day = day + timedelta(days=1)
    a = entry(30, stall_day)
    b = entry(31, stall_day)
    sql(f"UPDATE waitlist_entries SET status='notified', notified_at=NOW() - interval '1 hour', "
        f"confirm_deadline=NOW() - interval '5 minutes' WHERE id='{a}'")
    ctrl = (status(a), status(b))
    t0 = time.time()
    while time.time() - t0 < 330 and not (status(a) == "expired" and status(b) == "notified"):
        time.sleep(10)
    fresh = sql(f"SELECT confirm_deadline > NOW() FROM waitlist_entries WHERE id='{b}'")
    rec("16.2", "PASS" if ctrl == ("notified", "waiting") and status(a) == "expired" and status(b) == "notified" and fresh == "t" else "FAIL",
        f"before: A {ctrl[0]}, B {ctrl[1]}; after {round(time.time() - t0)}s with nothing else happening: A {status(a)}, "
        f"B {status(b)} with a fresh deadline: {fresh == 't'}")


SUITES = {2: suite2, 3: suite3, 4: suite4, 6: suite6, 7: suite7, 8: suite8, 9: suite9, 10: suite10,
          11: suite11, 12: suite12, 13: suite13, 14: suite14, 15: suite15, 16: suite16}


def teardown():
    tagged_salons = "SELECT id FROM salons WHERE name LIKE 'j2%'"
    cust = "SELECT id FROM users WHERE phone LIKE '+961763698%'"
    for q in [
        f"DELETE FROM media WHERE owner_type='artist' AND owner_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE 'j2.%')",
        f"DELETE FROM media WHERE owner_type='product' AND owner_id IN (SELECT id FROM products WHERE salon_id IN ({tagged_salons}))",
        f"DELETE FROM order_items WHERE order_id IN (SELECT id FROM orders WHERE salon_id IN ({tagged_salons}))",
        f"DELETE FROM orders WHERE salon_id IN ({tagged_salons})",
        f"DELETE FROM products WHERE salon_id IN ({tagged_salons})",
        f"DELETE FROM client_notes WHERE artist_id IN (SELECT id FROM artists WHERE salon_id IN ({tagged_salons}))",
        f"DELETE FROM invoices WHERE subscription_id IN (SELECT s.id FROM subscriptions s JOIN artists a ON a.id=s.artist_id JOIN users u ON u.id=a.user_id WHERE u.email LIKE 'j2.%')",
        "DELETE FROM plans WHERE code LIKE 'j2%'",
        f"DELETE FROM reviews WHERE booking_id IN (SELECT id FROM bookings WHERE salon_id IN ({tagged_salons}))",
        f"DELETE FROM waitlist_entries WHERE artist_id IN (SELECT id FROM artists WHERE salon_id IN ({tagged_salons}))",
        f"DELETE FROM notifications WHERE user_id IN ({cust})",
        f"DELETE FROM notifications WHERE booking_id IN (SELECT id FROM bookings WHERE salon_id IN ({tagged_salons}))",
        f"DELETE FROM bookings WHERE salon_id IN ({tagged_salons})",
        f"DELETE FROM business_hours_exceptions WHERE store_id IN (SELECT id FROM stores WHERE salon_id IN ({tagged_salons}))",
    ]:
        try:
            sql(q)
        except RuntimeError as e:
            print(f"  cleanup: {e}")
    c.cleanup()
    for q in [f"DELETE FROM refresh_tokens WHERE user_id IN ({cust})", f"DELETE FROM users WHERE id IN ({cust})"]:
        try:
            sql(q)
        except RuntimeError as e:
            print(f"  cleanup: {e}")
    left = sql(f"SELECT (SELECT count(*) FROM salons WHERE name LIKE 'j2%') + (SELECT count(*) FROM users WHERE phone LIKE '+961763698%')")
    print(f"  cleanup (journey rows): {left} residual{'' if left == '0' else '   !! NOT CLEAN !!'}")


if __name__ == "__main__":
    wanted = [int(a) for a in sys.argv[1:] if a.isdigit()]
    print("\n  E2E journeys - behaviour\n")
    try:
        w = World()
        for n, fn in SUITES.items():
            if wanted and n not in wanted:
                continue
            print(f"\n  ── Suite {n} ──")
            try:
                fn(w)
            except Exception as e:  # a harness failure is reported, never hidden
                rec(f"{n}.harness", "FAIL", f"{type(e).__name__}: {e}")
    except Exception as e:
        rec("setup", "FAIL", f"{type(e).__name__}: {e}")
    finally:
        teardown()
    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    print(f"\n  {p} pass, {f} fail\n")
    sys.exit(1 if f else 0)
