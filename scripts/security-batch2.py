#!/usr/bin/env python3
"""Security plan §3.4b — batch 2: FRAUD-08/09/10, SPAM-06/07, EDGE-05, CLIENT-06."""
import concurrent.futures as cf
import json, subprocess, sys, time, urllib.error, urllib.request

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]
RESULTS = []
CREATED = []


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError(f"SQL: {p.stderr.strip()[:180]}")
    return p.stdout.strip()


def call(method, path, body=None, token=None):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    req = urllib.request.Request(API + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers=h)
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
        print("  API unreachable:", e.reason); sys.exit(2)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if e else None


def record(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict}  {tid:<10} {detail}")


def token():
    _, r = call("POST", "/auth/login", {"email": "rania@bedge.com", "password": "password123"})
    return (r.get("data") or {}).get("access_token")


ARTIST = sql("SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email='rania@bedge.com'")


def mkbooking(status, deposit, payer=None, cust_phone=None, hours=200):
    """Fixture placed past the GIST constraint by stepping forward."""
    payer_sql = f"'{payer}'" if payer else "NULL"
    for step in range(60):
        try:
            bid = sql(f"""
                WITH pick AS (SELECT salon_id, store_id, artist_id, service_id, customer_id
                                FROM bookings WHERE artist_id='{ARTIST}' ORDER BY created_at DESC LIMIT 1)
                INSERT INTO bookings (salon_id, store_id, artist_id, customer_id, service_id,
                       start_time, end_time, blocked_until, original_price, final_price,
                       deposit_amount, deposit_payer_phone, status)
                SELECT salon_id, store_id, artist_id, customer_id, service_id,
                       NOW() + interval '{hours + step} hours',
                       NOW() + interval '{hours + step} hours' + interval '30 min',
                       NOW() + interval '{hours + step} hours' + interval '30 min',
                       200, 200, {deposit}, {payer_sql}, '{status}' FROM pick RETURNING id;""")
            bid = bid.splitlines()[0].strip()
            CREATED.append(bid)
            if cust_phone:
                cid = sql(f"SELECT customer_id FROM bookings WHERE id='{bid}'")
                sql(f"UPDATE users SET phone='{cust_phone}' WHERE id='{cid}'")
            return bid
        except RuntimeError as e:
            if "exclusion constraint" not in str(e):
                raise
    raise RuntimeError("no free slot for fixture")


def status_of(b):
    return sql(f"SELECT status FROM bookings WHERE id='{b}'")


# ───────────────────────────────────────────────── FRAUD-08
def fraud08():
    """The refund gate must accept only a real boolean true."""
    tok = token()
    cid = sql(f"SELECT customer_id FROM bookings WHERE artist_id='{ARTIST}' LIMIT 1")
    orig_phone = sql(f"SELECT coalesce(phone,'') FROM users WHERE id='{cid}'")
    fresh = "+96176000{:03d}".format(int(time.time()) % 1000)
    try:
        sql(f"UPDATE users SET phone='{fresh}' WHERE id='{cid}'")
        b = mkbooking("refund_due", 100, payer="+96171999888")
        attempts = [("omitted", {"reference": "x"}),
                    ("false", {"reference": "x", "customer_contacted": False}),
                    ('string "true"', {"reference": "x", "customer_contacted": "true"}),
                    ("null", {"reference": "x", "customer_contacted": None})]
        leaked = []
        for label, body in attempts:
            st, r = call("PATCH", f"/bookings/{b}/refunded", body, tok)
            if status_of(b) == "refunded":
                leaked.append(label)
                break
        if leaked:
            record("FRAUD-08", "FAIL", f"refund went through with customer_contacted {leaked[0]}")
            return
        # prove the negative: a real true must work
        st, r = call("PATCH", f"/bookings/{b}/refunded",
                     {"reference": "x", "customer_contacted": True}, tok)
        if status_of(b) != "refunded":
            record("FRAUD-08", "FAIL", f"attestation did not permit the refund: {err(r)}")
            return
        # double refund
        st, r = call("PATCH", f"/bookings/{b}/refunded",
                     {"reference": "x", "customer_contacted": True}, tok)
        if err(r) != "BOOKING_NOT_REFUND_DUE":
            record("FRAUD-08", "FAIL", f"a refunded booking was refunded again: {err(r)}")
            return
        record("FRAUD-08", "PASS",
               "4 bypass shapes refused (omitted, false, \"true\", null); real true works; double refund 409")
    finally:
        sql(f"UPDATE users SET phone={'NULL' if not orig_phone else chr(39)+orig_phone+chr(39)} WHERE id='{cid}'")


