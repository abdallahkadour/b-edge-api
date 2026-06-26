# B-Edge — 12 Missing Screens Stitch Prompts

Same brand rules apply to every prompt:
- Font: Inter, weights 400/500/600/700
- Ink: #0a0a0a · Gray scale · Success green: #16a34a only for confirmed/success states
- Mobile-only, 390px wide, black phone frame
- No blue, no gold
- 52px full-width CTAs flush to the bottom

---

## Prompt 1 — Customer PWA: Home / Artist Search & Filter

Design a single mobile screen for B-Edge called "Discover Artists".

This is the home screen of the customer PWA — what customers see when they open bedge.app directly (not via a specific artist link). They can search for beauty artists by service type, city, and price.

URL: bedge.app

Content:
- Header: B-Edge wordmark "B-Edge" left 18px weight 700 + small language toggle "AR | EN" top right in gray-400
- Search bar: full width, 48px height, rounded 24px, gray-100 background, magnifier icon left, placeholder "Search by artist, service, or city…"
- Filter chips horizontal scrollable row below: "All", "Makeup", "Hair", "Nails", "Lashes", "Skincare" — pill chips, selected chip has ink background + white text
- Section: "Popular in Beirut" — 15px weight 600 + "See all" link right in gray-400 12px
- Artist cards — vertical list, 2 per row grid:
  - Square card with artist photo top (rounded 12px), name below 13px weight 600, service category 11px gray-400, rating stars + number 11px, price "from $40" 12px weight 600 ink
  - 3 cards visible with partial 4th to indicate scrolling
  - One card has a "New" badge top-left in ink background
- Section below grid: "Near you in Tripoli" — same style
- Bottom tab bar: Home (active, ink), Bookings, Profile — 3 tabs, icons + labels

Clean discovery experience. Cards are the focus — no clutter above the fold.

---

## Prompt 2 — Customer PWA: Register (Sign Up)

Design a single mobile screen for B-Edge called "Create Account".

New customers create an account to track their bookings. Simple form — name, email, phone, password.

URL: bedge.app/register

Content:
- Header: "B-Edge" wordmark centered 18px weight 700, no back button (standalone screen)
- Heading: "Create your account" — 20px weight 700 ink
- Subheading: "Book beauty artists across Lebanon." — 13px gray-500
- Form:
  - Full name input: label "Full name", placeholder "Your name"
  - Email input: label "Email", placeholder "you@example.com"
  - Phone input: Lebanese flag 🇱🇧 +961 prefix, placeholder "70 000 000"
  - Password input: label "Password", placeholder "Min 8 characters", eye toggle
- Terms note: "By creating an account you agree to our Terms of Service and Privacy Policy." — 11px gray-400 centered with links underlined
- Primary CTA: black "Create account" button, full width, 52px flush bottom
- Already have account: "Already have an account? Sign in" — 12px gray-500 centered, above the button

---

## Prompt 3 — Customer PWA: Login

Design a single mobile screen for B-Edge called "Sign In" for customers.

This is distinct from the artist login — same visual style but for customer accounts.

URL: bedge.app/login

Content:
- Header: "B-Edge" wordmark centered 18px weight 700
- Heading: "Welcome back" — 20px weight 700 ink
- Subheading: "Sign in to view and manage your bookings." — 13px gray-500
- Form:
  - Email input: label "Email", placeholder "you@example.com"
  - Password input: label "Password", placeholder "••••••••", eye toggle
  - "Forgot password?" right-aligned below password, gray-400 12px
- Primary CTA: black "Sign in" button, full width, 52px flush bottom
- No account: "Don't have an account? Create one" — 12px gray-500 centered, above the button
- Note at bottom: small lock icon + "Guest booking available — no account needed to book." — 11px gray-400, helps users understand they don't have to register

---

## Prompt 4 — Customer PWA: My Bookings

Design a single mobile screen for B-Edge called "My Bookings".

Authenticated customers see their booking history — upcoming, past, and cancelled.

URL: bedge.app/bookings

Content:
- Header: "My Bookings" left 20px weight 700, no back button (tab screen)
- Filter tabs: "Upcoming", "Past", "Cancelled" — pill tabs, Upcoming selected by default
- Upcoming bookings section (2 cards visible):
  - Card 1: ink background, white text — "Bridal Makeup · Rania Studio · Mon 23 June · 10:00 AM · $200 · Status: Confirmed" — with green "Confirmed" pill badge, "View details" ghost button bottom right, and a small WhatsApp icon to message Rania
  - Card 2: lighter gray card — "Hair Blowout · Rania Studio · Thu 26 June · 2:00 PM · $80 · Status: Pending approval" — with gray "Pending" pill badge
