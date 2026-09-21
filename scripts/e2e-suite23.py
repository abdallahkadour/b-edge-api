#!/usr/bin/env python3
"""E2E suite 23 - a salon over time, with an adversary in it.

Six people, one salon, one continuous story. Every other suite tests an
action in isolation; this one accumulates state, because the defects that
reached the launch artist were alternate flows and an alternate flow only
exists once there is history to be alternate to.

  Amal    - founds the salon, sole artist at the start and the end
  Bassima - second artist; tries to leave while a customer holds a booking
  Carine  - third artist
  Dana    - customer, books Amal
  Elias   - customer, books Bassima, and is still holding it in 23.5
  Mallory - adversary, wants the deposit reference

WHY MALLORY MATTERS. B-Edge never touches customer money: a customer reads
GET /salons/:id/payment-methods and sends a deposit to the OMT or Whish
number it returns. There is no gateway to compromise. The entire financial
attack surface is ONE STRING, and whoever can write it takes every deposit
for that salon with the platform vouching for the instructions.

Builds its own salon rather than borrowing Rania's - this run creates and
destroys three artists, two customers and an adversary, and doing that
inside the launch artist's salon would be reckless. Restores in finally.
"""
import argparse, json, re, subprocess, sys, urllib.error, urllib.request

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]
PW = "password123"
ADMIN = "abdallah.kadour@b-edge.com"
TAG = "s23"          # every row this run creates carries it, for cleanup
RESULTS = []


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError("SQL: " + p.stderr.strip()[:250])
    lines = [l for l in p.stdout.strip().splitlines()
             if not re.match(r"^(INSERT|UPDATE|DELETE|SELECT|COPY)\s+\d", l)]
    return "\n".join(lines).strip()


def call(method, path, body=None, token=None):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    req = urllib.request.Request(
        API + path, method=method,
        data=json.dumps(body).encode() if body is not None else None, headers=h)
    try:
        with urllib.request.urlopen(req, timeout=25) as r:
            raw = r.read()
            try:
                return r.status, json.loads(raw or b"{}")
            except Exception:
                return r.status, {}
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw or b"{}")
        except Exception:
            return e.code, {}
    except urllib.error.URLError as e:
        print(f"  API unreachable: {e.reason}")
        sys.exit(2)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if e else None


def rec(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<4} {tid:<7} {detail}")


def login(email):
    st, r = call("POST", "/auth/login", {"email": email, "password": PW})
    return (r.get("data") or {}).get("access_token")


def claim(tok, key):
    import base64
    p = tok.split(".")[1]
    p += "=" * (-len(p) % 4)
    return json.loads(base64.urlsafe_b64decode(p)).get(key)


def register(name, email, phone, role="artist"):
    """Creates an account, reviving one a previous run soft-deleted."""
    sql(f"UPDATE users SET deleted_at = NULL WHERE email = '{email}'")
    st, r = call("POST", "/auth/register", {
        "name": name, "email": email, "password": PW, "role": role, "phone": phone})
    if st >= 400 and err(r) not in ("EMAIL_TAKEN", "PHONE_TAKEN"):
        raise RuntimeError(f"register {email}: {st} {err(r)} {r}")
    return sql(f"SELECT id FROM users WHERE email = '{email}'")


CAST = {
    "amal":    ("Amal S23",    f"{TAG}.amal@test.bedge.com",    "+96176230001"),
    "bassima": ("Bassima S23", f"{TAG}.bassima@test.bedge.com", "+96176230002"),
    "carine":  ("Carine S23",  f"{TAG}.carine@test.bedge.com",  "+96176230003"),
    "mallory": ("Mallory S23", f"{TAG}.mallory@test.bedge.com", "+96176230009"),
    "dana":    ("Dana S23",    f"{TAG}.dana@test.bedge.com",    "+96176230011"),
    "elias":   ("Elias S23",   f"{TAG}.elias@test.bedge.com",   "+96176230012"),
}


