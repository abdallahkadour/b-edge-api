# B-Edge — Documentation Index

> 39 active documents. Every decision made. Every gap closed. Ready to build.
> Last updated: June 26, 2026 · Schema v8 · 6 domains live · All verifications passed.

---

## Core Product

| File | Description |
|---|---|
| `B-Edge-PRD-v7-Final.docx` | Product requirements — every business rule, service catalogue, booking flow, deposit policy, notification events. Locked. |
| `B-Edge-BRD.docx` | Business requirements — market context, revenue model, platform overview, customer and artist flows. v1.1. |
| `B-Edge-Product-Roadmap.docx` | Phase 1 (MVP) → Phase 2 (growth) → Phase 3 (MENA expansion) feature roadmap with timeline. |
| `B-Edge-Booking-Scenarios.docx` | Complex booking edge cases pre-solved: multi-person, cross-city, home visit, outside Lebanon, processing gaps. |

---

## Technical Design

| File | Description |
|---|---|
| `B-Edge-Technical-Decisions-v1.docx` | 30 validated decisions, 11 bugs pre-solved, 7 migration rules — the engineering bible before line one. |
| `B-Edge-HLD.docx` | High level design — system architecture, component responsibilities, key flows, data model overview. v1.1 Go stack. |
| `B-Edge-LLD-v2-Go.docx` | Low level design (Go stack) — folder structure, handler/service/repository pattern, middleware, Go types, validation rules. |
| `B-Edge-Booking-Domain-Spec-v1.docx` | Booking state machine, transitions, validations, two-step hold→submit, deposit deadlines, cancellation policy. |
| `B-Edge-Booking-Domain-Visual.html` | Booking state diagram — visual flowchart of all transitions (pending → confirmed → completed). |
| `B-Edge-Backend-Reality-Check-v1.md` | Schema audit — what the real database has vs. what the 40 screens need. Only 1 table + 2 columns genuinely missing. 3 migrations all that's required. **Read before building domains.** |
| `B-Edge-API-Reference-v1.docx` | Live endpoint reference — all 6 domains, 50 endpoints (auth, booking, artist, review, discovery, client). Method, path, auth, params, body, returns, errors per call. Built from the shipped handlers. Schema v8. |
| `B-Edge-API-Contract-v1.docx` | Go ↔ Angular contract — response envelope, HTTP status rules, 18 error codes in English and Arabic, pagination format, money/date conventions. |
| `B-Edge-Auth-API-Docs.docx` | Auth endpoints — register, login, refresh, logout, forgot-password, reset-password, freeze, delete. |
| `B-Edge-Diagrams.html` | Architecture diagrams, booking flow, notification flow, database ERD — open in browser. |
| `B-Edge-ERD.html` | Database entity-relationship diagram — all 17 tables, keys, constraints, relationships. |
| `B-Edge-INFRA.docx` | Infrastructure design — EC2, Docker, Kubernetes, PostgreSQL, deployment topology. v1.1 Go stack. |
| `B-Edge-Slot-Algorithm-Spec-v1.docx` | Full slot availability algorithm as Go pseudocode — 7 steps covering all edge cases: travel buffers, processing gaps, early bird, closing time, GIST constraint. |

---

## Frontend Design & Screens

| File | Description |
|---|---|
| `B-Edge-UI-Spec-v2.md` | Complete screen inventory (40 screens: 19 customer PWA, 21 artist dashboard). Screen-by-screen API dependency map with exact request/response shapes. Navigation flows. Pre-Angular build checklist. **Master reference for frontend.** |
| `B-Edge-Angular-PWA-Architecture-v1.docx` | Angular 21 workspace structure, two PWAs from one codebase, Arabic RTL implementation, service workers, state management with Signals. |
| `b-edge-8-missing-screens.md` | Stitch design prompts (ready to paste) — 8 screens: C-16/17/18/19 (customer password reset + booking detail + cancel modal), A-18/19/20/21 (artist password + change password + block dates). |
| `b-edge-12-missing-screens.md` | Stitch design prompts (ready to paste) — 12 screens: C-01/02/03/06/09/11/13/14 + PWA install prompt + A-07/11/12/13/14. Discovery, search, login, review, earnings, portfolio, CRM. |
| `b-edge-remaining-screens.md` | Stitch design prompts (ready to paste) — 7 error states + lookup + booking status screens. Complete coverage of all remaining UI. |

---

## Testing and Quality

| File | Description |
|---|---|
| `B-Edge-Test-Strategy-v1.docx` | Unit vs integration tests, test database setup, Go test patterns, coverage targets per domain, GitHub Actions CI config. |
| `CLAUDE-v4.md` | **CURRENT context document** — engineering rules, stack, current state (6 domains live, all verifications passed), key IDs, live results, pending work. Read this first in any new chat. Supersedes CLAUDE-v3.md. |
| `CLAUDE-v3.md` | Previous version (May 2026) — kept for reference. Superseded by CLAUDE-v4.md. |
| `CLAUDE.md` | Original context template (pre-Go rewrite) — kept for reference. Superseded by CLAUDE-v4.md. |

