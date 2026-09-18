package phone

import (
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// Parse normalises a submitted phone number to E.164, or returns a 400 the
// person who typed it can act on.
//
// Mirrors money.Parse deliberately: one function at the boundary, a named
// field in the error, and a canonical value from then on. The two problems are
// the same shape - a string that looks fine, is silently accepted, and breaks
// something downstream that nobody connects back to the form.
//
// The error text names the country and the expected length rather than saying
// "invalid phone number", because the commonest real mistake here is a correct
// number for a country the form did not expect.
func Parse(raw, defaultISO, field string) (string, error) {
	normalized, err := Normalize(raw, defaultISO)
	if err != nil {
		return "", apperror.UnprocessableEntity("VALIDATION_ERROR", []apperror.FieldError{{
			Field:   field,
			Message: err.Error(),
		}})
	}
	return normalized, nil
}

// ParseOptional is Parse for a nullable field. A nil pointer stays nil; an
// empty string is treated as absent rather than as an error, matching how
// optional.Text folds blank input elsewhere in this codebase.
func ParseOptional(raw *string, defaultISO, field string) (*string, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	normalized, err := Parse(*raw, defaultISO, field)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}
