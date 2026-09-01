package calendar

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// validToken is the shape a real token has: 64 lowercase hex characters.
const validToken = "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2"

type stubRepo struct {
	booking *Booking
	err     error
}

func (s *stubRepo) GetByCalendarToken(_ context.Context, _ string) (*Booking, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.booking, nil
}

func confirmedBooking() *Booking {
	return &Booking{
		ID:           testID,
		StartTime:    testStart,
		EndTime:      testEnd,
		Status:       "confirmed",
		Sequence:     0,
		ServiceName:  "Bridal makeup",
		StoreName:    "Rania Beauty",
		StoreCity:    "Beirut",
		StoreAddress: "Hamra Street, Beirut",
		Timezone:     "Asia/Beirut",
	}
}

func newTestApp(t *testing.T, repo Repository) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	svc := NewService(repo, "https://bedge.app")
	svc.now = func() time.Time { return testNow }
	h := NewHandler(svc, zap.NewNop())
	app.Get("/c/:token.ics", h.ICS)
	app.Get("/c/:token", h.Page)
	return app
}

func get(t *testing.T, app *fiber.App, path string) (int, string, map[string]string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, path, nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	hdr := map[string]string{
		"Content-Type":        resp.Header.Get("Content-Type"),
		"Content-Disposition": resp.Header.Get("Content-Disposition"),
		"Cache-Control":       resp.Header.Get("Cache-Control"),
	}
	return resp.StatusCode, string(body), hdr
}

// ── Routing ───────────────────────────────────────────────────────────────────

// The two routes differ only by a literal suffix on the same param. If
// Fiber does not treat ".ics" as a literal, "/c/<token>.ics" falls through
// to the page route with a token of "<token>.ics", which then fails the
// shape check and 404s - a silently broken download button.
func TestRouting_ICSSuffixIsNotSwallowedByThePageRoute(t *testing.T) {
	app := newTestApp(t, &stubRepo{booking: confirmedBooking()})

	status, body, hdr := get(t, app, "/c/"+validToken+".ics")

	require.Equal(t, fiber.StatusOK, status)
	assert.Contains(t, hdr["Content-Type"], "text/calendar")
	assert.Contains(t, body, "BEGIN:VCALENDAR")
}

func TestRouting_BareTokenServesTheHTMLPage(t *testing.T) {
	app := newTestApp(t, &stubRepo{booking: confirmedBooking()})

	status, body, hdr := get(t, app, "/c/"+validToken)

	require.Equal(t, fiber.StatusOK, status)
	assert.Contains(t, hdr["Content-Type"], "text/html")
	assert.Contains(t, body, "<!DOCTYPE html>")
}

// ── The .ics response ─────────────────────────────────────────────────────────

// The filename matters as much as the MIME type on iOS - it is what makes
// the OS offer the calendar app rather than a text viewer.
func TestICS_SetsDownloadHeaders(t *testing.T) {
	app := newTestApp(t, &stubRepo{booking: confirmedBooking()})

	_, _, hdr := get(t, app, "/c/"+validToken+".ics")

	assert.Contains(t, hdr["Content-Disposition"], `filename="appointment.ics"`)
	assert.Equal(t, "no-store", hdr["Cache-Control"],
		"a cached .ics hands the customer a stale event exactly when they are fixing one")
}

func TestICS_UsesTheStoreAddressAsLocation(t *testing.T) {
	app := newTestApp(t, &stubRepo{booking: confirmedBooking()})

	_, body, _ := get(t, app, "/c/"+validToken+".ics")

	assert.Contains(t, strings.ReplaceAll(body, "\r\n ", ""), "Hamra Street")
}

// City is the fallback, not a second LOCATION line - a store that never set
// an address must still say where it is.
func TestICS_FallsBackToCityWhenAddressIsEmpty(t *testing.T) {
	b := confirmedBooking()
	b.StoreAddress = ""
	app := newTestApp(t, &stubRepo{booking: b})

	_, body, _ := get(t, app, "/c/"+validToken+".ics")

	assert.Contains(t, body, "LOCATION:Beirut")
}

// ── Cancellation ──────────────────────────────────────────────────────────────

// A cancelled booking must still resolve. 404ing it would leave the stale
// appointment in the customer's calendar forever, which is worse than the
// problem this feature exists to solve.
func TestCancelledBooking_StillServesAWithdrawal(t *testing.T) {
	for _, status := range []string{"cancelled", "expired", "refunded", "refund_due"} {
		b := confirmedBooking()
		b.Status = status
		app := newTestApp(t, &stubRepo{booking: b})

		code, body, _ := get(t, app, "/c/"+validToken+".ics")

		require.Equal(t, fiber.StatusOK, code, "status %q must still resolve", status)
		assert.Contains(t, body, "METHOD:CANCEL", "status %q", status)
		assert.Contains(t, body, "STATUS:CANCELLED", "status %q", status)
	}
}

// A soft-deleted booking is a cancellation, not a 404, for the same reason.
func TestDeletedBooking_TreatedAsCancelled(t *testing.T) {
	b := confirmedBooking()
	b.Deleted = true
	app := newTestApp(t, &stubRepo{booking: b})

	_, body, _ := get(t, app, "/c/"+validToken+".ics")

	assert.Contains(t, body, "METHOD:CANCEL")
}

