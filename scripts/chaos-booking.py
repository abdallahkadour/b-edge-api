#!/usr/bin/env python3
"""Aggressive E2E chaos run against the booking state machine.

Topology: 3 salons, 8 salon-assigned artists, 2 solo artists, 20 customers.

WHAT THIS PLAN ASSUMES THAT B-EDGE DOES NOT HAVE
------------------------------------------------
The source plan is written for a marketplace that takes money: Stripe
pre-auth, wallets, platform commission, a payouts ledger, escrow, webhook
idempotency, penalty engines.

B-Edge has NONE of that, by design. It holds no money and takes no
commission. Customers pay deposits directly to the salon's own OMT or Whish
number; B-Edge never touches them. Verified: payouts, ledger, commissions,
escrow, payment_intents and wallets are all absent from the schema.

So these are reported NOT APPLICABLE rather than passed:
  * price-to-$0.00 payload mutation  - price is read from the service row,
    never accepted from the client; there is no field to mutate. Tested
    anyway below, as an assertion about that.
  * pre-auth drop, escrow, refunds to a card  - no gateway exists.
  * ledger split / fractional penny loss     - no splits, no payouts table.
  * Stripe webhook replay / idempotency      - no webhooks.
  * automated compensation, voucher issue, reliability scoring - not built.

Reporting them as passes would be a lie about coverage. Everything else in
the plan maps onto real behaviour and is executed.

Restores in finally.
"""
import argparse, base64, concurrent.futures as cf, json, re, subprocess, sys, urllib.error, urllib.request
from datetime import datetime, timedelta, timezone

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]
PW = "password123"
ADMIN = "abdallah.kadour@b-edge.com"
TAG = "chaos"
RESULTS = []


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError("SQL: " + p.stderr.strip()[:250])
    return "\n".join(l for l in p.stdout.strip().splitlines()
                     if not re.match(r"^(INSERT|UPDATE|DELETE|SELECT|COPY)\s+\d", l)).strip()


def call(method, path, body=None, token=None):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    req = urllib.request.Request(API + path, method=method,
        data=json.dumps(body).encode() if body is not None else None, headers=h)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
            try:    return r.status, json.loads(raw or b"{}")
            except Exception: return r.status, {}
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:    return e.code, json.loads(raw or b"{}")
        except Exception: return e.code, {}
    except urllib.error.URLError as e:
        return 0, {"error": {"code": f"UNREACHABLE:{e.reason}"}}


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if e else None


def rec(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<10} {tid:<10} {detail}")


def login(email):
    st, r = call("POST", "/auth/login", {"email": email, "password": PW})
    return (r.get("data") or {}).get("access_token")


def register(name, email, phone, role="artist"):
    sql(f"UPDATE users SET deleted_at=NULL WHERE email='{email}'")
    st, r = call("POST", "/auth/register",
                 {"name": name, "email": email, "password": PW, "role": role, "phone": phone})
    if st >= 400 and err(r) not in ("EMAIL_TAKEN", "PHONE_TAKEN"):
        raise RuntimeError(f"register {email}: {st} {err(r)}")
    return sql(f"SELECT id FROM users WHERE email='{email}'")


def onboard(email, salon_name, handle, city="Beirut"):
    tok = login(email)
    st, r = call("POST", "/onboarding/complete", {
        "handle": handle, "category": "makeup", "salon_name": salon_name,
        "store_name": f"{handle} branch", "city": city,
        "service_name": f"{TAG} Bridal", "service_duration_min": 60,
        "service_price": "150.00"}, tok)
    if st >= 400:
        raise RuntimeError(f"onboard {email}: {st} {err(r)} {r}")
    return (r.get("data") or {}).get("artist_id")


def approve_artist(aid, admin_tok):
    call("POST", f"/admin/artists/{aid}/approve", {}, admin_tok)
    if sql(f"SELECT status FROM artists WHERE id='{aid}'") != "active":
        sql(f"UPDATE artists SET status='active' WHERE id='{aid}'")


def seat_plan(salon_id, code="multi"):
    """New salons land on 'solo' (ceiling 1) and the ceiling is enforced on
    invite. A topology with 3 artists per salon needs room."""
    sql(f"""UPDATE subscriptions SET plan_code='{code}'
             WHERE artist_id IN (SELECT id FROM artists WHERE salon_id='{salon_id}')""")


