package calendar

import (
	"fmt"
	"html"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Handler serves the calendar landing page and the .ics itself.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a calendar Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes mounts the calendar routes.
//
// Not under /api/v1, and deliberately unauthenticated, for the same reasons
// as internal/share: these URLs are opened by a human from a WhatsApp
// message, and the customer has no account to authenticate with. "/c/" is
// short because the whole path travels inside a message body.
//
// The .ics route is registered first. Fiber matches in registration order,
// and "/c/:token" would otherwise swallow "/c/abc.ics" with a token of
// "abc.ics" - the same route-shadowing bug that bit internal/media.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	client := strings.TrimSpace(strings.Split(os.Getenv("CLIENT_URL"), ",")[0])
	svc := NewService(NewRepository(pool), strings.TrimSuffix(client, "/"))
	h := NewHandler(svc, log)

	app.Get("/c/:token.ics", h.ICS)
	app.Get("/c/:token", h.Page)
}

// ICS returns the calendar file.
func (h *Handler) ICS(c *fiber.Ctx) error {
	view, err := h.svc.GetEvent(c.Context(), c.Params("token"))
	if err != nil {
		return err
	}

	// text/calendar with a filename, so a browser offers to open it with
	// the calendar app rather than displaying it as text. The extension
	// matters as much as the type on iOS.
	c.Set(fiber.HeaderContentType, "text/calendar; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="appointment.ics"`)
	// A booking can be rescheduled or cancelled at any time, and a cached
	// .ics would hand the customer a stale event precisely when they are
	// trying to correct one.
	c.Set(fiber.HeaderCacheControl, "no-store")

	return c.SendString(view.Event.Format())
}

// Page renders the "add to calendar" chooser.
//
// A page rather than serving the .ics directly from the link, because a
// bare .ics download is a poor flow on Android - a file-manager round trip
// many users abandon - and Android is the majority platform here. The page
// sends Google users to Google's own web flow and everyone else to the
// file.
func (h *Handler) Page(c *fiber.Ctx) error {
	view, err := h.svc.GetEvent(c.Context(), c.Params("token"))
	if err != nil {
		return err
	}

	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.SendString(renderPage(view))
}

// renderPage builds the chooser document.
//
// Hand-written HTML rather than a template engine, matching internal/share:
// there is exactly one document, it has no layout to share, and adding a
// template dependency for it would be more moving parts than the thing
// itself. Every interpolated value is escaped.
func renderPage(v *View) string {
	title := html.EscapeString(v.StoreName)
	summary := html.EscapeString(fmt.Sprintf("%s at %s", v.Event.ServiceName, v.Event.StoreName))
	when := html.EscapeString(v.LocalTime)
	where := html.EscapeString(v.Event.Location)
	google := html.EscapeString(v.GoogleURL)
	ics := html.EscapeString(v.ICSPath)

	if v.Cancelled {
		// The cancelled page still offers the .ics, because that file is
		// what REMOVES the event from a calendar it was already added to.
		// Hiding it here would leave the stale appointment in place.
		return page(title, fmt.Sprintf(`
    <p class="tag tag-cancel">Cancelled</p>
    <h1>%s</h1>
    <p class="when">%s</p>
    <p class="note">This appointment was cancelled. If you added it to your
      calendar, download the update below to remove it.</p>
    <a class="btn btn-secondary" href="%s">Remove from calendar</a>`,
			summary, when, ics))
	}

	locationRow := ""
	if where != "" {
		locationRow = fmt.Sprintf(`<p class="where">%s</p>`, where)
	}

	return page(title, fmt.Sprintf(`
    <h1>%s</h1>
    <p class="when">%s</p>
    %s
    <a class="btn btn-primary" href="%s" target="_blank" rel="noopener noreferrer">Add to Google Calendar</a>
    <a class="btn btn-secondary" href="%s">Apple Calendar / Outlook</a>
    <p class="note">Adding this to your calendar is just a reminder — your
      booking is already confirmed either way.</p>`,
		summary, when, locationRow, google, ics))
}

// page wraps content in the document shell.
//
// Single-theme on purpose: this is opened once, from a message, usually on
// a phone, and it is the only page in the product served straight from the
// API. Every colour is stated explicitly so it renders identically whatever
// the device's theme is, rather than inheriting half a palette.
func page(title, body string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>` + title + ` · B-Edge</title>
<style>
  :root { color-scheme: light; }
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 24px;
    background: #fafafa; color: #0a0a0a;
    font: 15px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    display: flex; align-items: center; justify-content: center; min-height: 100vh;
  }
  .card {
    background: #fff; border: 1px solid #e5e5e5; border-radius: 16px;
    padding: 28px 24px; width: 100%; max-width: 380px;
  }
  .brand { font-size: 13px; font-weight: 600; color: #a1a1aa; letter-spacing: .04em;
           text-transform: uppercase; margin: 0 0 18px; }
  h1 { font-size: 20px; line-height: 1.3; margin: 0 0 6px; text-wrap: balance; }
  .when { font-size: 15px; font-weight: 600; margin: 0 0 4px; }
  .where { font-size: 14px; color: #71717a; margin: 0 0 4px; }
  .tag { display: inline-block; font-size: 11px; font-weight: 700; letter-spacing: .06em;
         text-transform: uppercase; border-radius: 999px; padding: 4px 10px; margin: 0 0 12px; }
  .tag-cancel { background: #fee2e2; color: #991b1b; }
  .btn { display: block; text-align: center; text-decoration: none;
         font-weight: 700; font-size: 14px; border-radius: 10px;
         padding: 13px 16px; margin-top: 12px; }
  .btn-primary { background: #0a0a0a; color: #fff; }
  .btn-secondary { background: #fff; color: #0a0a0a; border: 1px solid #0a0a0a; }
  .note { font-size: 12px; color: #a1a1aa; margin: 18px 0 0; }
</style>
</head>
<body>
  <div class="card">
    <p class="brand">B-Edge</p>` + body + `
  </div>
</body>
</html>`
}
