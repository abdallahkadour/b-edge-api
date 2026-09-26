#!/usr/bin/env python3
"""Generate project-docs/B-Edge-ERD.html from the LIVE database schema.

    python3 scripts/erd.py            # reads bedge-postgres, writes the ERD

WHY GENERATED

The previous ERD was drawn by hand in June and never touched again; by
migration 052 it showed about half the tables. A diagram generated from the
schema cannot drift that way: after a migration, run this and commit the
output. It reads the database the migrations built (docker exec into
bedge-postgres, like the other scripts), so it shows what IS, not what a
spec intended.

One diagram per area rather than one for all 36 tables, which no one can
read. A table referenced from another area appears as a name-only stub so
the links stay visible. A table not assigned to any area lands in "Other"
- it is never silently dropped - and an area naming a table that does not
exist is an error, so AREAS cannot rot quietly either.
"""
import json
import subprocess
import sys
from datetime import date
from html import escape
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "project-docs" / "B-Edge-ERD.html"

# Areas, in reading order. Every table belongs to exactly one.
AREAS = [
    ("Accounts and sign-in", "Every person is one users row; artists and customers sign in differently.",
     ["users", "refresh_tokens", "password_resets", "customer_otps"]),
    ("Salons and team", "A salon, its owner and members, its stores, and invitations to join.",
     ["salons", "artists", "salon_invitations", "stores", "artist_stores", "artist_store_buffers"]),
    ("Menu and pricing", "The salon's service menu, each artist's switch and own price, and portfolio tags.",
     ["services", "service_categories", "artist_services", "media", "media_services"]),
    ("Opening hours and schedules", "When stores are open and when each artist works.",
     ["business_hours", "business_hours_exceptions", "artist_schedules", "artist_schedule_exceptions"]),
    ("Bookings", "A booking ties a customer to one artist, store and service; reviews, waitlist and discounts hang off it.",
     ["bookings", "waitlist_entries", "reviews", "discounts", "discount_redemptions", "client_notes",
      "salon_payment_methods"]),
    ("Shop", "Products a salon sells and the orders customers place.",
     ["products", "orders", "order_items"]),
    ("Billing", "Plans, each artist's subscription, and invoices.",
     ["plans", "subscriptions", "invoices"]),
    ("Messages and records", "Outbound messages, the in-app inbox, the audit trail and user reports.",
     ["notifications", "user_notifications", "audit_events", "reports"]),
]

QUERY = r"""
SELECT json_build_object(
  'version', (SELECT version FROM schema_migrations),
  'tables', (SELECT json_agg(table_name ORDER BY table_name) FROM information_schema.tables
             WHERE table_schema='public' AND table_type='BASE TABLE' AND table_name <> 'schema_migrations'),
  'columns', (SELECT json_agg(json_build_object('t', c.table_name, 'c', c.column_name, 'type', c.udt_name,
                                                'null', c.is_nullable = 'YES') ORDER BY c.table_name, c.ordinal_position)
              FROM information_schema.columns c
              JOIN information_schema.tables t ON t.table_name = c.table_name AND t.table_schema = c.table_schema
              WHERE c.table_schema='public' AND t.table_type='BASE TABLE' AND c.table_name <> 'schema_migrations'),
  'pks', (SELECT json_agg(json_build_object('t', tc.table_name, 'c', kcu.column_name))
          FROM information_schema.table_constraints tc
          JOIN information_schema.key_column_usage kcu ON kcu.constraint_name = tc.constraint_name
           AND kcu.table_schema = tc.table_schema
          WHERE tc.table_schema='public' AND tc.constraint_type='PRIMARY KEY'),
  'uniques', (SELECT json_agg(json_build_object('t', rel.relname, 'cols',
                (SELECT json_agg(a.attname) FROM unnest(con.conkey) k JOIN pg_attribute a
                   ON a.attrelid = con.conrelid AND a.attnum = k)))
              FROM pg_constraint con JOIN pg_class rel ON rel.oid = con.conrelid
              JOIN pg_namespace n ON n.oid = rel.relnamespace
              WHERE n.nspname='public' AND con.contype IN ('u','p')),
  'fks', (SELECT json_agg(json_build_object(
            'from', src.relname, 'to', dst.relname,
            'cols', (SELECT json_agg(a.attname) FROM unnest(con.conkey) k JOIN pg_attribute a
                       ON a.attrelid = con.conrelid AND a.attnum = k),
            'del', con.confdeltype) ORDER BY src.relname, con.conname)
          FROM pg_constraint con
          JOIN pg_class src ON src.oid = con.conrelid
          JOIN pg_class dst ON dst.oid = con.confrelid
          JOIN pg_namespace n ON n.oid = src.relnamespace
          WHERE n.nspname='public' AND con.contype='f')
);
"""


