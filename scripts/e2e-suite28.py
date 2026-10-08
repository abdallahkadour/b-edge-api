#!/usr/bin/env python3
"""E2E suite 28 - who an invitation is for, and when a login should stop working.

Added 2026-10-08 with security plan section 3.4f. The API half; the join
page is driven by b-edge-web/scripts/e2e-suite28-ui.mjs, and 28.11-28.13 are
Go tests.

  28.2/28.3 (API)  only the invitee answers; a decline needs a login
  28.4      the invitee accepts once; the link cannot be used again
  28.5      AUTH-20   an invitation that names two different people
  28.6      AUTH-14b  every member write through a removed member's token
  28.7      AUTH-21   deleting an account ends its sessions
  28.8      AUTH-22   a frozen or suspended account cannot keep itself signed in
  AUTH-23   logout ends the refresh token
  28.9      the API refuses what the onboarding form explains
  28.10     a customer's reschedule has no screen (a gap, reported as GAP)
  INJ-09    hostile invitation tokens never produce a 500
  FRAUD-23  (opt-in, --fraud23) forged address headers do not reset the
            promo-code limiter. Opt-in for the same reason as suite 27's
            --fraud20: it spends this machine's code budget for 10 minutes.

28.6 runs every case twice: with the member's token from BEFORE removal,
and with a fresh login AFTER it. A stale token that gets through where a
fresh one is refused is the stale token's doing; both getting through is a
different defect (the route never checks the salon at all), and is reported
as such rather than folded into AUTH-14.

Builds its own salon and accounts and destroys them in a finally block,
reporting the residual count. Helpers come from chaos-booking.py. Requires
the API built with -tags devbypass (make dev).

  python3 scripts/e2e-suite28.py
  python3 scripts/e2e-suite28.py --fraud23
"""
import http.client
import importlib.util
import json
import os
import subprocess
import sys
import time
import urllib.parse
from datetime import date, timedelta

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("chaos", os.path.join(HERE, "chaos-booking.py"))
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)
c.TAG = "s28"            # every row this run creates carries it, for cleanup

RESULTS = []
PHONE = "+9617636{:04d}"     # artists 5100-5120; cleanup() clears customer_otps under +9617636%
GUEST = "+961763659{:02d}"   # guest buyers and bookers, hard-deleted in teardown
HOST, PORT, BASE = "localhost", 3000, "/api/v1"


def rec(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<5} {tid:<10} {detail}")


def raw(method, path, body=None, token=None, cookie=None, headers=None):
    """One request, returning (status, json, refresh cookie). The artist
    refresh token travels only in an httpOnly cookie, so urllib's helper -
    which drops response headers - cannot test it.

    Waits out the GENERAL rate limit (RATE_LIMIT_EXCEEDED) and only that:
    any other 429 is the behaviour under test."""
    for _ in range(12):
        h = {"Content-Type": "application/json"}
        if token:
            h["Authorization"] = "Bearer " + token
        if cookie:
            h["Cookie"] = "refresh_token=" + cookie
        h.update(headers or {})
        conn = http.client.HTTPConnection(HOST, PORT, timeout=30)
        try:
            conn.request(method, BASE + path, body=json.dumps(body) if body is not None else None, headers=h)
            resp = conn.getresponse()
            data = resp.read()
            set_cookie = None
            for k, v in resp.getheaders():
                if k.lower() == "set-cookie" and v.startswith("refresh_token="):
                    set_cookie = v.split(";", 1)[0].split("=", 1)[1]
            try:
                j = json.loads(data) if data else {}
            except ValueError:
                j = {}
        except (ConnectionError, http.client.HTTPException, OSError) as e:
            return 0, {"error": {"code": f"CONNECTION:{type(e).__name__}"}}, None
        finally:
            conn.close()
        if resp.status == 429 and err(j) == "RATE_LIMIT_EXCEEDED":
            print("  ... general rate limit reached; waiting 30 s")
            time.sleep(30)
            continue
        return resp.status, j, set_cookie
    return resp.status, j, set_cookie


def call(method, path, body=None, token=None, headers=None):
    st, j, _ = raw(method, path, body, token, headers=headers)
    return st, j


# The chaos helpers (register, onboard, join, login) wait out the general
# limit too - suite 27 died on an unpaced setup call on 2026-10-08.
c.call = lambda method, path, body=None, token=None: call(method, path, body, token)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if isinstance(e, dict) else None


