package booking

// The booking horizon.
//
// validateBookingTime bounds a requested start time at both ends. Before it
// existed the two entry points disagreed about what the rules even were: the
// guest hold path rejected the past and nothing rejected the far future, while
// CreateBooking checked NEITHER - it parsed the timestamp and went straight to
// the insert. A hold 365 days out returned 201, and so did 305. Measured
// 2026-09-23.
//
// The horizon is deliberately GENEROUS. Bridal is booked a year or more ahead
// and is the highest-value work on the platform - the launch artist takes
// wedding bookings 11-12 months out. A test that pinned a tight bound would be
// encoding the same mistake the 90-day picker made, which was to pick a number
// that sounded far away without checking it against what the business sells.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

func TestValidateBookingTime_InThePast_Rejected(t *testing.T) {
	err := validateBookingTime(time.Now().UTC().Add(-1 * time.Hour))

	require.Error(t, err, "a past start time must be rejected")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr, "must be a typed error")
	assert.Equal(t, "BOOKING_IN_PAST", appErr.Code)
}

func TestValidateBookingTime_OneYearAhead_Accepted(t *testing.T) {
	// The measured case. If this ever fails, the horizon has been tightened
	// to a number that refuses the launch artist's most valuable bookings.
	err := validateBookingTime(time.Now().UTC().Add(365 * 24 * time.Hour))

	assert.NoError(t, err, "a booking one year out must be accepted - bridal is booked that far ahead")
}

func TestValidateBookingTime_JustInsideHorizon_Accepted(t *testing.T) {
	err := validateBookingTime(time.Now().UTC().Add(MaxBookingHorizon - 24*time.Hour))

	assert.NoError(t, err, "one day inside the bound must be accepted")
}

func TestValidateBookingTime_BeyondHorizon_Rejected(t *testing.T) {
	err := validateBookingTime(time.Now().UTC().Add(MaxBookingHorizon + 48*time.Hour))

	require.Error(t, err, "a start time beyond the horizon must be rejected")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr, "must be a typed error")
	assert.Equal(t, "BOOKING_TOO_FAR_AHEAD", appErr.Code)
}

func TestValidateBookingTime_RejectionNamesTheLimit(t *testing.T) {
	// A client has to be able to tell the customer something true.
	// "Invalid date" is not something true.
	err := validateBookingTime(time.Now().UTC().Add(MaxBookingHorizon + 48*time.Hour))

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "550"),
		"the rejection must name the limit in days, got %q", err.Error())
}