def read_schema():
    out = subprocess.run(
        ["docker", "exec", "-i", "bedge-postgres", "psql", "-U", "postgres", "-d", "bedge",
         "-tA", "-v", "ON_ERROR_STOP=1", "-c", QUERY],
        capture_output=True, text=True)
    if out.returncode != 0:
        sys.exit(f"reading the schema failed: {out.stderr.strip()}")
    return json.loads(out.stdout)


def build(schema):
    tables = set(schema["tables"])
    assigned = [t for _, _, ts in AREAS for t in ts]
    unknown = sorted(set(assigned) - tables)
    if unknown:
        sys.exit(f"AREAS names tables that do not exist: {', '.join(unknown)}")
    dupes = sorted({t for t in assigned if assigned.count(t) > 1})
    if dupes:
        sys.exit(f"AREAS assigns a table twice: {', '.join(dupes)}")
    areas = list(AREAS)
    other = sorted(tables - set(assigned))
    if other:
        areas.append(("Other", "Tables no area claims yet. Add each to AREAS in scripts/erd.py.", other))

    cols = {}
    for c in schema["columns"]:
        cols.setdefault(c["t"], []).append(c)
    pk = {(p["t"], p["c"]) for p in schema["pks"] or []}
    single_unique = {(u["t"], u["cols"][0]) for u in schema["uniques"] or [] if len(u["cols"]) == 1}
    fks = schema["fks"] or []
    fk_cols = {(f["from"], col) for f in fks for col in f["cols"]}
    nullable = {(c["t"], c["c"]): c["null"] for c in schema["columns"]}

    def entity(t):
        lines = [f"  {t} {{"]
        for c in cols.get(t, []):
            keys = [k for k, on in (("PK", (t, c["c"]) in pk), ("FK", (t, c["c"]) in fk_cols)) if on]
            if not keys and (t, c["c"]) in single_unique:
                keys = ["UK"]
            lines.append(f"    {c['type']} {c['c']}{(' ' + ', '.join(keys)) if keys else ''}")
        lines.append("  }")
        return "\n".join(lines)

    def relation(f):
        # child }o--|| parent: many children to one parent. A unique FK column
        # is one-to-one; a nullable one means the parent is optional.
        col = f["cols"][0] if len(f["cols"]) == 1 else None
        many = "|o" if col and (f["from"], col) in single_unique else "}o"
        optional = all(nullable.get((f["from"], c), False) for c in f["cols"])
        one = "o|" if optional else "||"
        return f'  {f["from"]} {many}--{one} {f["to"]} : "{", ".join(f["cols"])}"'

    sections, toc = [], []
    for i, (title, blurb, ts) in enumerate(areas):
        mine = set(ts)
        rels = [f for f in fks if f["from"] in mine or f["to"] in mine]
        stubs = sorted({t for f in rels for t in (f["from"], f["to"])} - mine)
        body = ["erDiagram"] + [entity(t) for t in ts]
        body += [f"  {t} {{\n    other_area see_its_section\n  }}" for t in stubs]
        body += [relation(f) for f in rels]
        anchor = f"area-{i + 1}"
        toc.append(f'<li><a href="#{anchor}">{escape(title)}</a> <span>{len(ts)} tables</span></li>')
        stub_note = (f'<p class="stubs">Also shown, from other areas: {", ".join(escape(s) for s in stubs)}.</p>'
                     if stubs else "")
        sections.append(
            f'<section id="{anchor}"><h2>{escape(title)}</h2><p>{escape(blurb)}</p>'
            f'<p class="tables">{", ".join(f"<code>{escape(t)}</code>" for t in ts)}</p>{stub_note}'
            f'<div class="panel"><pre class="mermaid">\n{escape(chr(10).join(body))}\n</pre></div></section>')

    return PAGE.format(
        version=schema["version"], ntables=len(tables), nfks=len(fks), today=date.today().isoformat(),
        toc="\n".join(toc), sections="\n".join(sections))