def data(r):
    return (r or {}).get("data") or {}


def login_full(email):
    st, r, ck = raw("POST", "/auth/login", {"email": email, "password": c.PW})
    return data(r).get("access_token"), ck, st, err(r)


def refresh(cookie):
    st, r, ck = raw("POST", "/auth/refresh", {}, cookie=cookie)
    return st, err(r), data(r).get("access_token"), ck


def invite(owner_tok, phone=None, email=None):
    body = {}
    if phone:
        body["phone"] = phone
    if email:
        body["email"] = email
    st, r = call("POST", "/artists/salon/members/invite", body, owner_tok)
    link = data(r).get("link") or ""
    return st, err(r), link.rsplit("/", 1)[-1] if link else None


def inv_status(token_raw):
    import hashlib
    h = hashlib.sha256(token_raw.encode()).hexdigest()
    return c.sql(f"SELECT status FROM salon_invitations WHERE token_hash='{h}'")


def artist(name, n):
    """A registered artist account with a verified phone and no salon."""
    email = f"s28.{name}@test.bedge.com"
    c.register(f"s28 {name}", email, PHONE.format(n))
    c.sql(f"UPDATE users SET status='active' WHERE email='{email}'")
    c.verify_phone(email)
    return email


def free_day(owner, store, service):
    for d in range(2, 14):
        day = (date.today() + timedelta(days=d)).isoformat()
        st, r = call("GET", f"/bookings/slots?artist_id={owner}&store_id={store}&service_id={service}&date={day}")
        s = data(r) if isinstance(data(r), list) else (data(r).get("slots") if isinstance(data(r), dict) else [])
        times = [x["start_time"] for x in (s or [])]
        if len(times) >= 4:
            return day, times
    return None, []


# ── 28.2-28.5 Invitations ───────────────────────────────────────────────

def invitations(owner_tok):
    a = artist("a", 5101)
    x = artist("x", 5102)
    a_tok, x_tok = c.login(a), c.login(x)

    # 28.2/28.3 (API): someone else, and nobody, cannot answer it; the invitee can
    st, code, tok = invite(owner_tok, phone=PHONE.format(5101))
    if st != 201 or not tok:
        rec("28.2", "FAIL", f"setup: invite refused {st} {code}")
        return
    sp, rp = call("GET", f"/invitations/{tok}")
    s_none, r_none = call("POST", f"/invitations/{tok}/decline", {})
    s_xd, r_xd = call("POST", f"/invitations/{tok}/decline", {}, x_tok)
    s_xa, r_xa = call("POST", f"/invitations/{tok}/accept", {"handle": "s28-x", "category": "makeup"}, x_tok)
    still = inv_status(tok)
    ok = (sp == 200 and s_none == 401 and s_xd == 403 and err(r_xd) == "INVITATION_NOT_FOR_YOU"
          and s_xa == 403 and err(r_xa) == "INVITATION_NOT_FOR_YOU" and still == "pending")
    rec("28.2", "PASS" if ok else "FAIL",
        f"preview {sp}; decline with no login {s_none} {err(r_none)}; another artist: decline {s_xd} {err(r_xd)}, "
        f"accept {s_xa} {err(r_xa)}; status still {still}")

    s_d, r_d = call("POST", f"/invitations/{tok}/decline", {}, a_tok)
    after = inv_status(tok)
    s_p2, r_p2 = call("GET", f"/invitations/{tok}")
    s_a2, r_a2 = call("POST", f"/invitations/{tok}/accept", {"handle": "s28-a", "category": "makeup"}, a_tok)
    ok = s_d in (200, 204) and after == "declined" and s_p2 >= 400 and s_a2 >= 400
    rec("28.3", "PASS" if ok else "FAIL",
        f"the invitee declines -> {s_d} {err(r_d)}, status {after}; the link afterwards: preview {s_p2} {err(r_p2)}, "
        f"accept {s_a2} {err(r_a2)}")

    # 28.4 a fresh invitation, accepted once
    st, code, tok2 = invite(owner_tok, phone=PHONE.format(5101))
    if st != 201:
        rec("28.4", "FAIL", f"setup: re-invite after a decline refused {st} {code}")
    else:
        s1, r1 = call("POST", f"/invitations/{tok2}/accept", {"handle": "s28-a", "category": "makeup"}, a_tok)
        s2, r2 = call("POST", f"/invitations/{tok2}/accept", {"handle": "s28-a", "category": "makeup"}, c.login(a))
        member = c.sql(f"""SELECT count(*) FROM artists ar JOIN users u ON u.id=ar.user_id
                            WHERE u.email='{a}' AND ar.salon_id IS NOT NULL""")
        st_l, r_l = call("GET", "/artists/salon/invitations", None, owner_tok)
        listed = [i.get("status") for i in (r_l.get("data") or []) if isinstance(i, dict)]
        ok = s1 == 201 and member == "1" and s2 >= 400 and inv_status(tok2) == "accepted"
        rec("28.4", "PASS" if ok else "FAIL",
            f"accept {s1}; a member now: {member == '1'}; accept again {s2} {err(r2)}; "
            f"status {inv_status(tok2)}; owner's list shows {sorted(set(listed))}")

    # 28.5 / AUTH-20: the phone of one artist and the email of another
    cc = artist("c", 5103)
    ee = artist("e", 5104)
    st, code, tok3 = invite(owner_tok, phone=PHONE.format(5103), email=ee)
    if st >= 400:
        rec("AUTH-20", "PASS", f"an invitation naming two different people is refused at invite: {st} {code}")
        return
    # The email's owner first: if she is refused while the invitation is
    # still pending, the lookup chose the phone's owner - not a race.
    se, re_ = call("POST", f"/invitations/{tok3}/accept", {"handle": "s28-e", "category": "makeup"}, c.login(ee))
    sc, rc = call("POST", f"/invitations/{tok3}/accept", {"handle": "s28-c", "category": "makeup"}, c.login(cc))
    who = "the phone's owner" if sc == 201 else "the email's owner" if se == 201 else "neither"
    rec("AUTH-20", "FAIL",
        f"sent ({st}) to the phone of one artist and the email of another; the email's owner -> {se} {err(re_)}, "
        f"then the phone's owner -> {sc} {err(rc)}. {who} joined - chosen by LIMIT 1 with no ORDER BY, "
        f"not by anything the owner said")