def onboard(email, salon_name, handle):
    """Founds a salon through the real onboarding transaction."""
    tok = login(email)
    st, r = call("POST", "/onboarding/complete", {
        "handle": handle, "category": "makeup",
        "salon_name": salon_name,
        "store_name": f"{TAG} Branch", "city": "Beirut",
        "service_name": f"{TAG} Bridal", "service_duration_min": 60,
        "service_price": "100.00",
    }, tok)
    if st >= 400:
        raise RuntimeError(f"onboard {email}: {st} {err(r)} {r}")
    artist_id = (r.get("data") or {}).get("artist_id")

    # Onboarding must leave the store bookable.
    #
    # Until migration 049 it created a store and NO business_hours rows, so a
    # newly-founded salon was completely unbookable and nothing said so:
    # openinghours.Resolve found no row, reported the store closed, and slot
    # generation returned an empty list for every date forever. This helper
    # used to paper over it by seeding hours itself. It now checks instead -
    # a fixture that quietly repairs the thing under test is how a regression
    # hides.
    salon = sql(f"SELECT salon_id FROM artists WHERE id='{artist_id}'")
    store = sql(f"SELECT id FROM stores WHERE salon_id='{salon}' LIMIT 1")
    seeded = sql(f"SELECT count(*) FROM business_hours WHERE store_id='{store}'")
    window = sql(f"""SELECT DISTINCT to_char(open_time,'HH24:MI')||'-'||
                                     to_char(close_time,'HH24:MI')
                       FROM business_hours WHERE store_id='{store}'""")
    if seeded != "7":
        raise RuntimeError(
            f"onboarding left the store with {seeded} business_hours rows - a new "
            f"artist would be unbookable with no indication why (migration 049)")
    rec("23.0", "PASS",
        f"onboarding left the store bookable: {seeded} days at {window}, "
        f"from stores.default_open_time/default_close_time")

    return artist_id


def approve(artist_id, admin_tok):
    if admin_tok:
        call("POST", f"/admin/artists/{artist_id}/approve", {}, admin_tok)
    if sql(f"SELECT status FROM artists WHERE id='{artist_id}'") != "active":
        sql(f"UPDATE artists SET status='active' WHERE id='{artist_id}'")


def join_salon(owner_tok, joiner_email, joiner_phone, handle, admin_tok):
    """The real invite -> accept -> approve path."""
    st, r = call("POST", "/artists/salon/members/invite", {"phone": joiner_phone}, owner_tok)
    if st != 201:
        raise RuntimeError(f"invite {joiner_phone}: {st} {err(r)} {r}")
    token = (r.get("data") or {}).get("link", "").rsplit("/", 1)[-1]

    jt = login(joiner_email)
    st, r = call("POST", f"/invitations/{token}/accept",
                 {"handle": handle, "category": "makeup"}, jt)
    if st != 201:
        raise RuntimeError(f"accept for {joiner_email}: {st} {err(r)} {r}")
    aid = sql(f"""SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id
                   WHERE u.email='{joiner_email}'""")
    approve(aid, admin_tok)
    return aid, token


def book(artist_id, salon_id, store, service, customer_id, days_ahead, label):
    """A confirmed future booking. Written directly: the funnel is covered by
    suites 1-8, and what this suite tests is what happens to a booking when
    the artist who owns it tries to walk out."""
    return sql(f"""INSERT INTO bookings
        (artist_id, salon_id, store_id, service_id, customer_id,
         start_time, end_time, blocked_until, status,
         original_price, final_price, deposit_amount, special_requests)
        VALUES ('{artist_id}','{salon_id}','{store}','{service}','{customer_id}',
                NOW() + interval '{days_ahead} days',
                NOW() + interval '{days_ahead} days 1 hour',
                NOW() + interval '{days_ahead} days 1 hour',
                'confirmed', 100, 100, 20, '{label}') RETURNING id""")


