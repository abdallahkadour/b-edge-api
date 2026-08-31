// Package share tests. Pure functions plus one handler test through a real
// fiber app - no database.
package share

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func strPtr(s string) *string { return &s }

// ── OG image URL ──────────────────────────────────────────────────────────────

// The transformation is inserted after /upload/, which is what makes
// Cloudinary resize on delivery rather than us storing a derivative.
func TestOGImageURL_CloudinaryURL_InsertsTransform(t *testing.T) {
	a := ArtistPreview{CoverURL: strPtr("https://res.cloudinary.com/demo/image/upload/v123/photo.jpg")}

	got, ok := a.OGImageURL()

	require.True(t, ok)
	assert.Equal(t,
		"https://res.cloudinary.com/demo/image/upload/c_fill,g_auto,w_1200,h_630/v123/photo.jpg",
		got)
}

// A non-Cloudinary URL passes through unchanged. Serving a correctly-sized
// image matters less than serving one at all, so an unrecognised host is
// not a reason to drop the tag.
func TestOGImageURL_NonCloudinary_PassesThrough(t *testing.T) {
	a := ArtistPreview{CoverURL: strPtr("https://example.com/photo.jpg")}

	got, ok := a.OGImageURL()

	require.True(t, ok)
	assert.Equal(t, "https://example.com/photo.jpg", got)
}

// No photo reports false so the caller can fall back, rather than emitting
// an empty og:image which renders as a broken card.
func TestOGImageURL_NoCover_ReportsFalse(t *testing.T) {
	_, ok := ArtistPreview{}.OGImageURL()
	assert.False(t, ok)

	_, ok = ArtistPreview{CoverURL: strPtr("")}.OGImageURL()
	assert.False(t, ok)
}

// ── Share slug ────────────────────────────────────────────────────────────────

func TestShareSlug_PrefersHandle(t *testing.T) {
	id := uuid.New()
	a := ArtistPreview{ID: id, Handle: strPtr("rania")}
	assert.Equal(t, "rania", a.ShareSlug())
}

func TestShareSlug_NoHandle_FallsBackToID(t *testing.T) {
	id := uuid.New()
	assert.Equal(t, id.String(), ArtistPreview{ID: id}.ShareSlug())
	assert.Equal(t, id.String(), ArtistPreview{ID: id, Handle: strPtr("")}.ShareSlug())
}

// ── Description ───────────────────────────────────────────────────────────────

func TestDescription_PrefersBio(t *testing.T) {
	a := ArtistPreview{Bio: strPtr("Bridal specialist in Beirut."), Category: strPtr("makeup")}
	assert.Equal(t, "Bridal specialist in Beirut.", a.Description())
}

// A whitespace-only bio is treated as absent - it would otherwise render as
// a blank second line in the card rather than collapsing.
func TestDescription_BlankBio_FallsBackToComposed(t *testing.T) {
	a := ArtistPreview{Bio: strPtr("   "), Category: strPtr("makeup"), City: strPtr("Beirut")}
	assert.Equal(t, "Makeup in Beirut · Book on B-Edge.", a.Description())
}

func TestDescription_NothingKnown_StillNonEmpty(t *testing.T) {
	assert.NotEmpty(t, ArtistPreview{}.Description(),
		"an empty description renders as a blank row, so there must always be one")
}

// ── HTML escaping ─────────────────────────────────────────────────────────────

// An artist's bio is user-controlled free text landing inside a meta
// attribute. This is the one place in this package where a missing escape
// would be an injection rather than a cosmetic bug.
func TestRenderPreviewHTML_EscapesUserContent(t *testing.T) {
	html := renderPreviewHTML(
		`Rania" onload="alert(1)`,
		`</title><script>alert(2)</script>`,
		"https://example.com/i.jpg",
		"https://example.com/book/rania",
	)

	assert.NotContains(t, html, `onload="alert(1)"`)
	assert.NotContains(t, html, "<script>alert(2)</script>")
	assert.Contains(t, html, "&lt;script&gt;", "the injected tag must be escaped, not stripped")
}

