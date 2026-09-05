// Package media implements the media/portfolio domain for B-Edge,
// providing portfolio photo management for artist profiles.
package media

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// Handler handles all HTTP requests for the media domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new media Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{
		svc: svc,
		log: log.With(zap.String("module", "media")),
	}
}

// RegisterRoutes attaches all media routes to the Fiber app.
//
// Public routes (no auth):
//
//	GET  /api/v1/media/portfolio/:artist_id           - public portfolio for a given artist
//	GET  /api/v1/media/products/:product_id/photos    - public photo gallery for a product
//
// Protected routes (artist Bearer):
//
//	GET    /api/v1/media/my                            - own portfolio
//	POST   /api/v1/media                               - upload a portfolio photo
//	DELETE /api/v1/media/:id                            - delete a portfolio photo
//	PATCH  /api/v1/media/:id/cover                      - set portfolio cover photo
//	PATCH  /api/v1/media/reorder                        - reorder portfolio photos
//	POST   /api/v1/media/products/:product_id/photos            - add a product gallery photo
//	PATCH  /api/v1/media/products/:product_id/photos/reorder    - reorder a product's gallery
//	DELETE /api/v1/media/product-photos/:id                     - delete a product gallery photo
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	// ── Public routes ─────────────────────────────────────────────────────────
	app.Get("/api/v1/media/portfolio/:artist_id", handler.GetPortfolio)
	app.Get("/api/v1/media/products/:product_id/photos", handler.GetProductPhotos)

	// ── Protected routes ──────────────────────────────────────────────────────
	m := app.Group("/api/v1/media", middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))
	m.Get("/my", handler.GetMyPortfolio)
	// Upload sits INSIDE the authed group deliberately: an unauthenticated
	// upload endpoint would recreate the exact hole signed uploads were
	// meant to close. This replaced a signature-only endpoint that let the
	// browser upload directly to Cloudinary - the file now passes through
	// this server first (see ProcessAndUploadImage's doc comment for why).
	m.Post("/upload", handler.UploadImage)
	m.Post("/", handler.AddPhoto)
	m.Patch("/reorder", handler.Reorder) // must be before /:id to avoid route conflict
	// /products/:product_id/... registered BEFORE /:id so a literal
	// "products" path segment never gets swallowed as a media UUID.
	m.Post("/products/:product_id/photos", handler.AddProductPhoto)
	m.Patch("/products/:product_id/photos/reorder", handler.ReorderProductPhotos)
	m.Delete("/product-photos/:id", handler.DeleteProductPhoto)
	m.Delete("/:id", handler.DeletePhoto)
	m.Patch("/:id/cover", handler.SetCover)
	m.Put("/:id/services", handler.SetMediaServices)
}

// GetPortfolio godoc
// @Summary      Get public portfolio for an artist
// @Description  Returns all photos for the artist ordered by display_order.
// @Description  No authentication required - used by the customer discovery screen.
// @Tags         media
// @Produce      json
// @Param        artist_id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=PortfolioResponse}
// @Failure      400 {object} response.ErrorBody
// @Router       /media/portfolio/{artist_id} [get]
func (h *Handler) GetPortfolio(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artist_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	portfolio, err := h.svc.GetPortfolio(c.Context(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, portfolio)
}

// GetMyPortfolio godoc
// @Summary      Get the authenticated artist's own portfolio
// @Tags         media
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=PortfolioResponse}
// @Failure      404 {object} response.ErrorBody "ARTIST_NOT_FOUND"
// @Router       /media/my [get]
func (h *Handler) GetMyPortfolio(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	portfolio, err := h.svc.GetMyPortfolio(c.Context(), userID)
	if err != nil {
		return err
	}

	return response.OK(c, portfolio)
}

