package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testID    = uuid.MustParse("11111111-2222-3333-4444-555555555555")
	testStart = time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC)
	testEnd   = time.Date(2026, 9, 16, 7, 15, 0, 0, time.UTC)
	testNow   = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

func testEvent() Event {
	return Event{
		BookingID:   testID,
		Start:       testStart,
		End:         testEnd,
		Now:         testNow,
		ServiceName: "Bridal makeup",
		StoreName:   "Rania Beauty",
		Location:    "Beirut Downtown",
		URL:         "https://b-edge.app/c/abc123",
	}
}

// lines splits a rendered document back into content lines, undoing the
// folding, so assertions can talk about properties rather than bytes.
func unfold(doc string) []string {
	return strings.Split(strings.ReplaceAll(doc, "\r\n ", ""), "\r\n")
}

func has(t *testing.T, doc, want string) {
	t.Helper()
	assert.Contains(t, unfold(doc), want)
}

// ── Structure ─────────────────────────────────────────────────────────────────

func TestFormat_ProducesAWellFormedDocument(t *testing.T) {
	doc := testEvent().Format()

	l := unfold(doc)
	assert.Equal(t, "BEGIN:VCALENDAR", l[0])
	assert.Equal(t, "VERSION:2.0", l[1])
	has(t, doc, "BEGIN:VEVENT")
	has(t, doc, "END:VEVENT")
	assert.Equal(t, "END:VCALENDAR", l[len(l)-2], "last line before the trailing break")
}

// Every line must end CRLF. Some desktop clients reject a bare-LF file
// outright rather than degrading, so this is not cosmetic.
func TestFormat_UsesCRLFLineEndings(t *testing.T) {
	doc := testEvent().Format()

	assert.NotContains(t, strings.ReplaceAll(doc, "\r\n", ""), "\n",
		"no LF may appear except as part of a CRLF pair")
	assert.True(t, strings.HasSuffix(doc, "\r\n"))
}

// ── Time ──────────────────────────────────────────────────────────────────────

// UTC instants, not floating local time. Lebanon has changed its DST dates
// by decree mid-season; a floating time would move the appointment by an
// hour on every device whose timezone database had not caught up.
func TestFormat_EmitsUTCInstants(t *testing.T) {
	doc := testEvent().Format()

	has(t, doc, "DTSTART:20260916T070000Z")
	has(t, doc, "DTEND:20260916T071500Z")
	has(t, doc, "DTSTAMP:20260901T120000Z")
}

// A caller that resolved the store's wall clock into a non-UTC location
// must still produce the same absolute instant.
func TestFormat_ConvertsNonUTCInputToUTC(t *testing.T) {
	beirut, err := time.LoadLocation("Asia/Beirut")
	require.NoError(t, err)

	e := testEvent()
	// 10:00 Beirut in September is UTC+3, so 07:00Z.
	e.Start = time.Date(2026, 9, 16, 10, 0, 0, 0, beirut)
	e.End = time.Date(2026, 9, 16, 10, 15, 0, 0, beirut)

	doc := e.Format()

	has(t, doc, "DTSTART:20260916T070000Z")
	has(t, doc, "DTEND:20260916T071500Z")
}

// ── Identity and updates ──────────────────────────────────────────────────────

// The UID is what makes a re-import update the existing event rather than
// add a second one. It must not change when the time does.
func TestUID_StableAcrossReschedule(t *testing.T) {
	before := testEvent()
	after := testEvent()
	after.Start = testStart.Add(30 * time.Minute)
	after.End = testEnd.Add(30 * time.Minute)
	after.Sequence = 1

	assert.Equal(t, before.UID(), after.UID())
	assert.Contains(t, before.UID(), testID.String())
}

func TestFormat_SequenceIsEmitted(t *testing.T) {
	e := testEvent()
	e.Sequence = 3

	has(t, e.Format(), "SEQUENCE:3")
}

// A brand-new event is SEQUENCE:0, not absent - a missing SEQUENCE means
// zero by default, but emitting it explicitly makes the increment on the
// next version unambiguous.
func TestFormat_NewEventIsSequenceZero(t *testing.T) {
	has(t, testEvent().Format(), "SEQUENCE:0")
}

// ── Cancellation ──────────────────────────────────────────────────────────────

// A cancellation must reuse the UID. A new UID would leave the original
// event sitting in the customer's calendar and add a second one telling
// them it was cancelled.
func TestFormat_Cancelled_KeepsUIDAndUsesCancelMethod(t *testing.T) {
	e := testEvent()
	e.Cancelled = true
	e.Sequence = 1

	doc := e.Format()

	has(t, doc, "UID:"+testEvent().UID())
	has(t, doc, "METHOD:CANCEL")
	has(t, doc, "STATUS:CANCELLED")
	assert.Contains(t, doc, "Cancelled: Bridal makeup")
}

func TestFormat_Active_UsesPublishNotRequest(t *testing.T) {
	doc := testEvent().Format()

	has(t, doc, "METHOD:PUBLISH")
	has(t, doc, "STATUS:CONFIRMED")
	assert.NotContains(t, doc, "METHOD:REQUEST",
		"REQUEST makes clients render RSVP buttons for an appointment already made")
}

// ── Escaping ──────────────────────────────────────────────────────────────────