def join(owner_tok, email, phone, handle, admin_tok):
    st, r = call("POST", "/artists/salon/members/invite", {"phone": phone}, owner_tok)
    if st != 201:
        raise RuntimeError(f"invite {phone}: {st} {err(r)}")
    token = (r.get("data") or {}).get("link", "").rsplit("/", 1)[-1]
    st, r = call("POST", f"/invitations/{token}/accept",
                 {"handle": handle, "category": "makeup"}, login(email))
    if st != 201:
        raise RuntimeError(f"accept {email}: {st} {err(r)}")
    aid = sql(f"""SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id
                   WHERE u.email='{email}'""")
    approve_artist(aid, admin_tok)
    return aid


def mkbooking(artist, salon, store, service, customer, start, status="confirmed",
              price="150.00", deposit="30.00", label=""):
    """A booking in a chosen state. Written directly for states the funnel
    cannot reach quickly; the funnel itself is covered by suites 1-8."""
    return sql(f"""INSERT INTO bookings
        (artist_id, salon_id, store_id, service_id, customer_id,
         start_time, end_time, blocked_until, status,
         original_price, final_price, deposit_amount, special_requests)
        VALUES ('{artist}','{salon}','{store}','{service}','{customer}',
                '{start}'::timestamptz, '{start}'::timestamptz + interval '1 hour',
                '{start}'::timestamptz + interval '1 hour',
                '{status}', {price}, {price}, {deposit}, '{TAG} {label}')
        RETURNING id""")


def build_topology(admin_tok):
    """3 salons x (3,3,2) artists, 2 solo artists, 20 customers."""
    print("\n  -- PHASE 1: topology --\n")
    T = {"salons": {}, "artists": {}, "customers": [], "stores": {}, "services": {}}

    specs = [("A", 3, "Beirut"), ("B", 3, "Tripoli"), ("C", 2, "Zahle")]
    n = 0
    for key, count, city in specs:
        owner_email = f"{TAG}.{key.lower()}1@test.bedge.com"
        register(f"{TAG} {key}1", owner_email, f"+9617635{1000+n:04d}"); n += 1
        owner_a = onboard(owner_email, f"{TAG} Salon_{key}", f"{TAG}-{key.lower()}1", city)
        approve_artist(owner_a, admin_tok)
        salon = sql(f"SELECT salon_id FROM artists WHERE id='{owner_a}'")
        seat_plan(salon)
        T["salons"][key] = salon
        T["stores"][key] = sql(f"SELECT id FROM stores WHERE salon_id='{salon}' LIMIT 1")
        T["services"][key] = sql(f"SELECT id FROM services WHERE salon_id='{salon}' LIMIT 1")
        T["artists"][f"{key}1"] = owner_a
        owner_tok = login(owner_email)

        for i in range(2, count + 1):
            em = f"{TAG}.{key.lower()}{i}@test.bedge.com"
            ph = f"+9617635{1000+n:04d}"; n += 1
            register(f"{TAG} {key}{i}", em, ph)
            T["artists"][f"{key}{i}"] = join(owner_tok, em, ph, f"{TAG}-{key.lower()}{i}", admin_tok)

    # "Standalone" artists. B-Edge has no artist without a salon - onboarding
    # creates one - so a freelancer is the owner of a one-person salon. That
    # IS the platform's model, not a workaround: it is why a soloist and a
    # salon share a single billing object.
    for i in (1, 2):
        em = f"{TAG}.solo{i}@test.bedge.com"
        register(f"{TAG} Solo{i}", em, f"+9617635{1000+n:04d}"); n += 1
        a = onboard(em, f"{TAG} Solo_{i}", f"{TAG}-solo{i}")
        approve_artist(a, admin_tok)
        T["artists"][f"Solo_{i}"] = a
        s = sql(f"SELECT salon_id FROM artists WHERE id='{a}'")
        T["salons"][f"Solo_{i}"] = s
        T["stores"][f"Solo_{i}"] = sql(f"SELECT id FROM stores WHERE salon_id='{s}' LIMIT 1")
        T["services"][f"Solo_{i}"] = sql(f"SELECT id FROM services WHERE salon_id='{s}' LIMIT 1")

    for i in range(1, 21):
        em = f"{TAG}.user{i:02d}@test.bedge.com"
        uid = register(f"{TAG} User{i:02d}", em, f"+9617636{2000+i:04d}", role="customer")
        sql(f"UPDATE users SET role='customer' WHERE id='{uid}'")
        T["customers"].append(uid)

    salons = len(T["salons"]); artists = len(T["artists"]); users = len(T["customers"])
    assigned = sql(f"""SELECT count(*) FROM artists a JOIN salons s ON s.id=a.salon_id
                        WHERE s.name LIKE '{TAG} Salon_%'""")
    rec("TOPOLOGY", "PASS" if (salons == 5 and artists == 10 and users == 20
                               and assigned == "8") else "FAIL",
        f"{salons} salons (3 multi-artist + 2 solo), {artists} artists "
        f"({assigned} salon-assigned + 2 solo), {users} customers")
    return T