// AddPhoto godoc
// @Summary      Add a photo to the artist's portfolio
// @Description  Appends a new photo at the end of the portfolio.
// @Description  Returns PORTFOLIO_FULL (409) if the artist already has 20 photos.
// @Description  The URL should be a publicly accessible Cloudinary URL.
// @Tags         media
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body AddMediaRequest true "Photo URL and optional Cloudinary ID"
// @Success      201 {object} response.Body{data=MediaResponse}
// @Failure      400 {object} response.ErrorBody
// @Failure      409 {object} response.ErrorBody "PORTFOLIO_FULL"
// @Router       /media [post]
func (h *Handler) AddPhoto(c *fiber.Ctx) error {
	var req AddMediaRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	userID := middleware.UserIDFromContext(c)

	photo, err := h.svc.AddPhoto(c.Context(), userID, req)
	if err != nil {
		return err
	}

	return response.Created(c, photo)
}

// DeletePhoto godoc
// @Summary      Delete a photo from the portfolio
// @Description  Permanently removes the photo. Only the owning artist can delete.
// @Tags         media
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Media UUID"
// @Success      204
// @Failure      403 {object} response.ErrorBody "FORBIDDEN"
// @Failure      404 {object} response.ErrorBody "MEDIA_NOT_FOUND"
// @Router       /media/{id} [delete]
func (h *Handler) DeletePhoto(c *fiber.Ctx) error {
	mediaID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid media ID")
	}

	userID := middleware.UserIDFromContext(c)

	if err := h.svc.DeletePhoto(c.Context(), userID, mediaID); err != nil {
		return err
	}

	return response.NoContent(c)
}

// SetCover godoc
// @Summary      Set a photo as the cover (first in portfolio)
// @Description  Moves the photo to display_order=0 and shifts all others up by 1.
// @Description  Only the owning artist can set the cover.
// @Tags         media
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Media UUID"
// @Success      204
// @Failure      403 {object} response.ErrorBody "FORBIDDEN"
// @Failure      404 {object} response.ErrorBody "MEDIA_NOT_FOUND"
// @Router       /media/{id}/cover [patch]
func (h *Handler) SetCover(c *fiber.Ctx) error {
	mediaID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid media ID")
	}

	userID := middleware.UserIDFromContext(c)

	if err := h.svc.SetCover(c.Context(), userID, mediaID); err != nil {
		return err
	}

	return response.NoContent(c)
}

// SetMediaServices godoc
// @Summary      Set which services a portfolio photo depicts
// @Description  Replaces the photo's entire tag set. Send an empty list to clear every tag. Every service must belong to the caller's salon.
// @Tags         media
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id   path string                   true "Media ID"
// @Param        body body SetMediaServicesRequest  true "Full desired set of service IDs"
// @Success      200 {object} response.Body{data=MediaResponse}
// @Failure      400 {object} response.ErrorBody "INVALID_SERVICE_ID"
// @Failure      404 {object} response.ErrorBody "MEDIA_NOT_FOUND"
// @Router       /media/{id}/services [put]
func (h *Handler) SetMediaServices(c *fiber.Ctx) error {
	mediaID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid media ID")
	}

	var req SetMediaServicesRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	userID := middleware.UserIDFromContext(c)

	item, err := h.svc.SetMediaServices(c.Context(), userID, mediaID, req)
	if err != nil {
		return err
	}

	return response.OK(c, item)
}

// Reorder godoc
// @Summary      Reorder all photos in the portfolio
// @Description  Accepts the full ordered list of media IDs. Must include all photos.
// @Tags         media
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body ReorderRequest true "Ordered list of all media IDs"
// @Success      204
// @Failure      400 {object} response.ErrorBody "INVALID_REORDER"
// @Router       /media/reorder [patch]
func (h *Handler) Reorder(c *fiber.Ctx) error {
	var req ReorderRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	userID := middleware.UserIDFromContext(c)

	if err := h.svc.Reorder(c.Context(), userID, req); err != nil {
		return err
	}

	return response.NoContent(c)
}

// ── Product gallery ─────────────────────────────────────────────────────────

// GetProductPhotos godoc
// @Summary      Get a product's photo gallery
// @Description  Additional photos beyond the product's own image_url (which
// @Description  stays the primary photo). No authentication required.
// @Tags         media
// @Produce      json
// @Param        product_id path string true "Product UUID"
// @Success      200 {object} response.Body{data=ProductGalleryResponse}
// @Failure      400 {object} response.ErrorBody
// @Router       /media/products/{product_id}/photos [get]
func (h *Handler) GetProductPhotos(c *fiber.Ctx) error {
	productID, err := uuid.Parse(c.Params("product_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid product ID")
	}

	gallery, err := h.svc.GetProductPhotos(c.Context(), productID)
	if err != nil {
		return err
	}
	return response.OK(c, gallery)
}