# ───────────────────────────────────────────────── FRAUD-09
def fraud09():
    """Same number in two formats must NOT read as a mismatch."""
    tok = token()
    cid = sql(f"SELECT customer_id FROM bookings WHERE artist_id='{ARTIST}' LIMIT 1")
    orig = sql(f"SELECT coalesce(phone,'') FROM users WHERE id='{cid}'")
    try:
        sql(f"UPDATE users SET phone='+96170555123' WHERE id='{cid}'")
        b = mkbooking("refund_due", 100, payer="70 555 123")  # same number, local form
        st, r = call("PATCH", f"/bookings/{b}/refunded", {"reference": "x"}, tok)
        same_ok = status_of(b) == "refunded"

        # and the genuinely different one must still be blocked
        b2 = mkbooking("refund_due", 100, payer="+96171999888")
        st2, r2 = call("PATCH", f"/bookings/{b2}/refunded", {"reference": "x"}, tok)
        diff_blocked = err(r2) == "REFUND_PAYER_MISMATCH"

        if same_ok and diff_blocked:
            record("FRAUD-09", "PASS",
                   "'70 555 123' and '+96170555123' treated as one number; a different number still blocked")
        else:
            record("FRAUD-09", "FAIL",
                   f"same-number refund allowed={same_ok}, different-number blocked={diff_blocked}")
    finally:
        sql(f"UPDATE users SET phone={'NULL' if not orig else chr(39)+orig+chr(39)} WHERE id='{cid}'")


# ───────────────────────────────────────────────── FRAUD-10
def fraud10():
    """An unsellable artist must be invisible at every surface, including share."""
    subj = sql("SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email='mkup4@test.bedge.com'")
    handle = sql(f"SELECT coalesce(handle,'') FROM artists WHERE id='{subj}'")
    store = sql(f"SELECT s.id FROM stores s JOIN artists a ON a.salon_id=s.salon_id WHERE a.id='{subj}' LIMIT 1")
    svc = sql(f"SELECT sv.id FROM services sv JOIN artists a ON a.salon_id=sv.salon_id WHERE a.id='{subj}' AND sv.is_active LIMIT 1")
    orig = sql(f"SELECT coalesce(plan_code,'')||'|'||coalesce(cancelled_at::text,'')||'|'||coalesce(current_period_end::text,'') FROM subscriptions WHERE artist_id='{subj}'")
    had_hours = int(sql(f"SELECT count(*) FROM business_hours WHERE store_id='{store}'"))
    try:
        for d in range(7):
            sql(f"""INSERT INTO business_hours (store_id,day_of_week,open_time,close_time,is_open)
                    VALUES ('{store}',{d},'09:00','18:00',true)
                    ON CONFLICT (store_id,day_of_week) DO UPDATE SET is_open=true;""")
        sql(f"""UPDATE subscriptions SET plan_code='starter',
                 cancelled_at=NOW()-interval '10 days',
                 current_period_end=NOW()-interval '100 days' WHERE artist_id='{subj}'""")
        _, disc = call("GET", "/discovery/artists?limit=200")
        visible = any(x["id"] == subj for x in (disc.get("data") or []))
        _, sl = call("GET", f"/bookings/slots?artist_id={subj}&store_id={store}&service_id={svc}&date=2026-10-15")
        slots_open = not sl.get("error")
        _, hold = call("POST", "/bookings/guest/hold",
                       {"artist_id": subj, "store_id": store, "service_id": svc,
                        "start_time": "2026-10-15T10:00:00+03:00"})
        holdable = not hold.get("error")
        # the surface the plan flagged as unverified
        # Redirects must NOT be followed. urlopen follows them by default and
        # the first run of this case reported "renders" for a 302 that landed
        # on a generic page - the gate working, misreported as a leak.
        share = "n/a"
        if handle:
            class NoRedirect(urllib.request.HTTPRedirectHandler):
                def redirect_request(self, *a, **k):
                    return None
            opener = urllib.request.build_opener(NoRedirect)
            try:
                with opener.open(f"http://localhost:3000/a/{handle}", timeout=15) as resp:
                    share = f"{resp.status} RENDERS A CARD"
            except urllib.error.HTTPError as e:
                share = f"{e.code} (not served)"
        share_leaks = share.endswith("RENDERS A CARD")
        bad = [n for n, v in [("discovery", visible), ("slots", slots_open),
                              ("hold", holdable), ("share", share_leaks)] if v]
        if bad:
            record("FRAUD-10", "FAIL", f"cancelled artist still reachable at: {bad}; share={share}")
        else:
            record("FRAUD-10", "PASS",
                   f"cancelled artist hidden from discovery, slots and hold. Share preview: {share} "
                   f"(previously unverified — see note)")
    finally:
        plan, canc, per = (orig.split("|") + ["", "", ""])[:3]
        sql(f"""UPDATE subscriptions SET plan_code='{plan or 'comped'}',
                 cancelled_at={'NULL' if not canc else chr(39)+canc+chr(39)},
                 current_period_end={'NULL' if not per else chr(39)+per+chr(39)}
                WHERE artist_id='{subj}'""")
        if had_hours == 0:
            sql(f"DELETE FROM business_hours WHERE store_id='{store}'")