// The redirect URL lands in a script body, a different escaping context
// from an HTML attribute. A value containing </script> must not be able to
// terminate the element.
func TestJSString_EscapesScriptTerminator(t *testing.T) {
	got := jsString(`https://x.test/</script><script>alert(1)</script>`)

	assert.NotContains(t, got, "</script>",
		"a literal </script> would terminate the surrounding element")
	assert.Contains(t, got, "\\u003c",
		"every < must survive as its unicode escape, which JS decodes back to the same string")
}

func TestJSString_EscapesQuotesAndBackslashes(t *testing.T) {
	got := jsString(`a"b\c`)
	assert.Equal(t, `"a\"b\\c"`, got)
}

// ── Handler ───────────────────────────────────────────────────────────────────

type stubRepo struct {
	preview *ArtistPreview
	err     error
}

func (s *stubRepo) GetPreviewByHandleOrID(_ context.Context, _ string) (*ArtistPreview, error) {
	return s.preview, s.err
}

func newTestApp(t *testing.T, repo Repository) *fiber.App {
	t.Helper()
	require.NoError(t, os.Setenv("CLIENT_URL", "https://bedge.app"))
	app := fiber.New()
	h := NewHandler(repo, zap.NewNop())
	app.Get("/a/:handle", h.ArtistPreview)
	return app
}

func TestArtistPreview_RendersOpenGraphTags(t *testing.T) {
	repo := &stubRepo{preview: &ArtistPreview{
		ID: uuid.New(), Handle: strPtr("rania"), Name: "Rania",
		Bio: strPtr("Bridal specialist."), Category: strPtr("makeup"),
		CoverURL: strPtr("https://res.cloudinary.com/demo/image/upload/v1/p.jpg"),
	}}
	app := newTestApp(t, repo)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/a/rania", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)
	doc := string(body)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get(fiber.HeaderContentType), "text/html")
	assert.Contains(t, doc, `property="og:title"`)
	assert.Contains(t, doc, `property="og:image"`)
	assert.Contains(t, doc, `name="twitter:card" content="summary_large_image"`)
	assert.Contains(t, doc, "c_fill,g_auto,w_1200,h_630", "the image must be the resized card format")
	assert.Contains(t, doc, "https://bedge.app/book/rania", "humans go to the funnel, by handle")
	assert.Contains(t, doc, "Rania")
}

// No photo still produces a full card, with a fallback image rather than an
// empty og:image.
func TestArtistPreview_NoCoverPhoto_StillEmitsImageTag(t *testing.T) {
	repo := &stubRepo{preview: &ArtistPreview{ID: uuid.New(), Name: "Newbie"}}
	app := newTestApp(t, repo)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/a/newbie", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, string(body), defaultOGImage)
	assert.NotContains(t, string(body), `og:image" content=""`)
}

// An unknown or hidden artist redirects home rather than showing an error
// page - a stale shared link should land somewhere useful.
func TestArtistPreview_UnknownArtist_RedirectsHome(t *testing.T) {
	app := newTestApp(t, &stubRepo{err: ErrArtistNotFound})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/a/nobody", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, fiber.StatusFound, resp.StatusCode)
	assert.Equal(t, "https://bedge.app/", resp.Header.Get("Location"))
}

// A database failure degrades the same way. A shared link is the first
// thing a prospective customer touches; a 500 there is worse than a
// redirect that merely lacks a preview.
func TestArtistPreview_RepoError_RedirectsHome(t *testing.T) {
	app := newTestApp(t, &stubRepo{err: errors.New("db down")})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/a/rania", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, fiber.StatusFound, resp.StatusCode)
}

// A trailing slash on CLIENT_URL must not produce "https://bedge.app//book/x".
func TestNewHandler_TrimsTrailingSlashAndTakesFirstOrigin(t *testing.T) {
	require.NoError(t, os.Setenv("CLIENT_URL", "https://bedge.app/,https://other.test"))
	h := NewHandler(&stubRepo{}, zap.NewNop())
	assert.Equal(t, "https://bedge.app", h.clientURL)
	assert.False(t, strings.HasSuffix(h.clientURL, "/"))
}