// AddProductPhoto godoc
// @Summary      Add a photo to a product's gallery
// @Description  Appends a new photo. Returns PRODUCT_GALLERY_FULL (409) at 8 photos.
// @Tags         media
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        product_id path string true "Product UUID"
// @Param        body body AddMediaRequest true "Photo URL and optional Cloudinary ID"
// @Success      201 {object} response.Body{data=MediaResponse}
// @Failure      403 {object} response.ErrorBody "FORBIDDEN"
// @Failure      409 {object} response.ErrorBody "PRODUCT_GALLERY_FULL"
// @Router       /media/products/{product_id}/photos [post]
func (h *Handler) AddProductPhoto(c *fiber.Ctx) error {
	productID, err := uuid.Parse(c.Params("product_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid product ID")
	}

	var req AddMediaRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	photo, err := h.svc.AddProductPhoto(c.Context(), productID, *salonID, req)
	if err != nil {
		return err
	}
	return response.Created(c, photo)
}

// DeleteProductPhoto godoc
// @Summary      Delete a photo from a product's gallery
// @Tags         media
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Media UUID"
// @Success      204
// @Failure      403 {object} response.ErrorBody "FORBIDDEN"
// @Failure      404 {object} response.ErrorBody "MEDIA_NOT_FOUND"
// @Router       /media/product-photos/{id} [delete]
func (h *Handler) DeleteProductPhoto(c *fiber.Ctx) error {
	mediaID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid media ID")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	if err := h.svc.DeleteProductPhoto(c.Context(), mediaID, *salonID); err != nil {
		return err
	}
	return response.NoContent(c)
}

// ReorderProductPhotos godoc
// @Summary      Reorder a product's gallery photos
// @Description  Accepts the full ordered list of media IDs. Must include all of this product's photos.
// @Tags         media
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        product_id path string true "Product UUID"
// @Param        body body ReorderRequest true "Ordered list of all media IDs"
// @Success      204
// @Failure      400 {object} response.ErrorBody "INVALID_REORDER"
// @Router       /media/products/{product_id}/photos/reorder [patch]
func (h *Handler) ReorderProductPhotos(c *fiber.Ctx) error {
	productID, err := uuid.Parse(c.Params("product_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid product ID")
	}

	var req ReorderRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	if err := h.svc.ReorderProductPhotos(c.Context(), productID, *salonID, req); err != nil {
		return err
	}
	return response.NoContent(c)
}

// UploadImage godoc
// @Summary      Upload an image (avatar, portfolio, or product photo)
// @Description  Accepts a single multipart file, validates and re-encodes
// @Description  it server-side (see ProcessAndUploadImage), then uploads
// @Description  the clean copy to Cloudinary. Returns the resulting URL,
// @Description  which the caller then attaches to the right place (POST
// @Description  /media for a portfolio photo, POST /media/products/:id/photos
// @Description  for a product photo, or PATCH the artist profile for an
// @Description  avatar) - this endpoint only produces a URL, it never
// @Description  decides what the photo is FOR.
// @Tags         media
// @Security     BearerAuth
// @Accept       multipart/form-data
// @Produce      json
// @Param        file formData file true "Image file (JPEG, PNG, GIF, or WebP, max 15MB)"
// @Success      200 {object} response.Body{data=UploadImageResponse}
// @Failure      400 {object} response.ErrorBody "INVALID_IMAGE or IMAGE_TOO_LARGE"
// @Failure      401 {object} response.Body
// @Router       /media/upload [post]
func (h *Handler) UploadImage(c *fiber.Ctx) error {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return apperror.BadRequest("MISSING_FILE", "No file was uploaded.")
	}

	result, err := ProcessAndUploadImage(fileHeader)
	if err != nil {
		return err
	}

	return response.OK(c, UploadImageResponse{
		URL:          result.URL,
		CloudinaryID: result.CloudinaryID,
	})
}
