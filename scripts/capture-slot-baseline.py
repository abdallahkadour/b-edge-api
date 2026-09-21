#!/usr/bin/env python3
"""Captures a golden baseline of slot-generation output.

Phase 0, task T0.5 of B-Edge-Multi-Artist-Salon-Plan-v1.md.

WHY THIS EXISTS, AND WHY IT CANNOT WAIT
---------------------------------------
Migration 047 adds `artist_schedules`, and booking.GetAvailableSlots starts
intersecting the store's opening window with the artist's personal rota. Every
artist on the platform today has ZERO rota rows, so the intersection must be a
provable identity operation for them or the change silently empties five live
calendars.

The only honest proof is a byte-identical comparison against output captured
BEFORE the change. Once 047 ships, this baseline can no longer be taken.

Run:
    python3 scripts/capture-slot-baseline.py > project-docs/slot-baseline-pre-046.json
    python3 scripts/capture-slot-baseline.py --compare project-docs/slot-baseline-pre-046.json

Design rules this follows (B-Edge-Security-Test-Plan-v1.md 3.4c):
  * Fixtures are DISCOVERED from the database, never hardcoded.
  * A failed request is recorded as an error, never as an empty slot list -
    a harness that turns a 500 into `[]` reports "no availability" and looks
    like a product defect. That exact mistake has been made on this project.
  * Tooling failure is surfaced, not swallowed.
"""
import argparse
import datetime as dt
import json
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request

