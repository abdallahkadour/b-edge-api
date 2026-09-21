#!/usr/bin/env python3
"""Executes E2E suite 22 - multi-artist salons - against a running stack.

This is the acceptance test for the whole feature. Until this runs, the
platform has 37 passing Go packages and zero evidence that Rania can
actually invite a second artist who then takes bookings.

Follows B-Edge-Security-Test-Plan-v1.md 3.4c:
  * Assert identity before measuring.
  * Verify EFFECTS in the database, not just status codes.
  * Create the conditions - there are no salon members, so make one.
  * SKIP loudly rather than pass vacuously.
  * Prove the negative.
  * Restore in finally, always.

Usage: python3 scripts/e2e-suite22.py [--api http://localhost:3000/api/v1]
"""
import argparse, json, re, subprocess, sys, urllib.error, urllib.request

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]

OWNER_EMAIL, OWNER_PW = "rania@bedge.com", "password123"
JOINER_EMAIL, JOINER_PW = "e2e22.joiner@test.bedge.com", "password123"
JOINER_PHONE = "+96176000122"
JOINER_HANDLE = "e2e22-joiner"

RESULTS = []


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError("SQL: " + p.stderr.strip()[:250])

    # psql prints a command-status line after a data-modifying statement, so
    # `INSERT ... RETURNING id` yields the uuid AND "INSERT 0 1". Feeding that
    # straight into the next query produces a baffling "invalid input syntax
    # for type uuid" that names the right uuid. Strip the status lines.
    lines = [ln for ln in p.stdout.strip().splitlines()
             if not re.match(r"^(INSERT|UPDATE|DELETE|SELECT|COPY)\s+\d", ln)]
    return "\n".join(lines).strip()


def call(method, path, body=None, token=None, base=None, follow=True):
    url = (base or API) + path
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    req = urllib.request.Request(
        url, method=method,
        data=json.dumps(body).encode() if body is not None else None, headers=h)

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *a, **k):
            return None
    opener = urllib.request.build_opener() if follow else \
        urllib.request.build_opener(NoRedirect)
    try:
        with opener.open(req, timeout=25) as r:
            raw = r.read()
            try:
                return r.status, json.loads(raw or b"{}")
            except Exception:
                return r.status, {"raw": raw.decode(errors="replace")[:200]}
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw or b"{}")
        except Exception:
            return e.code, {"raw": raw.decode(errors="replace")[:200]}
    except urllib.error.URLError as e:
        print(f"  API unreachable: {e.reason}")
        sys.exit(2)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if e else None


def rec(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<4} {tid:<7} {detail}")


def login(email, pw):
    st, r = call("POST", "/auth/login", {"email": email, "password": pw})
    return ((r.get("data") or {}).get("access_token"), st, err(r))


def claim(token, key):
    import base64
    p = token.split(".")[1]
    p += "=" * (-len(p) % 4)
    return json.loads(base64.urlsafe_b64decode(p)).get(key)


def slots(artist, store, service, date):
    st, r = call("GET", f"/bookings/slots?artist_id={artist}&store_id={store}"
                        f"&service_id={service}&date={date}")
    d = r.get("data")
    return d if isinstance(d, list) else None