# ── 28.6 / AUTH-14b everything a removed member's old token can do ───────

def stale_member(owner_tok, owner, salon, store, service):
    m = artist("m", 5105)
    m_id = c.join(owner_tok, m, PHONE.format(5105), "s28-m", c.login(c.ADMIN))
    m_old = c.login(m)

    # Things to act on, made before she is removed.
    pid = c.sql(f"INSERT INTO products (salon_id,name,price,stock_quantity) VALUES ('{salon}','s28 serum',20.00,10) RETURNING id")

    def order(n):
        st, r = call("POST", "/orders", {"salon_id": salon, "name": "s28 Buyer", "phone": GUEST.format(n),
                     "delivery_lat": 33.89, "delivery_lng": 35.5, "items": [{"product_id": pid, "quantity": 1}]})
        oid = data(r).get("id") or data(r).get("order_id")
        call("PATCH", f"/artists/salon/orders/{oid}/confirm-payment", {}, owner_tok)
        return oid
    o_ship, o_ship_fresh, o_deliver = order(1), order(2), order(3)
    call("PATCH", f"/artists/salon/orders/{o_deliver}/ship", {}, owner_tok)

    day, times = free_day(owner, store, service)
    gap = int(c.sql(f"SELECT duration_min + COALESCE(buffer_min,0) FROM services WHERE id='{service}'"))
    picked, last = [], None
    from datetime import datetime
    for t in times:
        at = datetime.fromisoformat(t.replace("Z", "+00:00"))
        if last is None or (at - last).total_seconds() >= (gap + 15) * 60:
            picked.append(t)
            last = at
    bookings = []
    for i, t in enumerate(picked[:2]):
        st, r = call("POST", "/bookings/guest/hold", {"artist_id": owner, "store_id": store,
                     "service_id": service, "start_time": t})
        b = data(r).get("booking_id")
        call("PATCH", f"/bookings/guest/{b}/submit", {"name": "s28 Guest", "phone": GUEST.format(10 + i)})
        bookings.append(b)
    cust = c.sql(f"SELECT customer_id FROM bookings WHERE id='{bookings[0]}'") if bookings else ""
    media = c.sql(f"""INSERT INTO media (owner_type, owner_id, url, type)
                      VALUES ('artist','{m_id}','https://res.cloudinary.com/demo/image/upload/sample.jpg','photo')
                      RETURNING id""")

    # Positive control: her token reads the salon's orders while she is a member.
    st, r = call("GET", "/artists/salon/orders", None, m_old)
    seen = o_ship in json.dumps(r)
    if st != 200 or not seen:
        rec("AUTH-14b", "FAIL", f"positive control: as a member she cannot read the salon's orders ({st} {err(r)})")
        return

    st, r = call("DELETE", f"/artists/salon/members/{m_id}", None, owner_tok)
    gone = c.sql(f"SELECT COALESCE(salon_id::text,'none') FROM artists WHERE id='{m_id}'")
    if st >= 400 or gone != "none":
        rec("AUTH-14b", "FAIL", f"setup: removal {st} {err(r)}, her salon now {gone}")
        return
    m_new = c.login(m)
    print(f"  removed -> {st}; her salon now: {gone}. Each line: fresh login / OLD token\n")

    b0 = bookings[0] if bookings else "00000000-0000-0000-0000-000000000000"
    rota = {"store_id": store, "days": [{"day_of_week": d, "start_time": "10:00", "end_time": "18:00",
                                         "is_working": True} for d in range(7)]}
    def shows(marker):
        """A read only leaks if the SALON'S data comes back. An empty list
        answered with 200 is not a leak: the client list is per artist, so a
        removed member with no clients of her own gets [] from any token -
        the first run of this suite read that 200 as a failure."""
        return lambda r: bool(marker) and marker in json.dumps(r)

    def wrote(check):
        return lambda r: check()

    cases = [
        # (label, method, path, body, did it expose the salon's data / change it?)
        ("read orders", "GET", "/artists/salon/orders", None, shows(o_ship)),
        ("read clients", "GET", "/clients", None, shows(cust)),
        ("read a client", "GET", f"/clients/{cust}", None, shows(cust)),
        ("read services", "GET", "/artists/salon/services", None, shows(service)),
        ("read the team", "GET", "/artists/salon/members", None, shows(owner)),
        ("read the owner's bookings", "GET", f"/bookings/artist/{owner}", None, shows(bookings[0] if bookings else None)),
        ("ship an order", "PATCH", "/artists/salon/orders/{oid}/ship", {},
         wrote(lambda: c.sql(f"SELECT status FROM orders WHERE id='{o_ship}'") == "shipped")),
        ("deliver an order", "PATCH", f"/artists/salon/orders/{o_deliver}/deliver", {},
         wrote(lambda: c.sql(f"SELECT status FROM orders WHERE id='{o_deliver}'") == "delivered")),
        ("write a client note", "PUT", f"/clients/{cust}/notes", {"content": "s28 stale note"},
         wrote(lambda: c.sql("SELECT count(*) FROM client_notes WHERE content='s28 stale note'") != "0")),
        ("switch a service on", "PUT", f"/artists/salon/my-services/{service}", {"offered": True},
         wrote(lambda: c.sql(f"SELECT count(*) FROM artist_services WHERE artist_id='{m_id}'") != "0")),
        ("set her hours at the salon's store", "PUT", "/artists/me/schedule", rota,
         wrote(lambda: c.sql(f"SELECT count(*) FROM artist_schedules WHERE artist_id='{m_id}'") != "0")),
        ("tag a photo with the salon's service", "PUT", f"/media/{media}/services", {"service_ids": [service]},
         wrote(lambda: c.sql(f"SELECT count(*) FROM media_services WHERE media_id='{media}'") != "0")),
        ("approve the owner's booking", "PATCH", f"/bookings/{b0}/approve", {},
         wrote(lambda: c.sql(f"SELECT status FROM bookings WHERE id='{b0}'") != "pending")),
        ("cancel the owner's booking", "PATCH", f"/bookings/{b0}/cancel", {"reason": "s28"},
         wrote(lambda: c.sql(f"SELECT status FROM bookings WHERE id='{b0}'") == "cancelled")),
    ]
    stale_got_through = []
    for label, method, path, body, exposed in cases:
        # Fresh login FIRST: it must change nothing, so the old token's turn
        # is measured on an untouched object.
        sf, rf = call(method, path.replace("{oid}", o_ship_fresh), body, m_new)
        fresh_hit = sf < 400 and exposed(rf)
        so, ro = call(method, path.replace("{oid}", o_ship), body, m_old)
        old_hit = so < 400 and exposed(ro)
        if old_hit and fresh_hit:
            verdict, note = "FAIL", "BOTH got the salon's data - the route does not check the salon at all"
        elif old_hit:
            verdict = "FAIL"
            note = "the old token " + ("CHANGED it" if method != "GET" else "got the salon's data")
            stale_got_through.append(label)
        elif so < 400:
            verdict, note = "PASS", "answered, with nothing of the salon's"
        else:
            verdict, note = "PASS", "refused"
        rec("AUTH-14b", verdict, f"{label:<38} fresh {sf} {err(rf) or '':<24} old {so} {err(ro) or '':<24} {note}")
    print(f"\n  AUTH-14b: {len(stale_got_through)} of {len(cases)} actions got through with the old token\n")


