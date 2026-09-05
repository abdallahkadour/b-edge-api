# 01 — Standards & Efficiency

**2026-09-06.** Reinvented wheels, idiom, dead code.

## Scope & files analysed

`internal/**` (24,659 lines of Go, excluding tests), `projects/**` (Angular 21
monorepo). Go 1.26.3, so the full generic stdlib (`slices`, `maps`, `cmp`) is
available.

---

## Headline: this axis is unusually clean

Searched for the standard offenders and found almost nothing:

| Anti-pattern | Instances |
|---|---|
| Hand-rolled retry / backoff loops | **0** |
| `time.Sleep` in request paths | **0** |
| Hand-rolled string utilities | **1** (below) |
| Hand-rolled slice search | **1** |
| Custom date arithmetic replacing stdlib | **0** |

Money is `shopspring/decimal`, never `float64`. UUIDs are `google/uuid`.
Validation is `go-playground/validator`. Postgres is `pgx` directly rather than
an ORM — a deliberate choice that avoids the entire class of ORM-generated
query problems.

The findings below are therefore few, but two of them are real and one is
**self-inflicted by a refactor two days ago**.

---

## F1 — 106 lines of dead code across 7 files · **P1**

`validationMessage` is defined in seven domains and **called from nowhere**.

```
booking/service.go        19463658…
product/service.go        57cb814f…   ← all seven
customerauth/service.go   d8e7e8b2…      hashes differ:
review/service.go         cf634aaf…      they had already
domain/auth/service.go    bed33e99…      drifted apart
billing/service.go        e581c444…
media/service.go          912b44e0…
```

**Provenance matters here.** `git show c37ee2f~1` shows 2 live call sites in
`booking` before that commit and 0 after. The consolidation of
`mapValidationError` into `internal/pkg/validation` (commit `c37ee2f`,
2026-09-05) removed the callers and left the callees. That refactor was mine,
and the cleanup was missed.

**Fix:** delete all seven. No behaviour change — they are unreachable.

```bash
# verification before deleting
grep -rn "validationMessage(" --include="*.go" internal/ | grep -v "func validationMessage"
# → must print nothing
```

---

## F2 — `money.upper()` reimplements `strings.ToUpper` · **P2**

`internal/pkg/money/money.go:118`

```go
// upper uppercases an ASCII field name for the error code. strings.ToUpper
// would do, but field names here are ASCII snake_case by construction and
// this keeps the package's only import list to decimal and apperror.
func upper(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}
```

The stated justification is *factually correct* — the file genuinely does not
import `strings` — and it is still a **bad trade**. A stdlib import costs
nothing at runtime, and the hand-rolled version is ASCII-only, so it silently
does the wrong thing the moment a field name is not pure ASCII. Written
yesterday, in this codebase, by this reviewer.

```diff
-func upper(s string) string {
-	out := []byte(s)
-	for i, c := range out {
-		if c >= 'a' && c <= 'z' {
-			out[i] = c - 32
-		}
-	}
-	return string(out)
-}
+// (deleted — use strings.ToUpper at the call site)

 func invalid(field string) error {
 	return apperror.BadRequest(
-		"INVALID_"+upper(field),
+		"INVALID_"+strings.ToUpper(field),
 		field+" must be an amount with at most 2 decimal places, between 0 and 99999999.99",
 	)
 }
```

**Rule this implies:** "avoiding an import" is never a reason to reimplement
stdlib. Worth adding to `CLAUDE.md` conventions.

---

## F3 — `internal/pkg/money` and `money.util.ts` are a hand-maintained pair · **P2, accepted**

The same regex exists twice, once per language, deliberately — a client-side
check for UX and a server-side check for correctness. The risk is drift, and
it is **already mitigated**: `money.util.spec.ts` mirrors
`money_test.go` case-for-case.

Recorded here rather than "fixed" because the alternatives are worse. Shipping
the rule as a shared WASM/JSON artefact for one regex would be
over-engineering, and removing the client check would return a 400 for an
ordinary typo.

---

## F4 — `strPtr` / `dec` test helpers duplicated 4× and 3× · **P3**

Test-only. `strPtr` in four packages, `dec` in three. Harmless, in-package,
and the house style explicitly favours hand-written per-package test scaffolding
over a shared test library. **Leave alone** — consolidating would create an
import from every test package into a shared one for two lines each.

---

## F5 — Angular: idiom is current, one gap · **P2**

Signals in 43 files, `computed` in 28, `OnPush` in 51, all routes lazy. This
is modern Angular, not Angular-2-era code.

The gap is `@angular/cdk` — it is a dependency and is used in 9 files, but
several hand-rolled behaviours duplicate what it already provides. See
`05-ui-design-system-overhaul.md` F3.

---

## Recommended action

| # | Action | Priority |
|---|---|---|
| F1 | Delete 7 dead `validationMessage` functions | **P1** |
| F2 | Replace `money.upper` with `strings.ToUpper` | P2 |
| F5 | Adopt CDK primitives where hand-rolled (see agent 05) | P2 |
| F3 | Keep the paired validators; keep the mirrored tests | accepted |
| F4 | Leave test helpers duplicated | no action |
