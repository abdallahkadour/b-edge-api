#!/usr/bin/env python3
"""Executes security plan section 3.4d - the multi-artist salon attack surface.

Thirteen cases were written when the feature shipped and none was executed
deliberately; FRAUD-13 only surfaced because E2E suite 23 tripped over it.
Written-and-unexecuted is not coverage.

Two of these were written from properties visible in the code rather than
from suspicion - DATA-03 and AUTH-14 - so they are expected to find
something. A case that reports UNDECIDED has measured a real behaviour that
needs a product decision, and must not be read as a pass.

Builds its own salon. Restores in finally.
"""
import argparse, base64, json, re, subprocess, sys, time, urllib.error, urllib.request

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]
PW = "password123"
ADMIN = "abdallah.kadour@b-edge.com"
TAG = "sec34d"
RESULTS = []


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError("SQL: " + p.stderr.strip()[:250])
    return "\n".join(l for l in p.stdout.strip().splitlines()
                     if not re.match(r"^(INSERT|UPDATE|DELETE|SELECT|COPY)\s+\d", l)).strip()


def call(method, path, body=None, token=None, base=None):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    req = urllib.request.Request((base or API) + path, method=method,
        data=json.dumps(body).encode() if body is not None else None, headers=h)
    try:
        with urllib.request.urlopen(req, timeout=25) as r:
            raw = r.read()
            try:    return r.status, json.loads(raw or b"{}")
            except Exception: return r.status, {}
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:    return e.code, json.loads(raw or b"{}")
        except Exception: return e.code, {}
    except urllib.error.URLError as e:
        print(f"  API unreachable: {e.reason}"); sys.exit(2)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if e else None


def rec(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<9} {tid:<9} {detail}")


def login(email):
    st, r = call("POST", "/auth/login", {"email": email, "password": PW})
    return (r.get("data") or {}).get("access_token")


def claim(tok, key):
    p = tok.split(".")[1]; p += "=" * (-len(p) % 4)
    return json.loads(base64.urlsafe_b64decode(p)).get(key)


def register(name, email, phone, role="artist"):
    sql(f"UPDATE users SET deleted_at=NULL WHERE email='{email}'")
    st, r = call("POST", "/auth/register",
                 {"name": name, "email": email, "password": PW, "role": role, "phone": phone})
    if st >= 400 and err(r) not in ("EMAIL_TAKEN", "PHONE_TAKEN"):
        raise RuntimeError(f"register {email}: {st} {err(r)}")
    return sql(f"SELECT id FROM users WHERE email='{email}'")


def onboard(email, salon, handle):
    tok = login(email)
    st, r = call("POST", "/onboarding/complete", {
        "handle": handle, "category": "makeup", "salon_name": salon,
        "store_name": f"{TAG} Branch", "city": "Beirut",
        "service_name": f"{TAG} Bridal", "service_duration_min": 60,
        "service_price": "100.00"}, tok)
    if st >= 400:
        raise RuntimeError(f"onboard {email}: {st} {err(r)} {r}")
    return (r.get("data") or {}).get("artist_id")


def seatPlan(salon_id, code="multi"):
    """Puts a test salon on a plan with room.

    New artists land on 'solo' (ceiling 1) since migration 050, and the
    ceiling is enforced on invite. A suite that exercises membership needs a
    plan that permits members - otherwise every invite is correctly refused
    and the suite tests nothing but the ceiling.

    Set explicitly rather than by disabling the check, so the enforcement
    under test is the same code that runs in production.
    """
    sql(f"""UPDATE subscriptions SET plan_code='{code}'
             WHERE artist_id IN (SELECT id FROM artists WHERE salon_id='{salon_id}')""")


def approve(aid, admin_tok):
    call("POST", f"/admin/artists/{aid}/approve", {}, admin_tok)
    if sql(f"SELECT status FROM artists WHERE id='{aid}'") != "active":
        sql(f"UPDATE artists SET status='active' WHERE id='{aid}'")


