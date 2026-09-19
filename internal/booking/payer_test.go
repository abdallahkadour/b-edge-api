package booking

import "testing"

func strp(s string) *string { return &s }

// The mismatch check decides whether a refund is blocked, so its edges are
// worth naming individually rather than table-driving them.

func TestDepositPayerMismatch_SameNumberDifferentFormat_NoMismatch(t *testing.T) {
	// The customer typed a local number at booking; the artist read E.164 off
	// a transfer receipt. Same human, same wallet - a warning here would fire
	// on correct data, which is how warnings get trained away.
	if depositPayerMismatch(strp("+96170123456"), strp("70123456")) {
		t.Fatal("same number in two formats must not be a mismatch")
	}
	if depositPayerMismatch(strp("0096170123456"), strp("70 123 456")) {
		t.Fatal("00-prefix and spaced local form must not be a mismatch")
	}
}

func TestDepositPayerMismatch_DifferentNumber_IsMismatch(t *testing.T) {
	if !depositPayerMismatch(strp("+96171999888"), strp("70123456")) {
		t.Fatal("genuinely different numbers must be a mismatch")
	}
}

func TestDepositPayerMismatch_NoPayerRecorded_NoMismatch(t *testing.T) {
	// Every booking predating migration 044 is in this state. Treating
	// "nobody recorded it" as "they differ" would raise the alarm on all of
	// them and the artist would learn to dismiss it.
	if depositPayerMismatch(nil, strp("70123456")) {
		t.Fatal("an unrecorded payer must not be reported as a mismatch")
	}
	if depositPayerMismatch(strp(""), strp("70123456")) {
		t.Fatal("an empty payer must not be reported as a mismatch")
	}
}

func TestDepositPayerMismatch_NoCustomerPhone_NoMismatch(t *testing.T) {
	if depositPayerMismatch(strp("+96170123456"), nil) {
		t.Fatal("a missing customer phone must not be reported as a mismatch")
	}
}

func TestDepositPayerMismatch_Unparseable_NoMismatch(t *testing.T) {
	// "Cannot tell" must not masquerade as "these differ". The artist still
	// sees both numbers on screen and can judge for themselves.
	if depositPayerMismatch(strp("not-a-number"), strp("70123456")) {
		t.Fatal("an unparseable payer must not be reported as a mismatch")
	}
}

func TestDepositPayerMismatch_ForeignNumber_IsMismatch(t *testing.T) {
	// A UAE wallet paying for a Beirut appointment is exactly the case the
	// refund gate exists for: the money cannot come back to a Lebanese number.
	if !depositPayerMismatch(strp("+971501234567"), strp("70123456")) {
		t.Fatal("a foreign payer number must be a mismatch")
	}
}