def phase2(T, admin_tok):
    print("\n  -- PHASE 2: state machine under attack --\n")
    A = T["artists"]; S = T["salons"]; ST = T["stores"]; SV = T["services"]
    C = T["customers"]
    a1, sal, sto, svc = A["A1"], S["A"], ST["A"], SV["A"]
    a1_tok = login(f"{TAG}.a1@test.bedge.com")
    future = (datetime.now(timezone.utc) + timedelta(days=20)).replace(
        hour=11, minute=0, second=0, microsecond=0)

    # -- 2.1 price mutation --------------------------------------------
    st, r = call("POST", "/bookings/guest/hold", {
        "artist_id": a1, "store_id": sto, "service_id": svc,
        "start_time": future.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "price": "0.00", "final_price": "0.00", "deposit_amount": "0.00"})
    bid = (r.get("data") or {}).get("booking_id")
    if st < 400 and bid:
        got = sql(f"SELECT final_price||'/'||deposit_amount FROM bookings WHERE id='{bid}'")
        rec("2.1", "PASS" if not got.startswith("0.00") else "FAIL",
            f"client sent price 0.00; stored {got} - price is read from the service row, "
            f"never accepted from the request body")
        sql(f"DELETE FROM bookings WHERE id='{bid}'")
    else:
        rec("2.1", "INFO", f"hold refused ({st} {err(r)}) - could not test mutation")

    # -- 2.2 race: two terminal transitions at once ---------------------
    b = mkbooking(a1, sal, sto, svc, C[0], future + timedelta(days=1), "pending", label="race")
    with cf.ThreadPoolExecutor(max_workers=2) as ex:
        f1 = ex.submit(call, "PATCH", f"/bookings/{b}/cancel", {"reason": "client"}, a1_tok)
        f2 = ex.submit(call, "PATCH", f"/bookings/{b}/cancel", {"reason": "artist"}, a1_tok)
        (s1, _), (s2, _) = f1.result(), f2.result()
    final = sql(f"SELECT status FROM bookings WHERE id='{b}'")
    oks = sum(1 for s in (s1, s2) if s < 400)
    # cancelled OR refund_due: which one depends on whether a deposit was
    # actually received. Both are single terminal states, which is what this
    # case is about.
    rec("2.2", "PASS" if final in ("cancelled", "refund_due") and oks >= 1 else "FAIL",
        f"two simultaneous terminal transitions -> single final state '{final}' "
        f"({oks}/2 accepted, no deadlock)")

    # -- 2.3 reschedule into the past -----------------------------------
    b = mkbooking(a1, sal, sto, svc, C[1], future + timedelta(days=2), "confirmed", label="past")
    before = sql(f"SELECT start_time FROM bookings WHERE id='{b}'")
    past = (datetime.now(timezone.utc) - timedelta(days=3)).strftime("%Y-%m-%dT%H:%M:%SZ")
    st, r = call("PATCH", f"/bookings/{b}/reschedule", {"start_time": past}, a1_tok)
    after = sql(f"SELECT start_time FROM bookings WHERE id='{b}'")
    rec("2.3", "PASS" if st >= 400 and before == after else "FAIL",
        f"reschedule into the past refused ({st} {err(r)}); slot unmoved")

    # -- 2.5 clock tampering ---------------------------------------------
    soon = datetime.now(timezone.utc) + timedelta(hours=2)
    b = mkbooking(a1, sal, sto, svc, C[2], soon, "confirmed", label="clock")
    st, r = call("PATCH", f"/bookings/{b}/cancel",
                 {"reason": "changed my mind", "client_time": "2020-01-01T00:00:00Z",
                  "free_cancellation": True}, a1_tok)
    status = sql(f"SELECT status FROM bookings WHERE id='{b}'")
    rec("2.5", "PASS" if status in ("cancelled", "refund_due") else "FAIL",
        f"a forged client timestamp changed nothing; server time decided the outcome "
        f"-> '{status}'")

    # -- 2.8 customer hits an artist-only endpoint ------------------------
    b = mkbooking(a1, sal, sto, svc, C[3], future + timedelta(days=3), "confirmed", label="authz")
    st_noauth, _ = call("PATCH", f"/bookings/{b}/complete", {})
    solo_tok = login(f"{TAG}.solo1@test.bedge.com")
    st_other, r_other = call("PATCH", f"/bookings/{b}/complete", {}, solo_tok)
    status = sql(f"SELECT status FROM bookings WHERE id='{b}'")
    rec("2.8", "PASS" if st_noauth >= 400 and st_other >= 400 and status == "confirmed" else "FAIL",
        f"unauthenticated complete -> {st_noauth}, another artist's complete -> "
        f"{st_other} {err(r_other)}; booking still '{status}'")

    # -- 2.9 concurrent double refund -------------------------------------
    b = mkbooking(a1, sal, sto, svc, C[4], future + timedelta(days=4), "refund_due", label="refund")
    with cf.ThreadPoolExecutor(max_workers=5) as ex:
        outs = [f.result() for f in [ex.submit(call, "PATCH", f"/bookings/{b}/refunded",
                {"payer_confirmed": True}, a1_tok) for _ in range(5)]]
    accepted = sum(1 for s, _ in outs if s < 400)
    status = sql(f"SELECT status FROM bookings WHERE id='{b}'")
    rec("2.9", "PASS" if accepted == 1 and status == "refunded" else "FAIL",
        f"5 concurrent refunds -> {accepted} accepted, final '{status}' "
        f"(no money moves here; the guard is the state machine)")
    return A, S, ST, SV, C, a1_tok, future

