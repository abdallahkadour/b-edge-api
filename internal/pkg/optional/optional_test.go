// optional_test.go pins the three states, because collapsing them back into
// two is silent: the API returns 200 and simply does not do what was asked.
package optional

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type body struct {
	Bio Field[string] `json:"bio"`
}

// TestUnmarshal_ThreeStates_AreDistinguishable is the whole reason this
// package exists. With a *string, the first two rows are identical.
func TestUnmarshal_ThreeStates_AreDistinguishable(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantSet  bool
		wantNull bool
		wantVal  string
	}{
		{"absent", `{}`, false, false, ""},
		{"explicit null", `{"bio":null}`, true, true, ""},
		{"present", `{"bio":"hello"}`, true, false, "hello"},
		{"present but empty", `{"bio":""}`, true, false, ""},
	}
	for _, tc := range cases {
		var b body
		require.NoError(t, json.Unmarshal([]byte(tc.raw), &b), tc.name)

		assert.Equal(t, tc.wantSet, b.Bio.IsSet(), "%s: IsSet", tc.name)
		assert.Equal(t, tc.wantNull, b.Bio.IsNull(), "%s: IsNull", tc.name)

		got, ok := b.Bio.Get()
		assert.Equal(t, tc.wantVal, got, "%s: value", tc.name)
		assert.Equal(t, tc.wantSet && !tc.wantNull, ok, "%s: Get ok", tc.name)
	}
}

// TestPtr_AbsentAndNull_BothNil - and this is why Ptr must never be used
// without IsSet beside it. On its own it cannot tell the two apart, which is
// exactly the bug this package was written to fix.
func TestPtr_AbsentAndNull_BothNil(t *testing.T) {
	var absent, null body
	require.NoError(t, json.Unmarshal([]byte(`{}`), &absent))
	require.NoError(t, json.Unmarshal([]byte(`{"bio":null}`), &null))

	assert.Nil(t, absent.Bio.Ptr())
	assert.Nil(t, null.Bio.Ptr())

	// The distinction survives only in IsSet.
	assert.False(t, absent.Bio.IsSet())
	assert.True(t, null.Bio.IsSet())
}

// TestText_EmptyAndWhitespace_BecomeNil is audit risk R2: a form submits ""
// for an emptied input, and an empty string and NULL both mean "no bio" to a
// person, while differing to `WHERE bio IS NULL`.
func TestText_EmptyAndWhitespace_BecomeNil(t *testing.T) {
	for _, raw := range []string{`{"bio":""}`, `{"bio":"   "}`, `{"bio":"\t\n"}`} {
		var b body
		require.NoError(t, json.Unmarshal([]byte(raw), &b), raw)
		assert.True(t, b.Bio.IsSet(), "%s: still counts as sent", raw)
		assert.Nil(t, Text(b.Bio), "%s: should normalise to nil", raw)
	}
}

func TestText_RealValue_IsPreserved(t *testing.T) {
	var b body
	require.NoError(t, json.Unmarshal([]byte(`{"bio":"  hello  "}`), &b))
	got := Text(b.Bio)
	require.NotNil(t, got)
	// Trimmed only for the emptiness TEST, never for the stored value -
	// trimming user text is a separate decision this package does not make.
	assert.Equal(t, "  hello  ", *got)
}

// TestUnmarshal_WrongType_ReturnsTypeError keeps validation.MapBodyError able
// to name the offending field: it depends on the error still being a
// *json.UnmarshalTypeError after passing through this UnmarshalJSON.
func TestUnmarshal_WrongType_ReturnsTypeError(t *testing.T) {
	var b body
	err := json.Unmarshal([]byte(`{"bio":123}`), &b)
	require.Error(t, err)

	var typeErr *json.UnmarshalTypeError
	require.ErrorAs(t, err, &typeErr, "must stay an UnmarshalTypeError")
	assert.Equal(t, "bio", typeErr.Field, "must still name the field")
}

// TestValidationValue_MirrorsGet - validation.New registers a custom type func
// that calls this. If it stopped returning nil for absent/null, `omitempty`
// would start rejecting fields that were never sent.
func TestValidationValue_MirrorsGet(t *testing.T) {
	var absent, null, present body
	require.NoError(t, json.Unmarshal([]byte(`{}`), &absent))
	require.NoError(t, json.Unmarshal([]byte(`{"bio":null}`), &null))
	require.NoError(t, json.Unmarshal([]byte(`{"bio":"x"}`), &present))

	assert.Nil(t, absent.Bio.ValidationValue())
	assert.Nil(t, null.Bio.ValidationValue())
	assert.Equal(t, "x", present.Bio.ValidationValue())
}

func TestConstructors_MatchUnmarshalledState(t *testing.T) {
	set := From("hi")
	assert.True(t, set.IsSet())
	assert.False(t, set.IsNull())
	v, ok := set.Get()
	assert.True(t, ok)
	assert.Equal(t, "hi", v)

	null := Null[string]()
	assert.True(t, null.IsSet())
	assert.True(t, null.IsNull())

	var zero Field[string]
	assert.False(t, zero.IsSet(), "the zero value must mean absent")
}

func TestMarshal_RoundTrips(t *testing.T) {
	out, err := json.Marshal(body{Bio: From("hello")})
	require.NoError(t, err)
	assert.JSONEq(t, `{"bio":"hello"}`, string(out))

	out, err = json.Marshal(body{Bio: Null[string]()})
	require.NoError(t, err)
	assert.JSONEq(t, `{"bio":null}`, string(out))
}