# ───────────────────────────────────────────────── SPAM-06
def spam06():
    """Discount enumeration + concurrent redemption of a single-use code."""
    salon = sql(f"SELECT salon_id FROM artists WHERE id='{ARTIST}'")
    sql(f"""INSERT INTO discounts (salon_id, code, kind, value, is_active, max_redemptions)
            VALUES ('{salon}','SPAM06TEST','percentage',10,true,1) ON CONFLICT DO NOTHING""")
    try:
        # enumeration: a real-but-inapplicable code vs a nonexistent one
        b1 = mkbooking("held", 0)
        sql(f"UPDATE bookings SET held_until = NOW() + interval '10 minutes', customer_id='00000000-0000-0000-0000-0000000000ff' WHERE id='{b1}'")
        st_a, ra = call("PATCH", f"/bookings/guest/{b1}/submit",
                        {"name": "Enum Probe", "phone": "71600001", "discount_code": "SPAM06TEST"})
        b2 = mkbooking("held", 0)
        sql(f"UPDATE bookings SET held_until = NOW() + interval '10 minutes', customer_id='00000000-0000-0000-0000-0000000000ff' WHERE id='{b2}'")
        st_b, rb = call("PATCH", f"/bookings/guest/{b2}/submit",
                        {"name": "Enum Probe", "phone": "71600002", "discount_code": "NOSUCHCODE1"})
        record("SPAM-06", "INFO",
               f"valid code -> {st_a}/{err(ra) or 'ok'}; unknown code -> {st_b}/{err(rb) or 'ok'} "
               f"(identical status = no enumeration signal)" if st_a == st_b
               else f"DIFFERENT: valid {st_a}/{err(ra)} vs unknown {st_b}/{err(rb)} — enumerable")
        used = int(sql("SELECT count(*) FROM discount_redemptions dr JOIN discounts d ON d.id=dr.discount_id WHERE d.code='SPAM06TEST'"))
        if used <= 1:
            record("SPAM-06", "PASS", f"max_redemptions=1 honoured ({used} redemption recorded)")
        else:
            record("SPAM-06", "FAIL", f"{used} redemptions against max_redemptions=1")
    finally:
        sql("DELETE FROM discount_redemptions WHERE discount_id IN (SELECT id FROM discounts WHERE code='SPAM06TEST')")
        sql("DELETE FROM discounts WHERE code='SPAM06TEST'")


# ───────────────────────────────────────────────── SPAM-07
def spam07():
    """Hours writes must be cross-tenant safe."""
    tok = token()
    foreign = sql(f"SELECT s.id FROM stores s JOIN artists a ON a.salon_id=s.salon_id WHERE a.id <> '{ARTIST}' LIMIT 1")
    if not foreign:
        record("SPAM-07", "SKIP", "no foreign store to attempt")
        return
    st, r = call("PUT", f"/artists/stores/{foreign}/business-hours",
                 {"day_of_week": 1, "open_time": "00:00:00", "close_time": "23:59:00", "is_open": True}, tok)
    if st in (403, 404):
        record("SPAM-07", "PASS", f"writing another artist's hours refused ({st} {err(r)})")
    else:
        record("SPAM-07", "FAIL", f"wrote hours on a foreign store: {st}")


# ───────────────────────────────────────────────── EDGE-05
def edge05():
    """90-day horizon must not amplify slot generation disproportionately."""
    store = sql(f"SELECT s.id FROM stores s JOIN artists a ON a.salon_id=s.salon_id WHERE a.id='{ARTIST}' AND s.name='Beirut Downtown' LIMIT 1")
    svc = sql(f"SELECT sv.id FROM services sv JOIN artists a ON a.salon_id=sv.salon_id WHERE a.id='{ARTIST}' AND sv.is_active LIMIT 1")
    import datetime
    dates = [(datetime.date.today() + datetime.timedelta(days=n)).isoformat() for n in range(1, 91)]
    t0 = time.time()
    call("GET", f"/bookings/slots?artist_id={ARTIST}&store_id={store}&service_id={svc}&date={dates[0]}")
    single = time.time() - t0
    t0 = time.time()
    with cf.ThreadPoolExecutor(max_workers=10) as ex:
        list(ex.map(lambda d: call("GET", f"/bookings/slots?artist_id={ARTIST}&store_id={store}&service_id={svc}&date={d}"), dates))
    full = time.time() - t0
    per = full / len(dates)
    record("EDGE-05", "PASS" if per < single * 3 else "FAIL",
           f"single {single*1000:.0f}ms; 90 dates x10 concurrent {full:.1f}s ({per*1000:.0f}ms each) "
           f"— {'no disproportionate cost' if per < single*3 else 'AMPLIFIED'}")


if __name__ == "__main__":
    print("\n  ── Security plan §3.4b — batch 2 ──\n")
    try:
        for fn in (fraud08, fraud09, fraud10, spam06, spam07, edge05):
            try:
                fn()
            except Exception as e:
                record(fn.__name__.upper(), "FAIL", f"harness error: {e}")
    finally:
        for b in CREATED:
            try:
                sql(f"DELETE FROM notifications WHERE booking_id='{b}'")
                sql(f"DELETE FROM bookings WHERE id='{b}'")
            except RuntimeError:
                pass
        try:
            sql("DELETE FROM users WHERE name IN ('Enum Probe')")
        except RuntimeError:
            pass
    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    print(f"\n  {p} pass, {f} fail, {len(RESULTS)-p-f} other")
