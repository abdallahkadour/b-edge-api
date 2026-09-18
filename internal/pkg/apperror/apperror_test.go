package apperror

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This package decides what status code every failure in the API becomes, and
// had no tests at all. The constructors are one line each, which is exactly
// why a missing one goes unnoticed: TooManyRequests did not exist, so the OTP
// limiter reached for BadRequest and a throttled client got a 400 for two
// releases. These tests pin the mapping so the next gap is a failure, not a
// silently wrong status.

func TestConstructors_MapToTheirStatusCodes(t *testing.T) {
	cases := []struct {
		name string
		err  *AppError
		want int
	}{
		{"BadRequest", BadRequest("C", "m"), http.StatusBadRequest},
		{"Unauthorized", Unauthorized("C", "m"), http.StatusUnauthorized},
		{"Forbidden", Forbidden("C", "m"), http.StatusForbidden},
		{"NotFound", NotFound("C", "m"), http.StatusNotFound},
		{"Conflict", Conflict("C", "m"), http.StatusConflict},
		{"TooManyRequests", TooManyRequests("C", "m"), http.StatusTooManyRequests},
		{"Internal", Internal("C", "m"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.err.HTTPStatus, tc.name)
		assert.Equal(t, "C", tc.err.Code, tc.name)
		assert.Equal(t, "m", tc.err.Message, tc.name)
	}
}

// Error() must return the message, because that is what surfaces in logs and
// in wrapped errors. Returning the code instead would be a plausible mistake
// and would make every log line useless.
func TestError_ReturnsTheMessage(t *testing.T) {
	assert.Equal(t, "something went wrong", BadRequest("CODE", "something went wrong").Error())
}

// errors.As is how callers recover the status. If AppError stopped satisfying
// it, every handler would fall through to a 500 while still compiling.
func TestAppError_IsRecoverableWithErrorsAs(t *testing.T) {
	wrapped := errors.Join(errors.New("context"), NotFound("GONE", "not here"))

	var appErr *AppError
	require.ErrorAs(t, wrapped, &appErr)
	assert.Equal(t, http.StatusNotFound, appErr.HTTPStatus)
	assert.Equal(t, "GONE", appErr.Code)
}

// The error handler logs at 404 and above. Rate limiting has to fall inside
// that window or abuse leaves no trace - which is precisely what happened
// while it was a 400.
func TestTooManyRequests_IsWithinTheLoggedRange(t *testing.T) {
	assert.GreaterOrEqual(t, TooManyRequests("RATE_LIMITED", "slow down").HTTPStatus,
		http.StatusNotFound,
		"429 must be >= 404 or the error handler will not log it")
}

func TestUnprocessableEntity_CarriesFieldDetails(t *testing.T) {
	err := UnprocessableEntity("VALIDATION", []FieldError{{Field: "price", Message: "required"}})

	assert.Equal(t, http.StatusUnprocessableEntity, err.HTTPStatus)
	require.Len(t, err.Details, 1)
	assert.Equal(t, "price", err.Details[0].Field)
}