API = "http://localhost:3000/api/v1"
PG = ["docker", "exec", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge", "-tA", "-F", "|"]
HORIZON_DAYS = 90

# middleware/register.go: maxRequestsPerWindow = 600 over rateLimitWindow = 5min,
# i.e. 2 req/sec sustained. The first run of this script fired 2790 requests at
# 8-way concurrency and 2191 of them came back 429 - the limiter working exactly
# as designed, which a careless reading would have logged as "no availability".
# Pace deliberately under the ceiling instead of racing it.
# 1.2/s is 360 requests per 5-minute window against a ceiling of 600. An
# earlier attempt at 1.7/s (510 per window, nominally under the limit) still
# tripped it intermittently and spent its time in backoff, averaging 0.53/s
# actual. Leave the margin wide - the run is unattended.
RATE_PER_SEC = 1.2
RETRY_429 = 4

TRIPLE_SQL = """
SELECT a.id, st.id, sv.id, u.email, st.name, sv.name
  FROM artists a
  JOIN artist_stores ast ON ast.artist_id = a.id
  JOIN stores   st ON st.id = ast.store_id AND st.is_active
  JOIN services sv ON sv.salon_id = a.salon_id AND sv.is_active
  JOIN users    u  ON u.id = a.user_id
 WHERE a.status = 'active'
 ORDER BY a.id, st.id, sv.id
"""


def sql(query):
    p = subprocess.run(PG + ["-c", query], capture_output=True, text=True)
    if p.returncode != 0 or "ERROR" in p.stderr:
        raise RuntimeError("psql failed: " + p.stderr.strip()[:300])
    return [line for line in p.stdout.strip().splitlines() if line.strip()]


def discover_triples():
    rows = [r.split("|") for r in sql(TRIPLE_SQL)]
    return [
        {
            "artist_id": r[0], "store_id": r[1], "service_id": r[2],
            "artist": r[3], "store": r[4], "service": r[5],
        }
        for r in rows
    ]


_pace_lock = threading.Lock()
_next_slot = [0.0]


def _pace():
    """Blocks until this caller's turn, holding the process under RATE_PER_SEC."""
    with _pace_lock:
        now = time.monotonic()
        due = max(now, _next_slot[0])
        _next_slot[0] = due + (1.0 / RATE_PER_SEC)
    if due > now:
        time.sleep(due - now)


def fetch_slots(artist_id, store_id, service_id, date):
    """Returns the slot list, or a dict describing the failure.

    Never returns [] for a failed call. An error and an empty day must stay
    distinguishable, or a rate-limited run reads as a calendar with no
    availability - which is precisely what the first run of this script
    produced before it was paced.
    """
    url = (f"{API}/bookings/slots?artist_id={artist_id}&store_id={store_id}"
           f"&service_id={service_id}&date={date}")

    for attempt in range(RETRY_429 + 1):
        _pace()
        try:
            with urllib.request.urlopen(url, timeout=30) as r:
                body = json.loads(r.read() or b"{}")
        except urllib.error.HTTPError as e:
            raw = e.read()
            if e.code == 429 and attempt < RETRY_429:
                time.sleep(20 * (attempt + 1))   # the window is 5 min; back off hard
                continue
            try:
                return {"__error__": f"HTTP {e.code}", "body": json.loads(raw or b"{}")}
            except Exception:
                return {"__error__": f"HTTP {e.code}",
                        "body": raw.decode(errors="replace")[:200]}
        except urllib.error.URLError as e:
            return {"__error__": f"unreachable: {e.reason}"}
        except Exception as e:                  # noqa: BLE001 - surface, don't swallow
            return {"__error__": f"{type(e).__name__}: {e}"}

        data = body.get("data")
        if data is None:
            return {"__error__": "response carried no data key", "body": body}
        return data

    return {"__error__": "rate limited after retries"}


def capture():
    triples = discover_triples()
    if not triples:
        sys.exit("no bookable (artist, store, service) triples found - nothing to baseline")

    start = dt.date.today()
    dates = [(start + dt.timedelta(days=i)).isoformat() for i in range(HORIZON_DAYS)]

    jobs = []
    for t in triples:
        for d in dates:
            jobs.append((t, d))

    print(f"  {len(triples)} triples x {len(dates)} dates = {len(jobs)} samples",
          file=sys.stderr)

    eta = len(jobs) / RATE_PER_SEC / 60
    print(f"  paced at {RATE_PER_SEC}/s under the 600-per-5-min limiter "
          f"- about {eta:.0f} min", file=sys.stderr)

    results = {}
    errors = 0
    t0 = time.monotonic()
    for i, (t, d) in enumerate(jobs, 1):
        key = f'{t["artist_id"]}|{t["store_id"]}|{t["service_id"]}'
        out = fetch_slots(t["artist_id"], t["store_id"], t["service_id"], d)
        if isinstance(out, dict) and "__error__" in out:
            errors += 1
        results.setdefault(key, {})[d] = out
        if i % 200 == 0:
            el = (time.monotonic() - t0) / 60
            print(f"  {i}/{len(jobs)} - {el:.1f} min elapsed, {errors} errors",
                  file=sys.stderr)

    if errors:
        print(f"  WARNING: {errors} of {len(jobs)} samples errored - they are recorded "
              f"as errors, not as empty days. A baseline with errors in it is not a "
              f"baseline; fix the cause and recapture.", file=sys.stderr)

    # Per-triple coverage. A triple whose store has no open business_hours
    # returns an empty list for all 90 days - legitimate, but it contributes
    # nothing to the comparison, because empty compares equal to empty no
    # matter what the intersection does. Surfacing the count stops a baseline
    # that is mostly empty from being mistaken for a strong gate.
    #
    # As of 2026-09-21 the stores belonging to mkup3 and mkup4 have zero open
    # days, so their triples are always-empty by configuration, not by fault.
    coverage = {}
    for key, days in results.items():
        lists = [v for v in days.values() if isinstance(v, list)]
        coverage[key] = {
            "days_ok": len(lists),
            "days_with_slots": sum(1 for v in lists if v),
            "total_slots": sum(len(v) for v in lists),
        }
    productive = sum(1 for c in coverage.values() if c["days_with_slots"])
    print(f"  {productive} of {len(coverage)} triples produced any availability; "
          f"the rest are empty by configuration and compare vacuously",
          file=sys.stderr)

    baseline = {
        "captured_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "coverage": coverage,
        "productive_triples": productive,
        "purpose": "pre-migration-046 slot output; T0.5 of the multi-artist salon plan",
        "horizon_days": HORIZON_DAYS,
        "start_date": start.isoformat(),
        "triple_count": len(triples),
        "sample_count": len(jobs),
        "error_count": errors,
        "triples": {
            f'{t["artist_id"]}|{t["store_id"]}|{t["service_id"]}':
                f'{t["artist"]} @ {t["store"]} / {t["service"]}'
            for t in triples
        },
        "slots": results,
    }
    return baseline


def compare(path):
    old = json.load(open(path))
    new = capture()

    if old["start_date"] != new["start_date"]:
        print(f"  NOTE: baseline starts {old['start_date']}, this run starts "
              f"{new['start_date']} - only overlapping dates are compared",
              file=sys.stderr)

    # The capture date cannot be compared across runs.
    #
    # Same-day minimum notice means today's availability legitimately shrinks
    # as the clock advances, so a baseline taken at 05:00 and a comparison run
    # at 07:00 disagree about today no matter what the code does. The first
    # run of this comparison reported 24 differences and every one of them was
    # on day 0 - which was noise around one real finding (an empty result
    # serialising as null rather than []), and would have buried a genuine
    # regression had there been one.
    #
    # Skipped loudly rather than silently: a comparison that quietly drops
    # samples is how a gate stops being a gate.
    same_day = old["start_date"]
    skipped_same_day = 0

    drift, checked, missing = [], 0, 0
    for key, days in old["slots"].items():
        if key not in new["slots"]:
            missing += 1
            drift.append(f"TRIPLE GONE: {old['triples'].get(key, key)}")
            continue
        for d, want in days.items():
            got = new["slots"][key].get(d)
            if got is None:
                continue                      # outside the new run's horizon
            if d == same_day:
                skipped_same_day += 1
                continue
            checked += 1
            if got != want:
                drift.append(
                    f"{old['triples'].get(key, key)} on {d}:\n"
                    f"    was {json.dumps(want)[:160]}\n"
                    f"    now {json.dumps(got)[:160]}")

    print(f"\n  compared {checked} overlapping samples across "
          f"{len(old['slots'])} triples")
    if skipped_same_day:
        print(f"  skipped {skipped_same_day} samples on the capture date "
              f"({same_day}) - same-day notice makes today's availability "
              f"shrink with the clock, so it is not comparable across runs")
    if drift:
        print(f"  DRIFT in {len(drift)} samples:\n")
        for d in drift[:25]:
            print("   -", d)
        if len(drift) > 25:
            print(f"   ... and {len(drift) - 25} more")
        sys.exit(1)
    print("  IDENTICAL - slot generation is unchanged\n")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--compare", metavar="BASELINE",
                    help="re-run and diff against a captured baseline")
    # Point this at a snapshot instance on its own port when capturing while
    # the repository is being edited. `air` rebuilds and restarts the :3000
    # dev API on every Go file save, and a restart mid-capture turns
    # in-flight requests into errors - which is what ruined the second
    # attempt at this baseline.
    ap.add_argument("--api", default=API, metavar="URL",
                    help="API base URL (default %(default)s)")
    args = ap.parse_args()
    API = args.api

    if args.compare:
        compare(args.compare)
    else:
        result = capture()
        json.dump(result, sys.stdout, indent=2, sort_keys=True)
        print()
        if result["error_count"]:
            # Exit non-zero so an unattended run cannot quietly leave an
            # error-bearing file in place of a baseline. The file is still
            # written, so the failures can be inspected.
            sys.exit(f"\n  {result['error_count']} sample(s) failed - this is "
                     f"NOT a usable baseline. Diagnose and recapture.")
