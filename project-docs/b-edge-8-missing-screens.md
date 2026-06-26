# B-Edge — 8 Missing Screens Stitch Prompts

Add these to your existing B-Edge Stitch project (not a new app).

Brand rules (same for every prompt):
- Font: Inter, weights 400/500/600/700
- Ink: #0a0a0a · Success green: #16a34a only for success states
- Error/cancel red: #dc2626
- Mobile-only 390px, black phone frame
- 52px full-width CTA flush bottom

---

## Prompt 1 — C-16: Customer Forgot Password

Design a single mobile screen for B-Edge called "Forgot Password" for customer accounts.

URL: bedge.app/forgot-password

- Header: "B-Edge" wordmark centered 16px weight 700, no back button
- Heading: "Forgot your password?" 20px weight 700 ink
- Body: "Enter your email and we'll send you a reset link." 13px gray-500
- Email input: label "Email", placeholder "you@example.com"
- Success state (shown after submit, replaces form): large green checkmark circle (56px, #16a34a) centered + "Check your email" 18px weight 700 + "We sent a reset link to you@example.com. It expires in 30 minutes." 12px gray-500, centered. No button in success state.
- Primary CTA: "Send reset link" black 52px full-width flush bottom
- Above button: "Back to login" gray-400 12px centered

No account needed note: this screen should feel reassuring, not alarming.

---

## Prompt 2 — C-17: Customer Reset Password

Design a single mobile screen for B-Edge called "Set New Password" for customers.

URL: bedge.app/reset-password

- Header: "B-Edge" wordmark centered 16px weight 700
- Heading: "Set a new password" 20px weight 700 ink
- Body: "Choose a strong password for your B-Edge account." 13px gray-500
- New password input: label "New password", placeholder "••••••••", eye icon toggle right
- Confirm password input: label "Confirm password", placeholder "••••••••", eye icon toggle right
- Password hint below the second input: small lock icon + "At least 8 characters · 1 uppercase · 1 number" — 10px gray-400
- Error state banner (full-width red/amber card at top, shown when token is expired): "This reset link has expired." + "Request a new one" underlined link
- Primary CTA: "Set password" black 52px flush bottom, disabled (gray) when fields are incomplete or passwords don't match

---

## Prompt 3 — C-18: Customer Booking Detail

Design a single mobile screen for B-Edge called "Your Booking" — the customer's view of a single booking.

URL: bedge.app/bookings/[id]

This is the detail screen customers reach by tapping a booking card in My Bookings.

- Header: back arrow left + "Your booking" centered 14px weight 600
- Artist info row at top: initials circle 44px (ink background, white "R") + "Rania" 15px weight 700 + "@rania.beauty" gray-400 11px + WhatsApp chat icon button far right (green #25D366)
- Booking details card (ink border):
  - Service: "Bridal Makeup" 15px weight 600 + status pill top-right (green "Confirmed")
  - Date & Time: "Monday, 23 June 2026 · 10:00 AM"
  - Location: "Beirut Downtown · Rania Studio"
  - Duration: "120 minutes"
  - Total price: "$200"
  - Booking ref: "#BE-96102" in monospace gray-400
- Deposit info card (amber tint — only shown when status is `approved`): "💰 Deposit required: $50 · Due by 25 Jun 6:00 PM". Second line: "Send via Wish Money to +961 70 XXX XXX. Send screenshot to Rania on WhatsApp." Amber border, amber-50 background.
- Status timeline (same read-only style as artist Booking Detail screen): Booking submitted → Pending approval (current) → Deposit requested → Confirmed
- "Cancel booking" — small red text link at the very bottom center, 12px — only visible when status allows cancellation and appointment is >24h away
- No large bottom CTA button for non-cancellable states

---

## Prompt 4 — C-19: Cancel Booking (Customer Bottom Sheet)

Design a bottom sheet modal overlay for B-Edge called "Cancel this booking?" for customers.

This appears as a slide-up overlay on C-18 when the customer taps "Cancel booking".

- Background: the booking detail screen is visible but dimmed (rgba overlay)
- Bottom sheet: white, rounded top corners 24px, slides up from bottom
- Drag handle bar at top (gray pill, centered)
- Heading: "Cancel this booking?" 17px weight 700 ink
- Subtext: "Bridal Makeup · Mon 23 June · 10:00 AM" 12px gray-500

- Refund policy card (show Variant A in this design — full refund applicable):
  Variant A (>24h — shown): green-tint card — checkmark + "You'll receive a full refund of $50 sent to your Wish Money within 48 hours of cancellation."
  Note: show Variant B (amber-tint) as a smaller dimmed state below for reference — "Less than 24h before appointment: deposit is not refundable."

- Reason input: label "Reason for cancelling", placeholder "Tell Rania why you're cancelling…", 3 rows textarea, required

- Two buttons stacked with 8px gap:
  - Primary: "Yes, cancel booking" — full width 52px, #dc2626 red background, white text
  - Secondary: "Keep my booking" — full width 52px, white background, ink border, ink text

---

## Prompt 5 — A-18: Artist Forgot Password

Design a single mobile screen for B-Edge called "Forgot Password" for the artist dashboard.

URL: dashboard/forgot-password

Use the same shell as the Artist Login screen (A-01):
- Header: "B-Edge" wordmark + "Artist Dashboard" gray subtext, centered, with bottom border

- Body (centered content):
  - Heading: "Forgot your password?" 20px weight 700
  - Body text: "Enter your email and we'll send a reset link to your inbox." 12px gray-500
  - Email input: label "Email", placeholder "rania@b-edge.com"
  - Success state: green checkmark + "Reset link sent." + "Check your inbox — the link expires in 30 minutes." — replaces form on submit

- Bottom:
  - "Back to login" gray link centered, above button
  - "Send reset link" black 52px full-width flush bottom

---

## Prompt 6 — A-19: Artist Reset Password

Design a single mobile screen for B-Edge called "Set New Password" for the artist dashboard.

URL: dashboard/reset-password

- Same shell as A-01 (B-Edge wordmark + "Artist Dashboard" header)
- Heading: "Set a new password" 18px weight 700
- Body: "Your new password must be strong and different from your last one." 12px gray-500
- New password input with eye toggle
- Confirm password input with eye toggle
- Password strength hint: 10px gray-400
- Expired link error state: amber banner at top "This link has expired." + "Request a new link" underlined
- "Set password" black 52px full-width flush bottom

---

## Prompt 7 — A-20: Change Password (Artist)

Design a single mobile screen for B-Edge called "Change Password" in the artist dashboard.

URL: dashboard/settings/password

- Header: back arrow + "Change Password" centered 14px weight 600
- Three inputs stacked with generous spacing:
  - "Current password" — label + input with eye toggle
  - "New password" — label + input with eye toggle
  - "Confirm new password" — label + input with eye toggle
- Password hint below the last input: small gray lock icon + "At least 8 characters · 1 uppercase · 1 number" 10px gray-400
- Mismatch error: small red text below confirm field "Passwords do not match" — only shown after field is blurred
- Success state: green banner slides in from top "Password changed successfully." auto-dismiss after 3 seconds
- "Save new password" black 52px full-width CTA flush bottom
- CTA disabled state (gray background, gray text) when: any field is empty, or passwords don't match, or new password is same as current

---

## Prompt 8 — A-21: Block Dates (Artist)

Design a single mobile screen for B-Edge called "Block Dates" in the artist dashboard.

Rania uses this to mark days she is completely unavailable — no bookings can be made on a blocked date.

URL: dashboard/block-dates

- Header: back arrow + "Block Dates" centered 14px weight 600

- Store selector: row of 3 pill buttons — "Beirut Downtown" / "Tripoli" / "Both" — ink background for selected, gray-100 + gray-500 text for unselected. "Both" is the default selected.

- Calendar section:
  - Label: "Tap a date to block it" 11px gray-400 uppercase
  - Compact monthly calendar view: show current month (June 2026)
    - Day name headers (Mon–Sun) in gray-400 9px
    - Date number cells — 36px circular, gray-100 background default
    - Selected date: ink background, white number
    - Already-blocked dates: shown with a small red dot below the number
    - Left/right chevron arrows to navigate months
  - Today (June 18): subtle ink outline circle (not filled — just outlined)

- Selected date row below calendar: gray-100 chip showing "18 June 2026 · Both stores" with a small ✕ to deselect

- Reason input: "Reason (optional)" label, placeholder "e.g. Personal day, Eid holiday…", single line input

- "Block this day" black 52px CTA button (active when a date is selected)

- Divider + "Blocked dates" section:
  - 2 existing blocked date rows:
    - "25 Dec 2026 · Christmas Day · Beirut + Tripoli" — small red calendar icon left + date text + right: ✕ delete button
    - "01 Jan 2027 · New Year · Both stores" — same style
  - Empty state (shown when no blocked dates): "No blocked dates yet." gray-400 centered, 12px