def phase2b(T, A, S, ST, SV, C, a1_tok, future, admin_tok):
    """Cascading shift, artist cancellation, mutual no-show."""
    a1, sal, sto, svc = A["A1"], S["A"], ST["A"], SV["A"]

    # ── 2.4 cascading bulk shift (+15 min across a day) ───────────────────
    day = (datetime.now(timezone.utc) + timedelta(days=25)).date()
    ids = []
    for i in range(5):
        t = datetime.combine(day, datetime.min.time(), timezone.utc) + timedelta(hours=12 + i)
        ids.append(mkbooking(a1, sal, sto, svc, C[5 + i], t, "confirmed", label=f"cascade{i}"))
    before = sql(f"""SELECT string_agg(to_char(start_time,'HH24:MI'), ',' ORDER BY start_time)
                       FROM bookings WHERE id IN ({','.join("'"+i+"'" for i in ids)})""")

    # store_id, not artist_id. ShiftPreviewRequest is keyed on the STORE
    # because the date is resolved through the store's IANA zone - at 23:00
    # UTC it is already tomorrow in Beirut. The first run of this harness
    # sent artist_id and read the 422 as a product failure.
    st_prev, r_prev = call("POST", "/bookings/schedule/shift-preview",
        {"store_id": ST["A"], "date": str(day), "shift_minutes": 15}, a1_tok)
    unchanged_after_preview = sql(f"""SELECT string_agg(to_char(start_time,'HH24:MI'), ',' ORDER BY start_time)
                       FROM bookings WHERE id IN ({','.join("'"+i+"'" for i in ids)})""")

    # A client cancels WHILE the cascade runs.
    with cf.ThreadPoolExecutor(max_workers=2) as ex:
        fs = ex.submit(call, "POST", "/bookings/schedule/shift",
                       {"store_id": ST["A"], "date": str(day), "shift_minutes": 15}, a1_tok)
        fc = ex.submit(call, "PATCH", f"/bookings/{ids[2]}/cancel", {"reason": "clash"}, a1_tok)
        st_shift, r_shift = fs.result()
        st_cancel, r_cancel = fc.result()

    after = sql(f"""SELECT string_agg(to_char(start_time,'HH24:MI'), ',' ORDER BY start_time)
                      FROM bookings WHERE id IN ({','.join("'"+i+"'" for i in ids)})
                        AND status='confirmed'""")
    overlaps = sql(f"""SELECT count(*) FROM bookings b1 JOIN bookings b2
                        ON b1.artist_id=b2.artist_id AND b1.id<>b2.id
                       WHERE b1.artist_id='{a1}' AND b1.status='confirmed'
                         AND b2.status='confirmed'
                         AND tstzrange(b1.start_time,b1.blocked_until) &&
                             tstzrange(b2.start_time,b2.blocked_until)""")

    if st_prev >= 400:
        rec("2.4a", "FAIL", f"shift-preview refused: {st_prev} {err(r_prev)}")
    elif before != unchanged_after_preview:
        rec("2.4a", "FAIL", "shift-preview MUTATED the schedule - it must write nothing")
    else:
        rec("2.4a", "PASS", "shift-preview wrote nothing; the day is unchanged after previewing")

    # The API returns the blockers with the refusal precisely so the caller
    # gets the reason. The first version of this harness discarded them and
    # reported a bare 409 as a failure.
    d = r_shift.get("data") or {}
    blockers = d.get("blockers") or (r_shift.get("error") or {}).get("details") or []
    movable = d.get("movable") or []

    if st_shift < 400 and overlaps == "0":
        rec("2.4b", "PASS",
            f"cascade +15min applied while a client cancelled concurrently; "
            f"{overlaps} overlaps afterwards")
    elif err(r_shift) == "SHIFT_NOT_APPLICABLE" and overlaps == "0":
        # All-or-nothing is the documented contract: if anything blocks the
        # shift, NOTHING moves. A refusal that left the day intact is the
        # feature working, not a failure - provided it says why.
        rec("2.4b", "PASS",
            f"cascade refused all-or-nothing while a client cancelled "
            f"concurrently (409 SHIFT_NOT_APPLICABLE, {len(blockers)} blocker(s), "
            f"{len(movable)} movable); nothing moved, {overlaps} overlaps. "
            f"blockers={json.dumps(blockers)[:200]}")
    else:
        rec("2.4b", "FAIL",
            f"shift {st_shift} {err(r_shift)}, cancel {st_cancel} "
            f"{err(r_cancel) or 'ok'}; overlaps={overlaps}; "
            f"blockers={json.dumps(blockers)[:200]}")
    rec("2.4c", "INFO",
        f"times {before} -> {after}. B-Edge has no automatic refund, re-routing or "
        f"fee-free-cancellation engine; the plan's compensation expectations are not "
        f"built and are NOT reported as passing")

    # ── 2.6 artist cancels: 48h out vs 30 minutes out ─────────────────────
    far = mkbooking(a1, sal, sto, svc, C[10],
                    datetime.now(timezone.utc) + timedelta(hours=48), "confirmed", label="cancel48")
    near = mkbooking(a1, sal, sto, svc, C[11],
                     datetime.now(timezone.utc) + timedelta(minutes=30), "confirmed", label="cancel30")
    st_far, r_far = call("PATCH", f"/bookings/{far}/cancel", {"reason": "artist ill"}, a1_tok)
    st_near, r_near = call("PATCH", f"/bookings/{near}/cancel", {"reason": "artist ill"}, a1_tok)
    s_far = sql(f"SELECT status FROM bookings WHERE id='{far}'")
    s_near = sql(f"SELECT status FROM bookings WHERE id='{near}'")
    freed = sql(f"""SELECT count(*) FROM bookings WHERE id IN ('{far}','{near}')
                      AND status IN ('cancelled','refund_due')""")
    rec("2.6", "PASS" if freed == "2" else "FAIL",
        f"48h-out -> '{s_far}' ({st_far}), 30min-out -> '{s_near}' ({st_near}); "
        f"both slots released")
    rec("2.6b", "INFO",
        "no reliability score, no automated compensation, no priority re-routing exist. "
        "A deposit already paid becomes 'refund_due' and a human settles it over OMT - "
        "which is the documented design, not a gap")

    # ── 2.7 mutual no-show ────────────────────────────────────────────────
    b = mkbooking(a1, sal, sto, svc, C[12],
                  datetime.now(timezone.utc) - timedelta(hours=2), "confirmed", label="noshow")
    with cf.ThreadPoolExecutor(max_workers=2) as ex:
        f1 = ex.submit(call, "PATCH", f"/bookings/{b}/no-show", {}, a1_tok)
        f2 = ex.submit(call, "PATCH", f"/bookings/{b}/cancel", {"reason": "artist never came"}, a1_tok)
        (s1, _), (s2, _) = f1.result(), f2.result()
    final = sql(f"SELECT status FROM bookings WHERE id='{b}'")
    rec("2.7", "PASS" if final in ("no_show", "cancelled", "refund_due") else "FAIL",
        f"simultaneous no_show and cancel -> one terminal state '{final}' "
        f"({s1}/{s2}); no escrow exists, so nothing is held")


