// Package salonrole holds the one authoritative answer to "may this member of
// a salon do this?".
//
// It exists as a leaf package — importing nothing else in this codebase — for
// the same reason internal/pkg/subscription does. internal/middleware must
// consult the rule on every guarded route, and several domain packages import
// middleware for RequireAuth, so a domain package owning the rule would close
// an import cycle.
//
// ── Why this is a table and not a scattering of if-statements ──────────────
//
// The most expensive recurring defect in this project is a rule written
// correctly in one place and hand-copied to another, after which only one copy
// gets fixed. The canonical case is subscriptionVisibleCond, which existed as
// three hand-written copies in internal/discovery, internal/artist and
// internal/share. One was corrected on 2026-09-20. The other two were found
// still wrong on 2026-09-21 — a cancelled artist stayed reachable through
// their share link and through handle lookup while being hidden from Discover
// and refused bookings.
//
// An authorisation rule is a far worse thing to get wrong that way, so this
// package is shaped to make copying it unattractive and detectable:
//
//   - matrix is the entire policy. Every capability carries an explicit true
//     or false under every role, never an omission, so a missing decision is a
//     test failure rather than a silent deny.
//   - TestNoInlineOwnerChecks parses internal/ and fails if any other file
//     compares against a salon's owner_id.
//
// ── Why the role is derived and never stored ───────────────────────────────
//
// salons.owner_id is the only source. Nothing writes a role column, because a
// second source of truth is a second thing to get out of step. The cost of
// that choice is real and must be paid in the code that transfers ownership:
// the role is resolved at token issue, so a transfer has to invalidate both
// parties' tokens or the old owner keeps owner capabilities until their
// access token expires.
//
// ── Why a solo artist must notice nothing ──────────────────────────────────
//
// Every artist on B-Edge today is the sole member of their own salon, and is
// therefore its owner. Owner holds every capability Member holds
// (TestCan_OwnerIsSupersetOfMember pins this), so introducing roles removes
// nothing from anyone who is working alone.
package salonrole

// Role is a user's standing within one salon. It is derived from
// salons.owner_id by Resolve and is never persisted.
type Role string

const (
	// None is a user with no salon at all — an artist mid-onboarding, or a
	// customer. It holds no capabilities whatsoever.
	None Role = ""

	// Owner is the artist named in salons.owner_id. Accountable for the
	// salon's menu, stores, hours, money and membership.
	Owner Role = "owner"

	// Member is an artist attached to the salon who does not own it. Runs
	// their own diary; cannot alter anything the salon shares.
	Member Role = "member"
)

// Capability is a single permission. The string values are stable and appear
// in no API response — they name rows of the matrix, nothing more.
type Capability string

// The shared-resource capabilities. These are what a salon holds in common,
// and the reason this package exists: before it, any artist carrying a
// salon_id claim could write every one of them.
const (
	ServicesWrite       Capability = "services:write"
	StoresWrite         Capability = "stores:write"
	StoreHoursWrite     Capability = "store_hours:write"
	DiscountsWrite      Capability = "discounts:write"
	ProductsWrite       Capability = "products:write"
	PaymentMethodsWrite Capability = "payment_methods:write"
	BillingWrite        Capability = "billing:write"
	MembersWrite        Capability = "members:write"
	EarningsSalonRead   Capability = "earnings:salon:read"
	CalendarSalonRead   Capability = "calendar:salon:read"
	BookingsAnyWrite    Capability = "bookings:any:write"
)

// The personal capabilities. Every member holds these; they are what makes a
// member able to work rather than merely exist.
const (
	MembersRead      Capability = "members:read"
	OwnScheduleWrite Capability = "own_schedule:write"
	OwnBookingsWrite Capability = "own_bookings:write"
	OwnProfileWrite  Capability = "own_profile:write"
	OwnEarningsRead  Capability = "own_earnings:read"
	ClientNotesWrite Capability = "client_notes:write"
)