# ── 28.7 / 28.8 / AUTH-23 sessions after the account changes ─────────────

def sessions():
    # AUTH-21 delete
    d = artist("d", 5106)
    acc, ck, st, code = login_full(d)
    if not ck:
        rec("AUTH-21", "FAIL", f"setup: login gave no refresh cookie ({st} {code})")
    else:
        sd, rd = call("DELETE", "/auth/delete-account", None, acc)
        sr, cr, new_acc, _ = refresh(ck)
        sl, rl, _ = raw("POST", "/auth/login", {"email": d, "password": c.PW})
        so, ro = call("GET", "/notifications", None, acc)
        ok = sd < 400 and sr == 401 and not new_acc and sl == 401
        rec("AUTH-21", "PASS" if ok else "FAIL",
            f"delete-account {sd} {err(rd)}; refresh afterwards {sr} {cr}; login {sl} {err(rl)}; "
            f"[informational] the old access token: {so} (it lives out its 15 minutes)")

    # AUTH-22 frozen (self-service) and suspended (operator)
    f = artist("f", 5107)
    acc, ck, st, code = login_full(f)
    sf, rf = call("PATCH", "/auth/freeze-account", {}, acc)
    sl, rl, _ = raw("POST", "/auth/login", {"email": f, "password": c.PW})
    sr, cr, new_acc, ck2 = refresh(ck)
    use = call("GET", "/notifications", None, new_acc)[0] if new_acc else None
    control = sl == 403 and err(rl) == "ACCOUNT_FROZEN"
    if not control:
        rec("AUTH-22", "FAIL", f"positive control: a frozen account's login was not refused ({sl} {err(rl)})")
    else:
        rec("AUTH-22", "PASS" if sr == 401 else "FAIL",
            f"frozen ({sf}); login refused {sl} ACCOUNT_FROZEN; refresh {sr} {cr or ''}"
            + (f" -> a NEW session, which reads the inbox: {use}" if new_acc else ""))

    s = artist("s", 5108)
    acc, ck, st, code = login_full(s)
    c.sql(f"UPDATE users SET status='suspended' WHERE email='{s}'")
    sl, rl, _ = raw("POST", "/auth/login", {"email": s, "password": c.PW})
    sr, cr, new_acc, ck2 = refresh(ck)
    rounds = 0
    while new_acc and ck2 and rounds < 3:     # does it renew indefinitely?
        sr2, _, new_acc, ck2 = refresh(ck2)
        rounds += 1 if sr2 == 200 else 0
    control = sl == 403 and err(rl) == "ACCOUNT_SUSPENDED"
    if not control:
        rec("AUTH-22", "FAIL", f"positive control: a suspended account's login was not refused ({sl} {err(rl)})")
    else:
        rec("AUTH-22", "PASS" if sr == 401 else "FAIL",
            f"suspended by an operator; login refused {sl} ACCOUNT_SUSPENDED; refresh {sr} {cr or ''}"
            + (f"; renewed {rounds} more times in a row" if sr == 200 else ""))

    # AUTH-23 logout
    l_ = artist("l", 5109)
    acc, ck, st, code = login_full(l_)
    so, ro, _ = raw("POST", "/auth/logout", {}, acc, cookie=ck)
    sr, cr, new_acc, _ = refresh(ck)
    sa, ra = call("GET", "/notifications", None, acc)
    rec("AUTH-23", "PASS" if so < 400 and sr == 401 else "FAIL",
        f"logout {so}; refresh with the old cookie {sr} {cr}; [informational] old access token {sa}")


