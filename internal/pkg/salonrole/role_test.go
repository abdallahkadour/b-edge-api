package salonrole

import "testing"

// ── Resolve ────────────────────────────────────────────────────────────────

func TestResolve_NoSalon_ReturnsNone(t *testing.T) {
	if got := Resolve("user-1", "user-1", false); got != None {
		t.Fatalf("an artist with no salon must resolve to None, got %q", got)
	}
}

func TestResolve_UserIsOwner_ReturnsOwner(t *testing.T) {
	if got := Resolve("user-1", "user-1", true); got != Owner {
		t.Fatalf("want Owner, got %q", got)
	}
}

func TestResolve_UserIsNotOwner_ReturnsMember(t *testing.T) {
	if got := Resolve("user-2", "user-1", true); got != Member {
		t.Fatalf("want Member, got %q", got)
	}
}

func TestResolve_EmptyUserID_ReturnsNone(t *testing.T) {
	// An empty user ID must never coincidentally match an empty owner ID and
	// hand out Owner. Both being unset is the shape a zero-valued claim has.
	if got := Resolve("", "", true); got != None {
		t.Fatalf("an empty user ID must resolve to None, got %q", got)
	}
}

// ── The matrix ─────────────────────────────────────────────────────────────

// TestMatrix_EveryCapabilityDecidedForEveryRole is the reason the matrix is
// written with explicit false entries rather than omissions.
//
// Go returns false for a missing map key, so a capability nobody decided on
// behaves identically to one deliberately withheld. That is fine until a
// capability is added and quietly denied to the owner, at which point the
// owner's dashboard loses a feature and nothing failed. This test makes the
// omission loud.
func TestMatrix_EveryCapabilityDecidedForEveryRole(t *testing.T) {
	for _, r := range []Role{Owner, Member} {
		for _, c := range All() {
			if _, ok := matrix[r][c]; !ok {
				t.Errorf("capability %q has no entry for role %q - add it to the "+
					"matrix deliberately rather than relying on the zero value", c, r)
			}
		}
	}
}

// TestMatrix_HasNoCapabilityMissingFromAll catches the other half of the same
// list drifting: a capability added to the matrix but not to all, which would
// hide it from the completeness test above.
func TestMatrix_HasNoCapabilityMissingFromAll(t *testing.T) {
	known := make(map[Capability]bool, len(all))
	for _, c := range All() {
		known[c] = true
	}
	for role, caps := range matrix {
		for c := range caps {
			if !known[c] {
				t.Errorf("role %q decides capability %q, which is not in all - "+
					"the completeness test cannot see it", role, c)
			}
		}
	}
}

// TestCan_OwnerIsSupersetOfMember pins the invariant that makes this change
// safe to ship to a platform of soloists.
//
// Every artist on B-Edge today owns their own one-member salon. If there were
// anything a Member could do that an Owner could not, then transferring a
// salon to a colleague would cost the new owner an ability, and more
// immediately, introducing roles at all would take something away from the
// people already using the product.
func TestCan_OwnerIsSupersetOfMember(t *testing.T) {
	for _, c := range All() {
		if Can(Member, c) && !Can(Owner, c) {
			t.Errorf("Member holds %q but Owner does not - Owner must be a superset", c)
		}
	}
}

func TestCan_None_HoldsNothing(t *testing.T) {
	for _, c := range All() {
		if Can(None, c) {
			t.Errorf("role None holds %q; a user with no salon must hold nothing", c)
		}
	}
}

func TestCan_UnknownRole_HoldsNothing(t *testing.T) {
	// A role decoded from a token could be anything. Indexing the matrix with
	// it must not panic and must not permit.
	for _, c := range All() {
		if Can(Role("administrator"), c) {
			t.Errorf("an unrecognised role holds %q", c)
		}
	}
}

// ── The specific boundary this package was built for ───────────────────────

// TestCan_MemberCannotWriteSharedSalonResources names, one by one, the things
// a second artist in Rania's salon must not be able to change.
//
// Listed explicitly rather than derived from the matrix: a test that reads the
// same map it is checking proves only that the map equals itself. These are
// the requirements from B-Edge-Multi-Artist-Salon-BRD-v1.md BR-8, restated
// independently so that relaxing one in the matrix fails here.
func TestCan_MemberCannotWriteSharedSalonResources(t *testing.T) {
	forbidden := []Capability{
		ServicesWrite, StoresWrite, StoreHoursWrite, DiscountsWrite,
		ProductsWrite, PaymentMethodsWrite, BillingWrite, MembersWrite,
		EarningsSalonRead, CalendarSalonRead, BookingsAnyWrite,
	}
	for _, c := range forbidden {
		if Can(Member, c) {
			t.Errorf("a salon member must not hold %q", c)
		}
	}
}

// TestCan_MemberCanRunTheirOwnDay is the counterweight. A boundary that also
// stops a member working is not a boundary, it is a broken account.
func TestCan_MemberCanRunTheirOwnDay(t *testing.T) {
	required := []Capability{
		MembersRead, OwnScheduleWrite, OwnBookingsWrite, OwnProfileWrite,
		OwnEarningsRead, ClientNotesWrite, OwnServicesWrite,
	}
	for _, c := range required {
		if !Can(Member, c) {
			t.Errorf("a salon member must hold %q to be able to work", c)
		}
	}
}

func TestCan_MemberCannotSetAnotherMembersPrices(t *testing.T) {
	// PP-3: the artist sets her own; only the owner may change a colleague's.
	if Can(Member, MemberServicesWrite) {
		t.Fatal("a member must not be able to change another member's services or prices")
	}
}

// TestCan_Owner_HoldsEverything states the soloist guarantee directly.
func TestCan_Owner_HoldsEverything(t *testing.T) {
	for _, c := range All() {
		if !Can(Owner, c) {
			t.Errorf("Owner lacks %q; every artist on the platform today is an "+
				"owner, so this removes a capability from a live account", c)
		}
	}
}

// ── Valid ──────────────────────────────────────────────────────────────────

func TestValid_KnownRoles_ReturnTrue(t *testing.T) {
	for _, r := range []Role{None, Owner, Member} {
		if !Valid(r) {
			t.Errorf("Valid(%q) = false", r)
		}
	}
}

func TestValid_UnknownRole_ReturnsFalse(t *testing.T) {
	if Valid(Role("owner ")) || Valid(Role("OWNER")) || Valid(Role("root")) {
		t.Fatal("Valid accepted a role this package does not issue")
	}
}

// ── All ────────────────────────────────────────────────────────────────────

func TestAll_ReturnsACopy(t *testing.T) {
	got := All()
	got[0] = Capability("tampered")
	if All()[0] == Capability("tampered") {
		t.Fatal("All() exposes the package's own slice; a caller can rewrite the policy")
	}
}