def pay_ref(salon_id):
    """What a customer is told to send money to. THE money surface."""
    st, r = call("GET", f"/salons/{salon_id}/payment-methods")
    return json.dumps(r.get("data") or [], sort_keys=True)


def slots(artist, store, service, date):
    st, r = call("GET", f"/bookings/slots?artist_id={artist}&store_id={store}"
                        f"&service_id={service}&date={date}")
    d = r.get("data")
    return d if isinstance(d, list) else None


def main():
    admin_tok = login(ADMIN)
    ids = {k: register(*v) for k, v in CAST.items()}
    # Dana and Elias are customers; their accounts exist only to own bookings.
    for who in ("dana", "elias"):
        sql(f"UPDATE users SET role='customer' WHERE id='{ids[who]}'")

    salon = store = service = amal = None
    try:
        # ══ 23.1 One artist, one salon ═════════════════════════════════════
        amal = onboard(CAST["amal"][1], f"{TAG} Amal Studio", f"{TAG}-amal")
        approve(amal, admin_tok)
        salon = sql(f"SELECT salon_id FROM artists WHERE id='{amal}'")
        store = sql(f"SELECT id FROM stores WHERE salon_id='{salon}' LIMIT 1")
        service = sql(f"SELECT id FROM services WHERE salon_id='{salon}' LIMIT 1")

        amal_tok = login(CAST["amal"][1])
        n = sql(f"SELECT count(*) FROM artists WHERE salon_id='{salon}'")
        owner_ok = sql(f"SELECT (owner_id='{ids['amal']}')::text FROM salons WHERE id='{salon}'")
        if n == "1" and owner_ok == "true" and claim(amal_tok, "salon_role") == "owner":
            rec("23.1", "PASS", "Amal founded a salon: 1 artist, she is the owner, token says owner")
        else:
            rec("23.1", "FAIL", f"artists={n} owner_ok={owner_ok} role={claim(amal_tok,'salon_role')}")
            return

        # Amal sets where customers send deposits. This is the row Mallory wants.
        st, r = call("PUT", "/artists/salon/payment-methods",
                     {"method": "omt", "account_name": "Amal S23", "account_ref": "70111222"},
                     amal_tok)
        honest_money = pay_ref(salon)
        if st >= 400 or "70111222" not in honest_money:
            rec("23.1b", "FAIL", f"could not set the salon's payment reference: {st} {err(r)}")
            return
        rec("23.1b", "PASS", "Amal's OMT reference 70111222 is what a customer is told to pay")

        # ══ 23.0b Changing the default moves the whole week ════════════════
        #
        # The first version of stores.default_open_time only applied at
        # onboarding: change it afterwards and nothing happened. That left
        # two controls that look identical - the default, and the hours
        # screen's "Apply to all days" - with the one named "default" doing
        # nothing after day one. A setting that does nothing is worse than
        # no setting.
        #
        # What must NOT move is is_open. The times change; which days the
        # salon trades does not. Opening a Sunday the artist deliberately
        # closed, because she adjusted a time, would put her on Discover on
        # a day she is not there.
        week = lambda: sql(f"""SELECT string_agg(day_of_week||':'||
                    to_char(open_time,'HH24:MI')||
                    CASE WHEN is_open THEN '' ELSE '(CLOSED)' END, ' '
                    ORDER BY day_of_week)
                  FROM business_hours WHERE store_id='{store}'""")

        sql(f"UPDATE business_hours SET is_open=false "
            f" WHERE store_id='{store}' AND day_of_week=0")
        st, r = call("PATCH", f"/artists/stores/{store}",
                     {"default_open_time": "10:00"}, amal_tok)
        moved = week()
        sunday_shut = "0:10:00(CLOSED)" in moved
        all_ten = all(f"{d}:10:00" in moved for d in range(1, 7))
        stored = sql(f"""SELECT to_char(default_open_time,'HH24:MI')
                           FROM stores WHERE id='{store}'""")

        if st == 200 and all_ten and sunday_shut and stored == "10:00":
            rec("23.0b", "PASS",
                "changing the default moved every day to 10:00 and left the "
                "artist's closed Sunday closed")
        else:
            rec("23.0b", "FAIL",
                f"{st} stored={stored} week={moved}")

        # Put it back so the rest of the suite measures the seeded default.
        call("PATCH", f"/artists/stores/{store}", {"default_open_time": "09:00"}, amal_tok)
        sql(f"UPDATE business_hours SET is_open=true WHERE store_id='{store}'")

        # A byte-for-byte record of Amal's availability before anything happens.
        probe = sql("SELECT (CURRENT_DATE + 21)::text")
        amal_before = slots(amal, store, service, probe)

        # ══ 23.2 Bassima joins ═════════════════════════════════════════════
        bassima, _ = join_salon(amal_tok, CAST["bassima"][1], CAST["bassima"][2],
                                f"{TAG}-bassima", admin_tok)
        b_tok = login(CAST["bassima"][1])
        n = sql(f"SELECT count(*) FROM artists WHERE salon_id='{salon}'")
        if n == "2" and claim(b_tok, "salon_role") == "member":
            rec("23.2", "PASS", "Bassima joined: 2 artists, her token says member")
        else:
            rec("23.2", "FAIL", f"artists={n} role={claim(b_tok,'salon_role')}")

        before = sql(f"SELECT md5(ROW(name,price)::text) FROM services WHERE id='{service}'")
        st, r = call("PATCH", f"/artists/salon/services/{service}", {"price": "1.00"}, b_tok)
        after = sql(f"SELECT md5(ROW(name,price)::text) FROM services WHERE id='{service}'")
        if st == 403 and err(r) == "SALON_ROLE_FORBIDDEN" and before == after:
            rec("23.2b", "PASS", "a member's price edit is refused and the row is unchanged")
        else:
            rec("23.2b", "FAIL", f"{st} {err(r)} changed={before != after}")

        # ══ 23.3 Carine joins, making three ════════════════════════════════
        carine, _ = join_salon(amal_tok, CAST["carine"][1], CAST["carine"][2],
                               f"{TAG}-carine", admin_tok)
        c_tok = login(CAST["carine"][1])
        n = sql(f"SELECT count(*) FROM artists WHERE salon_id='{salon}'")
        owners = sql(f"""SELECT count(*) FROM artists a JOIN salons s ON s.id=a.salon_id
                          WHERE a.salon_id='{salon}' AND s.owner_id=a.user_id""")
        if n == "3" and owners == "1":
            rec("23.3", "PASS", "three artists, exactly one owner")
        else:
            rec("23.3", "FAIL", f"artists={n} owners={owners}")

        # Carine narrows her hours. Three artists is where a rota keyed on the
        # wrong column starts leaking; two would not have caught it.
        dow = int(sql(f"SELECT EXTRACT(DOW FROM DATE '{probe}')"))
        b_before = slots(bassima, store, service, probe)
        st, r = call("PUT", "/artists/me/schedule", {
            "store_id": store,
            "days": [{"day_of_week": dow, "start_time": "14:00",
                      "end_time": "16:00", "is_working": True}]}, c_tok)
        c_after = slots(carine, store, service, probe) or []
        leaked = []
        if slots(amal, store, service, probe) != amal_before:
            leaked.append("Amal")
        if slots(bassima, store, service, probe) != b_before:
            leaked.append("Bassima")
        if st == 200 and c_after and not leaked and len(c_after) < len(amal_before or []):
            rec("23.3b", "PASS",
                f"Carine narrowed to {len(c_after)} slots; Amal and Bassima unchanged")
        else:
            rec("23.3b", "FAIL", f"{st} carine={len(c_after)} leaked_into={leaked}")

        # ══ 23.4 Two customers book ════════════════════════════════════════
        dana_b = book(amal, salon, store, service, ids["dana"], 14, f"{TAG} dana-with-amal")
        elias_b = book(bassima, salon, store, service, ids["elias"], 15, f"{TAG} elias-with-bassima")

        st, r = call("GET", f"/bookings/artist/{bassima}", None, b_tok)
        b_sees = {x.get("id") for x in (r.get("data") or [])}
        if elias_b in b_sees and dana_b not in b_sees:
            rec("23.4", "PASS",
                "each booking is attributed to its own artist; Bassima cannot see Dana's")
        else:
            rec("23.4", "FAIL",
                f"Bassima sees own={elias_b in b_sees} other={dana_b in b_sees}")

        # ══ 23.5 Bassima tries to leave holding Elias's booking ════════════
        elias_before = sql(f"""SELECT md5(ROW(artist_id, start_time, status,
                                             final_price, customer_id)::text)
                                 FROM bookings WHERE id='{elias_b}'""")

        st_leave, r_leave = call("POST", "/artists/salon/members/leave", None, b_tok)
        st_kick, r_kick = call("DELETE", f"/artists/salon/members/{bassima}", None, amal_tok)

        still_in = sql(f"SELECT (salon_id='{salon}')::text FROM artists WHERE id='{bassima}'")
        elias_after = sql(f"""SELECT md5(ROW(artist_id, start_time, status,
                                            final_price, customer_id)::text)
                                FROM bookings WHERE id='{elias_b}'""")

        problems = []
        if not (st_leave == 409 and err(r_leave) == "HAS_FUTURE_BOOKINGS"):
            problems.append(f"leave gave {st_leave} {err(r_leave)}")
        if not (st_kick == 409 and err(r_kick) == "HAS_FUTURE_BOOKINGS"):
            problems.append(f"owner-removal gave {st_kick} {err(r_kick)}")
        if still_in != "true":
            problems.append("she left anyway")
        if elias_before != elias_after:
            problems.append("ELIAS'S BOOKING WAS MODIFIED")
        if problems:
            rec("23.5", "FAIL", "; ".join(problems))
        else:
            rec("23.5", "PASS",
                "both leaving and being removed are refused while Elias holds a booking, "
                "and his appointment is byte-identical afterwards")

        # ══ 23.6 Bassima leaves properly ═══════════════════════════════════
        sql(f"UPDATE bookings SET status='cancelled' WHERE id='{elias_b}'")
        st, r = call("POST", "/artists/salon/members/leave", None, b_tok)
        gone = sql(f"SELECT coalesce(salon_id::text,'NULL') FROM artists WHERE id='{bassima}'")
        history = sql(f"""SELECT count(*) FROM bookings
                           WHERE artist_id='{bassima}' AND special_requests LIKE '{TAG}%'""")
        n = sql(f"SELECT count(*) FROM artists WHERE salon_id='{salon}'")
        owner_ok = sql(f"SELECT (owner_id='{ids['amal']}')::text FROM salons WHERE id='{salon}'")

        b_tok_new = login(CAST["bassima"][1])
        st_read, _ = call("GET", "/artists/salon/services", None, b_tok_new)

        if (st == 204 and gone == "NULL" and history == "1" and n == "2"
                and owner_ok == "true" and st_read >= 400):
            rec("23.6", "PASS",
                "Bassima left once the booking was resolved; her booking history survived, "
                f"the salon is back to 2 artists, Amal still owns it, and Bassima's new "
                f"token can no longer read the menu ({st_read})")
        else:
            rec("23.6", "FAIL",
                f"leave={st} salon={gone} history={history} artists={n} "
                f"owner_ok={owner_ok} can_still_read={st_read}")

        # ══ 23.7 Mallory ═══════════════════════════════════════════════════
        m_tok = login(CAST["mallory"][1])

        # a. no salon at all
        st, r = call("PUT", "/artists/salon/payment-methods",
                     {"method": "omt", "account_name": "Mallory", "account_ref": "70999999"}, m_tok)
        if st == 403 and err(r) == "NO_SALON" and pay_ref(salon) == honest_money:
            rec("23.7a", "PASS", "an artist with no salon cannot touch any salon's money")
        else:
            rec("23.7a", "FAIL", f"{st} {err(r)}; money moved={pay_ref(salon) != honest_money}")

        # b. a LEGITIMATE member redirecting the salon's money
        mallory, m_invite = join_salon(amal_tok, CAST["mallory"][1], CAST["mallory"][2],
                                       f"{TAG}-mallory", admin_tok)
        m_tok = login(CAST["mallory"][1])
        st, r = call("PUT", "/artists/salon/payment-methods",
                     {"method": "omt", "account_name": "Mallory", "account_ref": "70999999"}, m_tok)
        after_money = pay_ref(salon)
        if st == 403 and err(r) == "SALON_ROLE_FORBIDDEN" and after_money == honest_money:
            rec("23.7b", "PASS",
                "a member of the salon cannot redirect the salon's deposits - "
                "refused, and the public reference is unchanged")
        else:
            rec("23.7b", "FAIL",
                f"{st} {err(r)}; PUBLIC PAY REFERENCE CHANGED={after_money != honest_money}")

        # c. naming another salon explicitly - salon comes from the token only
        other_salon = sql(f"""SELECT a.salon_id FROM artists a JOIN users u ON u.id=a.user_id
                               WHERE u.email='rania@bedge.com'""")
        other_before = pay_ref(other_salon)
        call("PUT", "/artists/salon/payment-methods",
             {"method": "omt", "account_name": "Mallory", "account_ref": "70999999",
              "salon_id": other_salon}, m_tok)
        if pay_ref(other_salon) == other_before:
            rec("23.7c", "PASS",
                "a salon_id in the body is inert - the salon is taken from the token")
        else:
            rec("23.7c", "FAIL", "ANOTHER SALON'S PAYMENT REFERENCE WAS CHANGED FROM A BODY FIELD")

        # d. redeeming an invitation issued to someone else
        st, r = call("POST", "/artists/salon/members/invite",
                     {"phone": "+96176230077"}, amal_tok)
        stolen = (r.get("data") or {}).get("link", "").rsplit("/", 1)[-1]
        st2, r2 = call("POST", f"/invitations/{stolen}/accept",
                       {"handle": f"{TAG}-thief", "category": "makeup"}, m_tok)
        visible = False
        if st2 < 400:
            st3, r3 = call("GET", "/artists/salon/members", None, amal_tok)
            visible = any(m["artist_id"] == mallory for m in (r3.get("data") or []))
        rec("23.7d", "INFO",
            f"an invitation issued to another number was redeemed by a different account: "
            f"{'accepted' if st2 < 400 else 'refused ' + str(err(r2))}. The token is the "
            f"credential, so this is by design - but the owner must see who joined "
            f"(visible in roster: {visible if st2 < 400 else 'n/a'}). Decide deliberately.")
        call("DELETE", f"/artists/salon/invitations/{(r.get('data') or {}).get('invitation',{}).get('id','')}",
             None, amal_tok)

        # e. an impostor an admin has REFUSED must not be bookable
        #
        # The first version of this case concluded "pending artists are
        # blocked" from a slot count of zero. The zero came from the store
        # having no business hours. Establish the condition explicitly: a
        # LIVE subscription, so that any refusal can only come from approval
        # status and not from billing.
        live_sub = sql(f"""SELECT count(*) FROM subscriptions
                            WHERE artist_id='{mallory}' AND cancelled_at IS NULL""")
        if live_sub == "0":
            sql(f"""INSERT INTO subscriptions (artist_id, plan_code, monthly_price,
                                               current_period_end)
                    VALUES ('{mallory}','starter', 7.00, NOW() + interval '30 days')""")

        outcomes = {}
        for i, status in enumerate(("pending", "rejected", "active")):
            sql(f"UPDATE artists SET status='{status}' WHERE id='{mallory}'")
            st_h, r_h = call("POST", "/bookings/guest/hold", {
                "artist_id": mallory, "store_id": store, "service_id": service,
                "start_time": f"{probe}T1{i}:00:00Z"})
            outcomes[status] = "HELD" if st_h < 400 else f"refused {err(r_h)}"
        sql(f"UPDATE artists SET status='active' WHERE id='{mallory}'")
        sql(f"DELETE FROM bookings WHERE artist_id='{mallory}' AND status='held'")

        if outcomes["pending"].startswith("refused") and \
           outcomes["rejected"].startswith("refused") and outcomes["active"] == "HELD":
            rec("23.7e", "PASS",
                f"admin review gates the money path: pending and rejected are refused, "
                f"active is held ({outcomes})")
        else:
            rec("23.7e", "FAIL",
                f"an artist an admin has not approved can take a deposit: {outcomes}")

        # f. a captured token outliving removal
        captured = login(CAST["mallory"][1])
        call("DELETE", f"/artists/salon/members/{mallory}", None, amal_tok)
        still = {}
        for label, m_, p_, b_ in [
            ("read the service menu", "GET", "/artists/salon/services", None),
            ("read the roster", "GET", "/artists/salon/members", None),
            ("read the payment method", "GET", "/artists/salon/payment-methods", None),
            ("redirect the money", "PUT", "/artists/salon/payment-methods",
             {"method": "omt", "account_name": "M", "account_ref": "70999999"}),
        ]:
            s_, r_ = call(m_, p_, b_, captured)
            still[label] = s_
        money_held = pay_ref(salon) == honest_money
        if still["redirect the money"] >= 400 and money_held:
            rec("23.7f", "INFO",
                f"a removed member's access token still reaches: "
                f"{ {k: v for k, v in still.items() if v < 400} or 'nothing'} for up to 15 "
                f"minutes (RevokeAllForUser revokes refresh tokens only). It does NOT reach "
                f"the money. Tracked as AUTH-14.")
        else:
            rec("23.7f", "FAIL",
                f"a REMOVED member could still redirect the money: {still}; "
                f"reference intact={money_held}")

        # g. another artist's customers
        st, r = call("GET", f"/bookings/artist/{amal}", None, captured)
        leak = "leak" if st < 400 and (r.get("data") or []) else "refused"
        st2, r2 = call("GET", f"/bookings/{dana_b}", None, captured)
        if leak == "refused" and st2 >= 400:
            rec("23.7g", "PASS",
                f"a removed member cannot read another artist's bookings ({st}) "
                f"or one by id ({st2})")
        else:
            rec("23.7g", "FAIL", f"customer data reachable: list={st} byid={st2}")

        # ══ 23.8 The salon survives its own history ════════════════════════
        final_n = sql(f"SELECT count(*) FROM artists WHERE salon_id='{salon}'")
        final_owner = sql(f"SELECT (owner_id='{ids['amal']}')::text FROM salons WHERE id='{salon}'")
        final_money = pay_ref(salon)
        amal_after = slots(amal, store, service, probe)
        kept = sql(f"SELECT count(*) FROM bookings WHERE special_requests LIKE '{TAG}%'")
        audits = sql(f"""SELECT count(*) FROM audit_events
                          WHERE salon_id='{salon}' AND actor_id IS NOT NULL""")

        problems = []
        if final_n != "2":
            problems.append(f"{final_n} artists, want 2 (Amal + Carine)")
        if final_owner != "true":
            problems.append("Amal is no longer the owner")
        if final_money != honest_money:
            problems.append("THE PAYMENT REFERENCE MOVED")
        if amal_after != amal_before:
            problems.append("Amal's availability changed across the whole story")
        if kept != "2":
            problems.append(f"{kept} bookings survived, want 2")
        if problems:
            rec("23.8", "FAIL", "; ".join(problems))
        else:
            rec("23.8", "PASS",
                f"after three joins, two bookings, one blocked exit, one clean exit and "
                f"seven attempts on the money: Amal still owns a 2-artist salon, her "
                f"availability is byte-identical to 23.1, both bookings survive, the "
                f"deposit reference is untouched, and {audits} membership events are audited")

    finally:
        if salon:
            # Every booking in the salon, not only tagged ones: 23.7e holds
            # through the API and those rows carry no special_requests.
            sql(f"DELETE FROM bookings WHERE salon_id='{salon}'")
            sql(f"DELETE FROM bookings WHERE artist_id IN "
                f"(SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id "
                f" WHERE u.email LIKE '{TAG}.%')")
            sql(f"DELETE FROM artist_schedules WHERE artist_id IN "
                f"(SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id "
                f" WHERE u.email LIKE '{TAG}.%')")
            sql(f"DELETE FROM salon_invitations WHERE salon_id='{salon}'")
            sql(f"DELETE FROM artist_stores WHERE store_id IN "
                f"(SELECT id FROM stores WHERE salon_id='{salon}')")
            sql(f"DELETE FROM subscriptions WHERE artist_id IN "
                f"(SELECT id FROM artists WHERE salon_id='{salon}' OR user_id IN "
                f" (SELECT id FROM users WHERE email LIKE '{TAG}.%'))")
            sql(f"DELETE FROM salon_payment_methods WHERE salon_id='{salon}'")
            sql(f"DELETE FROM services WHERE salon_id='{salon}'")
            sql(f"DELETE FROM business_hours WHERE store_id IN "
                f"(SELECT id FROM stores WHERE salon_id='{salon}')")
            sql(f"DELETE FROM stores WHERE salon_id='{salon}'")
            sql(f"DELETE FROM artists WHERE user_id IN "
                f"(SELECT id FROM users WHERE email LIKE '{TAG}.%')")
            sql(f"DELETE FROM audit_events WHERE salon_id='{salon}'")
            sql(f"DELETE FROM salons WHERE id='{salon}'")
        sql(f"DELETE FROM refresh_tokens WHERE user_id IN "
            f"(SELECT id FROM users WHERE email LIKE '{TAG}.%')")
        # Soft delete: audit_events elsewhere may reference these actors, and
        # that history is not ours to rewrite.
        sql(f"UPDATE users SET deleted_at = NOW() WHERE email LIKE '{TAG}.%'")

        residue = sql(f"""SELECT (SELECT count(*) FROM users
                                   WHERE email LIKE '{TAG}.%' AND deleted_at IS NULL)
                        + (SELECT count(*) FROM bookings WHERE special_requests LIKE '{TAG}%')
                        + (SELECT count(*) FROM salons WHERE name LIKE '{TAG}%')""")
        rania_money = sql("""SELECT count(*) FROM salon_payment_methods
                              WHERE account_ref = '70999999'""")
        print(f"\n  cleanup: {residue} residual rows"
              f"{'' if residue == '0' else '   !! NOT CLEAN !!'}")
        print(f"  Mallory's account_ref present anywhere: {rania_money}"
              f"{'' if rania_money == '0' else '   !! MONEY REDIRECTED !!'}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--api", default=API)
    a = ap.parse_args()
    API = a.api

    print(f"\n  ── E2E suite 23: a salon over time, with an adversary ──  ({API})\n")
    try:
        main()
    except Exception as e:                       # noqa: BLE001 - surface it
        import traceback
        print(f"\n  harness error: {type(e).__name__}: {e}")
        traceback.print_exc()
        sys.exit(2)

    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    i = sum(1 for _, v, _ in RESULTS if v == "INFO")
    print(f"\n  {p} pass, {f} fail, {i} to decide\n")
    sys.exit(1 if f else 0)
