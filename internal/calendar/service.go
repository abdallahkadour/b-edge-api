package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/bidi"
)

// cancelledStatuses are the booking states where the calendar event should
// be WITHDRAWN rather than shown.
//
// `completed` and `no_show` are deliberately absent: both mean the
// appointment happened (or the slot was held for someone who did not turn
// up), and deleting a past event from someone's calendar would rewrite
// their history. Only states meaning "this is not going to happen" cancel.
var cancelledStatuses = map[string]bool{
	"cancelled":  true,
	"expired":    true,
	"refunded":   true,
	"refund_due": true,
}

// Service builds calendar events from bookings.
type Service struct {
	repo Repository
	// clientURL is the customer app's base, used for the event's URL
	// property so the calendar entry links back to something actionable.
	clientURL string
	// now is injected so tests are not clock-dependent; DTSTAMP is part of
	// the rendered output.
	now func() time.Time
}

// NewService creates a calendar Service.
func NewService(repo Repository, clientURL string) *Service {
	return &Service{repo: repo, clientURL: clientURL, now: time.Now}
}

// View is what the landing page renders.
type View struct {
	Event Event
	// LocalTime is the appointment in the STORE's zone, pre-formatted.
	// The customer is told when to turn up where the appointment is, not
	// where their phone currently thinks it is - a customer viewing this
	// from abroad before travelling must not read a converted time.
	LocalTime string
	// GoogleURL is the calendar.google.com template deep link. Google's
	// Android flow for a downloaded .ics is poor, and Android is the
	// majority here, so this is the path most customers will actually use.
	GoogleURL string
	// ICSPath is the relative path to the .ics for Apple/Outlook.
	ICSPath   string
	StoreName string
	Cancelled bool
}

// GetEvent resolves a token to a renderable event.
func (s *Service) GetEvent(ctx context.Context, token string) (*View, error) {
	// Cheap shape check before touching the database. The token is 64 hex
	// characters by construction; anything else cannot match a row, so
	// there is no reason to spend a query on it.
	if !isPlausibleToken(token) {
		return nil, apperror.NotFound("CALENDAR_LINK_NOT_FOUND", "This calendar link is not valid")
	}

	b, err := s.repo.GetByCalendarToken(ctx, token)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("CALENDAR_LINK_NOT_FOUND", "This calendar link is not valid")
		}
		return nil, fmt.Errorf("get calendar event: %w", err)
	}

	cancelled := b.Deleted || cancelledStatuses[b.Status]

	location := b.StoreAddress
	if location == "" {
		location = b.StoreCity
	}

	// Stripped here, not only in the ICS escaper, because these same
	// strings also go into the Google deep link and the landing page -
	// three renderers, one place to clean. escapeText strips again, which
	// is idempotent and keeps Format safe for any direct caller.
	ev := Event{
		BookingID:   b.ID,
		Start:       b.StartTime,
		End:         b.EndTime,
		Sequence:    b.Sequence,
		Now:         s.now(),
		ServiceName: bidi.StripControls(b.ServiceName),
		StoreName:   bidi.StripControls(b.StoreName),
		Location:    bidi.StripControls(location),
		URL:         fmt.Sprintf("%s/my-bookings/%s", s.clientURL, b.ID),
		Cancelled:   cancelled,
	}

	return &View{
		Event:     ev,
		LocalTime: formatLocal(b.StartTime, b.EndTime, b.Timezone),
		GoogleURL: googleCalendarURL(ev),
		ICSPath:   "/c/" + token + ".ics",
		StoreName: ev.StoreName,
		Cancelled: cancelled,
	}, nil
}

// isPlausibleToken reports whether a string could be one of our tokens:
// exactly 64 lowercase hex characters, matching generateReviewToken's
// output shape and the VARCHAR(64) column.
func isPlausibleToken(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// formatLocal renders the appointment in the store's own zone.
//
// An unloadable zone falls back to UTC with the offset shown, rather than
// silently rendering the server's local time - a wrong time with no
// indication it is wrong is the worst of the available failures.
func formatLocal(start, end time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	s, e := start.In(loc), end.In(loc)
	return fmt.Sprintf("%s · %s–%s",
		s.Format("Monday, 2 January 2006"), s.Format("15:04"), e.Format("15:04"))
}

// googleCalendarURL builds the calendar.google.com template link.
//
// Google's own web flow, not a file. Handing an Android user a downloaded
// .ics means a file-manager round trip that many simply abandon; this
// opens Google Calendar with the event pre-filled.
//
// The trade-off, worth stating because it is invisible: an event added this
// way carries GOOGLE's id, not our UID, so a later reschedule cannot update
// it - the customer gets a second entry. Apple/Outlook users taking the
// .ics do get in-place updates. Fixing that would mean OAuth into the
// customer's Google account, which is far more than this feature is worth.
func googleCalendarURL(e Event) string {
	const layout = "20060102T150405Z"
	q := url.Values{}
	q.Set("action", "TEMPLATE")
	q.Set("text", fmt.Sprintf("%s at %s", e.ServiceName, e.StoreName))
	q.Set("dates", e.Start.UTC().Format(layout)+"/"+e.End.UTC().Format(layout))
	if e.Location != "" {
		q.Set("location", e.Location)
	}
	if e.URL != "" {
		q.Set("details", "Booked on B-Edge: "+e.URL)
	}
	return "https://calendar.google.com/calendar/render?" + q.Encode()
}