---

## Infrastructure and Operations

| File | Description |
|---|---|
| `B-Edge-DevOps-Infrastructure-v1.docx` | 13 infrastructure gaps with exact fixes, 20-item production checklist, 6 disaster recovery scenarios, WAL-G backup config, monitoring stack. |
| `B-Edge-WhatsApp-API-Templates-v1.docx` | Twilio vs Meta Cloud API comparison, Lebanon pricing (~$0.012/message), all 16 notification templates in English and Arabic ready to submit to Meta. |
| `B-Edge-Rania-Onboarding-Runbook-v1.docx` | Pre-launch checklist, Rania account setup steps, 12-item walkthrough session script, go-live day protocol, support response times. |
| `B-Edge-Session-Commands.md` | Every terminal command used during development — `docker exec` SQL, `curl` API tests, migration checks. Live command reference for this session. |

---

## Business and Market

| File | Description |
|---|---|
| `B-Edge-Pricing-Strategy-v1.docx` | Competitor pricing analysis (Fresha $14.95/staff, Booksy $20/staff), Lebanese market context, B-Edge model: free solo + $19/staff Studio. |
| `B-Edge-Lebanese-Market-GTM-v1.docx` | 1,003 salons in Lebanon (April 2026 data), real competitor is Instagram DMs, three-phase GTM: Rania Effect → Beauty Network → Open Platform. |

---

## Competitor Intelligence

| File | Description |
|---|---|
| `B-Edge-Competitor-Analysis-v1.docx` | Fresha, DINGG, Zenoti — full feature comparison, strengths, weaknesses, market position. |
| `B-Edge-Competitor-Problems-v1.docx` | Every documented bug across all three competitors from verified user reviews and BBB complaints. |
| `B-Edge-Competitor-Flows-v1.docx` | Every booking flow, use case, and user journey competitors have built. |
| `B-Edge-Competitor-Implementation-v1.docx` | Implementation regrets, limitations, and what each competitor would do differently. |
| `B-Edge-Competitor-Technical-v1.docx` | Fresha Snowflake schema (confirmed column names), Zenoti stack from official job postings, API architectures. |
| `B-Edge-Competitor-Architecture-v1.docx` | Code structure, deployment architecture, engineering practices, and programming problems each competitor faced. |
| `B-Edge-Competitor-Failures-v1.docx` | 8 failed competitors — Vaniday, Glosslab, StyleSeat, Mindbody, Booker, Schedulicity, Treatwell, ClassPass — why they died and the 8 universal failure patterns. |

---

## Development Reference

| File | Description |
|---|---|
| `DOCUMENTATION.md` | This file — the index of all 39 documents. |

---

## Summary by Use Case

### **Starting a new chat about B-Edge backend?**
1. Read `CLAUDE-v4.md` (current context, 6 domains live, verification results)
2. Refer to `B-Edge-Backend-Reality-Check-v1.md` (what the schema actually has)
3. Check `B-Edge-LLD-v2-Go.docx` (code patterns) and `B-Edge-API-Reference-v1.docx` (endpoints)

### **Building a frontend screen?** 
1. Find the screen in `B-Edge-UI-Spec-v2.md` (master reference)
2. Check API dependency in the spec (which endpoint it calls)
3. Verify endpoint exists in `B-Edge-API-Reference-v1.docx`
4. If missing, check `b-edge-8-missing-screens.md`, `b-edge-12-missing-screens.md`, or `b-edge-remaining-screens.md` for Stitch prompts

### **Understanding the booking flow?**
1. Read `B-Edge-Booking-Domain-Spec-v1.docx` (state machine)
2. View `B-Edge-Booking-Domain-Visual.html` (diagram)
3. Check `B-Edge-Backend-Reality-Check-v1.md` → "PART 3 — booking domain" (code status)

### **Deploying to production?**
1. `B-Edge-INFRA.docx` (infrastructure design)
2. `B-Edge-DevOps-Infrastructure-v1.docx` (checklist + fixes)
3. `B-Edge-Rania-Onboarding-Runbook-v1.docx` (launch protocol)

### **Implementing a notification template?**
1. `B-Edge-WhatsApp-API-Templates-v1.docx` (all 16 templates, English + Arabic)

### **Running a development session?**
1. `B-Edge-Session-Commands.md` (docker exec SQL, curl API tests)
2. `CLAUDE-v4.md` (key IDs: artist, store, service UUIDs)

---

## File Statistics

| Category | Count |
|---|---|
| Core Product | 4 |
| Technical Design | 13 |
| Frontend Design | 5 |
| Testing & Quality | 4 |
| Infrastructure & Ops | 4 |
| Business & Market | 2 |
| Competitor Intelligence | 7 | 
| Development Reference | 1 |
| **TOTAL** | **39** |

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · June 26, 2026*
*All critical verifications passed · 6 domains live (auth, booking, artist, review, discovery, client) · Schema v8 · Ready for Earnings + Media domains*