// Package pricing is the one definition of what a customer pays for a
// service, as SQL.
//
// # WHY THIS EXISTS
//
// Since migration 052 a service's price depends on the artist: her override
// in artist_services if she set one, the salon's otherwise. A customer sees
// that number in four places - the discovery profile, the artist's public
// service list, the hold, and the booking. Four independent calculations is
// how "shown $100, charged $200" happens, and this codebase has already
// shipped that class three times (subscriptionVisibleCond, calendar_sequence,
// the cross-salon booking). So the calculation lives here, once, and
// noleak_test.go fails the build on the common shapes of any other SQL
// reading services.price or services.deposit_amount - in a single literal
// or assembled from constants with + or fmt.Sprintf. Its header lists the
// shapes it is known to miss.
//
// # WHY SQL EXPRESSIONS WITHOUT ALIASES
//
// Callers scan into existing structs whose column order is fixed (for
// example artist.scanServices). An unaliased expression can be dropped into
// any position; an aliased fragment of three columns could not.
//
// s is the alias of the services table, os of artist_services. With a LEFT
// JOIN and no row, os.* is NULL and every expression falls back to the
// salon's values - which is what the settings screen needs for a service
// she has not switched on.
package pricing

import "fmt"

// Price is the effective price: her override, else the salon's.
func Price(s, os string) string {
	return fmt.Sprintf("COALESCE(%[2]s.price, %[1]s.price)", s, os)
}

// Deposit is the effective deposit, capped at the effective price (PP-6).
// The cap only ever lowers what a customer pays upfront, and a booking must
// never fail because two numbers set by two people drifted apart.
func Deposit(s, os string) string {
	return fmt.Sprintf("LEAST(COALESCE(%[2]s.deposit_amount, %[1]s.deposit_amount), %[3]s)",
		s, os, Price(s, os))
}

// DepositCapped reports whether the cap in Deposit changed the value, so the
// settings screen can say so instead of silently showing a lower number.
func DepositCapped(s, os string) string {
	return fmt.Sprintf("(COALESCE(%[2]s.deposit_amount, %[1]s.deposit_amount) > %[3]s)",
		s, os, Price(s, os))
}

// Detail is the settings-screen row: salon price, salon deposit, own price,
// own deposit, effective price, effective deposit, deposit capped - in that
// order, which is part of the contract (TestDetail_ColumnOrderIsTheContract).
func Detail(s, os string) string {
	return fmt.Sprintf("%[1]s.price, %[1]s.deposit_amount, %[2]s.price, %[2]s.deposit_amount, %[3]s, %[4]s, %[5]s",
		s, os, Price(s, os), Deposit(s, os), DepositCapped(s, os))
}