// completed and no_show mean the appointment HAPPENED. Withdrawing those
// would rewrite the customer's own history.
func TestCompletedAndNoShow_AreNotCancellations(t *testing.T) {
	for _, status := range []string{"completed", "no_show"} {
		b := confirmedBooking()
		b.Status = status
		app := newTestApp(t, &stubRepo{booking: b})

		_, body, _ := get(t, app, "/c/"+validToken+".ics")

		assert.Contains(t, body, "METHOD:PUBLISH", "status %q must not withdraw a past event", status)
	}
}

// The cancelled page must still offer the file, since that file is what
// removes the event. Hiding it leaves the stale entry in place.
func TestCancelledPage_StillOffersTheICS(t *testing.T) {
	b := confirmedBooking()
	b.Status = "cancelled"
	app := newTestApp(t, &stubRepo{booking: b})

	_, body, _ := get(t, app, "/c/"+validToken)

	assert.Contains(t, body, "Remove from calendar")
	assert.Contains(t, body, ".ics")
}

// ── Not found ─────────────────────────────────────────────────────────────────

func TestUnknownToken_Returns404(t *testing.T) {
	app := newTestApp(t, &stubRepo{err: ErrBookingNotFound})

	status, _, _ := get(t, app, "/c/"+validToken)

	assert.Equal(t, fiber.StatusNotFound, status)
}

// A malformed token is rejected on shape before a query is spent on it,
// and returns the SAME 404 as a well-formed unknown one - a caller must not
// be able to tell "wrong shape" from "no such booking".
func TestMalformedToken_Returns404WithoutQueryingTheDatabase(t *testing.T) {
	repo := &stubRepo{err: errors.New("the database must not be reached")}
	app := newTestApp(t, repo)

	for _, tok := range []string{
		"short",
		strings.Repeat("a", 65),
		strings.Repeat("Z", 64), // valid length, not hex
		strings.Repeat("A", 64), // uppercase hex is not our shape
		"../../etc/passwd",
	} {
		status, _, _ := get(t, app, "/c/"+tok)
		assert.Equal(t, fiber.StatusNotFound, status, "token %q", tok)
	}
}

// ── Page content ──────────────────────────────────────────────────────────────

func TestPage_OffersBothCalendarPaths(t *testing.T) {
	app := newTestApp(t, &stubRepo{booking: confirmedBooking()})

	_, body, _ := get(t, app, "/c/"+validToken)

	assert.Contains(t, body, "calendar.google.com/calendar/render")
	assert.Contains(t, body, "Apple Calendar / Outlook")
}

// The time shown is the STORE's local time. A customer viewing this from
// abroad before travelling must not read a converted one.
func TestPage_ShowsStoreLocalTimeNotServerTime(t *testing.T) {
	app := newTestApp(t, &stubRepo{booking: confirmedBooking()})

	_, body, _ := get(t, app, "/c/"+validToken)

	// 07:00 UTC is 10:00 in Asia/Beirut in September.
	assert.Contains(t, body, "10:00")
	assert.Contains(t, body, "Wednesday, 16 September 2026")
}

// An unloadable zone must not silently fall back to the server's local
// time - a wrong time with no sign it is wrong is the worst failure here.
func TestPage_UnknownTimezoneFallsBackToUTC(t *testing.T) {
	b := confirmedBooking()
	b.Timezone = "Mars/Olympus_Mons"
	app := newTestApp(t, &stubRepo{booking: b})

	_, body, _ := get(t, app, "/c/"+validToken)

	assert.Contains(t, body, "07:00", "UTC, the instant we actually stored")
}

// The store and service names are artist-supplied and land on a customer's
// screen. This page is raw HTML, so escaping is load-bearing.
func TestPage_EscapesArtistSuppliedText(t *testing.T) {
	b := confirmedBooking()
	b.StoreName = `Rania"><script>alert(1)</script>`
	app := newTestApp(t, &stubRepo{booking: b})

	_, body, _ := get(t, app, "/c/"+validToken)

	assert.NotContains(t, body, "<script>alert(1)</script>")
	assert.Contains(t, body, "&lt;script&gt;")
}

func TestPage_StripsBidiOverrides(t *testing.T) {
	b := confirmedBooking()
	b.StoreName = "Rania ‮moc.live//:sptth"
	app := newTestApp(t, &stubRepo{booking: b})

	_, body, _ := get(t, app, "/c/"+validToken)

	assert.NotContains(t, body, "‮")
}

// The Google deep link carries the same artist-supplied text, so a bidi
// override there would reorder the event title inside Google Calendar.
func TestGoogleURL_CarriesStrippedText(t *testing.T) {
	b := confirmedBooking()
	b.ServiceName = "Makeup ‮txet desrever"
	app := newTestApp(t, &stubRepo{booking: b})

	_, body, _ := get(t, app, "/c/"+validToken)

	assert.NotContains(t, body, "%E2%80%AE", "the override must not survive percent-encoding either")
}

func TestPage_IsNotIndexable(t *testing.T) {
	app := newTestApp(t, &stubRepo{booking: confirmedBooking()})

	_, body, _ := get(t, app, "/c/"+validToken)

	assert.Contains(t, body, `name="robots" content="noindex, nofollow"`,
		"a private appointment link must never be indexed")
}
