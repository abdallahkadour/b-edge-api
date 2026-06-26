# B-Edge — All Remaining Screen Prompts for Google Stitch

Same brand rules apply to every prompt below:
- Font: Inter, weights 400/500/600/700
- Ink: #0a0a0a · Gray scale · Success green: #16a34a only for confirmed/success states
- Mobile-only, 390px wide, black phone frame
- No blue, no gold, no shadows beyond 1px
- 52px full-width CTAs flush to the bottom

---

## Prompt 1 — Slot Unavailable (Error Screen)

Design a single mobile screen for B-Edge called "Slot Unavailable".

This screen appears when a customer submits their booking but the slot was taken by someone else in the last few seconds (race condition). It replaces the Booking Confirmed screen in this failure case.

Content:
- No header or back button — full centered layout
- Large warning icon: a circle with an X inside, 72px, ink #0a0a0a color (not red), centered
- Heading: "This slot was just taken" — 22px weight 700 ink, centered
- Subheading: "Someone else booked this time while you were filling in your details. Please pick another slot." — 15px gray-500, centered, max 280px width
- Two action buttons stacked at the bottom:
  - Primary: black full-width 52px "Choose another time" — goes back to date picker
  - Secondary: white outlined button 52px "Back to Rania's profile" below it
- Subtle note in gray-400 13px centered above the buttons: "Your details have been saved — you won't need to re-enter them."

Same visual style as the Booking Confirmed screen — clean, centered, calm. Not alarming. No red anywhere.

---

## Prompt 2 — Artist Not Found (404)

Design a single mobile screen for B-Edge called "Artist Not Found".

This screen appears when someone visits bedge.app/invalid-username — the artist doesn't exist or the link is broken.

Content:
- Full screen, no header
- Centered layout with generous padding
- Simple illustration: a broken link icon or a paintbrush with a question mark, ink color, 64px, inside a gray-100 circle
- Heading: "Artist not found" — 22px weight 700 ink, centered
- Body: "This link may be expired or incorrect. Check with the artist for their latest booking link." — 15px gray-500, centered, max 260px
- Single CTA at the bottom: ghost/outlined button "Visit B-Edge" — white background, ink border and text, full width 52px flush to bottom
- Small B-Edge wordmark at the very top center: "B-Edge" 14px weight 700

Same clean, restrained aesthetic. No dramatic error colors.

---

## Prompt 3 — Booking Lookup (Customer)

Design a single mobile screen for B-Edge called "Check Your Booking".

Customers can look up their booking status by entering the phone number they used when booking. Accessible via a link Rania sends on WhatsApp.

URL: bedge.app/booking

Content:
- Header: "B-Edge" wordmark 16px weight 700 centered, no back button
- Section title: "Find your booking" — 20px weight 600 ink
- Body text: "Enter the phone number you used when booking." — 14px gray-500, margin below
- Form:
  - Phone input with 🇱🇧 prefix and +961, same style as the Your Details screen
  - Large black "Find booking" button, full width, 52px, flush bottom
- Below the button: small gray-400 text "We'll show your most recent booking."
- If no result found (show inline): a small gray card with "No booking found for this number. Double-check and try again." — 13px gray-500, centered inside a gray-100 rounded card

Minimal, focused. One job: find the booking.

---

## Prompt 4 — Booking Status (Customer View)

Design a single mobile screen for B-Edge called "Your Booking".

This is what the customer sees after looking up their booking. Shows the current status and any next steps they need to take.

URL: bedge.app/booking/[id]

Content:
- Header: back arrow + "Your booking" centered, 15px weight 600
- Status banner at top — full width card, rounded 12px:
  - If status = "pending": gray-100 background, ink text — "⏳ Awaiting Rania's approval"
  - If status = "approved": warm yellow-50 background — "💰 Deposit required · Due by [date]"
  - If status = "confirmed": green-50 background, green text — "✅ Confirmed"
  - Show one of these three states in the mockup — use "approved" state with deposit due
