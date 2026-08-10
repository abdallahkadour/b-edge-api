# B-Edge — Documentation Index

> 42 active documents. Read `CLAUDE-v5.md` first in any new chat — it supersedes all earlier CLAUDE.md versions.
> Last updated: August 7, 2026 · Schema v13 · 9 domains live · Gap analysis complete against locked specs.

---

## ⚠️ Before anything else — two conflicts to resolve

1. **`B-Edge-Product-Roadmap.docx` and `B-Edge-PRD-v7-Final.docx` disagree** on Product Store (Phase 3 vs. Phase 1) and Waitlist (Phase 2 vs. Phase 1). See `B-Edge-Targets-vs-Actual-Analysis-v1.md` for the full breakdown. PRD v7 is later and claims to be the reviewed/final version — worth explicitly deciding it wins, and updating the Roadmap doc to match.
2. **Two files referenced in earlier sessions weren't found in the current upload set:** `B-Edge-Angular-PWA-Architecture-v1.docx` and `B-Edge-Competitor-Analysis-v1.docx`. Worth re-uploading if they still matter.

---

## Core Product

| File | Description |
|---|---|
| `B-Edge-PRD-v7-Final.docx` | Product requirements — every business rule, service catalogue, booking flow, deposit policy, notification events, product store, features roadmap. Claims "Final," reviewed against 3 AI models, 34 gaps resolved. **Conflicts with Product-Roadmap.docx on 2 phase assignments — see above.** |
| `B-Edge-BRD.docx` | Business requirements — market context, revenue model, platform overview, customer and artist flows. |
| `B-Edge-Product-Roadmap.docx` | Phase 1→4 feature roadmap with timeline. **Older than PRD v7 — 2 conflicts, see above.** |
| `B-Edge-Booking-Scenarios.docx` | Complex booking edge cases pre-solved: multi-person, cross-city, home visit, outside Lebanon, processing gaps. |

---

## Technical Design

| File | Description |
|---|---|
| `B-Edge-Technical-Decisions-v1.docx` | 30 validated decisions, 11 bugs pre-solved, 7 migration rules — the engineering bible. |
| `B-Edge-HLD.docx` | High level design — system architecture, component responsibilities, key flows. |
| `B-Edge-LLD-v2-Go.docx` | Low level design (Go stack) — folder structure, handler/service/repository pattern. |
| `B-Edge-Booking-Domain-Spec-v1.docx` | Booking state machine, transitions, two-step hold→submit, deposit deadlines, cancellation policy. **Describes a two-tap deposit-confirm flow that the actual build deliberately collapsed to one tap — see gap analysis.** |
| `B-Edge-Booking-Domain-Visual.html` | Booking state diagram — visual flowchart of all transitions. |
| `B-Edge-Backend-Reality-Check-v1.md` | Schema audit (June 2026) — what the DB actually had vs. what screens needed at that point. Historical, but useful for understanding why certain migrations exist. |
| `B-Edge-API-Reference-v1.docx` | Endpoint reference as of schema v8 (June 26) — now stale; schema is v13. Useful as a reference pattern, not current truth. |
| `B-Edge-API-Contract-v1.docx` | Go ↔ Angular contract — response envelope, HTTP status rules, error codes, pagination format. |
| `B-Edge-Auth-API-Docs.docx` | Auth endpoints — register, login, refresh, logout, forgot-password, reset-password, freeze, delete. |
| `B-Edge-Diagrams.html` | Architecture diagrams, booking flow, notification flow, database ERD. |
| `B-Edge-ERD.html` | Database entity-relationship diagram. |
| `B-Edge-INFRA.docx` | Infrastructure design — EC2, Docker, Kubernetes, PostgreSQL, deployment topology. |
| `B-Edge-Slot-Algorithm-Spec-v1.docx` | Full slot availability algorithm as Go pseudocode. **Only the travel-buffer step has been cross-checked against real code so far (confirmed exact match) — the rest of the 7-step algorithm not yet verified.** |

---

## Frontend Design & Screens

