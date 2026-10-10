// Package discovery implements the public customer-facing artist discovery surface.
package discovery

import (
	"github.com/abdallahkadour/b-edge-api/internal/pkg/httpcache"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler handles all HTTP requests for the discovery domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new discovery Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log.With(zap.String("module", "discovery"))}
}

// RegisterRoutes attaches discovery routes to the Fiber app.
//
// All discovery routes are public (no auth) - this is the customer browse surface.
//
//	GET /api/v1/discovery/artists      - browse/search artist cards
//	GET /api/v1/discovery/artists/:id  - public artist profile (stores + services)
//
// And, for a signed-in customer only:
//
//	GET    /api/v1/customers/me/favourites           - her saved artists, as cards
//	PUT    /api/v1/customers/me/favourites/:artistId - save one (repeatable)
//	DELETE /api/v1/customers/me/favourites/:artistId - forget one (repeatable)
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	d := app.Group("/api/v1/discovery")
	d.Get("/artists", handler.ListArtists)
	d.Get("/artists/:id", handler.GetArtistProfile)

	// A signed-in customer's saved artists (migration 056). Not public, and
	// not under /discovery, because the list is hers - but read through
	// Discover's card query, so who may be shown is decided in one place.
	fav := app.Group("/api/v1/customers/me/favourites", middleware.RequireAuth(), middleware.RequireRole("customer"))
	fav.Get("/", handler.ListFavourites)
	fav.Put("/:artistId", handler.AddFavourite)
	fav.Delete("/:artistId", handler.RemoveFavourite)
}

// ListArtists godoc
// @Summary      Browse and search artists (public)
// @Description  Returns artist cards for the discovery screen. An artist with
// @Description  stores in multiple cities appears once per city. Optional filters:
// @Description  city, category (one of makeup/hair/nails/lashes/skincare), and q
// @Description  (name search).
// @Tags         discovery
// @Produce      json
// @Param        city     query string false "Filter by store city"
// @Param        category query string false "Filter by artist category"
// @Param        q        query string false "Search artist name"
// @Param        limit    query int    false "Page size (default 20, max 50)"
// @Success      200 {object} response.Body{data=[]ArtistCard}
// @Failure      400 {object} response.ErrorBody "INVALID_CATEGORY"
// @Router       /discovery/artists [get]
func (h *Handler) ListArtists(c *fiber.Ctx) error {
	// Public and identical for every caller - safe for a shared cache.
	httpcache.Public(c, httpcache.Discovery)

	params := ListArtistsParams{
		City:     c.Query("city"),
		Category: c.Query("category"),
		Query:    c.Query("q"),
		Limit:    c.QueryInt("limit", 0),
	}

	cards, err := h.svc.ListArtists(c.UserContext(), params)
	if err != nil {
		return err
	}

	return response.OK(c, cards)
}

// GetArtistProfile godoc
// @Summary      Get an artist's public profile (public)
// @Description  Returns the artist with their stores and service menu in one
// @Description  response. Used by the customer-facing artist profile screen.
// @Tags         discovery
// @Produce      json
// @Param        id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=PublicArtistProfile}
// @Failure      404 {object} response.ErrorBody "ARTIST_NOT_FOUND"
// @Router       /discovery/artists/{id} [get]
func (h *Handler) GetArtistProfile(c *fiber.Ctx) error {
	// Public and identical for every caller - safe for a shared cache.
	httpcache.Public(c, httpcache.Discovery)

	artistID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	profile, err := h.svc.GetArtistProfile(c.UserContext(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, profile)
}

// ListFavourites godoc
// @Summary      A customer's saved artists
// @Description  As Discover cards, one per artist, through Discover's own query:
// @Description  a saved artist Discover would not show is not shown here either.
// @Tags         discovery
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]ArtistCard}
// @Router       /customers/me/favourites [get]
func (h *Handler) ListFavourites(c *fiber.Ctx) error {
	cards, err := h.svc.ListFavourites(c.UserContext(), middleware.UserIDFromContext(c))
	if err != nil {
		return err
	}
	return response.OK(c, cards)
}

// AddFavourite godoc
// @Summary      Save an artist
// @Description  Saving one already saved is not an error.
// @Tags         discovery
// @Security     BearerAuth
// @Param        artistId path string true "Artist UUID"
// @Success      204
// @Failure      404 {object} response.ErrorBody "ARTIST_NOT_FOUND"
// @Router       /customers/me/favourites/{artistId} [put]
func (h *Handler) AddFavourite(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artistId"))
	if err != nil {
		return apperror.NotFound("ARTIST_NOT_FOUND", "Artist not found")
	}
	if err := h.svc.AddFavourite(c.UserContext(), middleware.UserIDFromContext(c), artistID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// RemoveFavourite godoc
// @Summary      Forget a saved artist
// @Description  Forgetting one never saved is not an error.
// @Tags         discovery
// @Security     BearerAuth
// @Param        artistId path string true "Artist UUID"
// @Success      204
// @Router       /customers/me/favourites/{artistId} [delete]
func (h *Handler) RemoveFavourite(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artistId"))
	if err != nil {
		return c.SendStatus(fiber.StatusNoContent)
	}
	if err := h.svc.RemoveFavourite(c.UserContext(), middleware.UserIDFromContext(c), artistID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
