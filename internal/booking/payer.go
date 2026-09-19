package booking

import "github.com/abdallahkadour/b-edge-api/internal/pkg/phone"

// depositPayerMismatch reports whether a deposit demonstrably arrived from a
// number other than the customer's own.
//
// # WHY THIS MATTERS
//
// B-Edge holds no money. Deposits move customer-to-artist over OMT and Whish,
// both addressed by phone number, and a refund is that same transfer in
// reverse. If the deposit came from a spouse's wallet or an OMT counter, a
// refund pushed to the booking's own number does not reach the person owed
// it - and these rails have no chargeback to undo it with.
//
// # WHY IT NORMALISES RATHER THAN COMPARING STRINGS
//
// The two numbers reach the database by different routes. The customer's is
// whatever they typed at booking; the payer's is read off a transfer receipt
// by the artist. "70 123 456", "+96170123456" and "0096170123456" are one
// number written three ways, and a raw string comparison would call every one
// of them a mismatch - which is worse than not checking, because a warning
// that fires on correct data is a warning people learn to click through.
// Migration 043 normalised the stored phones for exactly this reason.
//
// # WHY UNKNOWN IS NOT A MISMATCH
//
// A nil payer means nobody recorded one, which is NOT evidence that the
// numbers differ. Every booking predating migration 044 is in that state.
// Treating unknown as a mismatch would raise the alarm on all of them and
// train the artist to dismiss it before a real one ever appeared.
//
// An unparseable number on either side returns false for the same reason: the
// honest answer is "cannot tell", and "cannot tell" must not masquerade as
// "these differ". The artist still sees both numbers and can judge.
func depositPayerMismatch(payerPhone, customerPhone *string) bool {
	if payerPhone == nil || customerPhone == nil {
		return false
	}
	if *payerPhone == "" || *customerPhone == "" {
		return false
	}

	// Lebanon is the default country for a bare local number, matching every
	// other phone entry point in the system.
	payer, err := phone.Normalize(*payerPhone, "LB")
	if err != nil {
		return false
	}
	customer, err := phone.Normalize(*customerPhone, "LB")
	if err != nil {
		return false
	}
	return payer != customer
}
