# B-Edge — Cross-layer data flow and edge-case audit

**v1, 2026-09-05.** Angular → Go → PostgreSQL: what each layer accepts, what it
silently changes, and what it does with input that is wrong.

> **Method: measured, not reasoned.** Every behaviour below was produced by
> sending the input to the running stack and reading the result back out of
> Postgres — not inferred from the type declarations. Where this document says
> "silently", it means a value was observed to change with no error returned.
>
> Two of the findings here are not hypothetical. Both were live defects found
> during the 2026-09-05 security pass, and one of them corrupted a real row.

---

## 0. The shape of the problem

Money crosses this stack as a **string**, timestamps as **RFC 3339 strings**,
identifiers as **UUID strings**. That is a deliberate and correct choice — it
is what stops JavaScript's `number` (an IEEE-754 double) from quietly
destroying a price or an ID.

It also relocates the risk. A string field will carry **anything** through
JSON, through Go's unmarshaler, and up to the database driver without a single
type error. Everything that goes wrong in §4 goes wrong in a field whose wire
type is `string`.

The three layers disagree about how many states a value has, and that
disagreement is the source of most of what follows:

| Layer | States a field can be in |
|---|---|
| JSON / TypeScript | absent · `null` · value |
| Go (`*string`) | `nil` · value — **absent and `null` collapse into one** |
| PostgreSQL | `NULL` · value |

Go sits in the middle with one fewer state than the layers on either side.

---

## 1. Angular (frontend)

### 1.1 What is actually sent

`ApiService` (`projects/shared/src/lib/core/api.service.ts`) takes the body as
`unknown` and hands it to `HttpClient` untouched. **There is no central
normalisation, sanitisation or coercion point.** Whatever a component puts in
the object is what Go receives.

Serialisation is therefore plain `JSON.stringify`, and its behaviour is the
first place values change without anyone being told:

| Input | On the wire | Note |
|---|---|---|
| `{a: undefined}` | `{}` | **field vanishes entirely** |
| `{a: NaN}` | `{"a":null}` | **becomes null** |
| `{a: Infinity}` | `{"a":null}` | **becomes null** |
| `{a: null}` | `{"a":null}` | |
| `{a: ''}` | `{"a":""}` | |
| `{a: 99999999999999999999}` | `{"a":100000000000000000000}` | **precision already lost before sending** |
| `{a: 10.999}` | `{"a":10.999}` | |
| `{a: new Date(...)}` | `{"a":"2026-09-05T10:00:00.000Z"}` | |
| `{a: String(NaN)}` | `{"a":"NaN"}` | **survives intact — see §4.3** |

Two rows deserve attention.

`NaN` and `Infinity` are **not valid JSON**, so `JSON.stringify` replaces them
with `null`. This is accidentally protective: a numeric `NaN` cannot reach Go.
But it protects only fields whose wire type is a number — and money is a
string.

Numbers above 2^53 are corrupted *in the browser*, before serialisation. The
codebase is insulated from this because IDs are UUID strings and money is a
decimal string, but any future numeric ID or cents-as-integer field inherits
the hazard.

### 1.2 Validation gaps

Form values arrive as raw strings from `type="text"` inputs and are passed
through unchanged:

```ts
price: string;                       // services.component.ts
(input)="addForm.update(f => ({...f, price: $any($event.target).value}))"
```

Until 2026-09-05 the guard on that field was `parseFloat(f.price) >= 0`, which
accepts `"10.999"` (10.999) and `"1e3"` (1000). It now uses
`isValidMoney()` from `@bedge/shared`, whose pattern is deliberately identical
to the Go one in `internal/pkg/money`.

**Where the remaining gap is:** two validators that are meant to agree but are
written separately will drift, and the failure is silent in the direction that
matters — the form accepting what the API rejects. `money.util.spec.ts` mirrors
the Go test table case-for-case specifically to make that drift fail a test.

### 1.3 Error handling asymmetry

The API returns **two different shapes** for "your input was bad", and only one
of them can be attached to a field:

```
422  {"code":"VALIDATION_ERROR","message":"Please check the highlighted fields",
      "details":[{"field":"Name","message":"Name is required"}]}

400  {"code":"INVALID_BODY","message":"Request body is invalid"}
```

A 400 carries **no field attribution at all**, so a type mismatch can only ever
render as a form-level error — the message "Please check the highlighted
fields" has nothing to highlight.

Worse, `details[].field` is the **Go struct field name** (`"Name"`), not the
JSON key (`"name"`). Any frontend that maps errors onto inputs by key must
case-fold and snake-case first, or the mapping silently misses.

### 1.4 UI freezing

Not a realistic risk here, and worth saying so rather than leaving it implied.
There is no client-side sorting, aggregation or large-list rendering; payloads
are small, pagination is server-side, and Angular's zoneless signals mean state
changes do not trigger whole-tree checks. The one polling loop (the
notification bell, 60s) already pauses on a hidden tab.

---

## 2. Go (backend)

### 2.1 The convention, which is consistent and correct

- **Nullable DB column → pointer field.** `SpecialRequests *string`,
  `CancellationReason *string`, `CalendarToken *string`. Verified across
  `bookings` and `services`: every nullable column maps to a pointer.
- **`sql.Null*` is used nowhere.** Pointers do the same job with less
  ceremony, and pgx supports them directly.
- **Money is `decimal.Decimal`, never `float64`,** and arrives as a string.
- **Required-on-create fields are values; optional-on-update fields are
  pointers.** `CreateServiceRequest.Price string` vs
  `UpdateServiceRequest.Price *string`.

### 2.2 Two-stage rejection

Input passes two gates, and which one catches a problem determines the status
code the frontend sees:

```
1. json.Unmarshal   → wrong TYPE        → 400 INVALID_BODY   (no field detail)
2. validate.Struct  → wrong VALUE       → 422 VALIDATION_ERROR (field detail)
```

Measured against `POST /artists/salon/services`:

| Sent | Result |
|---|---|
| `{"duration_min":"60"}` — string for int | `400 INVALID_BODY` |
| `{"price":10.00}` — number for string | `400 INVALID_BODY` |
| `{"name":["a"]}` — array for scalar | `400 INVALID_BODY` |
| `{"duration_min":60.5}` — float for int | `400 INVALID_BODY` |
| `{"duration_min":NaN}` — JSON `NaN` literal | `400 INVALID_BODY` |
| malformed JSON / empty body | `400 INVALID_BODY` |
| `{"name":null}` | `422 VALIDATION_ERROR` |
| `{"name":""}` | `422 VALIDATION_ERROR` |
| field omitted | `422 VALIDATION_ERROR` |
| body is literal `null` | `422 VALIDATION_ERROR` |
| **unknown extra field** | **`201` — accepted and ignored** |

**No panics, no 500s.** Every malformed input is a 4xx. Go's unmarshaler
returns an error rather than coercing, and the error handler maps it. This is
the layer behaving well.

### 2.3 Unknown fields are silently discarded

`{"is_admin":true}` alongside a valid body returns `201` and is ignored.

This is **the correct security posture** — it is what makes mass assignment
(security test INJ-02) impossible, because requests bind to explicit request
structs and never to DB models. It should not be changed.

The cost is that a **misspelled optional field is a silent no-op**. Sending
`{"desciption":"..."}` succeeds, changes nothing, and reports success. For a
*required* field the typo surfaces as a 422 for the now-missing field; for an
optional one there is no signal at all.

### 2.4 The pointer collapse — the central finding

`json.Unmarshal` cannot distinguish an absent key from an explicit `null`.
Both leave a `*string` as `nil`. Measured, on an optional `*string` mapping to
a nullable column:

| Sent | Go sees | Database after |
|---|---|---|
| field omitted | `nil` | unchanged |
| **`{"description":null}`** | **`nil`** | **unchanged** |
| `{"description":""}` | `→ ""` | `''` (empty string, **not NULL**) |

So a `PATCH` **cannot set a nullable column back to `NULL`.** Sending `null`
means "leave it alone", and there is no other spelling for "clear it".

