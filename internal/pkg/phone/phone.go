// Package phone parses, validates and normalises subscriber numbers for the
// countries B-Edge operates in.
//
// # WHY THIS EXISTS
//
// Two defects made it necessary, both found by audit rather than by anything
// failing:
//
//  1. THE API DID NOT VALIDATE PHONE FORMAT AT ALL. The only rule was
//     `validate:"required,min=7,max=20"`, which is a LENGTH check on a string -
//     "aaaaaaa" passed it. The frontend had a real Lebanese pattern, but the
//     frontend is not the guarantee.
//
//  2. STORAGE WAS INCONSISTENT AND UNSENDABLE. 24 of 29 user rows held bare
//     local digits ("71900001") and 5 held some form of + prefix. The
//     notification worker does `"whatsapp:" + storedValue` with no
//     normalisation, so Twilio would have been handed `whatsapp:71900001` -
//     not a valid destination. That is a silent total failure of every
//     notification to those users, invisible today only because WhatsApp
//     delivery is still blocked on Meta verification.
//
// The fix for both is one function that turns whatever a human typed into
// E.164, and refuses what it cannot.
//
// # WHY MENA AND NOT "ANY INTERNATIONAL NUMBER"
//
// B-Edge was Lebanon-only because deposits move over OMT and Whish, which are
// Lebanese rails. That constraint is real and has not gone away - but it is a
// constraint on PAYMENT, not on reachability, and the two were conflated. A
// Gulf client booking a Beirut artist can pay in person; refusing their phone
// number at the form turns a reachable customer into a lost one.
//
// So: the countries listed here, not the world. libphonenumber would be the
// answer for genuinely global coverage, and this deliberately is not it -
// a 300KB dependency and a monthly metadata update is the wrong trade for
// eleven countries whose numbering plans have been stable for decades.
package phone

import (
	"fmt"
	"strings"
)

// Country is one supported numbering plan.
type Country struct {
	// ISO 3166-1 alpha-2, which is what a UI country picker keys on.
	ISO string
	// Name as a human would recognise it.
	Name string
	// CallingCode without the leading +.
	CallingCode string
	// NationalLengths are the valid digit counts AFTER the calling code.
	// A set rather than a single number because several of these plans have
	// both 7- and 8-digit subscriber numbers still in service.
	NationalLengths []int
	// MobilePrefixes are the leading digits of the national number that
	// belong to mobile ranges. Empty means "not enforced" - see Validate.
	MobilePrefixes []string
}

// supported is every numbering plan B-Edge accepts.
//
// Lebanon first because it is the home market and the default.
//
// MobilePrefixes are populated only where the mobile ranges are both stable
// and well documented. Where they are not, the slice is left empty and only
// length is enforced: a wrong rejection is worse than a permissive accept,
// because the person on the other end cannot argue with a form.
var supported = []Country{
	{"LB", "Lebanon", "961", []int{7, 8}, []string{"3", "70", "71", "76", "78", "79", "81"}},
	{"AE", "United Arab Emirates", "971", []int{9}, []string{"50", "52", "54", "55", "56", "58"}},
	{"SA", "Saudi Arabia", "966", []int{9}, []string{"5"}},
	{"QA", "Qatar", "974", []int{8}, []string{"3", "5", "6", "7"}},
	{"KW", "Kuwait", "965", []int{8}, []string{"5", "6", "9"}},
	{"BH", "Bahrain", "973", []int{8}, []string{"3"}},
	{"OM", "Oman", "968", []int{8}, []string{"7", "9"}},
	{"JO", "Jordan", "962", []int{9}, []string{"7"}},
	{"EG", "Egypt", "20", []int{10}, []string{"10", "11", "12", "15"}},
	{"IQ", "Iraq", "964", []int{10}, []string{"7"}},
	{"SY", "Syria", "963", []int{9}, []string{"9"}},
}

// DefaultISO is the country assumed when a caller supplies a bare national
// number with no country context. Lebanon: the home market, and what every
// number already in the database is.
const DefaultISO = "LB"