# ── 28.9 the API and the onboarding form agree ───────────────────────────

def onboarding_rules():
    g = artist("g", 5110)
    tok = c.login(g)
    good = {"handle": "s28-g", "category": "makeup", "salon_name": "s28 G Salon", "store_name": "s28 g branch",
            "city": "Beirut", "service_name": "s28 Bridal", "service_duration_min": 60, "service_price": "150.00"}
    bad = [("service_duration_min", 10), ("service_duration_min", 481),
           ("service_price", "-5"), ("service_price", "50.999"), ("handle", "ab")]
    for field, value in bad:
        body = dict(good, **{field: value})
        st, r = call("POST", "/onboarding/complete", body, tok)
        named = field in json.dumps((r or {}).get("error") or {})
        # The point is that the server refuses what the form explains, naming
        # the field. 400 vs 422 is recorded, not judged: the validator's
        # rules answer 422, the money parser's 400.
        rec("28.9", "PASS" if 400 <= st < 500 and named else "FAIL",
            f"{field}={value!r:<8} -> {st} {err(r)}; names the field: {named}")
    onboarded = c.sql(f"""SELECT count(*) FROM artists ar JOIN users u ON u.id=ar.user_id WHERE u.email='{g}'""")
    if onboarded != "0":
        rec("28.9", "FAIL", "one of the bad submissions onboarded her")