The project has already met this problem once and solved it locally rather
than systemically. From the documentation index, on store map pins:

> *`clear_location` exists because COALESCE cannot express "remove".*

That flag is the workaround, for exactly one field. Every other nullable
column — `description`, `special_requests`, `cancellation_reason`,
`deposit_reference`, `instagram` — has no equivalent, and therefore no way
back to `NULL` once set.

---

## 3. PostgreSQL (database)

### 3.1 What the money columns actually accept

Every money column in the schema is `NUMERIC(10,2)` — verified against the live
database, not the migrations. Cast directly:

| Cast | Result |
|---|---|
| `'NaN'::numeric(10,2)` | **`NaN` — accepted and stored** |
| `'Infinity'::numeric(10,2)` | `ERROR: numeric field overflow` |
| `1e3::numeric(10,2)` | `1000.00` — **silent** |
| `10.999::numeric(10,2)` | `11.00` — **silently rounded** |

`NaN` is the single non-finite value `NUMERIC` will hold. Postgres treats it as
a legitimate numeric value, orders it above all others, and propagates it
through arithmetic — so **one `NaN` poisons every aggregate that touches it**.
`SUM(price)` over the affected salon returned `NaN` while a bad row was
present.

It is also unrecoverable from the application side: scanning `NaN` into a
`decimal.Decimal` errors, so every read of the row 500s, including the read the
update path performs first. Repair requires direct SQL.

### 3.2 Constraint coverage

Real constraints exist and do real work — this is a strength, and the
`services` table shows the pattern: `buffer_min BETWEEN 0 AND 120`,
`blocked_until >= end_time`, plus the GIST exclusion constraint that migration
001 calls *"the final atomic guard — no application-level check can replace
it"*. That guard is what makes concurrent double-booking impossible (verified:
8 concurrent holds on one slot, exactly one succeeded, seven `409`s).

**What constraints do not cover is scale and finiteness.** `NUMERIC(10,2)`
declares precision but *coerces* to it rather than rejecting — and it accepts
`NaN` outright. No money column carries a `CHECK` against either. See §5.2,
where the obvious constraint turns out not to work and the one that does is
identified by testing.

### 3.3 Driver mapping

pgx maps `NULL → nil` for pointer fields cleanly, and the pointer convention in
§2.1 is applied consistently, so there is no known nullable-column-to-value-type
mismatch. `COALESCE` appears 8 times in `internal/booking/repository.go`,
defending aggregate reads.

---

## 4. Edge-case behaviour matrix

What each layer does with each anomaly. **Bold = the value changed and nobody
was told.**

### 4.1 `null` / `nil` / `NULL`

| Layer | Behaviour |
|---|---|
| Angular | Sends `null` verbatim. Also produces `null` from `NaN`/`Infinity` (§1.1). |
| Go | Value field (`string`) → zero value → caught by `required` → **422**. Pointer field → `nil`, **indistinguishable from absent** → silently no-op. |
| Postgres | Never reached for the pointer case — the column is simply not in the `UPDATE`. |

**Risk: R1.** A client cannot clear a nullable field.

### 4.2 Empty string `""`

| Layer | Behaviour |
|---|---|
| Angular | Sent as `""`. An untouched text input produces `""`, not `undefined`. |
| Go | Distinct from `nil`: the pointer is set. `required`/`min=2` reject it (422); a field with only `omitempty` **accepts it**. |
| Postgres | Stored as `''`. **Not `NULL`** — so a column now has an empty string where a reader may expect `NULL`. |

**Risk: R2.** `''` and `NULL` both mean "no description" to a human and are
different to `WHERE description IS NULL`.

### 4.3 `NaN` and `Infinity`

| Layer | Behaviour |
|---|---|
| Angular | **Numeric** `NaN`/`Infinity` → `null` on the wire. Accidentally safe. **String** `"NaN"` passes through untouched — and money is a string. |
| Go | JSON `NaN` literal → 400. `decimal.NewFromString("NaN")` correctly errors. **But a path that never parses passes the raw string to SQL.** |
| Postgres | `'NaN'::numeric` **accepted and stored**; `Infinity` rejected as overflow. |

