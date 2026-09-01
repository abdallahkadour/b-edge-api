// Package booking implements the booking domain for B-Edge.
package booking

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/billing"
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
		log: log.With(zap.String("module", "booking")),
	}
}

// RegisterRoutes attaches all booking routes to the Fiber app.
//
// Public routes (no auth):
//
//	GET   /api/v1/bookings/slots             - slot availability for a date
//	POST  /api/v1/bookings/guest/hold        - guest holds a slot (C-04)
//	PATCH /api/v1/bookings/guest/:id/submit  - guest submits details (C-05)
//
// Protected routes (RequireAuth):
//
//	POST   /api/v1/bookings/           - create booking (authenticated customer)
//	GET    /api/v1/bookings/:id        - get booking by ID
//	PATCH  /api/v1/bookings/:id/submit - submit a held booking
//	... (artist-only lifecycle routes)
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	// billing.NewRepository is used here purely as a SubscriptionStatusReader
	// (see that interface's doc comment) - this domain's own Repository
	// stays entirely separate from billing's.
	svc := NewService(repo, billing.NewRepository(pool), log)
	handler := NewHandler(svc, log)

	// ── Public routes - no authentication required ────────────────────────────
	// Registered BEFORE the protected group so Fiber matches /slots and /guest/*
	// before the parametric /:id route in the protected group.
	pub := app.Group("/api/v1/bookings")
	pub.Get("/slots", handler.GetAvailableSlots)
	pub.Post("/guest/hold", handler.HoldGuestSlot)
	pub.Post("/waitlist", handler.JoinWaitlist)
	pub.Patch("/guest/:id/submit", handler.SubmitGuestBooking)

	// ── Protected routes - JWT required ──────────────────────────────────────
	b := app.Group("/api/v1/bookings", middleware.RequireAuth())

	// Booking lifecycle
	b.Post("/", handler.CreateBooking)
	b.Get("/:id", handler.GetBookingByID)
	b.Patch("/:id/submit", handler.SubmitBooking)
	b.Patch("/:id/approve", handler.ApproveBooking)
	b.Patch("/:id/deposit-received", handler.MarkDepositReceived)
	b.Patch("/:id/confirm-deposit", handler.ConfirmDeposit)
	b.Patch("/:id/confirm-payment", handler.ConfirmDepositReceived)
	b.Patch("/:id/refunded", middleware.RequireRole("artist", "admin"), handler.MarkRefunded)
	b.Patch("/:id/cancel", handler.CancelBooking)
	b.Patch("/:id/complete", handler.CompleteBooking)
	b.Patch("/:id/no-show", handler.MarkNoShow)
	// Dry run for bulk schedule shifting. POST rather than GET because it
	// takes a body, but it writes NOTHING - see PreviewShiftDay. Registered
	// before /:id would be needed only if it collided; "schedule" is a
	// literal segment on a different path shape, so ordering is safe here.
	b.Post("/schedule/shift-preview", handler.PreviewShiftDay)

	// List endpoints
	b.Get("/artist/:artist_id", middleware.RequireRole("artist", "admin"), handler.GetBookingsByArtist)
	b.Get("/artist/:artist_id/calendar", middleware.RequireRole("artist", "admin"), handler.GetArtistCalendar)
	b.Get("/artist/:artist_id/waitlist", middleware.RequireRole("artist", "admin"), handler.GetWaitlistByArtist)
	b.Get("/customer/me", handler.GetBookingsByCustomer)
}

// GetAvailableSlots godoc
// @Summary      Get available time slots (public)
// @Tags         bookings
// @Produce      json
// @Param        artist_id  query string true "Artist UUID"
// @Param        store_id   query string true "Store UUID"
// @Param        service_id query string true "Service UUID"
// @Param        date       query string true "Date in YYYY-MM-DD format"
// @Success      200 {object} response.Body{data=[]TimeSlot}
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

// HoldGuestSlot godoc
// @Summary      Hold a slot for a guest (no auth)
// @Description  Called when a guest taps a time slot on C-04. Creates a held
// @Description  booking reserved for 10 minutes and returns the hold deadline.
// @Tags         bookings
// @Accept       json
// @Produce      json
// @Param        body body HoldGuestSlotRequest true "Chosen slot"
// @Success      201 {object} response.Body{data=HoldGuestSlotResponse}
// @Failure      400 {object} response.ErrorBody
// @Failure      409 {object} response.ErrorBody "SLOT_UNAVAILABLE"
// @Router       /bookings/guest/hold [post]
func (h *Handler) HoldGuestSlot(c *fiber.Ctx) error {
	var req HoldGuestSlotRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	res, err := h.svc.HoldGuestSlot(c.Context(), req)
	if err != nil {
		return err
	}

	return response.Created(c, res)
}

