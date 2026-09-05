// Package validation turns bad request bodies into the one error shape the
// frontend knows how to render.
//
// # WHY THIS EXISTS
//
// Two problems, both found by the cross-layer audit on 2026-09-05
// (project-docs/B-Edge-Cross-Layer-Data-Flow-Audit-v1.md, risks R5 and R6).
//
// FIRST: mapValidationError had TEN copies, one per domain, and they had
// already drifted into FOUR distinct variants. A rule duplicated ten times is
// ten places to miss, and the drift was invisible because each copy was
// individually correct. CLAUDE.md states the rule this violates: "When two
// domains need the same rule, it goes in a leaf package, not a copy."
//
// SECOND: the API answered "your input was bad" in two shapes, and only one of
// them could be attached to a field:
//
//	422  VALIDATION_ERROR  + details[{field, message}]
//	400  INVALID_BODY      + nothing
//
// A type mismatch produced the 400, so a form could only ever show it as a
// banner - and the banner said "Please check the highlighted fields" while
// having no field to highlight. MapBodyError fixes that: encoding/json's
// UnmarshalTypeError already carries the offending field name, and it was
// being thrown away.
//
// # THE FIELD NAME IS THE JSON KEY, NOT THE GO FIELD
//
// validator's FieldError.Field() returns the STRUCT field name, so the API was
// emitting {"field":"DepositAmount"} for a body key of "deposit_amount". Any
// frontend mapping errors onto inputs by key silently missed every multi-word
// field. New() registers a tag-name function so Field() returns the json tag
// instead.
//
// Messages are humanised separately - "Deposit amount is required", not
// "deposit_amount is required" and not "DepositAmount is required". The field
// is for machines, the message is for people, and they should not be the same
// string.
package validation

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/optional"
)

// validationValuer is implemented by optional.Field[T]. Declared here rather
// than imported so the coupling is one-way: optional knows nothing about
// validation.
type validationValuer interface {
	ValidationValue() any
}

// New returns the validator every service should use.
//
// The only configuration is the tag-name function, and it is the reason this
// constructor exists rather than calling validator.New() directly: a service
// that constructs its own validator silently goes back to emitting Go field
// names, and nothing fails to make that visible.
func New() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		// The json tag minus its options: `json:"deposit_amount,omitempty"`
		// yields "deposit_amount".
		name, _, _ := strings.Cut(fld.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			// No json tag, or explicitly not serialised. Fall back to the Go
			// name rather than reporting an empty field.
			return fld.Name
		}
		return name
	})

	// Validate the value INSIDE an optional.Field, not the struct wrapping
	// it. Without this, a tag like `max=500` on a Field[string] is applied to
	// the struct, matches nothing, and silently never fails - a validation
	// rule that looks present and does nothing is worse than no rule.
	v.RegisterCustomTypeFunc(func(field reflect.Value) any {
		if vv, ok := field.Interface().(validationValuer); ok {
			return vv.ValidationValue()
		}
		return nil
	}, optional.Field[string]{}, optional.Field[int]{}, optional.Field[bool]{})

	return v
}

// MapError converts a validator error into a 422 carrying one entry per
// offending field.
//
// A non-validator error is returned as a 400 rather than being dressed up as
// a field problem - it means validation itself failed, which is a different
// thing from the input being wrong.
func MapError(err error) error {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return apperror.BadRequest("VALIDATION_ERROR", err.Error())
	}

	details := make([]apperror.FieldError, 0, len(ve))
	for _, fe := range ve {
		details = append(details, apperror.FieldError{
			Field:   fe.Field(), // json key, via New's tag-name func
			Message: fieldMessage(fe),
		})
	}
	return apperror.UnprocessableEntity("VALIDATION_ERROR", details)
}

// MapBodyError converts a JSON decoding failure into the most specific error
// the failure supports.
//
// A type mismatch names its field and becomes a 422, so the frontend can
// highlight the input the user actually got wrong. Everything else - malformed
// syntax, a truncated body, an empty body - is genuinely unattributable and
// stays a 400.
func MapBodyError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		// typeErr.Field is the json path ("price", or "items[0].qty"), and
		// typeErr.Type is what the struct wanted.
		return apperror.UnprocessableEntity("VALIDATION_ERROR", []apperror.FieldError{{
			Field:   typeErr.Field,
			Message: humanise(typeErr.Field) + " must be " + article(typeErr.Type.String()),
		}})
	}
	return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
}

// fieldMessage renders one validator failure for a human.
//
// Consolidated from the four variants that existed across the ten copies.
// Cases absent from every copy are folded into the default deliberately: a
// vague "X is invalid" is better than a wrong specific message, and the tags
// actually in use are covered.
func fieldMessage(fe validator.FieldError) string {
	name := humanise(fe.Field())
	switch fe.Tag() {
	case "required":
		return name + " is required"
	case "min":
		return name + " must be at least " + fe.Param()
	case "max":
		return name + " must be at most " + fe.Param() + " characters"
	case "email":
		return name + " must be a valid email address"
	case "uuid", "uuid4":
		return name + " must be a valid UUID"
	case "oneof":
		return name + " must be one of: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	case "len":
		return name + " must be exactly " + fe.Param() + " characters"
	case "e164":
		return name + " must be a valid phone number"
	case "url":
		return name + " must be a valid URL"
	case "numeric":
		return name + " must be a number"
	default:
		return name + " is invalid"
	}
}

// humanise turns a json key into something worth showing a person:
// "deposit_amount" becomes "Deposit amount".
func humanise(jsonKey string) string {
	// A nested path ("items[0].qty") reads better as its last segment.
	if i := strings.LastIndex(jsonKey, "."); i >= 0 && i+1 < len(jsonKey) {
		jsonKey = jsonKey[i+1:]
	}
	s := strings.ReplaceAll(jsonKey, "_", " ")
	if s == "" {
		return "This field"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// article prefixes a Go type name with the right indefinite article, so the
// message reads "must be a number" rather than "must be number".
func article(goType string) string {
	switch goType {
	case "string":
		return "text"
	case "int", "int32", "int64", "float64":
		return "a number"
	case "bool":
		return "true or false"
	default:
		return "a valid " + goType
	}
}