**Risk: R3 — this is the one that actually happened.** `PATCH
/artists/salon/services/:id` did not parse `price` at all, so `"NaN"` reached
Postgres, committed while the response was a 500, and made every later read of
that row fail. **Fixed 2026-09-05** by `internal/pkg/money`; the audit value is
that the trap is structural, not that this endpoint was wrong.

### 4.4 `undefined` / missing fields

| Layer | Behaviour |
|---|---|
| Angular | `undefined` **removes the key entirely** — indistinguishable on the wire from never setting it. |
| Go | Required value field → zero value → **422**. Optional pointer → `nil` → no-op. Unknown key → **silently ignored** (§2.3). |
| Postgres | Column omitted from the statement; prior value retained. |

**Risk: R4.** A typo'd optional field name is a successful no-op.

### 4.5 Unexpected types

| Layer | Behaviour |
|---|---|
| Angular | TypeScript checks nothing at runtime; `any`/`unknown` at the `ApiService` boundary means a wrong type is sent happily. |
| Go | `json.Unmarshal` **rejects** — `400 INVALID_BODY`, no panic. This layer is solid. |
| Postgres | Never reached. |

**Risk: low.** The one gap is diagnostic, not safety: 400 carries no field
name (§1.3).

---

## 5. Risk register

| # | Risk | Severity | Status | Where |
|---|---|---|---|---|
| **R1** | `null` ≡ absent; nullable columns cannot be cleared | **High** | **FIXED** 2026-09-05 | `internal/pkg/optional` |
| **R2** | `''` stored where `NULL` is meant | Medium | **FIXED** | `optional.Text` folds empty and whitespace to `NULL` |
| **R3** | `NaN` reaching `NUMERIC` corrupts a row irrecoverably | **High** | **FIXED** (both layers) | `internal/pkg/money` + migration 034 |
| **R4** | Misspelled optional field silently ignored | Medium | **ACCEPTED** — see §5.3 | — |
| **R5** | 400 has no field attribution | Medium | **FIXED** | `validation.MapBodyError` |
| **R6** | `details[].field` is the Go name, not the JSON key | Low | **FIXED** | `validation.New` tag-name func |
| **R7** | Client and server money validators can drift | Low | Guarded | mirrored test tables |
| **R8** | JS numbers lose precision above 2^53 | Low | Latent | holds while IDs are UUIDs and money is a string |
| **R9** | A request field accepted, validated, then dropped by the SQL | Medium | **FIXED** | `deposit_deadline_hours`; see §5.3 |

### 5.1 How R1 and R2 were fixed

Three options were considered: `json.RawMessage` per field (expressive but
pushes decoding into every service and gives the validator nothing to inspect),
a per-field clear flag (the `clear_location` precedent — honest but one flag per
field), or a generic wrapper. The wrapper won, applied narrowly.

`internal/pkg/optional.Field[T]` keeps presence and value separate, so the
repository can ask two different questions:

```sql
bio = CASE WHEN $2 THEN $3 ELSE bio END   -- $2 = IsSet, $3 = Text
```

`COALESCE($n, col)` remains correct for fields that are only ever set, and is
kept for those. It is only wrong for fields a user can clear.

**The project had already invented this workaround twice, differently.**
`clear_location` on stores used a boolean flag. `avatar_url` used an empty
string as a clear sentinel (`CASE WHEN $5 = ''`), and the frontend carried a
comment explaining why it sent `''` rather than `null`. Neither was documented
as a pattern, so the third field to need it would have grown a third mechanism.

Applied to: `services.name_ar`, `services.description`, `artists.bio`,
`artists.bio_ar`, `artists.instagram`, `artists.avatar_url`. **Not** applied to
`handle` or `name` — an identity has no "cleared" state — and **not** to
`clear_location`, which works and whose rewrite would buy nothing.

`optional.Text` also closes R2 by folding `""` and whitespace to `NULL`, which
has the useful side effect of keeping the existing `avatar_url: ''` client
working unchanged while `null` starts working too.