// Countries returns the supported numbering plans, for a UI picker.
func Countries() []Country {
	out := make([]Country, len(supported))
	copy(out, supported)
	return out
}

// ByISO finds a country by its ISO code.
func ByISO(iso string) (Country, bool) {
	iso = strings.ToUpper(strings.TrimSpace(iso))
	for _, c := range supported {
		if c.ISO == iso {
			return c, true
		}
	}
	return Country{}, false
}

// digitsOnly strips everything that is not a digit.
//
// People type "+961 71 900 001", "00961-71-900-001" and "(03) 123 456". All
// three are the same number and all three should be accepted; the separators
// carry no information.
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Normalize turns human input into E.164, or explains why it cannot.
//
// defaultISO is used only when the input carries no country information at
// all. An input that starts with + or 00 always wins over it, so a Jordanian
// number typed into a form defaulting to Lebanon is still read as Jordanian.
//
// The returned string always begins with '+' and contains nothing else but
// digits - which is exactly what Twilio's `whatsapp:` destination requires.
func Normalize(raw, defaultISO string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("phone number is empty")
	}

	// "00" is the international prefix across all of these countries and is
	// how people actually dial abroad here; treat it as a synonym for "+".
	hasIntl := strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "00")
	digits := digitsOnly(trimmed)
	if strings.HasPrefix(trimmed, "00") {
		digits = strings.TrimPrefix(digits, "00")
	}
	if digits == "" {
		return "", fmt.Errorf("phone number contains no digits")
	}

	if hasIntl {
		// Longest calling code first, so 961 is never shadowed by a shorter
		// prefix that happens to match its opening digits.
		for _, c := range byCallingCodeLengthDesc() {
			if strings.HasPrefix(digits, c.CallingCode) {
				national := strings.TrimPrefix(digits, c.CallingCode)
				return c.build(national)
			}
		}
		return "", fmt.Errorf("that country is not supported yet")
	}

	country, ok := ByISO(defaultISO)
	if !ok {
		country, _ = ByISO(DefaultISO)
	}
	// A national number written with a trunk '0' - "03 123456" - is the same
	// subscriber as "3 123456". The trunk digit is a domestic dialling
	// convention and never appears in E.164.
	national := strings.TrimPrefix(digits, "0")
	return country.build(national)
}

func byCallingCodeLengthDesc() []Country {
	out := make([]Country, len(supported))
	copy(out, supported)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && len(out[j].CallingCode) > len(out[j-1].CallingCode); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (c Country) build(national string) (string, error) {
	if err := c.validateNational(national); err != nil {
		return "", err
	}
	return "+" + c.CallingCode + national, nil
}

// validateNational checks length, then mobile range where one is defined.
func (c Country) validateNational(national string) error {
	okLen := false
	for _, n := range c.NationalLengths {
		if len(national) == n {
			okLen = true
			break
		}
	}
	if !okLen {
		return fmt.Errorf("a %s number needs %s digits after +%s, got %d",
			c.Name, joinInts(c.NationalLengths), c.CallingCode, len(national))
	}

	// Enforced only where the ranges are documented and stable. A landline
	// cannot receive a WhatsApp message, so accepting one produces a booking
	// whose confirmation silently never arrives - the failure this package
	// exists to prevent.
	if len(c.MobilePrefixes) > 0 {
		for _, p := range c.MobilePrefixes {
			if strings.HasPrefix(national, p) {
				return nil
			}
		}
		return fmt.Errorf("that does not look like a %s mobile number", c.Name)
	}
	return nil
}

func joinInts(ns []int) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprint(n))
	}
	return strings.Join(parts, " or ")
}

// IsE164 reports whether s is already in the canonical stored form.
func IsE164(s string) bool {
	if !strings.HasPrefix(s, "+") || len(s) < 8 {
		return false
	}
	return digitsOnly(s) == s[1:]
}