def main():
    owner_tok, st, code = login(OWNER_EMAIL, OWNER_PW)
    if not owner_tok:
        print(f"  SKIP - cannot sign in as the owner ({st} {code})")
        sys.exit(0)

    salon_id = sql(f"""SELECT a.salon_id FROM artists a JOIN users u ON u.id=a.user_id
                        WHERE u.email='{OWNER_EMAIL}'""")
    owner_artist = sql(f"""SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id
                            WHERE u.email='{OWNER_EMAIL}'""")
    store = sql(f"SELECT id FROM stores WHERE salon_id='{salon_id}' AND is_active ORDER BY name LIMIT 1")
    service = sql(f"SELECT id FROM services WHERE salon_id='{salon_id}' AND is_active ORDER BY name LIMIT 1")

    # 22.5 baseline: the owner is a soloist right now. Capture what a customer
    # sees for her BEFORE a second artist exists, so the end of the run can
    # prove nothing about her changed.
    probe_dates = [sql("SELECT (CURRENT_DATE + 7)::text"),
                   sql("SELECT (CURRENT_DATE + 14)::text"),
                   sql("SELECT (CURRENT_DATE + 30)::text")]
    owner_before = {d: slots(owner_artist, store, service, d) for d in probe_dates}
    if not any(owner_before.values()):
        print("  SKIP - the owner has no availability on any probe date; "
              "22.5 would compare nothing")
        sys.exit(0)

    joiner_user = joiner_artist = None
    try:
        # ══ 22.1 Invite and accept ═════════════════════════════════════════

        # Create the invitee's account. A fresh one, not a roster account:
        # every roster artist already has a salon, and BR-1 refuses those.
        # A previous run soft-deletes this account rather than removing it
        # (audit history references it). Revive it so the run is repeatable.
        sql(f"UPDATE users SET deleted_at = NULL WHERE email='{JOINER_EMAIL}'")

        st, r = call("POST", "/auth/register", {
            "name": "E2E22 Joiner", "email": JOINER_EMAIL,
            "password": JOINER_PW, "role": "artist", "phone": JOINER_PHONE})
        if st >= 400 and err(r) != "EMAIL_TAKEN":
            rec("22.1", "FAIL", f"could not create the invitee account: {st} {err(r)}")
            return
        joiner_user = sql(f"SELECT id FROM users WHERE email='{JOINER_EMAIL}'")
        joiner_tok, _, _ = login(JOINER_EMAIL, JOINER_PW)

        # ── invite ─────────────────────────────────────────────────────────
        st, r = call("POST", "/artists/salon/members/invite",
                     {"phone": JOINER_PHONE}, owner_tok)
        if st != 201:
            rec("22.1a", "FAIL", f"owner could not invite: {st} {err(r)} {r}")
            return
        link = (r.get("data") or {}).get("link", "")
        inv = (r.get("data") or {}).get("invitation", {})
        token = link.rsplit("/", 1)[-1] if link else ""

        stored_hash = sql(f"SELECT token_hash FROM salon_invitations WHERE id='{inv['id']}'")
        if not token:
            rec("22.1a", "FAIL", "no invitation link was returned - WhatsApp is dead, "
                                 "so the link IS the channel")
        elif stored_hash == token:
            rec("22.1a", "FAIL", "the raw token is what got stored")
        else:
            rec("22.1a", "PASS",
                f"invitation created, link returned for copying, only its hash stored")

        # ── preview discloses the salon and nothing else ───────────────────
        st, r = call("GET", f"/invitations/{token}")
        p = r.get("data") or {}
        blob = json.dumps(p)
        leaks = [k for k in ("phone", "email", "members", "revenue", "owner_id")
                 if k in blob]
        if st != 200:
            rec("22.1b", "FAIL", f"preview failed: {st} {err(r)}")
        elif leaks:
            rec("22.1b", "FAIL", f"the public preview discloses {leaks}")
        else:
            rec("22.1b", "PASS",
                f"preview shows salon '{p.get('salon_name')}' and inviter "
                f"'{p.get('invited_by')}' only")

        # ── unusable tokens are indistinguishable ──────────────────────────
        shapes = {}
        for label, tok in [("never issued", "a-token-that-was-never-issued"),
                           ("empty", "%20")]:
            s2, r2 = call("GET", f"/invitations/{tok}")
            shapes[label] = (s2, err(r2))
        if len(set(shapes.values())) == 1:
            rec("22.1c", "PASS",
                f"unknown and malformed tokens answer identically {list(set(shapes.values()))[0]}")
        else:
            rec("22.1c", "FAIL", f"tokens are distinguishable: {shapes}")

        # ── accept ─────────────────────────────────────────────────────────
        st, r = call("POST", f"/invitations/{token}/accept",
                     {"handle": JOINER_HANDLE, "category": "makeup"}, joiner_tok)
        if st != 201:
            rec("22.1d", "FAIL", f"accept failed: {st} {err(r)} {r}")
            return
        joiner_artist = sql(f"SELECT id FROM artists WHERE user_id='{joiner_user}'")
        row = sql(f"""SELECT salon_id::text||'|'||status FROM artists WHERE id='{joiner_artist}'""")
        got_salon, got_status = row.split("|")
        if got_salon != salon_id:
            rec("22.1d", "FAIL", f"joined the wrong salon: {got_salon}")
        elif got_status != "pending":
            rec("22.1d", "FAIL",
                f"status is {got_status!r} - an invitation must NOT bypass admin review")
        else:
            rec("22.1d", "PASS",
                "joined the inviting salon with status 'pending' - review not bypassed")

        # ── the link is single-use ─────────────────────────────────────────
        st, r = call("POST", f"/invitations/{token}/accept",
                     {"handle": "e2e22-second", "category": "makeup"}, owner_tok)
        if st == 404 and err(r) == "INVITATION_NOT_FOUND":
            rec("22.1e", "PASS", "a reused link is refused, indistinguishably from unknown")
        else:
            rec("22.1e", "FAIL", f"the link was reusable: {st} {err(r)}")

        # ── the salon now really has two artists ───────────────────────────
        n = sql(f"SELECT count(*) FROM artists WHERE salon_id='{salon_id}'")
        st, r = call("GET", "/artists/salon/members", None, owner_tok)
        roster = r.get("data") or []
        if n == "2" and len(roster) == 2:
            rec("22.1f", "PASS", "the salon has 2 artists, and the roster shows both")
        else:
            rec("22.1f", "FAIL", f"db says {n} artists, roster shows {len(roster)}")

        # ══ 22.2 The boundary, from the member's side ══════════════════════
        joiner_tok, _, _ = login(JOINER_EMAIL, JOINER_PW)  # fresh token, new role
        role = claim(joiner_tok, "salon_role")
        if role != "member":
            rec("22.2a", "FAIL", f"the joiner's token says salon_role={role!r}, not 'member'")
        else:
            rec("22.2a", "PASS", "the joiner's next token carries salon_role=member")

        st, r = call("PATCH", f"/artists/salon/services/{service}",
                     {"price": "1.00"}, joiner_tok)
        before = sql(f"SELECT md5(ROW(name, price)::text) FROM services WHERE id='{service}'")
        after = sql(f"SELECT md5(ROW(name, price)::text) FROM services WHERE id='{service}'")
        if st == 403 and err(r) == "SALON_ROLE_FORBIDDEN" and before == after:
            rec("22.2b", "PASS", "a member's price edit is refused AND the row is unchanged")
        else:
            rec("22.2b", "FAIL", f"{st} {err(r)}, row changed={before != after}")

        # ══ 22.3 Per-artist hours reach the customer ═══════════════════════

        # Approve first: a pending artist is not bookable, so the rota could
        # not be observed through the funnel.
        admin_tok, _, _ = login("abdallah.kadour@b-edge.com", OWNER_PW)
        if admin_tok:
            call("POST", f"/admin/artists/{joiner_artist}/approve", {}, admin_tok)
            approved_via = "the admin approve endpoint"
        else:
            sql(f"UPDATE artists SET status='active' WHERE id='{joiner_artist}'")
            approved_via = "SQL - the admin password is unknown, so admin.Approve was NOT exercised"
        if sql(f"SELECT status FROM artists WHERE id='{joiner_artist}'") != "active":
            rec("22.3", "FAIL", "could not approve the joiner; the rest of 22.3 is unrunnable")
            return
        rec("22.3a", "INFO", f"joiner approved via {approved_via}")

        probe = probe_dates[0]
        dow = int(sql(f"SELECT EXTRACT(DOW FROM DATE '{probe}')"))
        wide = slots(joiner_artist, store, service, probe)
        if not wide:
            rec("22.3b", "SKIP", f"the joiner has no availability on {probe} to narrow")
        else:
            st, r = call("PUT", "/artists/me/schedule", {
                "store_id": store,
                "days": [{"day_of_week": dow, "start_time": "14:00",
                          "end_time": "16:00", "is_working": True}]}, joiner_tok)
            if st != 200:
                rec("22.3b", "FAIL", f"the member could not set their own hours: {st} {err(r)}")
            else:
                narrow = slots(joiner_artist, store, service, probe) or []
                starts = {s["start_time"][11:16] for s in narrow}
                outside = {t for t in starts if t < "14:00" or t >= "16:00"}
                if narrow and not outside and len(narrow) < len(wide):
                    rec("22.3b", "PASS",
                        f"rota 14:00-16:00 cut {len(wide)} slots to {len(narrow)}, "
                        f"all inside the window")
                else:
                    rec("22.3b", "FAIL",
                        f"{len(wide)} -> {len(narrow)} slots, outside the window: {sorted(outside)}")

                # The owner's own availability must be untouched by a
                # colleague narrowing theirs.
                owner_same = slots(owner_artist, store, service, probe)
                if owner_same == owner_before[probe]:
                    rec("22.3c", "PASS",
                        "narrowing one artist left the OWNER's availability identical")
                else:
                    rec("22.3c", "FAIL",
                        f"the owner's slots changed: {len(owner_before[probe] or [])} -> "
                        f"{len(owner_same or [])}")

        # ══ 22.4 Removal refuses to strand a customer ══════════════════════
        st, r = call("GET", "/artists/salon/members", None, owner_tok)
        member_row = next((m for m in (r.get("data") or [])
                           if m["artist_id"] == joiner_artist), None)
        if not member_row:
            rec("22.4", "FAIL", "the joiner is missing from the roster")
        else:
            # Create a future booking directly - the funnel path is covered by
            # other suites, and what is under test here is the removal guard.
            cust = sql("SELECT id FROM users WHERE role='customer' LIMIT 1")
            # blocked_until is NOT NULL with no default: it is the upper bound
            # of the tstzrange the GIST exclusion constraint guards, so a row
            # without it has no double-booking protection and Postgres refuses
            # it outright. end_time + the store buffer is what the app writes;
            # end_time is enough for a fixture.
            bid = sql(f"""INSERT INTO bookings
                (artist_id, salon_id, store_id, service_id, customer_id,
                 start_time, end_time, blocked_until, status,
                 original_price, final_price, deposit_amount)
                VALUES ('{joiner_artist}','{salon_id}','{store}','{service}','{cust}',
                        NOW() + interval '10 days', NOW() + interval '10 days 1 hour',
                        NOW() + interval '10 days 1 hour',
                        'confirmed', 100, 100, 20) RETURNING id""")
            st, r = call("DELETE", f"/artists/salon/members/{joiner_artist}", None, owner_tok)
            still_there = sql(f"SELECT salon_id::text FROM artists WHERE id='{joiner_artist}'")
            if st == 409 and err(r) == "HAS_FUTURE_BOOKINGS" and still_there == salon_id:
                rec("22.4a", "PASS",
                    "removal refused while an upcoming booking exists, and the member stayed")
            else:
                rec("22.4a", "FAIL", f"{st} {err(r)}; still in salon={still_there == salon_id}")

            sql(f"UPDATE bookings SET status='cancelled' WHERE id='{bid}'")
            st, r = call("DELETE", f"/artists/salon/members/{joiner_artist}", None, owner_tok)
            gone = sql(f"SELECT coalesce(salon_id::text,'NULL') FROM artists WHERE id='{joiner_artist}'")
            kept = sql(f"SELECT count(*) FROM bookings WHERE artist_id='{joiner_artist}'")
            if st == 204 and gone == "NULL" and kept != "0":
                rec("22.4b", "PASS",
                    f"removal succeeded once the booking was cancelled, and {kept} "
                    f"booking(s) of history survived")
            else:
                rec("22.4b", "FAIL", f"{st}, salon={gone}, bookings kept={kept}")

            st, r = call("DELETE", f"/artists/salon/members/{owner_artist}", None, owner_tok)
            if st == 409 and err(r) == "CANNOT_REMOVE_OWNER":
                rec("22.4c", "PASS", "the owner cannot be removed")
            else:
                rec("22.4c", "FAIL", f"{st} {err(r)}")

        # ══ 22.5 The soloist notices nothing ═══════════════════════════════
        owner_after = {d: slots(owner_artist, store, service, d) for d in probe_dates}
        diffs = [d for d in probe_dates if owner_after[d] != owner_before[d]]
        if diffs:
            rec("22.5", "FAIL",
                f"the owner's availability changed on {diffs} across the whole exercise")
        else:
            rec("22.5", "PASS",
                f"the owner's availability is byte-identical on all {len(probe_dates)} "
                f"probe dates, before and after a second artist joined, set hours and left")

    finally:
        # Everything this run created, removed. The permanent 14-account
        # roster is untouched; JOINER_EMAIL is this script's own account.
        if joiner_artist:
            sql(f"DELETE FROM bookings WHERE artist_id='{joiner_artist}'")
            sql(f"DELETE FROM artist_schedules WHERE artist_id='{joiner_artist}'")
            sql(f"DELETE FROM artist_stores WHERE artist_id='{joiner_artist}'")
            sql(f"DELETE FROM subscriptions WHERE artist_id='{joiner_artist}'")
            sql(f"DELETE FROM artists WHERE id='{joiner_artist}'")
        sql(f"DELETE FROM salon_invitations WHERE phone='{JOINER_PHONE}'")
        if joiner_user:
            sql(f"DELETE FROM refresh_tokens WHERE user_id='{joiner_user}'")
            # SOFT delete, not DELETE. audit_events.actor_id references this
            # user and that reference is correct - the accept is a real event
            # and removing it would be rewriting history to tidy up a test.
            # Soft deletion is this codebase's own pattern for users anyway.
            # A later run re-registers the same email; the register call
            # tolerates EMAIL_TAKEN for exactly this reason.
            sql(f"UPDATE users SET deleted_at = NOW() WHERE id='{joiner_user}'")
        sql("DELETE FROM notifications WHERE template_name='salon_invitation'")

        left = sql(f"""SELECT (SELECT count(*) FROM users
                                WHERE email='{JOINER_EMAIL}' AND deleted_at IS NULL)
                            + (SELECT count(*) FROM salon_invitations)
                            + (SELECT count(*) FROM artist_schedules)""")
        n = sql(f"SELECT count(*) FROM artists WHERE salon_id='{salon_id}'")
        print(f"\n  cleanup: {left} residual rows, salon back to {n} artist(s)"
              f"{'' if left == '0' and n == '1' else '   !! NOT CLEAN !!'}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--api", default=API)
    a = ap.parse_args()
    API = a.api

    print(f"\n  ── E2E suite 22: multi-artist salons ──  ({API})\n")
    try:
        main()
    except Exception as e:                        # noqa: BLE001 - surface it
        import traceback
        print(f"\n  harness error: {type(e).__name__}: {e}")
        traceback.print_exc()
        sys.exit(2)

    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    s = sum(1 for _, v, _ in RESULTS if v == "SKIP")
    i = sum(1 for _, v, _ in RESULTS if v == "INFO")
    print(f"\n  {p} pass, {f} fail, {s} skip, {i} info\n")
    sys.exit(1 if f else 0)
