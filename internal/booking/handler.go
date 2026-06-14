// Package booking implements the booking domain for B-Edge.
// HTTP request
//
//	→ Handler    parses + validates
//	→ Service    applies business rules
//	→ Repository runs SQL
//	→ PostgreSQL stores/returns data
//	→ Repository returns data or sentinel error
//	→ Service    converts to response or AppError
//	→ Handler    writes JSON
//	→ Client     receives result
package booking

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler handles all HTTP requests for the booking domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new booking Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{
		svc: svc,
		// This attaches "module: booking" to every log inside this file
		log: log.With(zap.String("module", "booking")),
	}
}

// RegisterRoutes attaches all booking routes to the Fiber app.
// Called once from cmd/main.go during server startup.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	b := app.Group("/api/v1/bookings", middleware.RequireAuth())

	// Slot availability — customer checks open slots
	b.Get("/slots", handler.GetAvailableSlots)

	// Booking lifecycle
	b.Post("/", handler.CreateBooking)
	b.Get("/:id", handler.GetBookingByID)
	b.Patch("/:id/submit", handler.SubmitBooking)
	b.Patch("/:id/approve", handler.ApproveBooking)
	b.Patch("/:id/deposit-received", handler.MarkDepositReceived)
	b.Patch("/:id/confirm-deposit", handler.ConfirmDeposit)
	b.Patch("/:id/cancel", handler.CancelBooking)
	b.Patch("/:id/complete", handler.CompleteBooking)
	b.Patch("/:id/no-show", handler.MarkNoShow)

	// List endpoints
	b.Get("/artist/:artist_id", middleware.RequireRole("artist", "admin"), handler.GetBookingsByArtist)
	b.Get("/customer/me", handler.GetBookingsByCustomer)
}

// GetAvailableSlots godoc
// @Summary      Get available time slots
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        artist_id  query string true "Artist UUID"
// @Param        store_id   query string true "Store UUID"
// @Param        service_id query string true "Service UUID"
// @Param        date       query string true "Date in YYYY-MM-DD format"
// @Success      200 {object} response.Body{data=[]TimeSlot}
// @Failure      400 {object} response.ErrorBody
// @Router       /bookings/slots [get]
func (h *Handler) GetAvailableSlots(c *fiber.Ctx) error {
	req := GetAvailableSlotsRequest{
		ArtistID:  c.Query("artist_id"),
		StoreID:   c.Query("store_id"),
		ServiceID: c.Query("service_id"),
		Date:      c.Query("date"),
	}

	slots, err := h.svc.GetAvailableSlots(c.Context(), req)
	if err != nil {
		return err
	}

	return response.OK(c, slots)
}

// CreateBooking godoc
// @Summary      Create a booking and hold the slot
// @Tags         bookings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body CreateBookingRequest true "Booking details"
// @Success      201 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /bookings [post]
func (h *Handler) CreateBooking(c *fiber.Ctx) error {
	var req CreateBookingRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	customerID := middleware.UserIDFromContext(c)
	salonID := middleware.SalonIDFromContext(c)

	if salonID == nil {
		// Customers do not have a salon_id in their token.
		// The salon_id comes from the artist/store being booked.
		// We resolve it from the store in the service layer.
		// For now pass a zero UUID — service will resolve it.
		zero := uuid.Nil
		salonID = &zero
	}

	booking, err := h.svc.CreateBooking(c.Context(), req, customerID, *salonID)
	if err != nil {
		return err
	}

	return response.Created(c, booking)
}

// GetBookingByID godoc
// @Summary      Get a booking by ID
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      404 {object} response.ErrorBody
// @Router       /bookings/{id} [get]
func (h *Handler) GetBookingByID(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	requesterID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)

	booking, err := h.svc.GetBookingByID(c.Context(), bookingID, requesterID, requesterRole)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// SubmitBooking godoc
