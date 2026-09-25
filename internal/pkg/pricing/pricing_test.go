package pricing

import (
	"strings"
	"testing"
)

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
	cols := strings.Split(Detail("sv", "o"), ", ")
	if len(cols) < 7 {
		t.Fatalf("Detail must yield 7 columns, got %d: %q", len(cols), Detail("sv", "o"))
	}
	if cols[0] != "sv.price" || cols[1] != "sv.deposit_amount" ||
		cols[2] != "o.price" || cols[3] != "o.deposit_amount" {
		t.Fatalf("first four columns must be salon price, salon deposit, own price, own deposit; got %q", cols[:4])
	}
}