# ── 28.10 reschedule has no screen ───────────────────────────────────────

def reschedule_screen():
    web = os.path.join(HERE, "..", "..", "b-edge-web", "projects")
    p = subprocess.run(["grep", "-rli", "--include=*.ts", "--include=*.html", "reschedule", web],
                       capture_output=True, text=True)
    files = [l for l in p.stdout.splitlines() if l and ".spec." not in l]
    if files:
        rec("28.10", "PASS", f"a screen calls reschedule: {files[:3]}")
    else:
        rec("28.10", "GAP", "PATCH /bookings/:id/reschedule works (E2E 14.4) but no screen in either app calls it")


# ── INJ-09 hostile invitation tokens ─────────────────────────────────────

def hostile_tokens(any_tok):
    tokens = ["A" * 10000, "..%2F..%2Fetc%2Fpasswd", "%00", urllib.parse.quote("رمز"), urllib.parse.quote("😀"),
              "' OR '1'='1"]
    worst = []
    for t in tokens:
        t_path = t if "%" in t else urllib.parse.quote(t, safe="")
        for method, path, tok in [("GET", f"/invitations/{t_path}", None),
                                  ("POST", f"/invitations/{t_path}/accept", any_tok),
                                  ("POST", f"/invitations/{t_path}/decline", any_tok)]:
            st, r = call(method, path, {} if method == "POST" else None, tok)
            worst.append((st, f"{method} {t[:12]}"))
    fivexx = [w for w in worst if w[0] >= 500 or w[0] == 0]
    codes = sorted({w[0] for w in worst})
    rec("INJ-09", "PASS" if not fivexx else "FAIL",
        f"{len(worst)} hostile requests, statuses {codes}" + (f"; 5xx or dropped: {fivexx}" if fivexx else ""))


# ── FRAUD-23 forged headers vs the code limiter (opt-in) ─────────────────

def fraud23(salon, product):
    body = {"salon_id": salon, "code": "S28NOPE", "items": [{"product_id": product, "quantity": 1}]}
    spent = 0
    for i in range(25):
        st, r = call("POST", "/orders/discount-preview", dict(body, code=f"S28X{i}"))
        if st == 429:
            break
        spent += 1
    if st != 429 or err(r) != "TOO_MANY_CODE_ATTEMPTS":
        rec("FRAUD-23", "FAIL", f"positive control: the code limit never answered ({st} {err(r)} after {spent})")
        return
    forged = {"CF-Connecting-IP": "203.0.113.23", "X-Forwarded-For": "203.0.113.23", "X-Real-IP": "203.0.113.23"}
    sf, rf = call("POST", "/orders/discount-preview", dict(body, code="S28FORGED"), headers=forged)
    rec("FRAUD-23", "PASS" if sf == 429 and err(rf) == "TOO_MANY_CODE_ATTEMPTS" else "FAIL",
        f"limited after {spent} codes; with all three address headers forged -> {sf} {err(rf)}")


