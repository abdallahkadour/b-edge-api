# B-Edge — Documentation Index

> 31 active documents. Every decision made. Every gap closed. Ready to build.

---

## Core Product

| File | Description |
|---|---|
| `B-Edge-PRD-v7-Final.docx` | Product requirements — every business rule, service catalogue, booking flow, deposit policy, notification events. Locked. |
| `B-Edge-BRD.docx` | Business requirements — market context, revenue model, platform overview, customer and artist flows. v1.1. |
| `B-Edge-Product-Roadmap.docx` | Phase 1 (MVP) → Phase 2 (growth) → Phase 3 (MENA expansion) feature roadmap. |
| `B-Edge-Booking-Scenarios.docx` | Complex booking edge cases pre-solved: multi-person, cross-city, home visit, outside Lebanon, processing gaps. |

---

## Technical Design

| File | Description |
|---|---|
| `B-Edge-Technical-Decisions-v1.docx` | 30 validated decisions, 11 bugs pre-solved, 7 migration rules — the engineering bible before line one. |
| `B-Edge-HLD.docx` | High level design — system architecture, component responsibilities, key flows, data model overview. v1.1 Go stack. |
| `B-Edge-LLD-v2-Go.docx` | Low level design (Go stack) — folder structure, handler/service/repository pattern, middleware, Go types, validation rules. |
| `B-Edge-Auth-API-Docs.docx` | Auth endpoints — register, login, refresh, logout, forgot-password, reset-password, freeze, delete. |
| `B-Edge-Diagrams.html` | Architecture diagrams, booking flow, notification flow, database ERD — open in browser. |
| `B-Edge-INFRA.docx` | Infrastructure design — EC2, Docker, Kubernetes, PostgreSQL, deployment topology. v1.1 Go stack. |
| `B-Edge-Backend-Reality-Check-v1.md` | Schema audit — what the real database already has vs. what the 40 screens need. Only 1 table + 2 columns genuinely missing. 3 migrations all that's required. |

---

## Algorithm and API

| File | Description |
|---|---|
| `B-Edge-Slot-Algorithm-Spec-v1.docx` | Full slot availability algorithm as Go pseudocode — 7 steps covering all edge cases: travel buffers, processing gaps, early bird, closing time, GIST constraint. |
| `B-Edge-API-Contract-v1.docx` | Go ↔ Angular contract — response envelope, HTTP status rules, 18 error codes in English and Arabic, pagination format, money/date conventions. |

---

## Frontend

| File | Description |
|---|---|
| `B-Edge-Angular-PWA-Architecture-v1.docx` | Angular 21 workspace structure, two PWAs from one codebase, Arabic RTL implementation, service workers, state management with Signals. |
| `B-Edge-UI-Spec-v2.md` | Complete screen inventory (40 screens: 19 customer PWA, 21 artist dashboard). Screen-by-screen API dependency map with exact request/response shapes. Navigation flows. Pre-Angular build checklist. |
| `b-edge-8-missing-screens.md` | Stitch design prompts ready to paste — 8 missing screens: C-16/17/18/19 (customer password reset + booking detail + cancel modal), A-18/19/20/21 (artist password + change password + block dates). |

---

## Testing and Quality

| File | Description |
|---|---|
| `B-Edge-Test-Strategy-v1.docx` | Unit vs integration tests, test database setup, Go test patterns, coverage targets per domain, GitHub Actions CI config. |
| `CLAUDE-v3.md` | Engineering rules enforced on every PR — Go doc comments, migrations, API endpoints (50+), all error codes, booking state machine, Go types, Angular rules, design tokens. Single source of truth. |

---

## Infrastructure and Operations

| File | Description |
|---|---|
| `B-Edge-DevOps-Infrastructure-v1.docx` | 13 infrastructure gaps with exact fixes, 20-item production checklist, 6 disaster recovery scenarios, WAL-G backup config, monitoring stack. |
| `B-Edge-WhatsApp-API-Templates-v1.docx` | Twilio vs Meta Cloud API comparison, Lebanon pricing (~$0.012/message), all 16 notification templates in English and Arabic ready to submit to Meta. |
| `B-Edge-Rania-Onboarding-Runbook-v1.docx` | Pre-launch checklist, Rania account setup steps, 12-item walkthrough session script, go-live day protocol, support response times. |

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
| `B-Edge-Commands.md` | Every terminal command used during development — explained, ordered, with Makefile reference and air hot reload flow. |

---

*B-Edge  ·  Beauty at the Edge  ·  الجمال عند الحافة  ·  June 2026*