- Empty state (shown in Past tab): centered illustration — calendar with a checkmark, "No past bookings yet." gray-500, "Book your first appointment" link in ink underlined
- Bottom tab bar: Home, Bookings (active, ink), Profile — same as Home screen

Cards feel like boarding passes — bold service name, date/time prominent, status pill clear.

---

## Prompt 5 — Customer PWA: Leave a Review

Design a single mobile screen for B-Edge called "Leave a Review".

After a completed appointment, the customer receives a WhatsApp link that opens this screen. One-time, post-appointment only.

URL: bedge.app/review/[booking-id]

Content:
- No header navigation — standalone screen focused entirely on the review
- Artist avatar: large initials circle 64px + artist name "Rania" 18px weight 700 + service "Bridal Makeup" 13px gray-400 — centered at top
- Star rating selector: 5 large star icons (40px each), tappable, highlighted gold/amber when selected — shown with 4 stars selected as default
- Review text field: labeled "Your review (optional)", placeholder "Tell others about your experience with Rania…", 4 rows, character counter "0/300"
- Recommendation toggle: "Would you recommend Rania?" — Yes/No pill toggle, "Yes" selected in green
- Primary CTA: black "Submit review" button, full width, 52px flush bottom
- Skip link: "Skip for now" gray-400 12px centered above the button

Warm and encouraging tone. Stars are large and satisfying to tap. Simple.

---

## Prompt 6 — Customer PWA: PWA Install Prompt

Design a single mobile screen for B-Edge called "Add to Home Screen".

A bottom sheet prompt that appears on the customer's second visit, encouraging them to install the PWA. Slides up from the bottom over the artist profile screen.

Content:
- Bottom sheet — white, rounded top corners 24px, slides up from bottom
- Drag handle bar at top (gray pill)
- B-Edge app icon: 56px rounded square, ink background with "B" in white weight 700
- Heading: "Add B-Edge to your home screen" — 17px weight 700 ink
- Body: "Get instant access to your bookings and new artists. No app store needed." — 13px gray-500, line height 1.6
- Feature list (3 rows, icon + text):
  - 📅 "Check your bookings anytime, even offline"
  - 🔔 "Get notified when your booking is confirmed"
  - ⚡ "Opens instantly like a native app"
- Two buttons stacked:
  - Primary: black "Add to home screen" 52px
  - Secondary: gray-400 text "Maybe later" centered link 12px
- The artist profile is visible dimmed behind the overlay

---

## Prompt 7 — Artist Dashboard: Deposit Queue

Design a single mobile screen for B-Edge called "Deposit Queue".

Rania sees all bookings that are waiting for deposit verification — she's checked Wish Money and needs to mark which ones have been paid.

URL: dashboard/deposits

Content:
- Header: "Deposits" left 20px weight 700 + "2 pending" small gray-400 badge right
- Explanation banner: amber-tinted card — "💰 Review your Wish Money transfers and mark deposits as received." — 12px, dismissible ✕
- Deposit cards — each showing:
  - Customer initials circle (36px) + name + service name
  - Deposit amount large: "$50" 20px weight 700 ink right-aligned
  - Due date: "Due by Wed 25 Jun · 6:00 PM" in amber if <24h, gray otherwise
  - Booking date/time: small gray-400
  - Two inline action buttons: "Mark received ✓" (green, small) and "Contact on WhatsApp 💬" (white outlined)
- Show 2 cards — one urgent (due in 2 hours, amber border), one normal
- Below the cards: "Past deposits" section with 2 completed rows (simpler list style, green checkmark, "Received · 19 Jun")
- Bottom nav: same 4-item nav as other dashboard screens, "Deposits" highlighted

Urgency is communicated through amber/deadline without red alarmism.

---

## Prompt 8 — Artist Dashboard: Refund Queue

Design a single mobile screen for B-Edge called "Refunds".

Rania sees all bookings where a refund is owed to a customer — she needs to send the money via Wish Money and then mark it as sent in B-Edge.

URL: dashboard/refunds

Content:
- Header: "Refunds" left 20px weight 700 + "1 pending" small badge
- Explanation banner: gray-50 card — "📋 Send refunds via Wish Money, then mark as sent here." — 12px, dismissible
- Refund cards — each showing:
  - Customer name + service name + cancellation date
  - Refund amount: "$50" 20px weight 700 ink right-aligned
  - Reason: small pill showing "Artist cancelled" or "Customer cancelled >24h" in gray-100
  - Customer phone (for Wish Money): "+961 70 123 456" with a copy icon
  - "Mark as refunded ✓" full-width green button inside the card
- Show 1 pending refund card + 1 completed past refund (dimmed, green checkmark "Refunded · 17 Jun")
- Empty state message if no pending: centered green checkmark + "All refunds are up to date." — 14px ink weight 600
- Bottom nav: same dashboard nav

