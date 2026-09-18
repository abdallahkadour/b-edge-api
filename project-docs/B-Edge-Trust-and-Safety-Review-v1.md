# B-Edge — Trust & Safety Review

**Date:** 2026-09-18 · **Question:** how do we stop clients being scammed?
**Method:** threat model against the live code, plus a survey of what other
platforms do.

---

## 1. The asymmetry that defines this problem

Booksy's own write-up on scams in the beauty industry describes five attacks.
**Every one of them targets the provider, not the client.** That is not an
oversight — it is what card rails do. On Booksy, Fresha or GlossGenius a client
who is defrauded raises a chargeback and the payment processor claws the money
back. Client-side protection is bought in, not built.

B-Edge has no card rails. The deposit moves directly from client to artist by
OMT or Whish. There is no escrow, no intermediary and no reversal. **The
platform is the only recourse a client has**, which means client-side trust is
a product feature here and not a vendor's problem.

Every recommendation below follows from that single fact.

---

## 2. Threat model

Ranked by how much money a single incident moves and how little the client can
do about it afterwards.

| | Threat | Status |
|---|---|---|
| **T1** | **Payment redirected.** The app never shows where to send the deposit, so the artist supplies a Whish or OMT number over WhatsApp. Anyone who can insert themselves into that conversation — or clone the artist's profile — substitutes their own number. B-Edge holds no record of the correct destination, so the dispute is unresolvable by anyone. | **OPEN — the biggest hole** |
| **T2** | **Deposit taken, service never delivered.** No escrow, no chargeback. | OPEN (mitigated only by reputation) |
| **T3** | **Deposit larger than the service.** A $50 service could ask for a $500 deposit and the funnel would render it. | **FIXED** — migration 038 |
| **T4** | **Fake artist profile** using stolen portfolio photos. | Partly mitigated: admin approval gate. No identity check. |
| **T5** | **Off-platform diversion** — "book with me directly". Removes every record. | Guidance only (added to the help guide) |
| **T6** | **False no-show** to keep a deposit. | OPEN — artist's word is final |
| **T7** | **Fake bookings** blocking prime slots (the classic attack on providers). | Well mitigated: approval + deposit |
| **T8** | **Fake reviews.** | **Strongly mitigated** — one-time token, tied to a real attended booking |

---

## 3. What B-Edge already does well

Worth stating, because the list of gaps is longer:

- **Artists cannot trade before a human reviews them.** Applications sit in an
  admin queue; nothing is public until approved.
- **Reviews cannot be faked.** They require a one-time token issued against a
  real, attended booking. Most platforms cannot claim this — B-Edge is
  *stronger* than the field here.
- **Nothing is held until the artist approves**, so a fake booking does not
  cost a real slot.
- **Audit logging** on money-touching admin actions.
- **Rate limiting**, with tighter limits on OTP and login.
- **Tenant isolation** between salons, covered by tests.
- **Deposit ≤ price**, as of this review.

---

## 4. What is missing

### F1 — The verified badge is dead code

`artists.is_verified` is `DEFAULT FALSE`. It is rendered as a badge on
discovery cards and artist profiles, and it sorts artists in discovery
(`ORDER BY a.is_verified DESC, ...`). There is an index built on it.

**Nothing in the codebase ever sets it to true.** Admin approval sets
`status = 'active'` and nothing else. So the badge can never appear, the sort
key is constant, and the partial index matches zero rows.

The result is worse than having no badge: the UI has a trust affordance that no
honest artist can earn, and if anyone ever sets the column by hand there is no
criteria, no audit trail and no process behind it.

### F2 — There is no payment destination of record

The schema has no field for an artist's Whish or OMT number — confirmed by
searching for every plausible name. The app therefore cannot tell a client
where to send money, so the number necessarily arrives over WhatsApp.

This is T1, and it is the single highest-value fix available. It also makes the
most useful safety advice possible — *"only send to the number shown in the
app"* — which today cannot honestly be given.

### F3 — Deposit confirmation is one-sided

Only the artist can mark a deposit received (`PATCH /bookings/:id/deposit-received`).
A client who has sent money has no way to assert it, and no way to escalate if
the artist simply does not record it.

### F4 — There is no way to report anything

No report, flag, complaint or dispute mechanism exists anywhere in the schema
or the API. A client being scammed has nowhere to go inside the product.

### F5 — Trust signals exist but are not surfaced

`created_at` (tenure) is selected in discovery but never displayed. Completed
booking count is not exposed at all. Both are cheap, honest signals that a
brand-new fake profile cannot fake.

---

## 5. What other platforms do, and what transfers

| Practice | Transfers to B-Edge? |
|---|---|
| **Escrow** — platform holds funds until service is delivered | **No, not yet.** Requires being a money transmitter. This is the real long-term answer and a regulatory project, not a sprint. |
| **Chargebacks** | No. No card rails. |
| **Risk scoring** (Sift, Signifyd, Kount) | No. They score card transactions. |
| **Verified badges / ID checks** | **Yes.** Cheap, and the column already exists. |
| **Reviews tied to real transactions** | **Already done, and done well.** |
| **On-platform dispute resolution** | **Yes.** The cited guidance is that speed matters more than perfect fairness — a dispute settled quickly rarely escalates. |
| **Keep the conversation and payment on-platform** | **Partly.** Guidance now; a payment destination of record would make it enforceable. |
| **Provider-side fraud education** | **Yes** — added to the artist guide. |

The honest summary: most of the industry's client-protection machinery is
bought from a payment processor. B-Edge cannot buy it, so the substitutes are
**identity, record-keeping and recourse**.

---

## 6. Recommended order

1. **Payment destination of record.** Artist registers their Whish/OMT number;
   the app displays it at the deposit step; it is never given over chat. Closes
   T1, and unlocks the strongest single piece of client advice.
2. **Make `is_verified` earnable.** An admin action with stated criteria
   (identity document seen, business confirmed), written to the audit log, and
   a badge that explains what it means when tapped. Or remove the badge — but
   shipping a permanently-unearnable trust signal is the worst of the three
   options.
3. **"Report a problem"** on a booking, creating a case an admin can see.
   Recourse is what makes the rest credible.
4. **Two-sided deposit confirmation.** The client marks "I sent it" with a
   reference; the artist confirms; a mismatch surfaces to an admin. Closes F3
   and gives T6 an evidence trail.
5. **Surface tenure and completed bookings** on the artist profile.
6. **Escrow**, once volume justifies the regulatory work. This is the endgame,
   and everything above is the bridge to it.

---

## 7. Shipped with this review

- **Deposit ≤ price**, enforced at three layers: a CHECK constraint
  (migration 038, clamping existing rows rather than failing), a readable 400
  in the service layer, and a disabled Save with an inline reason in the
  artist's form. Five named tests, including the partial-update case where a
  request lowering only the price breaches the rule against the stored deposit.
- **Safety guidance in both help guides** — client-side and artist-side.
  Deliberately limited to claims the product can currently back; the absence of
  "check the number in the app" is F2 showing through.
