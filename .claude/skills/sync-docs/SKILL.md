---
name: sync-docs
description: >
  Use this skill to bring B-Edge's documentation back in line with the code —
  after a feature lands, before a release, when the user asks to "update the
  docs", "check the docs", "update the README", "update the help pages", or
  whenever `./scripts/check-docs.sh` reports drift. It covers all four doc
  surfaces at once: `project-docs/` (including DOCUMENTATION.md, the index),
  both READMEs, the in-app help guides (`customer-guide.ts`,
  `artist-guide.ts`, `admin-guide.ts`), and the generated swagger spec. It
  spans BOTH repos — `b-edge-api` and `b-edge-web` — because the index
  documents both. Use it when documentation should be updated, not to write a
  new standalone design document.
---

# Sync the documentation to the code

## The problem this solves

Docs here do not rot through carelessness. They rot because **nothing fails
when they go stale**. A migration lands, the build is green, tests pass, the
PR merges — and `DOCUMENTATION.md` still says 33 migrations. Ten of those in a
row is how the index came to be a fortnight behind while every individual
commit was fine.

So the work is not "read everything and look for problems". It is: run the
detector, then apply judgement only where judgement is actually required.

## Step 1 — Run the detector first, always

```bash
cd b-edge-api && ./scripts/check-docs.sh
```

It reports two different kinds of wrong, and they need different work:

1. **Counted claims** — "43 migrations", "825 Go tests". Mechanical. Recomputed
   from the code and compared to `project-docs/doc-facts.baseline`. No
   judgement: if it drifted, the number in the doc is simply wrong.
2. **Implicated surfaces** — changed paths mapped to documents that have a
   standing relationship with them. **This half over-reports on purpose.** It
   flags `customer-guide.ts` for any change under `customer-pwa/features/`,
   including a pure CSS fix that needs no help text at all.

**Do not treat section 2 as a to-do list.** It is a list of places to *look*.
Deciding that a flagged document needs no change is a correct outcome — say so
explicitly in the report rather than silently skipping it.

## Step 2 — Fix counted claims

Two places hold these numbers, and updating one without the other is the most
common mistake:

- **`project-docs/doc-facts.baseline`** — machine-written. Regenerate at the
  end (Step 6), never hand-edit.
- **The "Verified against code" block in `DOCUMENTATION.md`** — the sentence a
  human actually reads and trusts. Update the numbers *and* the date.

The block is a snapshot with a date on it, not a running total. Rewrite it
wholesale rather than patching individual figures, and leave the older dated
lines above it alone — that history is deliberate, and several docs are
readable only against the state they were written in.

## Step 3 — Decide what each implicated surface actually needs

Ask one question per flagged document: **has anything a reader relies on
changed?**

| Change | Docs it genuinely affects | Docs it usually does NOT |
|---|---|---|
| New migration | `DOCUMENTATION.md` schema version + table count; `B-Edge-ERD.html` | help guides — customers do not read schemas |
| New/changed route | `make swagger`; `E2E-TEST-PLAN.md` | READMEs, unless the app cannot start without it |
| New leaf package | `CLAUDE.md` leaf-package list | help guides |
| New env var | both READMEs; `B-Edge-Deployment-Runbook-v1.md` | help guides |
| New user-visible screen or flow | the matching help guide | `CLAUDE.md` |
| Styling / refactor / test-only | **nothing** | everything |

The last row matters most. A refactor that changes no behaviour needs no
documentation change, and adding one is worse than leaving it — it implies to
the next reader that something about the product moved.

## Step 4 — Help pages, specifically

The three guides are **typed TypeScript data, not markdown**:
`GuideSection[]`, each with `GuideTopic[]` of
`{ id, title, summary, steps, notes? }` (`projects/shared/src/lib/help/model.ts`).
They are compiled, so a malformed edit **breaks the build** — that is a feature,
and it means you must build after editing (Step 5).

Match the established voice exactly:

- Second person, plain, instructional. No jargon, no marketing.
- **Bold** every literal UI label as it appears on screen — `**Select
  location**`, `**Booking request sent!**`. This is what makes a guide
  followable, and it is also what makes it *checkable*: if the label changed,
  the bold text is now a lie.
- `steps` are ordered actions the reader performs.
- `notes` are things the reader could not guess and would otherwise get wrong
  — "A booking is a request, not a confirmed appointment."

Which guide:

- `customer-pwa/.../help/customer-guide.ts` — customers
- `artist-dashboard/.../help/artist-guide.ts` — artists
- `artist-dashboard/.../help/admin-guide.ts` — the single admin account

**Add a topic only for something a user does.** A new screen, a new step, a
changed label, a behaviour that will surprise someone. Not for performance
work, not for internal structure. And when a UI label changes, the fix is
usually to correct existing bold text rather than to add a topic — search the
guides for the old label before writing anything new.

## Step 5 — Verify before claiming done

Documentation changes can break builds, and a guide edit is code:

```bash
# if any help guide changed
cd b-edge-web && npx ng build shared && npx ng build customer-pwa && npx ng build artist-dashboard

# if any route or annotation changed
cd b-edge-api && make swagger && go build ./... && go test ./...
```

`make swagger` matters even though **`docs/` is gitignored** (`.gitignore:12`)
so there is no diff to commit: the generated spec is what the annotations are
checked against, and a stale `@Success` type is invisible until someone reads
the wrong contract.

## Step 6 — Record the sync

```bash
cd b-edge-api
./scripts/doc-facts.sh > project-docs/doc-facts.baseline
./scripts/check-docs.sh          # must now exit 0
```

Then **commit the baseline with the change**. Committing it *is* the sync
record: the checker asks git which commit last touched that file and uses it
as the starting point for the next run. There is no separate bookkeeping to
keep in step — an earlier design stored the commit hash inside the file,
which cannot work, because a commit's own hash does not exist until after
the commit does, and `--amend` invalidated it outright.

Re-running the checker is the actual proof. If it still reports drift, the
work is not finished — do not report success on an unverified sync.

## Hard rules

- **Never write documentation into `b-edge-api/docs/`.** It is gitignored
  generated swagger output; anything put there is silently lost. Hand-written
  docs go in `project-docs/`. This has been got wrong before.
- **Never invent a fact to fill a gap.** If a number, behaviour or limit is not
  verifiable from the code in front of you, measure it or leave it out. The
  index's value is that its claims have been checked.
- **Preserve dated history.** Older "Previously updated" lines and
  stale-marked documents are deliberate context, not clutter to tidy.
- **A migration's header is documentation.** If one lacks a prose *why*, that
  is a finding worth raising — migrations 010, 016, 029 and 031 are the
  standard.
- **Report what you decided not to change, and why.** A flagged document left
  alone with a one-line reason is a complete answer. A flagged document that
  silently vanishes from the report is not.