// all is the enumeration the completeness test iterates. A new Capability that
// is not added here is invisible to that test, so keep it in step — the
// constant blocks above and this slice are the two halves of one list.
var all = []Capability{
	ServicesWrite, StoresWrite, StoreHoursWrite, DiscountsWrite,
	ProductsWrite, PaymentMethodsWrite, BillingWrite, MembersWrite,
	EarningsSalonRead, CalendarSalonRead, BookingsAnyWrite,
	MembersRead, OwnScheduleWrite, OwnBookingsWrite, OwnProfileWrite,
	OwnEarningsRead, ClientNotesWrite,
}

// matrix is the whole policy.
//
// Do not read a decision out of this map and re-express it as a conditional
// somewhere else. Call Can.
//
// Every capability appears under every role with an explicit value. That is
// deliberate: Go's zero value for a missing map key is false, so an omission
// would silently deny rather than fail, and a capability someone forgot to
// decide on would look exactly like one they decided to withhold.
// TestMatrix_EveryCapabilityDecidedForEveryRole refuses that.
var matrix = map[Role]map[Capability]bool{
	Owner: {
		// Shared salon resources — the owner's to set.
		ServicesWrite:       true,
		StoresWrite:         true,
		StoreHoursWrite:     true,
		DiscountsWrite:      true,
		ProductsWrite:       true,
		PaymentMethodsWrite: true,
		BillingWrite:        true,
		MembersWrite:        true,
		EarningsSalonRead:   true,
		CalendarSalonRead:   true,
		BookingsAnyWrite:    true,

		// Personal — an owner is also someone who does the work.
		MembersRead:      true,
		OwnScheduleWrite: true,
		OwnBookingsWrite: true,
		OwnProfileWrite:  true,
		OwnEarningsRead:  true,
		ClientNotesWrite: true,
	},
	Member: {
		// Shared salon resources — read them, never write them. This whole
		// block is the boundary that did not exist before 2026-09-21.
		ServicesWrite:       false,
		StoresWrite:         false,
		StoreHoursWrite:     false,
		DiscountsWrite:      false,
		ProductsWrite:       false,
		PaymentMethodsWrite: false,
		BillingWrite:        false,
		MembersWrite:        false,
		EarningsSalonRead:   false,
		CalendarSalonRead:   false,
		BookingsAnyWrite:    false,

		// Personal — everything needed to run their own day.
		//
		// ClientNotesWrite is true by decision D-MS3: client_notes already
		// carries both salon_id and artist_id, and a salon whose members
		// cannot brief each other on a client is not functioning as a salon.
		// Recorded in B-Edge-Multi-Artist-Salon-BRD-v1.md as a privacy
		// posture, not an oversight.
		MembersRead:      true,
		OwnScheduleWrite: true,
		OwnBookingsWrite: true,
		OwnProfileWrite:  true,
		OwnEarningsRead:  true,
		ClientNotesWrite: true,
	},
	None: {},
}

// Can reports whether r may do c.
//
// None returns false for everything, by way of the empty map — a user with no
// salon has nothing in a salon to permit.
func Can(r Role, c Capability) bool {
	return matrix[r][c]
}

// All returns every capability. For tests and for enumerating the policy; the
// returned slice is a copy, so a caller cannot edit the source of truth.
func All() []Capability {
	out := make([]Capability, len(all))
	copy(out, all)
	return out
}

// Resolve derives a user's role in a salon.
//
// salonOwnerID is salons.owner_id for the salon the user belongs to.
// hasSalon is false when artists.salon_id is NULL — an artist part-way through
// onboarding, or the one pre-existing row that never had a salon. Those get
// None and are refused by every guard, which is the safe direction.
func Resolve(userID, salonOwnerID string, hasSalon bool) Role {
	switch {
	case !hasSalon || userID == "":
		return None
	case userID == salonOwnerID:
		return Owner
	default:
		return Member
	}
}

// Valid reports whether r is a role this package issues. Used when decoding a
// role that arrived from outside the process — a JWT claim, say — so an
// unrecognised value becomes None rather than something that indexes the
// matrix by accident.
func Valid(r Role) bool {
	return r == None || r == Owner || r == Member
}
