// Package bidi strips Unicode bidirectional control characters from
// user-supplied text.
//
// A leaf package with no B-Edge imports, for the usual reason in this
// codebase: the two callers (internal/share, internal/notification) have no
// business importing each other, and the alternative to a shared home is
// two copies of a security-relevant rule that drift apart.
package bidi

import "strings"

// StripControls removes Unicode bidirectional override, embedding and
// isolate characters from user-supplied text.
//
// Why this is not covered by HTML escaping:
//
//	These are not HTML-special, so html.EscapeString passes them through
//	untouched. Nor does an Angular interpolation help - the framework
//	escapes markup, and these are not markup. They are invisible, and they
//	reorder the text AROUND them when rendered. A bio of
//
//	    Book now ‮moc.live//:sptth
//
//	displays in a WhatsApp link preview as "Book now https://evil.com".
//	The og:url still points at the real profile, so the link itself is
//	honest - but the preview card is the most trusted-looking surface
//	B-Edge has, and this is artist-controlled text reaching it.
//
//	Found by executing E2E-TEST-PLAN.md section 2.5.1 (finding 1,
//	2026-09-01), which was written specifically to look for this class.
//
// Apply it wherever user-controlled text is interpolated into a string a
// DIFFERENT person will read - the share card and the in-app notification
// body are both cases where the reader has no relationship with whoever
// supplied the text and no reason to be suspicious of it.
//
// What is stripped, and what deliberately is not:
//
//	Removed: U+202A-U+202E (embeddings and overrides) and U+2066-U+2069
//	(isolates). None of these are needed to render Arabic correctly - the
//	Unicode bidirectional algorithm handles Arabic script on its own - so
//	removing them cannot damage legitimate Arabic text.
//
//	KEPT: U+200E and U+200F (left-to-right and right-to-left MARKS). These
//	are weak directional hints rather than overrides, and are legitimately
//	used to disambiguate mixed Arabic/Latin strings such as a phone number
//	inside an Arabic sentence. Stripping them would degrade exactly the
//	Arabic-first typography this product is meant to be good at.
func StripControls(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '‪' && r <= '‮': // LRE, RLE, PDF, LRO, RLO
			return -1
		case r >= '⁦' && r <= '⁩': // LRI, RLI, FSI, PDI
			return -1
		default:
			return r
		}
	}, s)
}