// A literal newline ends the content line, so the text after it looks like
// a new property. This is how one multi-line value corrupts a whole file.
func TestEscapeText_NewlinesBecomeLiteralBackslashN(t *testing.T) {
	assert.Equal(t, `line one\nline two`, escapeText("line one\nline two"))
	assert.Equal(t, `a\nb`, escapeText("a\r\nb"))
	assert.Equal(t, `a\nb`, escapeText("a\rb"))
}

func TestEscapeText_SemicolonsAndCommas(t *testing.T) {
	assert.Equal(t, `Cut\, colour\; blow-dry`, escapeText("Cut, colour; blow-dry"))
}

// Backslash must be escaped FIRST, or the escapes added afterwards get
// re-escaped and the value arrives mangled.
func TestEscapeText_BackslashEscapedFirst(t *testing.T) {
	assert.Equal(t, `a\\b`, escapeText(`a\b`))
	assert.Equal(t, `100\\\, then`, escapeText(`100\, then`),
		"the backslash doubles and the comma escapes exactly once")
}

// A store or service name is artist-supplied text rendered on a CUSTOMER's
// device, inside a calendar app we do not control and cannot make escape
// anything. Same vector as the share card and the notification body.
func TestFormat_StripsBidiOverridesFromArtistSuppliedText(t *testing.T) {
	e := testEvent()
	e.StoreName = "Rania ‮moc.live//:sptth"

	doc := e.Format()

	assert.NotContains(t, doc, "‮",
		"a right-to-left override would reorder the summary in the customer's calendar")
	assert.Contains(t, doc, "moc.live//:sptth", "the visible text is kept")
}

func TestFormat_KeepsArabicIntact(t *testing.T) {
	e := testEvent()
	e.ServiceName = "مكياج عرائس"
	e.StoreName = "صالون الجمال"

	doc := e.Format()

	// Unfold before asserting - the Arabic summary is long enough to fold.
	assert.Contains(t, strings.ReplaceAll(doc, "\r\n ", ""), "مكياج عرائس")
	assert.Contains(t, strings.ReplaceAll(doc, "\r\n ", ""), "صالون الجمال")
}

// ── Folding ───────────────────────────────────────────────────────────────────

func TestFold_ShortLineUntouched(t *testing.T) {
	assert.Equal(t, "SUMMARY:short", fold("SUMMARY:short"))
}

func TestFold_LongLineSplitAt75Octets(t *testing.T) {
	line := "SUMMARY:" + strings.Repeat("a", 200)

	for i, seg := range strings.Split(fold(line), "\r\n ") {
		if i > 0 {
			seg = " " + seg // the continuation's own leading space
		}
		assert.LessOrEqual(t, len(seg), icsMaxOctets,
			"segment %d exceeds the octet limit", i)
	}
}

// The limit is in octets but the split must not land inside a UTF-8
// sequence. In an Arabic-first product nearly every character is
// multi-byte, so a naive byte slice would corrupt output almost every time
// rather than rarely.
func TestFold_NeverSplitsAMultiByteRune(t *testing.T) {
	line := "SUMMARY:" + strings.Repeat("ص", 100) // 2 octets each

	folded := fold(line)

	assert.True(t, utf8Valid(folded), "folding produced invalid UTF-8")
	assert.Equal(t, line, strings.ReplaceAll(folded, "\r\n ", ""),
		"unfolding must return exactly the original line")
}

func TestFold_EmojiSurviveFolding(t *testing.T) {
	line := "SUMMARY:" + strings.Repeat("💅", 40) // 4 octets each

	folded := fold(line)

	assert.True(t, utf8Valid(folded))
	assert.Equal(t, line, strings.ReplaceAll(folded, "\r\n ", ""))
}

// Round-trip is the property that actually matters: whatever we fold, a
// client unfolding it per the RFC must get the original back.
func TestFold_RoundTripsForRepresentativeValues(t *testing.T) {
	for _, in := range []string{
		"SUMMARY:Bridal makeup at Rania Beauty",
		"SUMMARY:" + strings.Repeat("Beirut ", 30),
		"LOCATION:" + strings.Repeat("صالون ", 30),
		"SUMMARY:" + strings.Repeat("a", icsMaxOctets),   // exactly at the limit
		"SUMMARY:" + strings.Repeat("a", icsMaxOctets+1), // one past it
	} {
		assert.Equal(t, in, strings.ReplaceAll(fold(in), "\r\n ", ""),
			"round trip failed for %.40s", in)
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// ── Optional fields ───────────────────────────────────────────────────────────

// An empty LOCATION renders as an empty map pin in some clients, which
// looks broken rather than absent.
func TestFormat_OmitsEmptyLocationAndURL(t *testing.T) {
	e := testEvent()
	e.Location = ""
	e.URL = ""

	doc := e.Format()

	assert.NotContains(t, doc, "LOCATION:")
	assert.NotContains(t, doc, "URL:")
}

// URL is not a TEXT-typed property. Escaping it would turn a query
// string's commas and semicolons into literal backslash sequences.
func TestFormat_DoesNotEscapeTheURL(t *testing.T) {
	e := testEvent()
	e.URL = "https://b-edge.app/c/abc?a=1,2;b=3"

	has(t, e.Format(), "URL:https://b-edge.app/c/abc?a=1,2;b=3")
}