def main():
    admin_tok = c.login(c.ADMIN)
    if not admin_tok:
        rec("setup", "FAIL", "admin login failed")
        return
    owner_email = "s28.owner@test.bedge.com"
    c.register("s28 Owner", owner_email, PHONE.format(5100))
    c.sql(f"UPDATE users SET status='active' WHERE email='{owner_email}'")
    owner = c.onboard(owner_email, "s28 Salon", "s28-owner")
    c.approve_artist(owner, admin_tok)
    salon = c.sql(f"SELECT salon_id FROM artists WHERE id='{owner}'")
    store = c.sql(f"SELECT id FROM stores WHERE salon_id='{salon}' LIMIT 1")
    service = c.sql(f"SELECT id FROM services WHERE salon_id='{salon}' LIMIT 1")
    if c.sql(f"SELECT count(*) FROM subscriptions WHERE artist_id='{owner}'") == "0":
        c.sql(f"INSERT INTO subscriptions (artist_id, plan_code, monthly_price) VALUES ('{owner}','multi',0)")
    c.seat_plan(salon, "multi")
    owner_tok = c.login(owner_email)
    print(f"  setup: salon {salon[:8]}\n")

    invitations(owner_tok)
    print()
    stale_member(owner_tok, owner, salon, store, service)
    sessions()
    print()
    onboarding_rules()
    reschedule_screen()
    hostile_tokens(owner_tok)
    if "--fraud23" in sys.argv:
        pid = c.sql(f"SELECT id FROM products WHERE salon_id='{salon}' LIMIT 1")
        fraud23(salon, pid)


def teardown():
    salons = "SELECT id FROM salons WHERE name LIKE 's28%'"
    guests = "SELECT id FROM users WHERE phone LIKE '+961763659%'"
    s28_artists = "SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE 's28.%'"
    for q in [
        f"DELETE FROM media_services WHERE media_id IN (SELECT id FROM media WHERE owner_type='artist' AND owner_id IN ({s28_artists}))",
        f"DELETE FROM media WHERE owner_type='artist' AND owner_id IN ({s28_artists})",
        f"DELETE FROM client_notes WHERE salon_id IN ({salons})",
        f"DELETE FROM artist_services WHERE artist_id IN ({s28_artists})",
        f"DELETE FROM order_items WHERE order_id IN (SELECT id FROM orders WHERE salon_id IN ({salons}))",
        f"DELETE FROM orders WHERE salon_id IN ({salons})",
        f"DELETE FROM products WHERE salon_id IN ({salons})",
        f"DELETE FROM notifications WHERE user_id IN ({guests})",
        f"DELETE FROM notifications WHERE booking_id IN (SELECT id FROM bookings WHERE salon_id IN ({salons}))",
        f"DELETE FROM bookings WHERE salon_id IN ({salons})",
        f"DELETE FROM salon_invitations WHERE salon_id IN ({salons})",
        # Frozen, suspended and deleted accounts are put back to active before
        # cleanup() soft-deletes them, or the next run's register() revives an
        # account that cannot log in.
        "UPDATE users SET status='active', deleted_at=NULL WHERE email LIKE 's28.%'",
    ]:
        try:
            c.sql(q)
        except RuntimeError as e:
            print(f"  cleanup: {e}")
    c.cleanup()
    for q in [f"DELETE FROM refresh_tokens WHERE user_id IN ({guests})",
              f"DELETE FROM users WHERE id IN ({guests})"]:
        try:
            c.sql(q)
        except RuntimeError as e:
            print(f"  cleanup: {e}")
    left = c.sql(f"""SELECT (SELECT count(*) FROM salons WHERE name LIKE 's28%')
                   + (SELECT count(*) FROM users WHERE phone LIKE '+961763659%')
                   + (SELECT count(*) FROM users WHERE email LIKE 's28.%' AND deleted_at IS NULL)
                   + (SELECT count(*) FROM products WHERE name LIKE 's28%')
                   + (SELECT count(*) FROM client_notes WHERE content LIKE 's28%')""")
    print(f"  cleanup (suite 28 rows): {left} residual{'' if left == '0' else '   !! NOT CLEAN !!'}")


if __name__ == "__main__":
    print("\n  E2E suite 28 + security 3.4f\n")
    try:
        main()
    except Exception as e:  # a harness failure is reported, never hidden
        import traceback
        traceback.print_exc()
        rec("harness", "FAIL", f"{type(e).__name__}: {e}")
    finally:
        teardown()
    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    g = sum(1 for _, v, _ in RESULTS if v == "GAP")
    print(f"\n  {p} pass, {f} fail, {g} gap\n")
    sys.exit(1 if f else 0)
