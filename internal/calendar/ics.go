// Package calendar serves "add to calendar" links for booked appointments.
//
// Why this exists as a fetched URL rather than a WhatsApp attachment:
// Twilio does not accept text/calendar on the WhatsApp channel - calendar
// files are restricted to MMS, and WhatsApp's document support is PDF,
// vCard and Office formats only. So the .ics cannot ride along with the
// message; the message carries a link and this package answers it.
//
// Public and unauthenticated, like internal/share. The booking's
// calendar_token is the only credential, the same trust model as the guest
// review link (migration 013): B-Edge's funnel is guest-first, so a
// customer never has a session to prove identity any other way.
package calendar

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/bidi"
)

// icsMaxOctets is RFC 5545's content-line limit: 75 octets, excluding the
// line break. Longer lines must be folded. This bites much sooner than it
// looks for this product - a store name in Arabic is two to three bytes per
// character, so "صالون الجمال في بيروت" alone is 38 octets before the
// property name or the rest of the summary.
const icsMaxOctets = 75

// Event is everything the ICS needs, already resolved. No database, no
// clock, no timezone lookup - those belong to the caller so this stays a
// pure function that can be tested against the RFC.
type Event struct {
	BookingID uuid.UUID
	// Start and End are absolute instants. The caller must have already
	// resolved the store's wall-clock time through its IANA zone; see
	// Format's doc comment for why they are emitted as UTC.
	Start time.Time
	End   time.Time
	// Sequence is bookings.calendar_sequence. It must increase whenever
	// Start or End changes, or a re-import creates a second event instead
	// of moving the first.
	Sequence int
	// Now is DTSTAMP - when this representation was generated. Injected
	// rather than read from the clock so the output is reproducible.
	Now time.Time

	ServiceName string
	StoreName   string
	// Location is the store address. Optional; omitted when empty rather
	// than emitted blank, which some clients render as an empty map pin.
	Location string
	// URL points back at the booking's own page, so the calendar entry can
	// be acted on rather than being a dead-end reminder.
	URL string
	// Cancelled switches the event to a cancellation notice. The UID stays
	// the same - that is what lets a client withdraw the event it already
	// holds instead of adding a second one saying "cancelled".
	Cancelled bool
}

// UID is the event's stable identity across every version of it.
//
// Derived from the booking id rather than stored, because the booking id is
// already stable, unique and never reused. A calendar client matches on
// this: same UID with a higher SEQUENCE updates the event it already has.
func (e Event) UID() string {
	return fmt.Sprintf("booking-%s@b-edge.app", e.BookingID)
}

// Format renders the event as an iCalendar document.
//
// Times are emitted as UTC instants (the trailing Z), not as local time
// with a VTIMEZONE block. Both are legal; UTC is the right choice here
// because it pins the absolute moment and cannot be re-interpreted by a
// client whose timezone database disagrees with ours. That matters more in
// Lebanon than most places - the country has changed its DST dates by
// decree mid-season, and a floating local time would silently move the
// appointment by an hour on every device that had not caught up.
//
// METHOD is PUBLISH rather than REQUEST. REQUEST means "you are invited"
// and makes clients render RSVP buttons and expect an ORGANIZER and
// ATTENDEE - wrong for a file the customer chose to download about an
// appointment they already made. A cancellation uses CANCEL, which is the
// one case where a client is expected to withdraw something it holds.
func (e Event) Format() string {
	method, status := "PUBLISH", "CONFIRMED"
	if e.Cancelled {
		method, status = "CANCEL", "CANCELLED"
	}

	summary := fmt.Sprintf("%s at %s", e.ServiceName, e.StoreName)
	if e.Cancelled {
		summary = "Cancelled: " + summary
	}

	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//B-Edge//Booking//EN",
		"CALSCALE:GREGORIAN",
		"METHOD:" + method,
		"BEGIN:VEVENT",
		"UID:" + e.UID(),
		fmt.Sprintf("SEQUENCE:%d", e.Sequence),
		"DTSTAMP:" + formatUTC(e.Now),
		"DTSTART:" + formatUTC(e.Start),
		"DTEND:" + formatUTC(e.End),
		"SUMMARY:" + escapeText(summary),
		"STATUS:" + status,
	}

	if e.Location != "" {
		lines = append(lines, "LOCATION:"+escapeText(e.Location))
	}
	if e.URL != "" {
		// URL is not a TEXT-typed property, so it is NOT escaped - doing so
		// would turn a query string's commas and semicolons into literal
		// backslash sequences and break the link.
		lines = append(lines, "URL:"+e.URL)
	}
	lines = append(lines, "END:VEVENT", "END:VCALENDAR")

	var b strings.Builder
	for _, line := range lines {
		// CRLF, not LF. RFC 5545 requires it, and some desktop clients
		// reject a bare-LF file outright rather than degrading.
		b.WriteString(fold(line))
		b.WriteString("\r\n")
	}
	return b.String()
}

// formatUTC renders an instant in iCalendar's UTC form: 20260916T070000Z.
func formatUTC(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// escapeText escapes a TEXT-typed property value per RFC 5545 §3.3.11.
//
// Backslash first, or every escape this function then adds would be
// re-escaped by the subsequent passes.
//
// Bidi controls are stripped for the same reason they are stripped from
// share cards and notification bodies: ServiceName and StoreName are
// artist-supplied text that ends up rendered on a CUSTOMER's device, in an
// app that is not ours and that we cannot make escape anything. A
// right-to-left override in a store name would reorder the summary in the
// customer's calendar. See internal/pkg/bidi for what is removed and why
// directional MARKS are deliberately kept.
func escapeText(s string) string {
	s = bidi.StripControls(s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	// A literal newline would end the content line and make the next line
	// look like a new property - the classic way a single multi-line
	// description corrupts a whole calendar file.
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\n`)
	return s
}

// fold breaks a content line at 75 octets, continuing with CRLF + one
// space, per RFC 5545 §3.1.
//
// The limit is in OCTETS but the split must not land inside a UTF-8
// sequence, or the file carries an invalid byte and clients show a
// replacement character - or reject the file. Since this product is
// Arabic-first, where nearly every character is multi-byte, a naive
// byte-slice would corrupt output almost every time rather than rarely.
func fold(line string) string {
	if len(line) <= icsMaxOctets {
		return line
	}

	var b strings.Builder
	// First line takes the full budget; continuation lines lose one octet
	// to the leading space that marks them as continuations.
	budget := icsMaxOctets
	used := 0

	for _, r := range line {
		size := len(string(r))
		if used+size > budget {
			b.WriteString("\r\n ")
			budget = icsMaxOctets - 1
			used = 0
		}
		b.WriteRune(r)
		used += size
	}
	return b.String()
}
