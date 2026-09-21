#!/usr/bin/env python3
"""Verifies the salon owner/member boundary against a running stack.

UC7 of the multi-artist salon work. Phase 1 checks (MS1-MS3) are implemented
here; Phase 2-4 checks are listed in the plan and land with their phases.

Follows the reliability rules in B-Edge-Security-Test-Plan-v1.md 3.4c:

  * Assert identity before measuring. A 403 from the wrong route, or from a
    rate limiter, is not the 403 under test.
  * Verify EFFECTS, not status codes alone. MS2 re-reads the row and compares
    it byte for byte, because a handler can return 403 and still have written.
  * Create the conditions. There are no salon members on this platform, so the
    run makes one and puts it back.
  * Change ONE variable. The same account runs every check twice - once while
    it owns its salon, once while salons.owner_id points elsewhere. Same
    credentials, same routes, same rows; only the role differs. Comparing two
    different accounts would have confounded the role with whatever else
    differs between them.
  * SKIP loudly rather than pass vacuously.
  * Restore in finally, always.

Usage: python3 scripts/verify-uc7.py [--api http://localhost:3000/api/v1]
"""
import argparse
import json
import subprocess
import sys
import urllib.error
import urllib.request

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]
RESULTS = []

SUBJECT_EMAIL = "rania@bedge.com"
PASSWORD = "password123"


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError("SQL: " + p.stderr.strip()[:250])
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
            return e.code, {"raw": raw.decode(errors="replace")[:200]}
    except urllib.error.URLError as e:
        print(f"  API unreachable at {API}: {e.reason}")
        sys.exit(2)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if e else None


def record(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<4} {tid:<6} {detail}")


def login(email):
    st, r = call("POST", "/auth/login", {"email": email, "password": PASSWORD})
    tok = (r.get("data") or {}).get("access_token")
    if not tok:
        raise RuntimeError(f"login failed for {email}: {st} {err(r)}")
    return tok


def claim(token, key):
    """Reads one claim out of a JWT payload without verifying it - this is a
    test asserting what the server put in the token, not trusting it."""
    import base64
    payload = token.split(".")[1]
    payload += "=" * (-len(payload) % 4)
    return json.loads(base64.urlsafe_b64decode(payload)).get(key)


