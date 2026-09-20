#!/usr/bin/env python3
"""
UC-6 · Subscription and access — executable verification.

WHY THIS SUITE IS NOT ABOUT DeriveStatus

`internal/pkg/subscription` is at 100% statement coverage with 26 tests. The
POLICY - which status permits what - is thoroughly settled, and re-asserting
it here would add nothing.

The gap is that the policy has THREE ENFORCEMENT POINTS and only two of them
call it:

    middleware/billing.go      -> subscription.Enforce(...).CanModifyAccount
    booking/service.go         -> subscription.Enforce(...).AcceptsNewBookings
    discovery/repository.go    -> SQL, because a per-row Go call is not
                                  available to a query

The third necessarily re-implements the rule, and on 2026-09-20 it had
drifted: Enforce(StatusCancelled) says VisibleInDiscovery FALSE - "a
deliberate exit... they simply stop being sold" - while the query treated
`cancelled_at IS NOT NULL` as visible. Measured live, an artist cancelled ten
days earlier with a period ended a hundred days earlier was still listed in
Discover.

Worse, GetAvailableSlots never consulted the gate at all, so that same artist
offered 33 bookable times and refused the hold at the last step. A customer
browsing, choosing a date, picking a time and only then being told no.

A correct policy function enforced inconsistently is not a correct system.
This suite checks the ENFORCEMENT, at every layer a customer actually meets.

    make verify-uc6

Needs the API on :3000 and bedge-postgres. It manipulates one test-roster
artist's subscription and restores it, including on failure.
"""

import datetime
import json
import subprocess
import sys
import urllib.error
import urllib.request

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA"]

# mkup4, from the permanent test roster. Chosen because it is a fixture
# account with no real bookings - never Rania, whose subscription is live and
# whose dashboard may be open while this runs.
SUBJECT_EMAIL = "mkup4@test.bedge.com"

PASS, FAIL, SKIP = [], [], []