- Booking detail card (same receipt style as Booking Confirmed):
  - Service / Date / Time / Location rows
  - Artist row showing "Rania · @rania.beauty"
- Deposit instructions card (shown only in approved state):
  - Ink background, white text card
  - Title: "Deposit required" 16px weight 600
  - Amount: "$50" large, 28px weight 700
  - Instructions: "Send via Wish Money or OMT to +961 70 XXX XXX · Screenshot and send to Rania on WhatsApp"
  - Deadline: "Due by Wednesday, 25 June · 6:00 PM" in white/70
- Bottom CTA: "Message Rania on WhatsApp" — WhatsApp green button (#25D366), white text, full width 52px

---

## Prompt 5 — Artist Dashboard: Booking Detail

Design a single mobile screen for B-Edge called "Booking Detail" for the artist dashboard.

Rania taps a booking card in her Bookings screen and lands here. She can see everything about the booking and take action.

URL: dashboard/bookings/[id]

Content:
- Header: back arrow + "Booking" centered + three-dot menu top right
- Customer info card at top — white card, ink border, rounded 12px:
  - Customer avatar: initials circle (gray-100 background, ink text, 48px)
  - Customer name: 18px weight 600
  - Phone with WhatsApp icon: tap to message
  - Booked via: "Customer PWA · 2 hours ago" in gray-400 12px
- Booking details section (same receipt row style):
  - Service / Price / Duration
  - Date / Time
  - Location (store name)
  - Deposit: "$50 · Not yet received" or "$50 · Received ✓"
  - Special requests (if any): shown as a gray-100 quote block
- Status timeline — vertical dots connecting:
  - ● Booking submitted (gray)
  - ● Pending approval ← current (ink, bold)
  - ○ Deposit requested (empty)
  - ○ Deposit received (empty)
  - ○ Confirmed (empty)
- Action buttons at bottom (stacked):
  - Primary: "Approve booking" — black, 52px
  - Secondary: "Decline" — white outlined, 52px, ink border

Show this in the "Pending" state — booking just came in, awaiting Rania's approval.

---

## Prompt 6 — Artist Dashboard: Notification Badge on Bookings

Design a single mobile screen for B-Edge showing the updated Bookings screen with a new incoming booking notification state.

This is the Bookings screen (already designed) but with:
- A red notification dot on the "Bookings" nav item showing "2"
- The first booking card in the list styled as "NEW" — a small green "New" pill badge top-right of the card
- The card expanded slightly to show the customer name, service, date, and a green "Approve" quick-action button inline on the right
- The rest of the list shows older bookings in the normal card style
- Same header: "Bookings" + Rania's name
- Same filter tabs: All / Pending / Confirmed / Completed / Cancelled

This screen communicates urgency without being alarmist — new bookings are highlighted but the overall tone stays calm and professional.

---

## Prompt 7 — Onboarding: Salon Setup (Step 1 of 3)

Design a single mobile screen for B-Edge called "Set up your salon" — Step 1 of the artist onboarding flow.

A new artist just registered and is being guided through setting up their salon before they can receive bookings.

URL: dashboard/onboarding/salon

Content:
- Progress indicator at top: three dots, first dot filled ink, two empty — "Step 1 of 3"
- Large heading: "Name your salon" — 24px weight 700 ink
- Subtext: "This is what customers will see on your booking page." — 15px gray-500
- Single input field — large, centered:
  - Label: "Salon name"
  - Placeholder: "e.g. Rania Studio"
  - 52px height, rounded 12px, ink border on focus
- Preview card below the input: shows a live mock of how it looks on the booking page — small artist card with "Rania Studio · Beirut" updating as they type
- Bottom CTA: "Continue" — black 52px button
- Skip link: "Set up later" in gray-400 12px centered above the button

Clean, focused. One field per screen. No clutter.