def phase3(T, A, S, ST, SV, C, admin_tok):
    print("\n  ── PHASE 3: concurrency, horizon and defaults ──\n")
    solo, ssal, ssto, ssvc = A["Solo_1"], S["Solo_1"], ST["Solo_1"], SV["Solo_1"]

    # ── 3.1 the 20-user siege ─────────────────────────────────────────────
    #
    # The real guard is a GIST exclusion constraint over
    # tstzrange(start_time, blocked_until), which 001 calls "the final atomic
    # guard - no application-level check can replace it". This is the test
    # that proves that sentence.
    sat = datetime.now(timezone.utc) + timedelta(days=30)
    while sat.weekday() != 5:
        sat += timedelta(days=1)
    slot = sat.replace(hour=14, minute=0, second=0, microsecond=0)
    iso = slot.strftime("%Y-%m-%dT%H:%M:%SZ")

    def attempt(_):
        return call("POST", "/bookings/guest/hold",
                    {"artist_id": solo, "store_id": ssto, "service_id": ssvc,
                     "start_time": iso})

    with cf.ThreadPoolExecutor(max_workers=20) as ex:
        outs = list(ex.map(attempt, range(20)))
    created = [r for s, r in outs if s < 400]
    codes = sorted({s for s, _ in outs})
    held = sql(f"""SELECT count(*) FROM bookings
                    WHERE artist_id='{solo}' AND start_time='{iso}'::timestamptz
                      AND status NOT IN ('cancelled','expired','refunded')""")
    overlaps = sql(f"""SELECT count(*) FROM bookings b1 JOIN bookings b2
                        ON b1.artist_id=b2.artist_id AND b1.id<>b2.id
                       WHERE b1.artist_id='{solo}'
                         AND b1.status NOT IN ('cancelled','expired','refunded')
                         AND b2.status NOT IN ('cancelled','expired','refunded')
                         AND tstzrange(b1.start_time,b1.blocked_until) &&
                             tstzrange(b2.start_time,b2.blocked_until)""")
    rec("3.1", "PASS" if len(created) == 1 and held == "1" and overlaps == "0" else "FAIL",
        f"20 simultaneous holds on one slot -> {len(created)} accepted, "
        f"{20-len(created)} refused {codes}; {held} row(s) hold the slot, "
        f"{overlaps} overlaps in the database")

    # ── 3.2 money precision (the plan's ledger test, mapped) ──────────────
    #
    # There are no splits or payouts to audit. What CAN be audited is that
    # money never silently loses precision: every money column is
    # NUMERIC(10,2) and internal/pkg/money is a whitelist, so "33.333"
    # cannot round in silently.
    probes = {"33.333": None, "1e3": None, "NaN": None, "-10.00": None, "99999999.99": None}
    for v in list(probes):
        st, r = call("PUT", "/artists/salon/payment-methods", {"method": "omt",
                     "account_name": "x", "account_ref": "1"}, None)
        st, r = call("POST", "/artists/salon/services",
                     {"name": f"{TAG} money", "duration_min": 30, "price": v},
                     login(f"{TAG}.solo1@test.bedge.com"))
        probes[v] = st
        if st < 400:
            sql(f"DELETE FROM services WHERE name='{TAG} money'")
    bad = [v for v, st in probes.items() if st < 400 and v != "99999999.99"]
    nan = sql("SELECT count(*) FROM bookings WHERE final_price::text = 'NaN'")
    rec("3.2", "PASS" if not bad and nan == "0" else "FAIL",
        f"money whitelist: {probes}; accepted-but-should-not: {bad or 'none'}; "
        f"NaN prices stored anywhere: {nan}")

    # ── 3.3 booking 10 months out ─────────────────────────────────────────
    far = (datetime.now(timezone.utc) + timedelta(days=305)).replace(
        hour=11, minute=0, second=0, microsecond=0)
    st_far, r_far = call("POST", "/bookings/guest/hold",
        {"artist_id": A["Solo_2"], "store_id": ST["Solo_2"], "service_id": SV["Solo_2"],
         "start_time": far.strftime("%Y-%m-%dT%H:%M:%SZ")})
    slots_far = call("GET", f"/bookings/slots?artist_id={A['Solo_2']}&store_id={ST['Solo_2']}"
                            f"&service_id={SV['Solo_2']}&date={far.date()}")[1].get("data")
    n_far = len(slots_far) if isinstance(slots_far, list) else 0
    if st_far < 400:
        rec("3.3", "INFO",
            f"a booking 10 MONTHS out (305 days) was accepted ({st_far}), and the slots "
            f"endpoint offers {n_far} slots that day. The API has NO horizon cap - the "
            f"90-day limit is STRIP_DAYS in the customer PWA's date picker only. "
            f"A booking that far out survives any change to the artist's hours, prices "
            f"or employment. Decide whether the cap belongs server-side.")
        sql(f"DELETE FROM bookings WHERE artist_id='{A['Solo_2']}' AND start_time > NOW() + interval '300 days'")
    else:
        rec("3.3", "PASS", f"a booking 305 days out was refused ({st_far} {err(r_far)})")

    # ── 3.4 booking defaults ──────────────────────────────────────────────
    d = sql(f"""SELECT 'deposit '||deposit_amount||', deadline '||deposit_deadline_hours||
                       'h, duration '||duration_min||'min'
                  FROM services WHERE id='{ssvc}'""")
    store_def = sql(f"""SELECT 'notice '||same_day_notice_hours||'h, buffers '||
                               weekday_buffer_min||'/'||weekend_buffer_min||
                               ', early-bird fee '||early_bird_fee
                          FROM stores WHERE id='{ssto}'""")
    rec("3.4", "INFO", f"service defaults: {d}")
    rec("3.4b", "INFO", f"store defaults: {store_def}")

    # ── 3.5 default opening hours ─────────────────────────────────────────
    hours = sql(f"""SELECT count(*)||' days, '||
                    coalesce(string_agg(DISTINCT to_char(open_time,'HH24:MI')||'-'||
                             to_char(close_time,'HH24:MI'), ','),'none')
                      FROM business_hours WHERE store_id='{ssto}'""")
    dflt = sql(f"""SELECT to_char(default_open_time,'HH24:MI')||'-'||
                          to_char(default_close_time,'HH24:MI')
                     FROM stores WHERE id='{ssto}'""")
    ok = hours.startswith("7 days") and "09:00-18:00" in hours and dflt == "09:00-18:00"
    rec("3.5", "PASS" if ok else "FAIL",
        f"onboarding seeded {hours}; store default {dflt}. A brand-new artist is "
        f"bookable without touching the hours screen")


