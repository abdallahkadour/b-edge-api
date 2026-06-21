// Package discovery implements the public customer-facing artist discovery surface.
package discovery

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

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
// All discovery routes are public (no auth) — this is the customer browse surface.
//
//	GET /api/v1/discovery/artists      — browse/search artist cards
//	GET /api/v1/discovery/artists/:id  — public artist profile (stores + services)
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	d := app.Group("/api/v1/discovery")
	d.Get("/artists", handler.ListArtists)
	d.Get("/artists/:id", handler.GetArtistProfile)
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
	params := ListArtistsParams{
		City:     c.Query("city"),
		Category: c.Query("category"),
		Query:    c.Query("q"),
		Limit:    c.QueryInt("limit", 0),
	}

	cards, err := h.svc.ListArtists(c.Context(), params)
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
	artistID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	profile, err := h.svc.GetArtistProfile(c.Context(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, profile)
}
