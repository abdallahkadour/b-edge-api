// Package optional carries the third state JSON has and Go does not.
//
// # THE PROBLEM
//
// A PATCH body has three meanings for any field:
//
//	{}                       leave it alone
//	{"bio": null}            clear it
//	{"bio": "hello"}         set it
//
// A `*string` can only hold two of them. encoding/json cannot distinguish an
// absent key from an explicit null - both leave the pointer nil - so the
// middle case was unreachable. Combined with the repository's
// `COALESCE($n, col)` pattern, which treats NULL as "keep the current value",
// the effect was that **no nullable column could be set back to NULL through
// the API**. Sending null silently did nothing and returned 200.
//
// Measured and written up as risk R1 in
// project-docs/B-Edge-Cross-Layer-Data-Flow-Audit-v1.md.
//
// The project had already met this once. `clear_location` on the store update
// exists, in the documentation index's own words, "because COALESCE cannot
// express remove" - a per-field boolean flag, invented for exactly this and
// solving it for exactly one field. This type is that idea generalised, so the
// next clearable field does not need its own flag.
//
// # WHY NOT JUST USE json.RawMessage EVERYWHERE
//
// It works and needs no new type, but it pushes decoding into every service
// method and gives the validator nothing to inspect. Field[T] decodes once,
// keeps the distinction in the type, and - via validation.New's custom type
// func - still validates the inner value, so `validate:"omitempty,max=500"`
// keeps working unchanged on the field it is written on.
//
// # SCOPE - DELIBERATELY NARROW
//
// Not every nullable column needs this. Most are never cleared in practice,
// and converting them all would be churn for no behaviour. Use Field[T] where
// a user can legitimately remove something they previously entered: a bio, an
// Instagram handle, a service description. Leave `clear_location` alone; it
// works, and rewriting it buys nothing.
package optional

import (
	"encoding/json"
	"strings"
)

// Field is a value that may be absent, explicitly null, or present.
//
// The zero value is "absent", which is what an omitted JSON key must produce -
// UnmarshalJSON is never called for a key that is not in the body, so the
// zero value has to be the correct answer for that case.
type Field[T any] struct {
	set   bool
	value *T // nil means the key was present and explicitly null
}

// UnmarshalJSON records that the key was present, then decodes it.
//
// Pointer receiver because it mutates; encoding/json addresses struct fields,
// so this is called for `Bio Field[string]` as expected.
func (f *Field[T]) UnmarshalJSON(data []byte) error {
	f.set = true

	// A literal null is a real instruction ("clear this"), not an absence.
	if string(data) == "null" {
		f.value = nil
		return nil
	}

	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		// Returned as-is so it stays a *json.UnmarshalTypeError and
		// validation.MapBodyError can still name the offending field.
		return err
	}
	f.value = &v
	return nil
}

// MarshalJSON keeps round-tripping honest: absent and null both render as
// null, since a response has no way to express "absent" for a present key.
func (f Field[T]) MarshalJSON() ([]byte, error) {
	if f.value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*f.value)
}

// IsSet reports whether the key appeared in the request body at all.
//
// This is the half of the pair the SQL needs: a column is only touched when
// its field was sent.
func (f Field[T]) IsSet() bool { return f.set }

// IsNull reports whether the key was present AND explicitly null - the
// "clear it" instruction.
func (f Field[T]) IsNull() bool { return f.set && f.value == nil }

// Ptr is the value to bind to SQL: nil for both absent and null.
//
// Always pair it with IsSet in the statement, or the two states collapse
// again and this type has bought nothing:
//
//	SET bio = CASE WHEN $1 THEN $2 ELSE bio END   -- $1 = IsSet, $2 = Ptr
func (f Field[T]) Ptr() *T { return f.value }

// Get returns the value and whether one is present.
func (f Field[T]) Get() (T, bool) {
	var zero T
	if f.value == nil {
		return zero, false
	}
	return *f.value, true
}

// Text is Ptr for a string field, with empty and whitespace-only values
// normalised to nil.
//
// This is risk R2 from the same audit: `""` and NULL both mean "no bio" to a
// person, but they are different to `WHERE bio IS NULL`, and a form submits
// `""` for an emptied input rather than null. Collapsing them at the boundary
// means the database holds one spelling of empty instead of two.
//
// Only for fields where empty genuinely means absent. A column where an
// empty string is a meaningful distinct value must use Ptr instead.
func Text(f Field[string]) *string {
	if f.value == nil {
		return nil
	}
	if strings.TrimSpace(*f.value) == "" {
		return nil
	}
	return f.value
}

// ValidationValue exposes the inner value to the validator.
//
// Without this, `validate:"omitempty,max=500"` on a Field[string] would be
// applied to the STRUCT rather than the string inside it, and would silently
// never fail. validation.New registers a custom type func that calls this;
// the two must stay in step, which is why the method is exported and
// documented rather than being an internal detail.
//
// Returns nil for both absent and null, which is exactly what `omitempty`
// expects to see for "nothing to validate".
func (f Field[T]) ValidationValue() any {
	if f.value == nil {
		return nil
	}
	return *f.value
}

// From builds a set, non-null Field - for tests and for constructing requests
// in Go rather than from JSON.
func From[T any](v T) Field[T] { return Field[T]{set: true, value: &v} }

// Null builds a set, explicitly-null Field.
func Null[T any]() Field[T] { return Field[T]{set: true} }