def sql(q):
    p = subprocess.run(PG + ["-c", q], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError(f"SQL failed: {p.stderr.strip()[:200]}")
    return p.stdout.strip()


def get(path):
    try:
        with urllib.request.urlopen(API + path, timeout=25) as r:
            return json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return json.loads(e.read() or b"{}")
        except Exception:
            return {}
    except urllib.error.URLError as e:
        print(f"\n  cannot reach the API ({e.reason})")
        sys.exit(2)


def check(fid, desc, ok, detail):
    (PASS if ok else FAIL).append(fid)
    print(f"  {'PASS' if ok else 'FAIL'}  {fid:5} {desc}\n         {detail}")


def main():
    artist = sql(f"""SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id
                     WHERE u.email='{SUBJECT_EMAIL}';""")
    if not artist:
        print(f"  {SUBJECT_EMAIL} not found — cannot run")
        sys.exit(2)
    store = sql(f"""SELECT s.id FROM stores s JOIN artists a ON a.salon_id=s.salon_id
                    WHERE a.id='{artist}' LIMIT 1;""")
    svc = sql(f"""SELECT sv.id FROM services sv JOIN artists a ON a.salon_id=sv.salon_id
                  WHERE a.id='{artist}' AND sv.is_active LIMIT 1;""")
    if not store or not svc:
        print("  subject artist has no store or active service — cannot run")
        sys.exit(2)

    original = sql(f"""SELECT coalesce(plan_code,'')||'|'||coalesce(cancelled_at::text,'')
                       ||'|'||coalesce(current_period_end::text,'')
                       FROM subscriptions WHERE artist_id='{artist}';""")
    had_hours = int(sql(f"SELECT count(*) FROM business_hours WHERE store_id='{store}';"))
    date = (datetime.datetime.now(datetime.timezone.utc)
            + datetime.timedelta(days=5)).strftime("%Y-%m-%d")

    def set_sub(plan, cancelled_sql, period_sql):
        sql(f"""UPDATE subscriptions SET plan_code='{plan}',
                  cancelled_at={cancelled_sql}, current_period_end={period_sql}
                WHERE artist_id='{artist}';""")

    def visible():
        d = get("/discovery/artists?limit=200").get("data") or []
        return any(x["id"] == artist for x in d)

    def slots():
        r = get(f"/bookings/slots?artist_id={artist}&store_id={store}"
                f"&service_id={svc}&date={date}")
        e = r.get("error")
        return ("refused", e["code"]) if e else ("offered", len(r.get("data") or []))

    print(f"\n  subject {SUBJECT_EMAIL} ({artist[:8]})\n")
    try:
        # Hours are needed for slot generation; absence would make every slot
        # assertion pass for the wrong reason.
        for d in range(7):
            sql(f"""INSERT INTO business_hours (store_id, day_of_week, open_time, close_time, is_open)
                    VALUES ('{store}',{d},'09:00','18:00',true)
                    ON CONFLICT (store_id, day_of_week)
                    DO UPDATE SET is_open=true, open_time='09:00', close_time='18:00';""")

        # ── an artist in good standing is sold ────────────────────────────
        set_sub("comped", "NULL", "NULL")
        kind, n = slots()
        check("S1", "a comped artist is visible and bookable",
              visible() and kind == "offered" and n > 0,
              f"discovery={'visible' if visible() else 'hidden'}, slots={kind}:{n}")

        # ── cancelled: Enforce says stop selling them ─────────────────────
        set_sub("starter", "NOW() - interval '10 days'", "NOW() - interval '100 days'")
        vis, (kind, n) = visible(), slots()
        check("S2", "a cancelled artist is hidden from discovery", not vis,
              f"discovery={'VISIBLE' if vis else 'hidden'}")
        check("S3", "a cancelled artist offers no bookable times",
              kind == "refused", f"slots={kind}:{n}")

        # ── past_due: invisible, unbookable, still able to fix and pay ────
        set_sub("starter", "NULL", "NOW() - interval '30 days'")
        vis, (kind, n) = visible(), slots()
        check("S4", "a past_due artist is hidden from discovery", not vis,
              f"discovery={'VISIBLE' if vis else 'hidden'}")
        check("S5", "a past_due artist offers no bookable times",
              kind == "refused", f"slots={kind}:{n}")

        # ── inside grace: still sold ──────────────────────────────────────
        # GraceDays is 21, so a period that ended 5 days ago is still grace,
        # and grace is explicitly full access. This is the boundary worth
        # holding: too strict here and an artist is cut off for being a few
        # days late on a payment they are still allowed to make.
        set_sub("starter", "NULL", "NOW() - interval '5 days'")
        vis, (kind, n) = visible(), slots()
        check("S6", "an artist inside the grace window is still sold",
              vis and kind == "offered",
              f"5 days past period end: discovery={'visible' if vis else 'HIDDEN'}, slots={kind}:{n}")

        # ── an active paid subscription ───────────────────────────────────
        set_sub("starter", "NULL", "NOW() + interval '20 days'")
        vis, (kind, n) = visible(), slots()
        check("S7", "an active paying artist is visible and bookable",
              vis and kind == "offered",
              f"discovery={'visible' if vis else 'HIDDEN'}, slots={kind}:{n}")

    finally:
        plan, cancelled, period = (original.split("|") + ["", "", ""])[:3]
        set_sub(plan or "comped",
                f"'{cancelled}'" if cancelled else "NULL",
                f"'{period}'" if period else "NULL")
        if had_hours == 0:
            sql(f"DELETE FROM business_hours WHERE store_id='{store}';")
        print(f"\n  restored: plan={plan or 'comped'}, hours={had_hours}")

    print(f"  {len(PASS)} passed, {len(FAIL)} failed, {len(SKIP)} skipped")
    if FAIL:
        print(f"  FAILED: {', '.join(FAIL)}")
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