// SubmitGuestBooking godoc
// @Summary      Submit a held guest booking (no auth)
// @Description  Attaches the guest's name and phone and transitions the held
// @Description  booking to pending. Must be called before the 10-minute hold
// @Description  expires. No authentication required.
// @Tags         bookings
// @Accept       json
// @Produce      json
// @Param        id   path string true "Booking UUID (from the hold step)"
// @Param        body body SubmitGuestBookingRequest true "Guest details"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      404 {object} response.ErrorBody "BOOKING_NOT_FOUND"
// @Failure      409 {object} response.ErrorBody "HOLD_EXPIRED"
// @Router       /bookings/guest/{id}/submit [patch]
func (h *Handler) SubmitGuestBooking(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	var req SubmitGuestBookingRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	booking, err := h.svc.SubmitGuestBooking(c.Context(), bookingID, req)
	if err != nil {
		return err
	}

	return response.OK(c, booking)
}

// CreateBooking godoc
// @Summary      Create a booking and hold the slot (authenticated)
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

	booking, err := h.svc.CreateBooking(c.Context(), req, customerID)
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
// @Success      200 {object} response.Body{data=EnrichedBookingResponse}
// @Failure      404 {object} response.ErrorBody
// @Router       /bookings/{id} [get]
func (h *Handler) GetBookingByID(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	requesterID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)

	booking, err := h.svc.GetEnrichedBookingByID(c.Context(), bookingID, requesterID, requesterRole)
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

// MarkRefundedRequest is the optional body for the refunded endpoint.
type MarkRefundedRequest struct {
	Reference *string `json:"reference"`
}

// MarkRefunded godoc
// @Summary      Record that an owed refund has been paid (artist only)
// @Description  Transitions refund_due to refunded. Refunds in Lebanon are
// @Description  out-of-band bank transfers, so this records the artist's own
// @Description  assertion that the money was sent - there is no rail to verify
// @Description  it against. Without this refund_due is terminal and the refund
// @Description  alert in the notification centre can never be cleared.
// @Description  The optional reference is the artist's own note (an OMT or Whish
// @Description  code) and is never shown to the customer.
// @Tags         bookings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Param        body body MarkRefundedRequest false "Optional reference note"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Failure      409 {object} response.ErrorBody "BOOKING_NOT_REFUND_DUE"
// @Router       /bookings/{id}/refunded [patch]
func (h *Handler) MarkRefunded(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	// Optional body, same as confirm-payment above.
	var req MarkRefundedRequest
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&req); err != nil {
			return apperror.BadRequest("INVALID_BODY", "Could not parse request body")
		}
	}

	booking, err := h.svc.MarkRefunded(c.Context(), bookingID, middleware.UserIDFromContext(c), req.Reference)
	if err != nil {
		return err
	}
	return response.OK(c, booking)
}

// ConfirmDepositReceivedRequest is the optional body for confirm-payment.
// Both fields are optional - an empty body is valid and behaves exactly as
// it did before this note field existed.
type ConfirmDepositReceivedRequest struct {
	Reference *string `json:"reference"`
}

// ConfirmDepositReceived godoc
// @Summary      Confirm deposit received and confirm booking in one step (artist only)
// @Description  Primary artist-facing deposit action - moves a booking straight
// @Description  from approved to confirmed, for the common case where the artist
// @Description  checks her OMT/Wish transfer and confirms the moment it lands.
// @Description  For edge cases (partial payment, disputed transfer) use the
// @Description  separate deposit-received / confirm-deposit endpoints instead.
// @Tags         bookings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path string true "Booking UUID"
// @Param        body body ConfirmDepositReceivedRequest false "Optional reference note"
// @Success      200 {object} response.Body{data=BookingResponse}
// @Router       /bookings/{id}/confirm-payment [patch]
func (h *Handler) ConfirmDepositReceived(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	// Body is entirely optional - this endpoint has always been callable
	// with no payload at all, and the reference note is a later addition.
	// Only attempt to parse if the caller actually sent something.
	var req ConfirmDepositReceivedRequest
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&req); err != nil {
			return apperror.BadRequest("INVALID_BODY", "Could not parse request body")
		}
	}

	requesterUserID := middleware.UserIDFromContext(c)

	booking, err := h.svc.ConfirmDepositReceived(c.Context(), bookingID, requesterUserID, req.Reference)
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
// @Router       /bookings/{id}/cancel [patch]
func (h *Handler) CancelBooking(c *fiber.Ctx) error {
	bookingID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid booking ID")
	}

	var req CancelBookingRequest
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
// @Description  Optional ?status= filters to one booking status (e.g. pending,
// @Description  approved, refund_due) for the dashboard tabs and queues.
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        artist_id path   string true  "Artist UUID"
// @Param        status    query  string false "Filter by booking status"
// @Param        cursor    query  string false "Pagination cursor"
// @Param        limit     query  int    false "Page size (default 20, max 100)"
// @Success      200 {object} response.Body{data=[]EnrichedBookingResponse}
// @Router       /bookings/artist/{artist_id} [get]
func (h *Handler) GetBookingsByArtist(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artist_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	cursor, limit := parsePaginationParams(c)
	status := c.Query("status") // optional; "" = all

	requesterUserID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)

	bookings, hasMore, err := h.svc.ListEnrichedBookingsByArtist(
		c.Context(), artistID, requesterUserID, requesterRole, status, cursor, limit)
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

