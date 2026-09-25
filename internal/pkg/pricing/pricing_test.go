package pricing

import "testing"

func TestPrice_OverrideBeforeSalon(t *testing.T) {
	// Breaks if the COALESCE order is reversed - the salon price would win
	// over every artist's override and the feature would silently do nothing.
	if got := Price("sv", "o"); got != "COALESCE(o.price, sv.price)" {
		t.Fatalf("got %q", got)
	}
}

func TestDeposit_IsCappedAtTheEffectivePrice(t *testing.T) {
	// PP-6: a deposit above the price makes a booking unstorable
	// (bookings_deposit_not_above_price). The cap is what prevents that.
	want := "LEAST(COALESCE(o.deposit_amount, sv.deposit_amount), COALESCE(o.price, sv.price))"
	if got := Deposit("sv", "o"); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestDetail_ColumnOrderIsTheContract(t *testing.T) {
	// The exact string below IS the contract: callers scan these seven
	// columns positionally (salon price, salon deposit, own price, own
	// deposit, effective price, effective deposit, deposit capped), so a
	// column dropped, duplicated, or reordered must fail this test. The
	// expected value is written out by hand, not built by calling
	// Price/Deposit/DepositCapped - doing that would make this a mirror of
	// Detail's own implementation, which passes no matter what Detail does.
	want := "sv.price, sv.deposit_amount, o.price, o.deposit_amount, " +
		"COALESCE(o.price, sv.price), " +
		"LEAST(COALESCE(o.deposit_amount, sv.deposit_amount), COALESCE(o.price, sv.price)), " +
		"(COALESCE(o.deposit_amount, sv.deposit_amount) > COALESCE(o.price, sv.price))"
	if got := Detail("sv", "o"); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