def main():
    salon_id = sql(f"""SELECT a.salon_id FROM artists a JOIN users u ON u.id = a.user_id
                        WHERE u.email = '{SUBJECT_EMAIL}'""")
    if not salon_id:
        print("  SKIP - the subject account has no salon")
        sys.exit(0)

    original_owner = sql(f"SELECT owner_id FROM salons WHERE id = '{salon_id}'")
    stand_in_owner = sql(f"""SELECT id FROM users WHERE role = 'artist'
                              AND id <> '{original_owner}' LIMIT 1""")
    service_id = sql(f"""SELECT id FROM services
                          WHERE salon_id = '{salon_id}' AND is_active LIMIT 1""")
    store_id = sql(f"SELECT id FROM stores WHERE salon_id = '{salon_id}' LIMIT 1")

    if not stand_in_owner:
        print("  SKIP - no second artist account to park ownership on")
        sys.exit(0)
    if not service_id or not store_id:
        print("  SKIP - the salon has no active service or store to attempt a write on")
        sys.exit(0)

    original_name = sql(f"SELECT name FROM services WHERE id = '{service_id}'")
    fingerprint = f"""SELECT md5(ROW(name, price, duration_min, deposit_amount,
                                     is_active)::text)
                        FROM services WHERE id = '{service_id}'"""

    # Every owner-only write this run attempts, as (method, path, body, plain
    # English). Each one is something a second artist in Rania's salon must
    # not be able to do.
    owner_only = [
        ("PATCH", f"/artists/salon/services/{service_id}", {"price": "1.00"},
         "cut a price to 1.00"),
        ("POST", "/artists/salon/services",
         {"name": "uc7 probe", "duration_min": 30, "price": "10.00"}, "add a service"),
        ("DELETE", f"/artists/salon/services/{service_id}", None, "delete a service"),
        ("POST", "/artists/salon/stores", {"name": "uc7 probe", "city": "Beirut"},
         "add a store"),
        ("PATCH", f"/artists/stores/{store_id}", {"name": "uc7 probe"}, "rename a store"),
        ("POST", f"/artists/stores/{store_id}/hours", {"hours": []},
         "wipe the opening hours"),
        ("POST", f"/artists/stores/{store_id}/exceptions",
         {"exception_date": "2026-12-25", "is_closed": True}, "close the salon for a day"),
        ("POST", "/artists/salon/discounts",
         {"code": "UC7PROBE", "type": "percentage", "value": "10"}, "create a discount"),
        ("PUT", "/artists/salon/payment-methods",
         {"method": "omt", "account_name": "uc7 probe", "account_ref": "70000000"},
         "redirect where every deposit is paid"),
        ("POST", "/artists/salon/products",
         {"name": "uc7 probe", "price": "5.00", "stock": 1}, "add a product"),

        # Membership (Phase 2). A member must not be able to change who else
        # is in the salon, nor hand the salon to someone.
        ("POST", "/artists/salon/members/invite", {"phone": "76000099"},
         "invite another artist"),
        ("DELETE", f"/artists/salon/members/{{OTHER_ARTIST}}", None,
         "remove another member"),
        ("POST", "/artists/salon/owner/transfer", {"artist_id": "{OTHER_ARTIST}"},
         "hand the salon to someone else"),
    ]

    # The removal and transfer probes need a real artist id in the path, or
    # a 404 from an unparseable UUID would masquerade as the 403 under test.
    other_artist = sql(f"""SELECT a.id FROM artists a JOIN users u ON u.id = a.user_id
                            WHERE u.email <> '{SUBJECT_EMAIL}' AND a.salon_id IS NOT NULL
                            LIMIT 1""")
    owner_only = [
        (m, p.replace("{OTHER_ARTIST}", other_artist), 
         (b if not isinstance(b, dict) else
          {k: (v.replace("{OTHER_ARTIST}", other_artist) if isinstance(v, str) else v)
           for k, v in b.items()}),
         w)
        for (m, p, b, w) in owner_only
    ]

    member_readable = [
        ("GET", "/artists/salon/services", "read the service menu"),
        ("GET", "/artists/salon/stores", "read the stores"),
        ("GET", f"/artists/stores/{store_id}/hours", "read the opening hours"),
        ("GET", "/artists/salon/discounts", "read the live discount codes"),
        ("GET", "/artists/me", "read their own profile"),
        ("GET", "/artists/salon/members", "see who they work with"),
        ("GET", "/artists/me/schedule", "read their own working hours"),
    ]

    try:
        # ══ AS OWNER ═══════════════════════════════════════════════════════
        tok = login(SUBJECT_EMAIL)
        role = claim(tok, "salon_role")
        if role != "owner":
            record("MS0a", "FAIL", f"the salon's owner resolved as {role!r}, not 'owner'")
            return
        record("MS0a", "PASS", "the salon's owner resolves to role 'owner' from salons.owner_id")

        st, r = call("PATCH", f"/artists/salon/services/{service_id}",
                     {"name": original_name}, tok)
        if st >= 400:
            record("MS3", "FAIL", f"the OWNER was refused their own service: {st} {err(r)}")
        else:
            record("MS3", "PASS",
                   "the owner can still edit the salon's services - no regression for "
                   "the soloists who are every artist on the platform today")

        # ══ SAME ACCOUNT, NOW A MEMBER ═════════════════════════════════════
        sql(f"UPDATE salons SET owner_id = '{stand_in_owner}' WHERE id = '{salon_id}'")

        tok = login(SUBJECT_EMAIL)          # a fresh token, or the role is stale
        role = claim(tok, "salon_role")
        if role != "member":
            record("MS0b", "FAIL",
                   f"after moving owner_id the same account resolved as {role!r}, "
                   f"not 'member' - role derivation is not reading salons.owner_id")
            return
        record("MS0b", "PASS",
               "moving salons.owner_id turns the same account into 'member' on the "
               "next login - the role is derived, not stored")

        # ── MS1: every owner-only write refused ────────────────────────────
        leaked = []
        for method, path, body, what in owner_only:
            st, r = call(method, path, body, tok)
            if st != 403 or err(r) != "SALON_ROLE_FORBIDDEN":
                leaked.append(f"{what} -> {st} {err(r)}")
        if leaked:
            record("MS1", "FAIL",
                   f"{len(leaked)} of {len(owner_only)} owner-only writes got through: "
                   + "; ".join(leaked))
        else:
            record("MS1", "PASS",
                   f"all {len(owner_only)} owner-only writes refused with 403 "
                   f"SALON_ROLE_FORBIDDEN")

        # ── MS2: refusals had no effect ────────────────────────────────────
        before = sql(fingerprint)
        call("PATCH", f"/artists/salon/services/{service_id}",
             {"price": "999.00", "name": "tampered"}, tok)
        after = sql(fingerprint)
        if before != after:
            record("MS2", "FAIL",
                   "a refused write still changed the row - the 403 is cosmetic")
        else:
            record("MS2", "PASS",
                   "the refused write left the row byte-identical (effect checked, "
                   "not just the status code)")

        # ── MS2b: nothing was created by the refused creates ───────────────
        strays = sql("""SELECT
              (SELECT count(*) FROM services WHERE name = 'uc7 probe')
            + (SELECT count(*) FROM stores   WHERE name = 'uc7 probe')
            + (SELECT count(*) FROM products WHERE name = 'uc7 probe')
            + (SELECT count(*) FROM discounts WHERE code = 'UC7PROBE')""")
        if strays != "0":
            record("MS2b", "FAIL", f"{strays} row(s) were created by refused writes")
        else:
            record("MS2b", "PASS", "no row was created by any refused write")

        # ── MS4: the member can still work ─────────────────────────────────
        blocked = []
        for method, path, what in member_readable:
            st, r = call(method, path, None, tok)
            if st >= 400:
                blocked.append(f"{what} -> {st} {err(r)}")
        if blocked:
            record("MS4", "FAIL",
                   "a member cannot do their job: " + "; ".join(blocked))
        else:
            record("MS4", "PASS",
                   f"a member can still do all {len(member_readable)} things they need "
                   f"to work - the boundary bounds, it does not disable")

    finally:
        sql(f"UPDATE salons SET owner_id = '{original_owner}' WHERE id = '{salon_id}'")
        sql(f"""UPDATE services SET name = '{original_name.replace("'", "''")}'
                 WHERE id = '{service_id}'""")
        for stmt in [
            "DELETE FROM services WHERE name = 'uc7 probe'",
            "DELETE FROM stores   WHERE name = 'uc7 probe'",
            "DELETE FROM products WHERE name = 'uc7 probe'",
            "DELETE FROM discounts WHERE code = 'UC7PROBE'",
            # Only reachable if the guard FAILED and the invite got through -
            # which is itself a reported failure. Cleaned up anyway so a
            # failing run does not poison the next one.
            "DELETE FROM salon_invitations WHERE phone = '+96176000099'",
        ]:
            try:
                sql(stmt)
            except RuntimeError as e:
                print(f"  cleanup warning: {e}")

        back = sql(f"SELECT owner_id FROM salons WHERE id = '{salon_id}'")
        mark = "" if back == original_owner else "  !! OWNERSHIP NOT RESTORED !!"
        print(f"\n  restored salons.owner_id to {back[:8]}...{mark}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--api", default=API)
    a = ap.parse_args()
    API = a.api

    print(f"\n  ── UC7: the salon owner/member boundary ──  ({API})\n")
    try:
        main()
    except Exception as e:                       # noqa: BLE001 - surface, don't swallow
        print(f"  harness error: {type(e).__name__}: {e}")
        sys.exit(2)

    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    print(f"\n  {p} pass, {f} fail\n")
    sys.exit(1 if f else 0)
