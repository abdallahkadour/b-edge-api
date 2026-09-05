package share

import (
	"errors"
	"fmt"
	"html"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/bidi"
)

// defaultOGImage is advertised for artists with no portfolio photo. A card
// with a title and description but a branded fallback image still previews
// well; omitting og:image entirely makes WhatsApp render a bare text row.
const defaultOGImage = "/assets/og-default.png"

// Handler serves the crawlable preview document.
type Handler struct {
	repo      Repository
	log       *zap.Logger
	clientURL string
}

// NewHandler creates a share Handler.
//
// clientURL is where human visitors are sent. It is read from the same
// CLIENT_URL the CORS whitelist uses, taking the first entry when several
// are configured, so there is no second place to keep the customer app's
// address in step.
func NewHandler(repo Repository, log *zap.Logger) *Handler {
	client := strings.TrimSpace(strings.Split(os.Getenv("CLIENT_URL"), ",")[0])
	return &Handler{repo: repo, log: log, clientURL: strings.TrimSuffix(client, "/")}
}

// RegisterRoutes mounts the share routes.
//
// Deliberately NOT under /api/v1: this returns HTML to a crawler, not JSON
// to a client, and the path is one a human will see in a shared message.
// "/a/rania" is short enough to read aloud.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	h := NewHandler(NewRepository(pool), log)
	app.Get("/a/:handle", h.ArtistPreview)
}

// ArtistPreview returns an HTML document carrying Open Graph tags.
//
// One document is served to everyone rather than sniffing user agents. The
// crawler reads the meta tags and stops; the human's browser runs the
// redirect. Sniffing would mean serving different content to different
// callers for the same URL, which is both fragile (the UA list is never
// complete) and the exact pattern search engines treat as cloaking.
func (h *Handler) ArtistPreview(c *fiber.Ctx) error {
	slug := c.Params("handle")

	preview, err := h.repo.GetPreviewByHandleOrID(c.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			// Send unknown links to the app's home rather than showing an
			// error page: a stale shared link should land somewhere useful,
			// and a crawler gets nothing to preview either way.
			return c.Redirect(h.clientURL+"/", fiber.StatusFound)
		}
		h.log.Error("share: preview lookup failed", zap.String("slug", slug), zap.Error(err))
		return c.Redirect(h.clientURL+"/", fiber.StatusFound)
	}

	target := fmt.Sprintf("%s/book/%s", h.clientURL, preview.ShareSlug())

	image := h.clientURL + defaultOGImage
	if u, ok := preview.OGImageURL(); ok {
		image = u
	}

	title := preview.Name
	if preview.Category != nil && *preview.Category != "" {
		title = fmt.Sprintf("%s · %s on B-Edge", preview.Name, *preview.Category)
	}

	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)

	// Narrow the API-wide `default-src 'none'` (middleware/secheaders.go)
	// just enough for this page, and no further. The only thing it needs
	// beyond nothing is its own inline redirect script.
	//
	// 'unsafe-inline' is unavoidable while the script interpolates the
	// target URL: a hash cannot cover content that varies per request. It
	// adds no new exposure - escaping via jsString is what actually defends
	// INJ-03, and that is unchanged - it simply means CSP contributes no
	// second layer here. The page also carries <meta http-equiv="refresh">,
	// so the script could be dropped entirely and this tightened to
	// 'none'; that is a behaviour change with tests attached, deliberately
	// not made in passing.
	c.Set("Content-Security-Policy",
		"default-src 'none'; script-src 'unsafe-inline'; "+
			"frame-ancestors 'none'; base-uri 'none'; form-action 'none'")

	return c.SendString(renderPreviewHTML(title, preview.Description(), image, target))
}

// renderPreviewHTML builds the preview document.
//
// Every interpolated value is HTML-escaped. An artist's bio is
// user-controlled free text that lands inside a meta attribute, so this is
// the one place in this package where a missing escape would be an
// injection rather than a cosmetic bug.
//
// The redirect uses BOTH a meta refresh and a script: the meta tag works
// with JavaScript disabled, and the script fires immediately rather than
// after the browser's refresh tick, so a human barely sees this page.
// Crawlers run neither.
func renderPreviewHTML(title, description, image, target string) string {
	// Bidi controls are stripped BEFORE escaping, because they are not
	// HTML-special and would otherwise pass straight through - see
	// bidi.StripControls for the spoofing this prevents.
	t := html.EscapeString(bidi.StripControls(title))
	d := html.EscapeString(bidi.StripControls(description))
	i := html.EscapeString(image)
	u := html.EscapeString(target)

	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>` + t + `</title>
<meta name="description" content="` + d + `">

<meta property="og:type" content="profile">
<meta property="og:title" content="` + t + `">
<meta property="og:description" content="` + d + `">
<meta property="og:image" content="` + i + `">
<meta property="og:url" content="` + u + `">
<meta property="og:site_name" content="B-Edge">

<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="` + t + `">
<meta name="twitter:description" content="` + d + `">
<meta name="twitter:image" content="` + i + `">

<link rel="canonical" href="` + u + `">
<meta http-equiv="refresh" content="0; url=` + u + `">
</head>
<body>
<p>Redirecting to <a href="` + u + `">` + t + `</a>…</p>
<script>window.location.replace(` + jsString(u) + `);</script>
</body>
</html>`
}

// jsString renders a JavaScript string literal safely.
//
// The URL is already HTML-escaped for attribute contexts, but a script body
// is a different context where &quot; would be literal text rather than a
// quote. Escaping the quote and backslash directly is what keeps the value
// from breaking out of the literal.
func jsString(s string) string {
	// "<" becomes its unicode escape so a value containing "</script>"
	// cannot terminate the surrounding element.
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", "", "\r", "", "<", `\u003c`)
	return `"` + r.Replace(s) + `"`
}