| File | Description |
|---|---|
| `B-Edge-UI-Spec-v2.md` | Screen inventory (40 screens: 19 customer PWA, 21 artist dashboard) with API dependency map. **Not yet cross-checked screen-by-screen against what's actually built (~15+ screens across both apps as of tonight).** |
| `b-edge-8-missing-screens.md` | Stitch prompts — customer/artist password reset, booking detail, cancel modal, block dates. Several of these screens are now built (Block Dates still isn't). |
| `b-edge-12-missing-screens.md` | Stitch prompts — Discover, register/login, my bookings, leave a review, PWA install, Deposit Queue, Refund Queue, client CRM, earnings, portfolio. Most of these are now built; Refund Queue and Onboarding are not. |
| `b-edge-remaining-screens.md` | Stitch prompts — error states, booking lookup, artist booking detail, onboarding step 1. |

---

## Testing, Quality & Gap Analysis

| File | Description |
|---|---|
| `B-Edge-Test-Strategy-v1.docx` | Unit vs integration tests, coverage targets, CI config. |
| **`B-Edge-Targets-vs-Actual-Analysis-v1.md`** | **NEW.** Verified gap analysis comparing PRD v7 / Booking Domain Spec / Technical Decisions against actual current code (not just docs or memory). Confirms exact-match wins (travel buffer, early-bird cutoffs), the deliberate state-machine deviation, and every real Phase-1-per-PRD gap (waitlist, product store, rescheduling, home-visit bookings, real discount system, SMS fallback, admin dashboard, Arabic RTL). **Read this alongside CLAUDE-v5.md when starting new work.** |
| `CLAUDE-v5.md` | **CURRENT context document.** Supersedes v4. Covers all 9 live domains, both frontend apps, every real bug fixed with root cause, the gap-analysis findings, parked decisions, key live IDs. Read this first in any new chat. |
| `CLAUDE-v4.md` | Superseded by v5. Kept for reference — was accurate as of June 26 (6 domains, schema v8). |
| `CLAUDE-v3.md`, `CLAUDE.md`, `CLAUDE__1_.md`, `CLAUDE__4_.md` | Older historical iterations, all superseded. Worth archiving out of the active docs folder at some point — five CLAUDE.md variants in one place is its own source of confusion. |

---

## Infrastructure and Operations

| File | Description |
|---|---|
| `B-Edge-DevOps-Infrastructure-v1.docx` | Infrastructure gaps + fixes, production checklist, disaster recovery scenarios, backup config, monitoring stack. |
| **`B-Edge-Command-Reference.md`** | **NEW.** Every command used across the whole project, organized by purpose (auth, migrations, booking lifecycle, services, handles, discovery, reviews, calendar, client CRM, direct DB queries, frontend build/serve, deployment pattern, diagnostics) rather than raw chronological order — built to be searchable when asked for a specific command later. Supersedes the original repo-setup-only `commands.txt` upload (Session 1–3 content preserved at the top) and overlaps with/extends `B-Edge-Session-Commands.md` below. |
| `B-Edge-WhatsApp-API-Templates-v1.docx` | Twilio vs Meta Cloud API comparison, Lebanon pricing, all 16 notification templates (EN + AR). **Not yet checked whether the 4 messages built tonight match the specced wording — worth a pass.** |
| `B-Edge-Rania-Onboarding-Runbook-v1.docx` | Pre-launch checklist, Rania account setup, go-live day protocol. |
| `B-Edge-Session-Commands.md` | Terminal command reference from the discovery+client-CRM session (June 2026) — narrower scope, now folded into `B-Edge-Command-Reference.md` above. Kept for historical detail. |
| `README.md` | Repo-level README — setup instructions, Makefile commands, architecture overview. Describes 27 documents and Go 1.26 — both now stale (41 docs, Go 1.22+ per CLAUDE-v5). |

---

## Business and Market

| File | Description |
|---|---|
| `B-Edge-Pricing-Strategy-v1.docx` | Competitor pricing analysis, B-Edge pricing model. |
| `B-Edge-Lebanese-Market-GTM-v1.docx` | Market sizing, GTM phases. |

---

## Competitor Intelligence

| File | Description |
|---|---|
| `B-Edge-Competitor-Architecture-v1.docx` | Code structure, deployment architecture, engineering practices per competitor. |
| `B-Edge-Competitor-Problems-v1.docx` | Documented bugs across competitors from verified reviews/complaints. |
| `B-Edge-Competitor-Flows-v1.docx` | Booking flows and user journeys competitors have built. |
| `B-Edge-Competitor-Implementation-v1.docx` | Implementation regrets and limitations per competitor. |
| `B-Edge-Competitor-Technical-v1.docx` | Confirmed schema/stack details per competitor. |
| `B-Edge-Competitor-Failures-v1.docx` | 8 failed competitors and the failure patterns behind them. |
| *(`B-Edge-Competitor-Analysis-v1.docx` referenced but not currently on disk — see note at top)* | |

---

## Development Reference

| File | Description |
|---|---|
| `DOCUMENTATION.md` | This file. |

---

## Summary by use case

**Starting a new chat about B-Edge?**
1. Read `CLAUDE-v5.md` (current context, all 9 domains, both frontend apps)
2. Read `B-Edge-Targets-vs-Actual-Analysis-v1.md` (what's deliberately deviated from spec, and what Phase-1 items are genuinely unbuilt)

**Building a frontend screen?**
1. Check `B-Edge-UI-Spec-v2.md` and the three Stitch-prompt docs for the design
2. Cross-check against `CLAUDE-v5.md`'s screen lists — many are already built

**Deciding what to build next?**
1. `B-Edge-Targets-vs-Actual-Analysis-v1.md` Part 2 has the full verified gap list, all tagged Phase 1 per the PRD
2. Resolve the PRD-vs-Roadmap conflict first — it changes what "next" even means for Product Store and Waitlist

---

**Need a specific command?**
1. `B-Edge-Command-Reference.md` — organized by purpose (auth, booking lifecycle, services, handles, discovery, reviews, calendar, DB queries, frontend build/serve, diagnostics), not raw chronological order

---

## File statistics

| Category | Count |
|---|---|
| Core Product | 4 |
| Technical Design | 12 |
| Frontend Design | 4 |
| Testing, Quality & Gap Analysis | 5 |
| Infrastructure & Ops | 6 |
| Business & Market | 2 |
| Competitor Intelligence | 6 (7th referenced, not on disk) |
| Development Reference | 1 |
| **TOTAL** | **42 (on disk) / 43 (referenced)** |

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · August 7, 2026*