// @Summary      Submit a held booking (transition held → pending)
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /bookings/{id}/submit [patch]
func (h *Handler) SubmitBooking(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	customerID := middleware.UserIDFromContext(c)

	booking, err := h.svc.SubmitBooking(c.Context(), bookingID, customerID)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// ApproveBooking godoc
// @Summary      Approve a pending booking (artist only)
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /bookings/{id}/approve [patch]
func (h *Handler) ApproveBooking(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	artistID := middleware.UserIDFromContext(c)

	booking, err := h.svc.ApproveBooking(c.Context(), bookingID, artistID)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// MarkDepositReceived godoc
// @Summary      Mark deposit as received (artist only)
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /bookings/{id}/deposit-received [patch]
func (h *Handler) MarkDepositReceived(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	artistID := middleware.UserIDFromContext(c)

	booking, err := h.svc.MarkDepositReceived(c.Context(), bookingID, artistID)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// ConfirmDeposit godoc
// @Summary      Confirm booking after deposit verification (artist only)
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /bookings/{id}/confirm-deposit [patch]
func (h *Handler) ConfirmDeposit(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	artistID := middleware.UserIDFromContext(c)

	booking, err := h.svc.ConfirmDeposit(c.Context(), bookingID, artistID)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// CancelBooking godoc
// @Summary      Cancel a booking
// @Tags         bookings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id   path string true "Booking UUID"
// @Param        body body CancelBookingRequest false "Cancellation reason"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /bookings/{id}/cancel [patch]
func (h *Handler) CancelBooking(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	var req CancelBookingRequest
	// Body is optional — cancellation reason may not be provided
	_ = c.BodyParser(&req)

	requesterID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)

	booking, err := h.svc.CancelBooking(c.Context(), bookingID, requesterID, requesterRole, req)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// CompleteBooking godoc
// @Summary      Mark a booking as completed (artist only)
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /bookings/{id}/complete [patch]
func (h *Handler) CompleteBooking(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	artistID := middleware.UserIDFromContext(c)

	booking, err := h.svc.CompleteBooking(c.Context(), bookingID, artistID)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// MarkNoShow godoc
// @Summary      Mark a booking as no-show (artist only)
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /bookings/{id}/no-show [patch]
func (h *Handler) MarkNoShow(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	artistID := middleware.UserIDFromContext(c)

	booking, err := h.svc.MarkNoShow(c.Context(), bookingID, artistID)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// GetBookingsByArtist godoc
// @Summary      Get paginated bookings for an artist
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        artist_id path   string true  "Artist UUID"
// @Param        cursor    query  string false "Pagination cursor (created_at of last item)"
// @Param        limit     query  int    false "Page size (default 20, max 100)"
// @Success      200 {object} response.Body{data=[]BookingResponse}
// @Router       /bookings/artist/{artist_id} [get]
func (h *Handler) GetBookingsByArtist(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artist_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	cursor, limit := parsePaginationParams(c)

	bookings, hasMore, err := h.svc.GetBookingsByArtist(c.Context(), artistID, cursor, limit)
	if err != nil {
		return err
	}

	var nextCursor string
	if hasMore && len(bookings) > 0 {
		nextCursor = bookings[len(bookings)-1].CreatedAt.Format(time.RFC3339Nano)
	}

	return response.List(c, bookings, &response.Meta{
		NextCursor: nextCursor,
		HasMore:    hasMore,
	})
}

// GetBookingsByCustomer godoc
// @Summary      Get paginated bookings for the authenticated customer
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        cursor query string false "Pagination cursor"
// @Param        limit  query int    false "Page size (default 20)"
// @Success      200 {object} response.Body{data=[]BookingResponse}
// @Router       /bookings/customer/me [get]
func (h *Handler) GetBookingsByCustomer(c *fiber.Ctx) error {
	customerID := middleware.UserIDFromContext(c)
	cursor, limit := parsePaginationParams(c)

	bookings, hasMore, err := h.svc.GetBookingsByCustomer(c.Context(), customerID, cursor, limit)
	if err != nil {
		return err
	}

	var nextCursor string
	if hasMore && len(bookings) > 0 {
		nextCursor = bookings[len(bookings)-1].CreatedAt.Format(time.RFC3339Nano)
	}

	return response.List(c, bookings, &response.Meta{
		NextCursor: nextCursor,
		HasMore:    hasMore,
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// parsePaginationParams extracts cursor and limit from query params.
// cursor defaults to now (returns most recent bookings).
// limit defaults to 20.
func parsePaginationParams(c *fiber.Ctx) (time.Time, int) {
	cursor := time.Now().UTC()
	if raw := c.Query("cursor"); raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			cursor = t
		}
	}

	limit := 20
	if raw := c.QueryInt("limit", 20); raw > 0 && raw <= 100 {
		limit = raw
	}

	return cursor, limit
}