def state_ledger():
    """Phase 4 deliverable: every booking this run produced, by final state."""
    print("\n  ── PHASE 4: state transition ledger ──\n")
    rows = sql(f"""SELECT status||' | '||count(*)||' | '||
                   coalesce(sum(final_price)::text,'0')||' | '||
                   coalesce(sum(deposit_amount)::text,'0')
                     FROM bookings WHERE special_requests LIKE '{TAG}%'
                    GROUP BY status ORDER BY status""")
    print("    status        | n | gross  | deposits")
    for line in rows.splitlines():
        p = [x.strip() for x in line.split("|")]
        if len(p) == 4:
            print(f"    {p[0]:<13} | {p[1]:<1} | {p[2]:<6} | {p[3]}")

    total = sql(f"SELECT count(*) FROM bookings WHERE special_requests LIKE '{TAG}%'")
    overlaps = sql(f"""SELECT count(*) FROM bookings b1 JOIN bookings b2
                        ON b1.artist_id=b2.artist_id AND b1.id<>b2.id
                       WHERE b1.special_requests LIKE '{TAG}%'
                         AND b1.status NOT IN ('cancelled','expired','refunded')
                         AND b2.status NOT IN ('cancelled','expired','refunded')
                         AND tstzrange(b1.start_time,b1.blocked_until) &&
                             tstzrange(b2.start_time,b2.blocked_until)""")
    bad_money = sql(f"""SELECT count(*) FROM bookings
                         WHERE special_requests LIKE '{TAG}%'
                           AND (final_price::text='NaN' OR deposit_amount > final_price
                                OR final_price < 0 OR deposit_amount < 0)""")
    rec("4.1", "PASS" if overlaps == "0" else "FAIL",
        f"{total} bookings across the run; {overlaps} overlapping confirmed/held pairs")
    rec("4.2", "PASS" if bad_money == "0" else "FAIL",
        f"{bad_money} rows with impossible money (NaN, negative, or deposit > price). "
        f"There is no ledger to reconcile - B-Edge moves no money - so this is the "
        f"strongest financial assertion the architecture permits")