Simple and functional. The phone number is prominent because Rania needs it for Wish Money.

---

## Prompt 9 — Artist Dashboard: Client List (CRM)

Design a single mobile screen for B-Edge called "Clients".

Rania's complete client list — every customer who has ever booked with her. Searchable. Tapping a client opens their profile.

URL: dashboard/clients

Content:
- Header: "Clients" left 20px weight 700 + total count "42 clients" gray-400 right
- Search bar: full width, 44px, gray-100, rounded 12px, placeholder "Search by name or phone…"
- Alphabetically grouped list:
  - Section header "M" — gray-400 11px uppercase
  - Client row: initials circle (40px, gray-100) + name + last service 11px gray-400 + last visit date right 11px gray-400
  - Section header "N"
  - 2 more rows
- Each row has a right chevron (›)
- One client row marked with a small VIP crown icon (gold — exception to no-gold rule: tiny icon only) for repeat customers (3+ bookings)
- Bottom nav: same 4-item dashboard nav, "Clients" highlighted (add clients tab to the nav)

Clean contacts-app feel. Fast to scan.

---

## Prompt 10 — Artist Dashboard: Client Detail

Design a single mobile screen for B-Edge called "Client Profile".

Rania taps a client and sees their full history — bookings, notes she's added, and quick actions.

URL: dashboard/clients/[id]

Content:
- Header: back arrow + client name "Maya Jaber" centered
- Client card at top:
  - Large initials circle 64px + name 20px weight 700 + phone number with WhatsApp icon + first booked date "Customer since Jun 2026" in gray-400
  - Stats row: 3 stats in a row — "3 bookings", "$340 total spent", "4.8 avg rating"
- Booking history section: "Booking History" 14px weight 600
  - 3 booking rows: service name + date + status pill + amount right
  - Most recent first, same style as bookings list
- Notes section: "My notes" 14px weight 600 + pencil edit icon
  - Text area showing Rania's private notes: "Prefers natural looks. Hair is very thick. Doesn't like heavy eyeshadow. Allergic to latex gloves."
  - Gray-100 background, rounded, not an active input by default — tapping edit opens a text editor
- Bottom CTA: "Message on WhatsApp" — WhatsApp green button, full width, 52px

Private notes are the key differentiator — Rania remembers everything about every client without asking.

---

## Prompt 11 — Artist Dashboard: Earnings Summary

Design a single mobile screen for B-Edge called "Earnings".

Rania sees her revenue — today, this week, this month. Simple financial snapshot.

URL: dashboard/earnings

Content:
- Header: "Earnings" left 20px weight 700 + month selector "June 2026 ‹ ›" right
- Large hero stat: current month total — "$1,840" 36px weight 700 ink centered + "June 2026" label below in gray-400 12px
- Stats row: 3 equal cards below the hero:
  - "Today" — $120
  - "This week" — $480
  - "This month" — $1,840
  - Each card: label gray-400 10px uppercase + amount 16px weight 700 ink
- Chart section: "Daily earnings" 14px weight 600 + simple bar chart showing last 7 days, bars in ink, today's bar slightly taller, y-axis in gray-400 tiny labels
- Booking breakdown: "Breakdown" 14px weight 600
  - Service rows: "Bridal Makeup · 3 bookings · $600", "Hair Blowout · 5 bookings · $400", etc.
  - Each row: service name + count left, amount right in ink weight 600
- Footer note: gray-400 11px — "Earnings reflect cash collected. Deposits are included when marked received."

No gamification, no confetti — clean financial data. Rania is a professional.

---

## Prompt 12 — Artist Dashboard: Portfolio Upload

Design a single mobile screen for B-Edge called "Portfolio".

Rania manages her work photos here — the gallery that customers see on her public profile page.

URL: dashboard/portfolio

Content:
- Header: "Portfolio" left 20px weight 700 + "18 photos" gray-400 count right
- Upload button: full-width dashed border button at the top — "+ Add photos" — 48px, ink dashed border 1.5px, rounded 12px, ink text
- Photo grid: 3-column grid, square thumbnails, rounded 8px corners
  - Show 9 photos (3 rows)
  - Photos are makeup/beauty work placeholders (use gray-200 placeholder squares with a subtle camera icon if no real images)
  - Each photo has a small ✕ delete button top-right corner (small circle, white background, ink ✕)
  - One photo has a star badge top-left: "Cover" — this is the photo shown on the booking page
- Long press / tap behavior note: tapping a photo opens it full screen. Long press shows "Set as cover" option.
- Limit note at bottom: "Maximum 20 photos. 18 of 20 used." — gray-400 11px centered
- Bottom nav: same 4-item dashboard nav

Grid-forward layout. The upload button is always visible at the top — encourages Rania to keep her portfolio fresh.