PAGE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>B-Edge Database Map</title>
<style>
  :root {{ --bg:#FAFAFA; --surface:#FFFFFF; --line:#E4E4E7; --ink:#0A0A0A; --ink-2:#3F3F46; --muted:#71717A; color-scheme: light; }}
  @media (prefers-color-scheme: dark) {{
    :root:not([data-theme="light"]) {{ --bg:#121214; --surface:#1A1A1D; --line:#34343A; --ink:#FAFAFA; --ink-2:#D4D4D8; --muted:#A5A5AF; color-scheme: dark; }}
  }}
  :root[data-theme="dark"] {{ --bg:#121214; --surface:#1A1A1D; --line:#34343A; --ink:#FAFAFA; --ink-2:#D4D4D8; --muted:#A5A5AF; color-scheme: dark; }}
  body {{ margin:0; background:var(--bg); color:var(--ink); font:15px/1.55 Inter, ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; }}
  main {{ max-width:1100px; margin:0 auto; padding:40px 16px 64px; display:grid; gap:44px; }}
  h1 {{ margin:0; font-size:clamp(26px,4vw,36px); letter-spacing:-0.02em; }}
  h2 {{ margin:0 0 6px; font-size:21px; letter-spacing:-0.01em; }}
  p {{ margin:0 0 8px; color:var(--ink-2); max-width:72ch; }}
  code {{ font:0.86em ui-monospace, "SF Mono", Menlo, monospace; background:var(--line); padding:1px 5px; border-radius:4px; color:var(--ink); }}
  .meta {{ color:var(--muted); font-size:13px; }}
  .toc {{ margin:0; padding:0; list-style:none; display:grid; grid-template-columns:repeat(auto-fit,minmax(240px,1fr)); gap:6px 18px; }}
  .toc li {{ border-top:1px solid var(--line); padding:8px 0; }}
  .toc span {{ color:var(--muted); font-size:13px; }}
  a {{ color:inherit; }}
  .stubs {{ font-size:13px; color:var(--muted); }}
  /* The diagrams stay on a light panel in both themes: mermaid draws its
     own colours, and a light panel keeps them legible on a dark page. */
  .panel {{ background:#FFFFFF; border:1px solid var(--line); border-radius:12px; padding:12px; overflow-x:auto; }}
  .panel pre {{ margin:0; }}
  .key {{ display:grid; gap:4px; font-size:14px; color:var(--ink-2); }}
</style>
</head>
<body>
<main>
<header>
  <p class="meta">Generated by <code>scripts/erd.py</code> from the live schema, migration {version}, on {today}. Do not edit by hand: run the script after a migration and commit the result.</p>
  <h1>B-Edge database map</h1>
  <p>{ntables} tables and {nfks} links between them, one diagram per area.</p>
</header>
<section class="key" aria-label="How to read the diagrams">
  <h2>How to read the diagrams</h2>
  <div>Each box is a table with its columns. <code>PK</code> is the primary key, <code>FK</code> points to another table, <code>UK</code> must be unique.</div>
  <div>A line runs from the table holding the link to the table it points at, labelled with the linking column. The crow's foot is the "many" side; a circle means that side is optional.</div>
  <div>Boxes marked <code>other_area</code> belong to a different area and are shown only so their links stay visible.</div>
</section>
<nav aria-label="Areas"><ul class="toc">
{toc}
</ul></nav>
{sections}
</main>
<script src="https://cdnjs.cloudflare.com/ajax/libs/mermaid/10.9.1/mermaid.min.js"></script>
<script>
  mermaid.initialize({{ startOnLoad: true, theme: "neutral", er: {{ useMaxWidth: false }}, securityLevel: "strict" }});
</script>
</body>
</html>
"""


if __name__ == "__main__":
    html = build(read_schema())
    OUT.write_text(html, encoding="utf-8")
    print(f"wrote {OUT.relative_to(ROOT)}")