// GetArtistCalendar godoc
// @Summary      Get an artist's committed appointments for a week (calendar grid)
// @Description  Returns CalendarStatuses appointments in the 7-day window starting
// @Description  at week_start (YYYY-MM-DD), ordered by start time. No pagination.
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        artist_id  path  string true  "Artist UUID"
// @Param        week_start query string true  "Week start date YYYY-MM-DD"
// @Success      200 {object} response.Body{data=[]EnrichedBookingResponse}
// @Failure      400 {object} response.ErrorBody
// @Router       /bookings/artist/{artist_id}/calendar [get]
func (h *Handler) GetArtistCalendar(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artist_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	weekStartRaw := c.Query("week_start")
	if weekStartRaw == "" {
		return apperror.BadRequest("MISSING_WEEK_START", "week_start query parameter is required (YYYY-MM-DD)")
	}
	weekStart, err := time.Parse("2006-01-02", weekStartRaw)
	if err != nil {
		return apperror.BadRequest("INVALID_WEEK_START", "week_start must be in YYYY-MM-DD format")
	}

	requesterUserID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)

	bookings, err := h.svc.ListEnrichedBookingsForWeek(
		c.Context(), artistID, requesterUserID, requesterRole, weekStart)
	if err != nil {
		return err
	}

	// A calendar week is bounded, so it returns as a plain list with no cursor.
	return response.List(c, bookings, &response.Meta{HasMore: false})
}

// JoinWaitlist godoc
// @Summary      Join the waitlist for a fully-booked date (public, no account)
// @Description  Guest-friendly, matching the rest of the booking funnel
// @Description  identity is resolved by phone, no login required.
// @Tags         bookings
// @Accept       json
// @Param        body body JoinWaitlistRequest true "Waitlist request"
// @Success      201
// @Router       /bookings/waitlist [post]
func (h *Handler) JoinWaitlist(c *fiber.Ctx) error {
	var req JoinWaitlistRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	entryID, err := h.svc.JoinWaitlist(c.Context(), req)
	if err != nil {
		return err
	}

	return response.Created(c, fiber.Map{"id": entryID})
}

// GetWaitlistByArtist godoc
// @Summary      Get an artist's active waitlist queue
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        artist_id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=[]WaitlistEntryResponse}
// @Router       /bookings/artist/{artist_id}/waitlist [get]
func (h *Handler) GetWaitlistByArtist(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artist_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	requesterUserID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)

	entries, err := h.svc.GetWaitlistByArtist(c.Context(), artistID, requesterUserID, requesterRole)
	if err != nil {
		return err
	}

	return response.OK(c, entries)
}

// GetBookingsByCustomer godoc
// @Summary      Get paginated bookings for the authenticated customer
// @Tags         bookings
// @Security     BearerAuth
// @Produce      json
// @Param        cursor query string false "Pagination cursor"
// @Param        limit  query int    false "Page size (default 20)"
// @Success      200 {object} response.Body{data=[]EnrichedBookingResponse}
// @Router       /bookings/customer/me [get]
func (h *Handler) GetBookingsByCustomer(c *fiber.Ctx) error {
	customerID := middleware.UserIDFromContext(c)
	cursor, limit := parsePaginationParams(c)

	bookings, hasMore, err := h.svc.ListEnrichedBookingsByCustomer(c.Context(), customerID, cursor, limit)
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

// PreviewShiftDay godoc
// @Summary      Preview shifting a day's bookings
// @Description  Dry run. Returns what would move, what would be skipped, and every reason the shift cannot proceed. Writes nothing.
// @Tags         bookings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body ShiftPreviewRequest true "Store, date and shift in minutes"
// @Success      200 {object} response.Body{data=ShiftPreviewResponse}
// @Failure      400 {object} response.ErrorBody "INVALID_DATE"
// @Failure      404 {object} response.ErrorBody "STORE_NOT_FOUND"
// @Router       /bookings/schedule/shift-preview [post]
func (h *Handler) PreviewShiftDay(c *fiber.Ctx) error {
	var req ShiftPreviewRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Invalid request body")
	}

	userID := middleware.UserIDFromContext(c)

	result, err := h.svc.PreviewShiftDay(c.Context(), userID, req)
	if err != nil {
		return err
	}
	return response.OK(c, result)
}
