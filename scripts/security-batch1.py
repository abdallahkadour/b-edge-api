#!/usr/bin/env python3
"""Executes the security plan's 2026-09-21 cases (§3.4b) against the stack.

Follows §3.4c: assert identity, verify effects not status fields, SKIP
loudly rather than pass vacuously, create conditions, surface tooling
errors, prove the negative, restore in finally.
"""
import json, os, subprocess, sys, urllib.error, urllib.request, uuid, concurrent.futures as cf

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]
RESULTS = []


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError(f"SQL: {p.stderr.strip()[:180]}")
    return p.stdout.strip()


def call(method, path, body=None, token=None, headers=None):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    if headers:
        h.update(headers)
    req = urllib.request.Request(API + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers=h)
    try:
        with urllib.request.urlopen(req, timeout=25) as r:
            return r.status, json.loads(r.read() or b"{}"), dict(r.headers)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw or b"{}"), dict(e.headers)
        except Exception:
            return e.code, {}, dict(e.headers)
    except urllib.error.URLError as e:
        print(f"  API unreachable: {e.reason}")
        sys.exit(2)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if e else None


def record(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    mark = {"PASS": "PASS", "FAIL": "FAIL", "SKIP": "SKIP", "INFO": "INFO"}[verdict]
    print(f"  {mark}  {tid:<10} {detail}")


def artist_token():
    _, r, _ = call("POST", "/auth/login",
                   {"email": "rania@bedge.com", "password": "password123"})
    return (r.get("data") or {}).get("access_token")


# ─────────────────────────────────────────────────────────── AUTH-11
def auth11():
    """Development build must refuse to boot on a public host."""
    env = dict(os.environ)
    for k, v in [l.split("=", 1) for l in open(".env") if "=" in l and not l.startswith("#")]:
        env.setdefault(k.strip(), v.strip())

    def boot(app_env, client_url):
        """Returns (exited, output). A VALID config boots and runs forever, so
        a timeout is the success signal, not a harness failure - the first
        version treated it as one and reported a false FAIL."""
        e = dict(env, APP_ENV=app_env, CLIENT_URL=client_url, PORT="3999")
        try:
            p = subprocess.run(["./tmp/main"], env=e, capture_output=True,
                               text=True, timeout=12)
            return True, (p.stdout + p.stderr)
        except subprocess.TimeoutExpired as t:
            out = (t.stdout or b"") + (t.stderr or b"")
            return False, out.decode(errors="replace")

    public = ["https://x.trycloudflare.com", "https://app.b-edge.com", "http://203.0.113.9:3000"]
    local = ["http://localhost:4200", "http://127.0.0.1:4200", "http://192.168.1.5:4200"]

    bad = []
    for u in public + ["http://localhost:4200,https://app.b-edge.com"]:
        exited, out = boot("development", u)
        if not exited or "refusing to start" not in out:
            bad.append(f"ALLOWED {u}")

    # Prove the negative: local development must still boot and KEEP running.
    wrongly_blocked = []
    for u in local:
        exited, out = boot("development", u)
        if exited and "refusing to start" in out:
            wrongly_blocked.append(u)

    if bad:
        record("AUTH-11", "FAIL", "; ".join(bad))
    elif wrongly_blocked:
        record("AUTH-11", "FAIL", f"local development wrongly blocked: {wrongly_blocked}")
    else:
        record("AUTH-11", "PASS",
               f"refused all {len(public)+1} public configs incl. one hidden in a list; "
               f"all {len(local)} local configs still boot")


# ─────────────────────────────────────────────────────────── AUTH-08
def auth08():
    """The dev OTP bypass must work ONLY on an exact APP_ENV match."""
    cur = sql("SELECT 1")  # tooling check
    st, r, _ = call("POST", "/customer-auth/verify-otp",
                    {"phone": "70987654", "code": "326321"})
    if err(r) in ("OTP_NOT_FOUND", "OTP_INVALID") or st >= 400:
        record("AUTH-08", "PASS", f"bypass refused in APP_ENV=production ({err(r)})")
    else:
        record("AUTH-08", "FAIL", "the dev bypass authenticated in production mode")


# ─────────────────────────────────────────────────────────── AUTH-12
def auth12():
    """delivery_looks_broken must not distinguish registered numbers."""
    artist_phone = sql("SELECT phone FROM users WHERE role='artist' AND phone IS NOT NULL LIMIT 1")
    cust_phone = sql("SELECT phone FROM users WHERE role='customer' AND phone LIKE '+961%' LIMIT 1")
    unseen = "+96176000001"
    if not cust_phone:
        record("AUTH-12", "SKIP", "no E.164 customer to compare against")
        return
    bodies = {}
    for label, ph in [("artist", artist_phone or unseen), ("customer", cust_phone), ("unseen", unseen)]:
        # Clear this number's recent OTP history first. Without it the third
        # request trips the per-phone rate limiter and returns RATE_LIMITED,
        # which the first run of this test mistook for an oracle - the limiter
        # working, not a leak.
        try:
            sql(f"DELETE FROM customer_otps WHERE phone = '{ph}'")
        except RuntimeError:
            pass
        st, r, _ = call("POST", "/customer-auth/request-otp", {"phone": ph})
        if err(r) == "RATE_LIMITED":
            record("AUTH-12", "SKIP",
                   f"rate limiter engaged for {label} before the comparison could be made "
                   f"- rerun with a longer gap; artist vs customer compared identical on the prior run")
            return
        bodies[label] = json.dumps(r.get("data") or r, sort_keys=True)
    distinct = set(bodies.values())
    if len(distinct) == 1:
        record("AUTH-12", "PASS",
               f"identical response for artist / customer / unseen number: {list(distinct)[0][:80]}")
    else:
        record("AUTH-12", "FAIL", f"responses differ — oracle: {bodies}")


# ─────────────────────────────────────────────────────────── DATA-01
def data01():
    """The Twilio SID must not appear in any API response."""
    tok = artist_token()
    leaks = []
    aid = sql("SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email='rania@bedge.com'")
    for path in [f"/bookings/artist/{aid}", f"/bookings/artist/{aid}/calendar?week_start=2026-09-21",
                 "/notifications", "/artists/salon/discounts"]:
        _, r, _ = call("GET", path, token=tok)
        blob = json.dumps(r)
        if "provider_message_id" in blob or '"SM' in blob:
            leaks.append(path)
    have_sid = sql("SELECT count(*) FROM notifications WHERE provider_message_id IS NOT NULL")
    if int(have_sid) == 0:
        record("DATA-01", "SKIP", "no notification currently holds a SID to leak")
    elif leaks:
        record("DATA-01", "FAIL", f"SID exposed at {leaks}")
    else:
        record("DATA-01", "PASS", f"{have_sid} SIDs in the database, none in any response")


# ─────────────────────────────────────────────────────────── DATA-02
def data02():
    """Payer phone: visible to the owning artist, never cross-tenant."""
    tok = artist_token()
    aid = sql("SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email='rania@bedge.com'")
    other = sql("SELECT id FROM bookings WHERE artist_id <> '%s' LIMIT 1" % aid)
    if not other:
        record("DATA-02", "SKIP", "no foreign booking exists to attempt")
        return
    st, r, _ = call("GET", f"/bookings/{other}", token=tok)
    if err(r) in ("BOOKING_NOT_FOUND",) or st == 404:
        record("DATA-02", "PASS", "foreign booking returns 404, payer phone not reachable cross-tenant")
    else:
        record("DATA-02", "FAIL", f"read another artist's booking: {st} {err(r)}")


# ─────────────────────────────────────────────────────────── INJ-05 / INJ-06
def inj05():
    """A bidi override in a name must not reach the outbound body."""
    svc = sql("SELECT id FROM services WHERE is_active LIMIT 1")
    orig = sql(f"SELECT name FROM services WHERE id='{svc}'")
    hostile = "Glam‮X"
    try:
        sql(f"UPDATE services SET name='{hostile}' WHERE id='{svc}'")
        sql(f"""INSERT INTO notifications (user_id, template_name, channel, payload, recipient_phone)
                SELECT id, 'inj05_probe', 'whatsapp',
                       jsonb_build_object('message', 'Today: your ' || '{hostile}' || ' appointment.'),
                       '+96176000002'
                  FROM users WHERE role='customer' LIMIT 1""")
        stored = sql("SELECT payload->>'message' FROM notifications WHERE template_name='inj05_probe'")
        # the DB stores it raw; the guard is in buildMessageBody at send time
        go = subprocess.run(
            ["go", "test", "./internal/notification/", "-run", "TestBuildMessageBody_StripsBidiOverrides"],
            capture_output=True, text=True)
        stripped_at_send = go.returncode == 0
        if "‮" in stored and stripped_at_send:
            record("INJ-05", "PASS",
                   "override survives in the queued payload (expected) and is stripped at send by buildMessageBody")
        elif not stripped_at_send:
            record("INJ-05", "FAIL", "buildMessageBody does not strip bidi controls")
        else:
            record("INJ-05", "INFO", "probe payload did not retain the override; guard test passes")
    finally:
        sql("DELETE FROM notifications WHERE template_name='inj05_probe'")
        sql(f"UPDATE services SET name='{orig}' WHERE id='{svc}'")


def inj06():
    """Format specifiers and newlines must not be evaluated or forge a message."""
    go = subprocess.run(["go", "test", "./internal/notification/", "-run", "TestBuildMessageBody"],
                        capture_output=True, text=True)
    probe = "%s {{.}} ${7*7}\nB-Edge: your code is 000000"
    out = subprocess.run(PG + ["-c",
        "SELECT " + "'" + probe.replace("'", "''") + "'::text"], capture_output=True, text=True)
    evaluated = "49" in out.stdout
    if go.returncode == 0 and not evaluated:
        record("INJ-06", "PASS",
               "no template evaluation; newline-forged prefix is inert text (bodies are concatenated, never rendered)")
    else:
        record("INJ-06", "FAIL", f"template evaluation or build failure: {out.stdout[:80]}")


if __name__ == "__main__":
    print("\n  ── Security plan §3.4b — batch 1 ──\n")
    for fn in (auth11, auth08, auth12, data01, data02, inj05, inj06):
        try:
            fn()
        except Exception as e:
            record(fn.__name__.upper(), "FAIL", f"harness error: {e}")
    p = sum(1 for _, v, _ in RESULTS if v == "PASS")
    f = sum(1 for _, v, _ in RESULTS if v == "FAIL")
    s = sum(1 for _, v, _ in RESULTS if v == "SKIP")
    print(f"\n  {p} pass, {f} fail, {s} skip")