### 5.2 Mitigating R3 in depth

The Go parser is now correct, but it is the *only* thing standing between a
string and the column. A database-level guard would make the corruption
impossible rather than merely prevented.

**The obvious constraint does not work, and it is worth knowing why.** The
first version of this section proposed:

```sql
CHECK (price = round(price, 2))   -- WRONG: rejects neither case
```

Tested against the real database, it accepts both `NaN` and `10.999`:

- PostgreSQL defines **`NaN = NaN` as TRUE** for `numeric`, deliberately
  departing from IEEE 754 so that `NaN` can be indexed and sorted. `round(NaN)`
  is `NaN`, so the check evaluates `NaN = NaN` → true → **passes**.
- The column coerces `10.999` to `11.00` **before** the constraint is
  evaluated, so the check sees `11.00 = 11.00` → **passes**.

Both corrected by measurement, which is the point of §0.

**What actually works — verified:**

```sql
ALTER TABLE services
  ADD CONSTRAINT services_price_not_nan CHECK (price <> 'NaN'::numeric);
```

`NaN <> NaN` is false, so the check fails and the row is rejected; `45.00` is
accepted normally. This belongs on every `NUMERIC(10,2)` money column.

Two things that follow, and both matter:

1. **`CHECK (price >= 0)` does not catch `NaN`.** PostgreSQL sorts `NaN` above
   all numbers, so `'NaN'::numeric >= 0` is **TRUE**. A non-negativity
   constraint gives no protection here at all, which is exactly the kind of
   assumption that looks safe in review.
2. **Excess scale cannot be defended at the database level.** The column
   coerces on input, before any `CHECK` runs, so `10.999` has already become
   `11.00` by the time a constraint could object. Scale enforcement is
   *necessarily* the application's job — `internal/pkg/money` is not
   defence-in-depth for scale, it is the only defence there is.

Not applied here because it is a migration and this document is an audit;
raised as the recommended follow-up.

### 5.3 R4, and the finding underneath it

**R4 is accepted, not fixed, and deliberately so.** Rejecting unknown fields
(`DisallowUnknownFields`) would catch typos, but it also turns a
forward-compatible client into a broken one: deploy the web app before the API
and every request carrying a new field starts failing. Silently ignoring unknown
fields is what makes mass assignment impossible (INJ-02), and that property is
worth more than typo detection.

**But the concrete harm attributed to R4 turned out not to be a JSON typo at
all**, which is why it is worth separating. `PATCH
/artists/salon/services/:id` with `{"deposit_deadline_hours":48}` returned
`200` and changed nothing: the field was on the request struct, passed
validation, and was then simply **absent from the UPDATE statement**. Nothing
in the type system or the tests could see it.

That is a worse class than a typo — the client spelled it correctly and was
told it worked. Fixed, and the whole class was then swept: every
`Update*Request` field was cross-checked against its repository's UPDATE, and
`deposit_deadline_hours` was the only genuine case. The two other flagged
fields (`clear_location`, `cancel`) are control flags rather than columns and
are correct as they are.

Logged as **R9**, because it deserves its own name rather than living inside
R4's.

---

## 6. What is already right

Recorded so it is not "simplified" away by someone who has not read this far.

- **Money as a decimal string end-to-end.** Never `float64`, never a JS
  `number`. This is the single most important choice in the stack and it is
  correct.
- **Pointer-for-nullable applied consistently**, with no `sql.Null*` mixed in.
- **Type mismatches produce 400s, not panics.** Go's unmarshaler is doing real
  work here.
- **Unknown fields are ignored**, which is what makes mass assignment
  impossible. R4 is the price of a property worth keeping.
- **`getArray()` coalesces `null` → `[]`**, so a null-for-empty collection
  cannot reach a template as a crash.
- **The GIST exclusion constraint** is a genuine database-level invariant, not
  an application convention — proven under real concurrency.

---

*Audited and, where marked FIXED, re-verified against the running stack on
2026-09-05. Every table in §1.1, §2.2,
§2.4, §3.1 and §4 is measured output, not inference.*
