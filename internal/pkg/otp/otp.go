// Package otp owns the one-time-code rule: how a code is made, how it is
// stored, and the exact order in which a submitted code is judged.
//
// WHY THIS IS A LEAF PACKAGE
//
// Two domains need it. internal/customerauth logs a customer in; artist
// phone verification proves an artist controls the number a salon will invite
// them on. The outcomes are completely different - a session versus a
// timestamp - but the RULE is identical, and a rule that exists in two places
// is the defect class this codebase keeps producing. subscriptionVisibleCond
// lived in three files and two of them stayed wrong for a day.
//
// So the rule lives here, once, as a pure function with no database and no
// clock of its own. Storage stays with each domain, because the rows belong
// to different flows even though they share a table today.
//
// WHAT IS DELIBERATELY NOT HERE
//
// Rate limiting. It needs a COUNT over stored rows, which is a query, and
// pushing a query in here would mean inventing a storage interface that both
// domains then have to satisfy. The window and ceiling are declared here so
// the two callers cannot disagree about the numbers; the query stays theirs.
package otp

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"
)

const (
	// Length is the number of digits in a code. Six, which is also why the
	// dev bypass in internal/pkg/devbypass is 000000 rather than 0000 - the
	// request validators check len=6 and a shorter code never reaches
	// verification at all.
	Length = 6

	// Validity is how long a code is usable. Short, because the whole point
	// is proving possession of the handset right now.
	Validity = 5 * time.Minute

	// MaxAttempts is how many wrong guesses ONE code tolerates. At six
	// digits a brute force needs ~500k attempts on average; five closes that
	// without punishing a genuine typo.
	MaxAttempts = 5

	// RateLimitWindow and RateLimitMax bound how often a number may be sent
	// a new code. Declared here rather than in each caller so the two cannot
	// drift to different ceilings for the same phone.
	RateLimitWindow = 5 * time.Minute
	RateLimitMax    = 3
)

// Record is what storage must supply to judge a submission. Deliberately a
// plain struct rather than an interface: it is data, and an interface here
// would let a caller compute one of these fields on the fly.
type Record struct {
	Hash       string
	ExpiresAt  time.Time
	VerifiedAt *time.Time
	Attempts   int
}

// Result is the verdict. Each caller maps these onto its own error codes,
// because a customer logging in and an artist verifying a number are
// different conversations even when the reason is the same.
type Result int

const (
	OK Result = iota
	AlreadyUsed
	Expired
	TooManyAttempts
	WrongCode
)

// String makes a Result readable in a test failure or a log line.
func (r Result) String() string {
	switch r {
	case OK:
		return "OK"
	case AlreadyUsed:
		return "AlreadyUsed"
	case Expired:
		return "Expired"
	case TooManyAttempts:
		return "TooManyAttempts"
	case WrongCode:
		return "WrongCode"
	}
	return fmt.Sprintf("Result(%d)", int(r))
}

// Check judges a submitted code against a stored record.
//
// THE ORDER MATTERS and is part of the rule, not an implementation detail.
// State checks come before the comparison, so a code that is already used,
// expired, or out of attempts is refused WITHOUT the hash being consulted -
// which means those refusals cost the same whatever was submitted, and an
// attacker learns nothing from timing about a code they cannot use anyway.
//
// WrongCode is the only verdict a caller should respond to by incrementing
// the attempt counter. Incrementing on Expired or AlreadyUsed would let
// anyone burn down a victim's remaining attempts on a code that is already
// dead, which achieves nothing for them and nothing for us.
//
// `now` is a parameter because a rule that reads the clock cannot be tested
// at its boundaries, and the boundary is where expiry bugs live.
func Check(rec Record, submitted string, now time.Time) Result {
	if rec.VerifiedAt != nil {
		return AlreadyUsed
	}
	if now.After(rec.ExpiresAt) {
		return Expired
	}
	if rec.Attempts >= MaxAttempts {
		return TooManyAttempts
	}
	// Constant-time: the hashes are the same length, and comparing them with
	// == would leak how many leading bytes matched. That is not a practical
	// attack against a 5-minute six-digit code, but a cheap habit to keep -
	// the same reasoning already applied to token comparison elsewhere.
	if subtle.ConstantTimeCompare([]byte(Hash(submitted)), []byte(rec.Hash)) != 1 {
		return WrongCode
	}
	return OK
}

// Generate returns a cryptographically random code of Length digits.
//
// crypto/rand, not math/rand: a predictable OTP is not an OTP. Zero-padded,
// so "000123" stays six characters - the request validators check len=6 and
// a stripped leading zero would make one code in ten unusable.
func Generate() (string, error) {
	max := big.NewInt(1)
	for i := 0; i < Length; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}
	return fmt.Sprintf("%0*d", Length, n), nil
}

// Hash is what gets stored. The plaintext code never touches the database,
// so a dump of the OTP table cannot be replayed into anybody's account.
//
// Unsalted SHA-256 is adequate here and deliberately so: the input space is
// a million values but each code lives five minutes, is bound to one phone,
// and dies after five wrong guesses. A slow KDF would buy nothing against
// that and would be paid on every verification.
func Hash(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}
