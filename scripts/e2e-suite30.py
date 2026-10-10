#!/usr/bin/env python3
"""E2E suite 30 - the salon owner's dashboard (2026-10-10).

The API half; the screens are driven by b-edge-web/scripts/e2e-suite30-ui.mjs.

  30.1  GET /earnings/salon: the salon's figures are the database's, both
        artists are listed, the owner's row is hers
  30.2  a member gets 403 there, and keeps her own Earnings
  30.3  GET /bookings/salon: every artist's bookings here, one artist's on
        request; a member gets 403
  30.4  GET /bookings/salon/calendar: the team's week
  30.5  the owner approves a member's booking; the activity row names her
  30.6  a member cannot approve the owner's booking, and nothing is recorded
  30.7  a member's own action is recorded under her name
  30.8  a payment account changed: the feed shows what it was and what it became
  30.9  the feed is this salon's only, with the names the screen shows
  30.10 a member and a customer are refused the feed
  30.11 the feed's filters
  30.12 a menu price changed is in the feed with both prices

Builds its own salon of two artists and destroys it in a finally block,
reporting the residual count. Helpers come from chaos-booking.py. Requires
the API built with -tags devbypass (make dev).

  python3 scripts/e2e-suite30.py
"""
import http.client
import importlib.util
import json
import os
import sys
import time
from datetime import datetime, timedelta, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("chaos", os.path.join(HERE, "chaos-booking.py"))
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)
c.TAG = "s30"            # every row this run creates carries it, for cleanup

RESULTS = []
PHONE = "+9617636{:04d}"     # artists 5300-5310; chaos cleanup clears customer_otps under +9617636%
HOST, PORT, BASE = "localhost", 3000, "/api/v1"


def rec(tid, verdict, detail):
    RESULTS.append((tid, verdict, detail))
    print(f"  {verdict:<5} {tid:<6} {detail}")


def call(method, path, body=None, token=None):
    """One request. Waits out the GENERAL rate limit only."""
    for _ in range(12):
        h = {"Content-Type": "application/json"}
        if token:
            h["Authorization"] = "Bearer " + token
        conn = http.client.HTTPConnection(HOST, PORT, timeout=30)
        try:
            conn.request(method, BASE + path, body=json.dumps(body) if body is not None else None, headers=h)
            resp = conn.getresponse()
            raw = resp.read()
            try:
                j = json.loads(raw) if raw else {}
            except ValueError:
                j = {}
        except (ConnectionError, http.client.HTTPException, OSError) as e:
            return 0, {"error": {"code": f"CONNECTION:{type(e).__name__}"}}
        finally:
            conn.close()
        if resp.status == 429 and err(j) == "RATE_LIMIT_EXCEEDED":
            print("  ... general rate limit reached; waiting 30 s")
            time.sleep(30)
            continue
        return resp.status, j
    return resp.status, j


c.call = lambda method, path, body=None, token=None: call(method, path, body, token)


def err(r):
    e = (r or {}).get("error")
    return e.get("code") if isinstance(e, dict) else None


def data(r):
    d = (r or {}).get("data")
    return d if d is not None else {}


def sql(q):
    return c.sql(q)


def iso(t):
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


def artist(name, n):
    email = f"s30.{name}@test.bedge.com"
    c.register(f"s30 {name.title()}", email, PHONE.format(n))
    sql(f"UPDATE users SET status='active' WHERE email='{email}'")
    c.verify_phone(email)
    return email


def booking(salon, store, service, artist_id, customer, start, status, price="100.00", deposit="0"):
    """A booking made directly, so each case states its own conditions. One
    hour each, so one artist's never overlap."""
    return sql(f"""INSERT INTO bookings (salon_id, store_id, artist_id, customer_id, service_id,
                       start_time, end_time, blocked_until, status, original_price, final_price,
                       deposit_amount, special_requests)
                   VALUES ('{salon}','{store}','{artist_id}','{customer}','{service}',
                       '{iso(start)}','{iso(start + timedelta(hours=1))}','{iso(start + timedelta(hours=1))}',
                       '{status}',{price},{price},{deposit},'s30 case') RETURNING id""")


def audit_rows(entity_id, action):
    return sql(f"""SELECT count(*) FROM audit_events WHERE entity_id='{entity_id}' AND action='{action}'""")