def invite(owner_tok, phone):
    st, r = call("POST", "/artists/salon/members/invite", {"phone": phone}, owner_tok)
    d = r.get("data") or {}
    return st, r, d.get("link", "").rsplit("/", 1)[-1], (d.get("invitation") or {}).get("id")


def main():
    admin_tok = login(ADMIN)
    ids = {k: register(n, e, p) for k, (n, e, p) in {
        "owner":   (f"{TAG} Owner",   f"{TAG}.owner@test.bedge.com",   "+96176340001"),
        "member":  (f"{TAG} Member",  f"{TAG}.member@test.bedge.com",  "+96176340002"),
        "outsider":(f"{TAG} Outside", f"{TAG}.outsider@test.bedge.com","+96176340003"),
        "thief":   (f"{TAG} Thief",   f"{TAG}.thief@test.bedge.com",   "+96176340004"),
    }.items()}

    salon = store = service = owner_a = None
    try:
        owner_a = onboard(f"{TAG}.owner@test.bedge.com", f"{TAG} Salon", f"{TAG}-owner")
        approve(owner_a, admin_tok)
        salon = sql(f"SELECT salon_id FROM artists WHERE id='{owner_a}'")
        store = sql(f"SELECT id FROM stores WHERE salon_id='{salon}' LIMIT 1")
        service = sql(f"SELECT id FROM services WHERE salon_id='{salon}' LIMIT 1")
        owner_tok = login(f"{TAG}.owner@test.bedge.com")
        call("PUT", "/artists/salon/payment-methods",
             {"method": "omt", "account_name": "Owner", "account_ref": "70345678"}, owner_tok)

        # A real member, so the owner/member cases below have someone to test
        # with. Without this AUTH-19 skips and reports nothing.
        seatPlan(salon)   # room to hire; FRAUD-14 below drops back to 'solo'

        member_a = None
        st, r, mtok, minv = invite(owner_tok, "+96176340002")
        if st == 201:
            jt = login(f"{TAG}.member@test.bedge.com")
            st2, r2 = call("POST", f"/invitations/{mtok}/accept",
                           {"handle": f"{TAG}-member", "category": "makeup"}, jt)
            if st2 == 201:
                member_a = sql(f"""SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id
                                    WHERE u.email='{TAG}.member@test.bedge.com'""")
                approve(member_a, admin_tok)
            else:
                print(f"  could not seat a member: accept {st2} {err(r2)}")
        else:
            print(f"  could not seat a member: invite {st} {err(r)}")

        # ── FRAUD-12 phone equivalence ─────────────────────────────────────
        st1, r1, tok1, inv1 = invite(owner_tok, "76340099")
        variants, blocked = ["+96176340099", " 76 340 099 ", "0076340099"], []
        for v in variants:
            st, r, _, _ = invite(owner_tok, v)
            if st == 201:
                blocked.append(f"{v!r} created a SECOND live invitation")
        live = sql(f"""SELECT count(*) FROM salon_invitations
                        WHERE salon_id='{salon}' AND status='pending'""")
        if st1 == 201 and not blocked and live == "1":
            rec("FRAUD-12", "PASS",
                f"all {len(variants)+1} spellings of one number collapse to a single "
                f"live invitation")
        else:
            rec("FRAUD-12", "FAIL", f"{blocked}; live invitations={live}")

        # ── FRAUD-11 public decline ────────────────────────────────────────
        st, r = call("POST", f"/invitations/{tok1}/decline")   # NO auth
        after = sql(f"SELECT status FROM salon_invitations WHERE id='{inv1}'")
        if st < 400 and after == "declined":
            rec("FRAUD-11", "UNDECIDED",
                "an UNAUTHENTICATED caller holding the link killed the invitation "
                f"({st}, now '{after}'). Reading the link is harmless, burning it is "
                "not, and both need only the same thing. Accept requires an account; "
                "decline should too. Product decision.")
        else:
            rec("FRAUD-11", "PASS", f"unauthenticated decline refused ({st} {err(r)})")

        # ── AUTH-16 redeem an invitation issued to someone else ────────────
        st, r, tok2, inv2 = invite(owner_tok, "+96176340077")
        thief_tok = login(f"{TAG}.thief@test.bedge.com")
        st2, r2 = call("POST", f"/invitations/{tok2}/accept",
                       {"handle": f"{TAG}-thief", "category": "makeup"}, thief_tok)
        thief_a = sql(f"""SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id
                           WHERE u.email='{TAG}.thief@test.bedge.com'""")
        joined = bool(thief_a) and sql(f"SELECT salon_id FROM artists WHERE id='{thief_a}'") == salon
        if joined:
            st3, r3 = call("GET", "/artists/salon/members", None, owner_tok)
            seen = any(m["artist_id"] == thief_a for m in (r3.get("data") or []))
            rec("AUTH-16", "UNDECIDED",
                f"an invitation addressed to +96176340077 was redeemed by an unrelated "
                f"account. The token is the credential, so this may be intended - but "
                f"it is a phishing primitive. Owner CAN see who joined: {seen}. "
                f"Product decision.")
        else:
            rec("AUTH-16", "PASS", f"acceptance is bound to the invited contact ({st2} {err(r2)})")

        # ── AUTH-14 the access token outlives removal ──────────────────────
        if joined:
            captured = login(f"{TAG}.thief@test.bedge.com")
            call("DELETE", f"/artists/salon/members/{thief_a}", None, owner_tok)
            still = {}
            for label, m_, p_, b_ in [
                ("service menu", "GET", "/artists/salon/services", None),
                ("roster", "GET", "/artists/salon/members", None),
                ("payment reference", "GET", "/artists/salon/payment-methods", None),
                ("REDIRECT the money", "PUT", "/artists/salon/payment-methods",
                 {"method": "omt", "account_name": "T", "account_ref": "70999999"}),
                ("edit a price", "PATCH", f"/artists/salon/services/{service}", {"price": "1.00"}),
            ]:
                s_, _ = call(m_, p_, b_, captured)
                still[label] = s_
            reachable = [k for k, v in still.items() if v < 400]
            writes = [k for k in reachable if k.startswith(("REDIRECT", "edit"))]
            if writes:
                rec("AUTH-14", "FAIL",
                    f"a REMOVED member's token can still WRITE: {writes}")
            else:
                rec("AUTH-14", "UNDECIDED",
                    f"a removed member's access token still READS {reachable} for up to "
                    f"15 minutes - RevokeAllForUser revokes refresh tokens only, and the "
                    f"access token is self-contained. No writes get through. Decide "
                    f"whether 15 minutes of salon reads after removal is acceptable.")

            # ── AUTH-17 rejoin after removal ───────────────────────────────
            st, r, tok3, inv3 = invite(owner_tok, "+96176340004")
            st4, r4 = call("POST", f"/invitations/{tok3}/accept",
                           {"handle": f"{TAG}-thief2", "category": "makeup"},
                           login(f"{TAG}.thief@test.bedge.com"))
            back = sql(f"SELECT coalesce(salon_id::text,'NULL') FROM artists WHERE id='{thief_a}'")
            status = sql(f"SELECT status FROM artists WHERE id='{thief_a}'")
            if back == "NULL":
                rec("AUTH-17", "PASS",
                    f"a removed member cannot silently rejoin ({st4} {err(r4)}); "
                    f"their artist row is untouched")
            elif status == "active":
                rec("AUTH-17", "FAIL",
                    "a removed member rejoined AND came back 'active' - admin review skipped")
            else:
                rec("AUTH-17", "PASS", f"rejoined at status '{status}', review not skipped")
            call("DELETE", f"/artists/salon/invitations/{inv3}", None, owner_tok)

        # ── DATA-03 the raw token stored in plaintext beside its own hash ──
        st, r, tok4, inv4 = invite(owner_tok, "+96176340055")
        leaked = sql(f"""SELECT count(*) FROM notifications
                          WHERE template_name='salon_invitation'
                            AND payload->>'message' LIKE '%{tok4}%'""")
        hashed = sql(f"SELECT token_hash FROM salon_invitations WHERE id='{inv4}'")
        # Redaction: once the invitation is spent the token must stop being
        # recoverable. Before the fix this case passed for the wrong reason -
        # no notification row was ever written, because the INSERT failed on
        # a missing ::text cast and the caller discarded the error.
        call("DELETE", f"/artists/salon/invitations/{inv4}", None, owner_tok)
        after_revoke = sql(f"""SELECT count(*) FROM notifications
                                WHERE template_name='salon_invitation'
                                  AND position('{tok4}' in payload->>'message') > 0""")
        if leaked != "0" and after_revoke == "0":
            rec("DATA-03", "PASS",
                "the raw token is queued while the invitation is live (it has to be - "
                "the message IS the link) and is redacted the moment the invitation "
                "is spent, bounding exposure to the invitation's own lifetime")
        elif leaked == "0":
            rec("DATA-03", "FAIL",
                "no notification row was written at all - queueing is broken, and this "
                "case would pass for the wrong reason")
        elif hashed != tok4:
            total = sql("""SELECT count(*) FROM notifications
                            WHERE template_name='salon_invitation'""")
            dead = sql("""SELECT count(*) FROM notifications
                           WHERE template_name='salon_invitation' AND status <> 'sent'""")
            rec("DATA-03", "FAIL",
                f"salon_invitations stores only a SHA-256, but notifications.payload "
                f"holds the RAW token in cleartext - query that table and you have "
                f"working invitation links. {total} such rows exist, {dead} never sent, "
                f"and nothing redacts them. The hash design is defeated by the row "
                f"next to it.")
        else:
            rec("DATA-03", "PASS", "the raw token is not recoverable from notifications")

        # DATA-03b: does the token reach the request log? It is in the PATH.
        call("GET", f"/invitations/{tok4}")
        rec("DATA-03b", "INFO",
            "the token travels in the URL path of GET /invitations/:token and "
            "POST .../accept, so any access log or proxy log captures a working "
            "credential. Check middleware/logger.go output and any reverse proxy "
            "before production.")
        # ── SPAM-08 invitation flooding ────────────────────────────────────
        made, refused = 0, None
        for i in range(60):
            st, r, _, iid = invite(owner_tok, f"+9617634{1000+i:04d}")
            if st == 201:
                made += 1
            else:
                refused = f"{st} {err(r)}"
                break
        sql(f"""DELETE FROM salon_invitations WHERE salon_id='{salon}'
                 AND phone LIKE '+9617634%' AND status='pending'""")
        if refused:
            rec("SPAM-08", "PASS", f"refused after {made} invitations ({refused})")
        else:
            rec("SPAM-08", "FAIL",
                f"{made} invitations to {made} distinct numbers, no limit hit. Each "
                f"queues a WhatsApp message; the day Meta verification clears this is "
                f"a free SMS cannon firing from B-Edge's verified sender, and the "
                f"reputational damage lands on the platform, not the salon.")

        # ── SPAM-10 write a rota for another salon's store ─────────────────
        other_store = sql(f"""SELECT id FROM stores WHERE salon_id <> '{salon}'
                               AND is_active LIMIT 1""")
        before = sql(f"SELECT count(*) FROM artist_schedules WHERE store_id='{other_store}'")
        st, r = call("PUT", "/artists/me/schedule", {
            "store_id": other_store,
            "days": [{"day_of_week": 1, "start_time": "09:00",
                      "end_time": "18:00", "is_working": True}]}, owner_tok)
        after = sql(f"SELECT count(*) FROM artist_schedules WHERE store_id='{other_store}'")
        if st >= 400 and before == after:
            rec("SPAM-10", "PASS",
                f"writing a rota for another salon's store is refused ({st} {err(r)}) "
                f"and nothing was written - verified by re-reading the table")
        else:
            rec("SPAM-10", "FAIL", f"{st}; rows {before} -> {after}")

        # ── INJ-07 hostile time values ─────────────────────────────────────
        bad = {"25:00": None, "09:00'; DROP TABLE artist_schedules; --": None,
               "": None, "x" * 5000: None}
        for v in list(bad):
            st, r = call("PUT", "/artists/me/schedule", {
                "store_id": store,
                "days": [{"day_of_week": 2, "start_time": v,
                          "end_time": "18:00", "is_working": True}]}, owner_tok)
            bad[v] = st
        alive = sql("SELECT to_regclass('public.artist_schedules') IS NOT NULL")
        partial = sql(f"""SELECT count(*) FROM artist_schedules
                           WHERE store_id='{store}' AND day_of_week=2""")
        fives = [v for v, st in bad.items() if st >= 500]
        if alive == "t" and not fives and partial == "0":
            rec("INJ-07", "PASS",
                f"all 4 hostile time values rejected without a 500, table intact, "
                f"no partial week written: {sorted(set(bad.values()))}")
        else:
            rec("INJ-07", "FAIL",
                f"table_alive={alive} 500s={len(fives)} partial_rows={partial} {bad}")

        # ── SPAM-09 the token lookup is unauthenticated ────────────────────
        codes = []
        for i in range(25):
            st, _ = call("GET", f"/invitations/brute-force-probe-{i:03d}")
            codes.append(st)
        limited = any(c == 429 for c in codes)
        rec("SPAM-09", "PASS" if set(codes) <= {404, 429} else "FAIL",
            f"25 random tokens -> {sorted(set(codes))}; rate limiter engaged={limited}. "
            f"The token is 32 bytes of crypto/rand, so guessing is not the risk; the "
            f"endpoint being unauthenticated is.")

        # ══ 3.4e — pricing, ceilings and delivery ══════════════════════════

        # ── FRAUD-14 the plan ceiling ──────────────────────────────────────
        #
        # included_seats was redefined as a CEILING when per-seat billing was
        # rejected (migration 050). If nothing consults it, every tier above
        # Solo is unsellable: a $45 salon has what a $249 salon has.
        sql(f"""UPDATE subscriptions SET plan_code='solo'
                 WHERE artist_id='{owner_a}'""")
        ceiling = int(sql("SELECT included_seats FROM plans WHERE code='solo'"))
        members = int(sql(f"SELECT count(*) FROM artists WHERE salon_id='{salon}'"))
        before_inv = sql(f"SELECT count(*) FROM salon_invitations WHERE salon_id='{salon}'")
        st, r, _, iid = invite(owner_tok, "+96176340111")
        after_inv = sql(f"SELECT count(*) FROM salon_invitations WHERE salon_id='{salon}'")

        if members >= ceiling and st == 201:
            rec("FRAUD-14", "FAIL",
                f"a salon on 'solo' (ceiling {ceiling}) already has {members} artist(s) "
                f"and was allowed to invite another. Nothing consults "
                f"plans.included_seats - the tier table is decorative and the "
                f"multi-artist feature is free at every price point")
        elif st >= 400 and before_inv == after_inv:
            rec("FRAUD-14", "PASS",
                f"refused at the ceiling ({st} {err(r)}) and no invitation row written")
        else:
            rec("FRAUD-14", "INFO",
                f"salon has {members} of {ceiling} - below the ceiling, so this run "
                f"could not test enforcement")
        if iid:
            call("DELETE", f"/artists/salon/invitations/{iid}", None, owner_tok)
        sql(f"""UPDATE subscriptions SET plan_code='comped' WHERE artist_id='{owner_a}'""")

        # ── AUTH-19 who may move the salon's opening hours ─────────────────
        #
        # default_open_time rewrites EVERY day in business_hours when it
        # changes, so it is stores:write territory, not a personal setting.
        member_tok = login(f"{TAG}.member@test.bedge.com")
        m_role = claim(member_tok, "salon_role") if member_tok else None
        if m_role != "member":
            rec("AUTH-19", "SKIP",
                f"no member account in this salon to test with (role={m_role})")
        else:
            week_before = sql(f"""SELECT md5(string_agg(day_of_week||open_time::text,
                                                        '' ORDER BY day_of_week))
                                    FROM business_hours WHERE store_id='{store}'""")
            st, r = call("PATCH", f"/artists/stores/{store}",
                         {"default_open_time": "11:00"}, member_tok)
            week_after = sql(f"""SELECT md5(string_agg(day_of_week||open_time::text,
                                                       '' ORDER BY day_of_week))
                                   FROM business_hours WHERE store_id='{store}'""")
            if st == 403 and err(r) == "SALON_ROLE_FORBIDDEN" and week_before == week_after:
                rec("AUTH-19", "PASS",
                    "a member cannot move the salon's opening hours, and the week "
                    "is byte-identical afterwards")
            else:
                rec("AUTH-19", "FAIL",
                    f"{st} {err(r)}; week changed={week_before != week_after}")

        # ── INJ-08 hostile store default hours ─────────────────────────────
        #
        # The highest-blast-radius string field added this month: one write
        # rewrites seven business_hours rows.
        hostile = {"25:00": None,
                   "18:00'; DROP TABLE business_hours; --": None,
                   "": None,
                   "x" * 10000: None}
        for v in list(hostile):
            st, _ = call("PATCH", f"/artists/stores/{store}",
                         {"default_open_time": v}, owner_tok)
            hostile[v] = st
        alive = sql("SELECT to_regclass('public.business_hours') IS NOT NULL")
        rows = sql(f"SELECT count(*) FROM business_hours WHERE store_id='{store}'")
        fives = [v for v, st in hostile.items() if st >= 500]
        if alive == "t" and not fives and rows == "7":
            rec("INJ-08", "PASS",
                f"all 4 hostile defaults rejected without a 500, table intact, "
                f"week still {rows} days: {sorted(set(hostile.values()))}")
        else:
            rec("INJ-08", "FAIL",
                f"alive={alive} 500s={len(fives)} rows={rows} "
                f"{ {k[:30]: v for k, v in hostile.items()} }")

        # ── DATA-04 retired tiers stay resolvable ──────────────────────────
        st, r = call("GET", "/billing/plans")
        public = {p["code"] for p in (r.get("data") or [])}
        joinable = sql("""SELECT count(*) FROM subscriptions s
                           JOIN plans p ON p.code = s.plan_code""")
        total_subs = sql("SELECT count(*) FROM subscriptions")
        nonzero_seat = sql("SELECT count(*) FROM plans WHERE seat_price <> 0")
        retired_hidden = not ({"starter", "growth", "enterprise", "comped"} & public)
        if public == {"solo", "studio", "salon", "multi"} and retired_hidden \
                and joinable == total_subs and nonzero_seat == "0":
            rec("DATA-04", "PASS",
                f"public list is exactly {sorted(public)}; retired tiers hidden but "
                f"all {joinable} subscriptions still resolve; seat_price 0 everywhere")
        else:
            rec("DATA-04", "FAIL",
                f"public={sorted(public)} joinable={joinable}/{total_subs} "
                f"plans_with_seat_price={nonzero_seat}")

        # ── DATA-05 which transport carried a message ──────────────────────
        import os as _os
        if not _os.getenv("TWILIO_SMS_FROM"):
            rec("DATA-05", "SKIP",
                "TWILIO_SMS_FROM is unprovisioned, so no delivery path can be "
                "exercised end to end. This is procurement, not code - the fallback "
                "itself is covered by 5 unit tests in internal/notification. "
                "A silent pass here would report a delivery path that has never "
                "sent anything.")
        else:
            rec("DATA-05", "INFO",
                "TWILIO_SMS_FROM is set - run the worker against a real queued "
                "notification and confirm notifications.channel records the "
                "transport that actually delivered")

        # ── AUTH-18 salon_id from a request body is inert ──────────────────
        other_salon = sql(f"SELECT salon_id FROM stores WHERE id='{other_store}'")
        before_pay = sql(f"""SELECT coalesce(string_agg(account_ref,','),'-')
                              FROM salon_payment_methods WHERE salon_id='{other_salon}'""")
        call("PUT", "/artists/salon/payment-methods",
             {"method": "omt", "account_name": "X", "account_ref": "70999999",
              "salon_id": other_salon}, owner_tok)
        after_pay = sql(f"""SELECT coalesce(string_agg(account_ref,','),'-')
                             FROM salon_payment_methods WHERE salon_id='{other_salon}'""")
        if before_pay == after_pay:
            rec("AUTH-18", "PASS",
                "a salon_id in the request body is ignored; the salon comes from the token")
        else:
            rec("AUTH-18", "FAIL", "ANOTHER SALON'S PAYMENT REFERENCE MOVED VIA A BODY FIELD")

    finally:
        if salon:
            for stmt in [
                f"DELETE FROM bookings WHERE salon_id='{salon}'",
                f"DELETE FROM artist_schedules WHERE store_id IN (SELECT id FROM stores WHERE salon_id='{salon}')",
                f"DELETE FROM artist_schedules WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE '{TAG}.%')",
                f"DELETE FROM salon_invitations WHERE salon_id='{salon}'",
                f"DELETE FROM artist_stores WHERE store_id IN (SELECT id FROM stores WHERE salon_id='{salon}')",
                f"DELETE FROM subscriptions WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE '{TAG}.%')",
                f"DELETE FROM salon_payment_methods WHERE salon_id='{salon}'",
                f"DELETE FROM services WHERE salon_id='{salon}'",
                f"DELETE FROM business_hours WHERE store_id IN (SELECT id FROM stores WHERE salon_id='{salon}')",
                f"DELETE FROM stores WHERE salon_id='{salon}'",
                f"DELETE FROM artists WHERE user_id IN (SELECT id FROM users WHERE email LIKE '{TAG}.%')",
                f"DELETE FROM audit_events WHERE salon_id='{salon}'",
                f"DELETE FROM salons WHERE id='{salon}'",
            ]:
                try: sql(stmt)
                except RuntimeError as e: print(f"  cleanup: {e}")
        sql(f"DELETE FROM notifications WHERE template_name='salon_invitation'")
        sql(f"DELETE FROM refresh_tokens WHERE user_id IN (SELECT id FROM users WHERE email LIKE '{TAG}.%')")
        sql(f"UPDATE users SET deleted_at=NOW() WHERE email LIKE '{TAG}.%'")
        residue = sql(f"""SELECT (SELECT count(*) FROM salons WHERE name LIKE '{TAG}%')
                        + (SELECT count(*) FROM users WHERE email LIKE '{TAG}.%' AND deleted_at IS NULL)
                        + (SELECT count(*) FROM salon_payment_methods WHERE account_ref='70999999')""")
        print(f"\n  cleanup: {residue} residual rows"
              f"{'' if residue == '0' else '   !! NOT CLEAN !!'}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser(); ap.add_argument("--api", default=API)
    API = ap.parse_args().api
    print(f"\n  ── Security plan 3.4d: multi-artist salon attack surface ──  ({API})\n")
    try:
        main()
    except Exception as e:                        # noqa: BLE001
        import traceback; print(f"\n  harness error: {type(e).__name__}: {e}")
        traceback.print_exc(); sys.exit(2)
    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    u = sum(1 for _, v, _ in RESULTS if v == "UNDECIDED")
    i = sum(1 for _, v, _ in RESULTS if v == "INFO")
    print(f"\n  {p} pass, {f} FAIL, {u} undecided, {i} info\n")
    sys.exit(1 if f else 0)
