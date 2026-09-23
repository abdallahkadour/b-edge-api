package otp

import (
	"testing"
	"time"
)

// The rule, at its boundaries. Check takes `now` as a parameter precisely so
// these can be written - a rule that reads the clock cannot be tested at the
// edge, and the edge is where expiry bugs live.

func valid(now time.Time, code string) Record {
	return Record{Hash: Hash(code), ExpiresAt: now.Add(Validity)}
}

func TestCheck_CorrectCodeWithinValidity_OK(t *testing.T) {
	now := time.Now().UTC()
	if got := Check(valid(now, "123456"), "123456", now); got != OK {
		t.Fatalf("expected OK, got %s", got)
	}
}

func TestCheck_WrongCode_WrongCode(t *testing.T) {
	now := time.Now().UTC()
	if got := Check(valid(now, "123456"), "123457", now); got != WrongCode {
		t.Fatalf("expected WrongCode, got %s", got)
	}
}

func TestCheck_AtTheExpiryInstant_StillValid(t *testing.T) {
	// The boundary. `now.After(ExpiresAt)` is exclusive, so a code submitted
	// at the exact expiry instant is still good. Flipping this to >= would
	// make a code unusable a moment before its stated lifetime is up, which
	// users experience as "it said 5 minutes and it didn't work".
	now := time.Now().UTC()
	rec := valid(now, "123456")
	if got := Check(rec, "123456", rec.ExpiresAt); got != OK {
		t.Fatalf("at the expiry instant the code must still work, got %s", got)
	}
}

func TestCheck_OneNanosecondPastExpiry_Expired(t *testing.T) {
	now := time.Now().UTC()
	rec := valid(now, "123456")
	if got := Check(rec, "123456", rec.ExpiresAt.Add(time.Nanosecond)); got != Expired {
		t.Fatalf("expected Expired, got %s", got)
	}
}

func TestCheck_AlreadyVerified_BeatsEverythingElse(t *testing.T) {
	// State before comparison. A used code is refused without the hash being
	// consulted, so the refusal costs the same whatever was submitted.
	now := time.Now().UTC()
	used := now.Add(-time.Minute)
	rec := valid(now, "123456")
	rec.VerifiedAt = &used

	if got := Check(rec, "123456", now); got != AlreadyUsed {
		t.Fatalf("a correct but already-used code must be AlreadyUsed, got %s", got)
	}
}

func TestCheck_AttemptsExhausted_BeatsACorrectCode(t *testing.T) {
	now := time.Now().UTC()
	rec := valid(now, "123456")
	rec.Attempts = MaxAttempts

	if got := Check(rec, "123456", now); got != TooManyAttempts {
		t.Fatalf("the correct code must not rescue an exhausted record, got %s", got)
	}
}

func TestCheck_OneAttemptRemaining_CorrectCodeStillWorks(t *testing.T) {
	// POSITIVE CONTROL for the test above. A guard that refused at
	// MaxAttempts-1 would satisfy it while costing a genuine user their last
	// legitimate try.
	now := time.Now().UTC()
	rec := valid(now, "123456")
	rec.Attempts = MaxAttempts - 1

	if got := Check(rec, "123456", now); got != OK {
		t.Fatalf("the last permitted attempt must be honoured, got %s", got)
	}
}

func TestGenerate_IsSixDigitsAndZeroPadded(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		code, err := Generate()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(code) != Length {
			t.Fatalf("code %q is %d chars, must be %d - a stripped leading zero "+
				"makes one code in ten fail the len=6 request validator", code, len(code), Length)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("code %q is not all digits", code)
			}
		}
		seen[code] = true
	}
	// Not a randomness test - just enough to catch a constant or a
	// catastrophically small range.
	if len(seen) < 400 {
		t.Fatalf("only %d distinct codes in 500 draws; generation looks degenerate", len(seen))
	}
}

func TestHash_IsStableAndNotThePlaintext(t *testing.T) {
	h := Hash("123456")
	if h == "123456" {
		t.Fatal("the plaintext code must never be what is stored")
	}
	if h != Hash("123456") {
		t.Fatal("hashing must be deterministic or no code could ever verify")
	}
	if Hash("123457") == h {
		t.Fatal("distinct codes must hash differently")
	}
}