def main():
    admin_tok = c.login(c.ADMIN)
    if not admin_tok:
        rec("setup", "FAIL", "admin login failed")
        return

    # ── a salon of two ────────────────────────────────────────────────────
    owner_email = artist("owner", 5300)
    owner = c.onboard(owner_email, "s30 Salon", "s30-owner")
    c.approve_artist(owner, admin_tok)
    salon = sql(f"SELECT salon_id FROM artists WHERE id='{owner}'")
    store = sql(f"SELECT id FROM stores WHERE salon_id='{salon}' LIMIT 1")
    service = sql(f"SELECT id FROM services WHERE salon_id='{salon}' LIMIT 1")
    if sql(f"SELECT count(*) FROM subscriptions WHERE artist_id='{owner}'") == "0":
        sql(f"INSERT INTO subscriptions (artist_id, plan_code, monthly_price) VALUES ('{owner}','multi',0)")
    c.seat_plan(salon, "multi")
    owner_tok = c.login(owner_email)
    owner_user = sql(f"SELECT user_id FROM artists WHERE id='{owner}'")

    member_email = artist("member", 5301)
    member = c.join(owner_tok, member_email, PHONE.format(5301), "s30-member", admin_tok)
    member_tok = c.login(member_email)
    member_user = sql(f"SELECT user_id FROM artists WHERE id='{member}'")
    owner_tok = c.login(owner_email)   # a fresh token after the salon gained a member

    customer = sql("""INSERT INTO users (name, email, password_hash, role)
                      VALUES ('s30 Sara', 's30.sara@test.bedge.com', 'x', 'customer')
                      ON CONFLICT (email) DO UPDATE SET deleted_at = NULL RETURNING id""")
    cust_email = "s30.cust@test.bedge.com"
    c.register("s30 Cust", cust_email, PHONE.format(5302), role="customer")
    sql(f"UPDATE users SET status='active' WHERE email='{cust_email}'")
    cust_tok = c.login(cust_email)
    print(f"  setup: salon {salon[:8]}, owner {owner[:8]}, member {member[:8]}\n")

    # Last month (complete) and the coming days. Last month is used for the
    # money figures so "today" never straddles a boundary mid-run.
    now = datetime.now(timezone.utc).replace(minute=0, second=0, microsecond=0)
    first_this = now.replace(day=1, hour=9)
    last_month = (first_this - timedelta(days=1)).replace(day=10)
    lm_from, lm_to = last_month.replace(day=1).strftime("%Y-%m-%d"), (first_this - timedelta(days=1)).strftime("%Y-%m-%d")
    booking(salon, store, service, owner, customer, last_month, "completed", "120.00", "30.00")
    booking(salon, store, service, member, customer, last_month + timedelta(hours=2), "completed", "80.00")
    booking(salon, store, service, member, customer, last_month + timedelta(hours=4), "no_show", "60.00", "30.00")
    booking(salon, store, service, member, customer, last_month + timedelta(hours=6), "cancelled", "999.00")

    soon = now + timedelta(days=3)
    m_pending = booking(salon, store, service, member, customer, soon.replace(hour=8), "pending")
    o_pending = booking(salon, store, service, owner, customer, soon.replace(hour=8), "pending")
    m_approved = booking(salon, store, service, member, customer, soon.replace(hour=11), "approved")
    m_confirmed = booking(salon, store, service, member, customer, soon.replace(hour=14), "confirmed")

    # ── 30.1 / 30.2 the overview ──────────────────────────────────────────
    st, r = call("GET", f"/earnings/salon?from={lm_from}&to={lm_to}", None, owner_tok)
    d = data(r)
    want = sql(f"""SELECT COALESCE(sum(final_price),0) FROM bookings WHERE salon_id='{salon}'
                   AND status IN ('completed','no_show')
                   AND start_time >= '{lm_from}'::date AT TIME ZONE 'Asia/Beirut'
                   AND start_time < ('{lm_to}'::date + 1) AT TIME ZONE 'Asia/Beirut'""")
    rows = {a["artist_id"]: a for a in d.get("by_artist", [])} if st == 200 else {}
    if st != 200:
        rec("30.1", "FAIL", f"owner refused: {st} {err(r)}")
    elif float(d["totals"]["earned"]) != float(want) or float(want) != 260.0:
        rec("30.1", "FAIL", f"earned {d['totals']['earned']} vs database {want} (expected 260)")
    elif set(rows) != {owner, member} or not rows[owner]["is_you"] or rows[member]["is_you"]:
        rec("30.1", "FAIL", f"by_artist wrong: {list(rows)} you={[a['is_you'] for a in rows.values()]}")
    elif d["totals"]["no_shows"] != 1 or d["totals"]["cancelled"] != 1 or float(rows[member]["earned"]) != 140.0:
        rec("30.1", "FAIL", f"counts wrong: {d['totals']} member={rows[member]}")
    else:
        rec("30.1", "PASS", f"earned ${d['totals']['earned']} = database; owner and member listed, member earned ${rows[member]['earned']}")

    st, r = call("GET", f"/earnings/salon?from={lm_from}&to={lm_to}", None, member_tok)
    st2, r2 = call("GET", f"/earnings/summary?from={lm_from}&to={lm_to}", None, member_tok)
    if st != 403 or err(r) != "SALON_ROLE_FORBIDDEN":
        rec("30.2", "FAIL", f"member reached the salon overview: {st} {err(r)}")
    elif st2 != 200 or float(data(r2).get("total_revenue", -1)) != 140.0:
        rec("30.2", "FAIL", f"member's own earnings wrong: {st2} {data(r2).get('total_revenue')}")
    else:
        rec("30.2", "PASS", "member: 403 SALON_ROLE_FORBIDDEN on the salon; her own Earnings show her $140 only")

    # ── 30.3 / 30.4 the team's bookings ───────────────────────────────────
    st, r = call("GET", "/bookings/salon?limit=100", None, owner_tok)
    ids = {b["id"] for b in data(r)} if st == 200 else set()
    st_m, r_m = call("GET", f"/bookings/salon?artist_id={member}&limit=100", None, owner_tok)
    only = {b["artist_id"] for b in data(r_m)} if st_m == 200 else set()
    st_x, r_x = call("GET", "/bookings/salon", None, member_tok)
    if st != 200 or not {m_pending, o_pending, m_confirmed} <= ids:
        rec("30.3", "FAIL", f"owner's team list: {st} {err(r)} has {len(ids)}")
    elif only != {member}:
        rec("30.3", "FAIL", f"artist filter returned {only}")
    elif st_x != 403 or err(r_x) != "SALON_ROLE_FORBIDDEN":
        rec("30.3", "FAIL", f"member reached the team list: {st_x} {err(r_x)}")
    else:
        rec("30.3", "PASS", f"owner sees both artists' {len(ids)} bookings, the filter narrows to one; member 403")

    monday = (soon - timedelta(days=soon.weekday())).strftime("%Y-%m-%d")
    st, r = call("GET", f"/bookings/salon/calendar?week_start={monday}", None, owner_tok)
    cal = {b["id"] for b in data(r)} if st == 200 else set()
    if st == 200 and m_confirmed in cal and m_pending not in cal:
        rec("30.4", "PASS", f"team week: the member's confirmed booking, not her pending request ({len(cal)} on the grid)")
    else:
        rec("30.4", "FAIL", f"{st} {err(r)} confirmed={m_confirmed in cal} pending={m_pending in cal}")

    # ── 30.5-30.7 acting on bookings, and who is named ───────────────────
    st, r = call("PATCH", f"/bookings/{m_pending}/approve", {}, owner_tok)
    actor = sql(f"SELECT actor_id FROM audit_events WHERE entity_id='{m_pending}' AND action='booking.approve'")
    status = sql(f"SELECT status FROM bookings WHERE id='{m_pending}'")
    if st != 200 or status != "approved":
        rec("30.5", "FAIL", f"owner approving the member's booking: {st} {err(r)} status={status}")
    elif actor != owner_user:
        rec("30.5", "FAIL", f"activity row names {actor!r}, not the owner")
    else:
        rec("30.5", "PASS", "owner approved the member's booking; the activity row names the owner")

    st, r = call("PATCH", f"/bookings/{o_pending}/approve", {}, member_tok)
    status = sql(f"SELECT status FROM bookings WHERE id='{o_pending}'")
    if st == 404 and err(r) == "BOOKING_NOT_FOUND" and status == "pending" and audit_rows(o_pending, "booking.approve") == "0":
        rec("30.6", "PASS", "member approving the owner's booking: 404, still pending, nothing recorded")
    else:
        rec("30.6", "FAIL", f"{st} {err(r)} status={status}")

    st, r = call("PATCH", f"/bookings/{m_approved}/confirm-payment", {}, member_tok)
    actor = sql(f"SELECT actor_id FROM audit_events WHERE entity_id='{m_approved}' AND action='booking.confirm'")
    if st == 200 and actor == member_user:
        rec("30.7", "PASS", "member confirming her own deposit is recorded under her name")
    else:
        rec("30.7", "FAIL", f"{st} {err(r)} actor={actor!r}")

    # ── 30.8 a payment account ────────────────────────────────────────────
    call("PUT", "/artists/salon/payment-methods", {"method": "whish", "account_name": "s30 Owner", "account_ref": "71000001"}, owner_tok)
    call("PUT", "/artists/salon/payment-methods", {"method": "whish", "account_name": "s30 Owner", "account_ref": "71000002"}, owner_tok)
    st, r = call("GET", "/salon/activity?kind=payments", None, owner_tok)
    saves = [e for e in data(r) if e.get("action") == "payment_method.save"] if st == 200 else []
    changed = [e for e in saves if (e.get("old") or {}).get("account_ref") == "71000001"
               and (e.get("new") or {}).get("account_ref") == "71000002"]
    if st != 200:
        rec("30.8", "FAIL", f"feed refused: {st} {err(r)}")
    elif len(saves) != 2 or not changed or not changed[0]["actor"]["is_you"]:
        rec("30.8", "FAIL", f"{len(saves)} saves, change shown={bool(changed)}")
    else:
        rec("30.8", "PASS", "the account change shows 71000001 -> 71000002, by you")

    # ── 30.9 the feed is this salon's ─────────────────────────────────────
    st, r = call("GET", "/salon/activity?limit=100", None, owner_tok)
    feed = data(r) if st == 200 else []
    ids = ",".join(f"'{e['id']}'" for e in feed) or "NULL"
    foreign = sql(f"SELECT count(*) FROM audit_events WHERE id IN ({ids}) AND salon_id IS DISTINCT FROM '{salon}'")
    appr = [e for e in feed if e["entity_id"] == m_pending and e["action"] == "booking.approve"]
    subj = appr[0]["subject"] if appr else {}
    if st != 200 or not feed:
        rec("30.9", "FAIL", f"{st} {err(r)} {len(feed)} rows")
    elif foreign != "0":
        rec("30.9", "FAIL", f"{foreign} rows from another salon")
    elif subj.get("artist_name") != "s30 Member" or subj.get("customer_name") != "s30 Sara" or not subj.get("service_name"):
        rec("30.9", "FAIL", f"subject names wrong: {subj}")
    elif "ip_address" in json.dumps(feed):
        rec("30.9", "FAIL", "the feed exposes IP addresses")
    else:
        rec("30.9", "PASS", f"{len(feed)} rows, all this salon's; names joined ({subj['service_name']} for {subj['customer_name']} with {subj['artist_name']}); no IPs")

    # ── 30.10 who may read it ─────────────────────────────────────────────
    st_m, r_m = call("GET", "/salon/activity", None, member_tok)
    st_c, r_c = call("GET", "/salon/activity", None, cust_tok)
    if st_m == 403 and err(r_m) == "SALON_ROLE_FORBIDDEN" and st_c == 403:
        rec("30.10", "PASS", f"member 403 SALON_ROLE_FORBIDDEN; customer 403 {err(r_c)}")
    else:
        rec("30.10", "FAIL", f"member {st_m} {err(r_m)}, customer {st_c} {err(r_c)}")

    # ── 30.11 filters ─────────────────────────────────────────────────────
    st, r = call("GET", f"/salon/activity?actor={member_user}", None, owner_tok)
    by_member = {e["actor"].get("id") for e in data(r)} if st == 200 else set()
    st_b, r_b = call("GET", "/salon/activity?kind=bookings", None, owner_tok)
    kinds = {e["entity_type"] for e in data(r_b)} if st_b == 200 else set()
    st_x, r_x = call("GET", "/salon/activity?kind=everything", None, owner_tok)
    if by_member == {member_user} and kinds == {"booking"} and st_x == 400 and err(r_x) == "INVALID_KIND":
        rec("30.11", "PASS", "actor and kind narrow the feed; an unknown kind is 400 INVALID_KIND")
    else:
        rec("30.11", "FAIL", f"actor={by_member} kinds={kinds} bogus={st_x} {err(r_x)}")

    # ── 30.12 a menu price ────────────────────────────────────────────────
    st, r = call("PATCH", f"/artists/salon/services/{service}", {"price": "175.00"}, owner_tok)
    st2, r2 = call("GET", "/salon/activity?kind=menu", None, owner_tok)
    ups = [e for e in data(r2) if e.get("action") == "service.update" and e["entity_id"] == service] if st2 == 200 else []
    if st == 200 and ups and ups[0]["old"].get("price") == "150.00" and ups[0]["new"].get("price") == "175.00":
        rec("30.12", "PASS", "menu price 150.00 -> 175.00 in the feed, with the service's name")
    else:
        rec("30.12", "FAIL", f"{st} {err(r)} feed={ups[:1]}")


def teardown():
    for q in [
        "DELETE FROM notifications WHERE booking_id IN (SELECT id FROM bookings WHERE special_requests LIKE 's30%')",
        "DELETE FROM bookings WHERE special_requests LIKE 's30%'",
        "DELETE FROM artist_services WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email LIKE 's30.%')",
    ]:
        try:
            sql(q)
        except RuntimeError as e:
            print(f"  cleanup: {e}")
    c.cleanup()
    left = sql("""SELECT (SELECT count(*) FROM salons WHERE name LIKE 's30%')
                + (SELECT count(*) FROM bookings WHERE special_requests LIKE 's30%')
                + (SELECT count(*) FROM users WHERE email LIKE 's30.%' AND deleted_at IS NULL)""")
    print(f"  cleanup (suite 30 rows): {left} residual{'' if left == '0' else '   !! NOT CLEAN !!'}")


if __name__ == "__main__":
    print("\n  E2E suite 30 - the owner's dashboard\n")
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
    print(f"\n  {p} pass, {f} fail\n")
    sys.exit(1 if f else 0)