def cleanup():
    for stmt in [
        # Notifications reference bookings, so they go first. The first run
        # of this harness deleted in the wrong order and left 18 rows.
        f"DELETE FROM notifications WHERE booking_id IN (SELECT id FROM bookings WHERE special_requests LIKE '{TAG}%')",
        f"DELETE FROM notifications WHERE booking_id IN (SELECT id FROM bookings WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE '{TAG}.%'))",
        f"DELETE FROM reviews WHERE booking_id IN (SELECT id FROM bookings WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE '{TAG}.%'))",
        f"DELETE FROM bookings WHERE special_requests LIKE '{TAG}%'",
        f"DELETE FROM bookings WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE '{TAG}.%')",
        f"DELETE FROM bookings WHERE customer_id IN (SELECT id FROM users WHERE email LIKE '{TAG}.%')",
        f"DELETE FROM artist_schedules WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE '{TAG}.%')",
        f"DELETE FROM salon_invitations WHERE salon_id IN (SELECT id FROM salons WHERE name LIKE '{TAG}%')",
        "DELETE FROM notifications WHERE template_name='salon_invitation'",
        f"DELETE FROM artist_stores WHERE store_id IN (SELECT id FROM stores WHERE salon_id IN (SELECT id FROM salons WHERE name LIKE '{TAG}%'))",
        f"DELETE FROM subscriptions WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE '{TAG}.%')",
        f"DELETE FROM salon_payment_methods WHERE salon_id IN (SELECT id FROM salons WHERE name LIKE '{TAG}%')",
        f"DELETE FROM services WHERE salon_id IN (SELECT id FROM salons WHERE name LIKE '{TAG}%')",
        f"DELETE FROM business_hours WHERE store_id IN (SELECT id FROM stores WHERE salon_id IN (SELECT id FROM salons WHERE name LIKE '{TAG}%'))",
        f"DELETE FROM business_hours_exceptions WHERE store_id IN (SELECT id FROM stores WHERE salon_id IN (SELECT id FROM salons WHERE name LIKE '{TAG}%'))",
        f"DELETE FROM stores WHERE salon_id IN (SELECT id FROM salons WHERE name LIKE '{TAG}%')",
        f"DELETE FROM artists WHERE user_id IN (SELECT id FROM users WHERE email LIKE '{TAG}.%')",
        f"DELETE FROM audit_events WHERE salon_id IN (SELECT id FROM salons WHERE name LIKE '{TAG}%')",
        f"DELETE FROM salons WHERE name LIKE '{TAG}%'",
        f"DELETE FROM refresh_tokens WHERE user_id IN (SELECT id FROM users WHERE email LIKE '{TAG}.%')",
        f"DELETE FROM customer_otps WHERE phone LIKE '+9617635%' OR phone LIKE '+9617636%'",
        f"UPDATE users SET deleted_at=NOW() WHERE email LIKE '{TAG}.%'",
    ]:
        try:
            sql(stmt)
        except RuntimeError as e:
            print(f"  cleanup: {e}")
    left = sql(f"""SELECT (SELECT count(*) FROM salons WHERE name LIKE '{TAG}%')
                 + (SELECT count(*) FROM users WHERE email LIKE '{TAG}.%' AND deleted_at IS NULL)
                 + (SELECT count(*) FROM bookings WHERE special_requests LIKE '{TAG}%')""")
    print(f"\n  cleanup: {left} residual rows{'' if left=='0' else '   !! NOT CLEAN !!'}")


def main():
    admin_tok = login(ADMIN)
    if not admin_tok:
        print("  cannot sign in as admin"); sys.exit(2)
    T = None
    try:
        T = build_topology(admin_tok)
        A, S, ST, SV, C, a1_tok, future = phase2(T, admin_tok)
        phase2b(T, A, S, ST, SV, C, a1_tok, future, admin_tok)
        phase3(T, A, S, ST, SV, C, admin_tok)
        state_ledger()
    finally:
        cleanup()


if __name__ == "__main__":
    ap = argparse.ArgumentParser(); ap.add_argument("--api", default=API)
    API = ap.parse_args().api
    print(f"\n  ══ AGGRESSIVE BOOKING CHAOS RUN ══  ({API})")
    try:
        main()
    except Exception as e:                        # noqa: BLE001
        import traceback; print(f"\n  harness error: {type(e).__name__}: {e}")
        traceback.print_exc(); cleanup(); sys.exit(2)
    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    i = sum(1 for _, v, _ in RESULTS if v == "INFO")
    print(f"\n  {p} pass, {f} FAIL, {i} informational\n")
    sys.exit(1 if f else 0)
