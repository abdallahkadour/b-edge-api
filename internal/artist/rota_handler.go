package artist

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// rotaError maps the rota domain's sentinel errors onto HTTP.
//
// ErrStoreNotLinked is a 404 rather than a 403: a store the caller does not
// work at should be indistinguishable from one that does not exist, so store
// IDs cannot be probed. ErrNotAnArtist is a 403 because it describes the
// CALLER - the same reasoning that keeps NO_SALON and NOT_AN_ARTIST at 403
// while ownership failures are 404.
func rotaError(err error) error {
	switch {
	case errors.Is(err, ErrNotAnArtist):
		return apperror.Forbidden("NOT_AN_ARTIST", "This account has no artist profile")
	case errors.Is(err, ErrStoreNotLinked), errors.Is(err, pgx.ErrNoRows):
		return apperror.NotFound("STORE_NOT_FOUND", "No such store")
	}
	return err
}

// GetMyRota godoc
//
//	@Summary		My working hours
//	@Description	An empty list means available for the whole of each
//	@Description	store's opening hours - absence is not unavailability.
//	@Tags			artists
//	@Produce		json
//	@Success		200	{object}	response.Body{data=[]ArtistSchedule}
//	@Router			/artists/me/schedule [get]
func (h *Handler) GetMyRota(c *fiber.Ctx) error {
	rota, err := h.svc.GetMyRota(c.UserContext(), middleware.UserIDFromContext(c))
	if err != nil {
		return rotaError(err)
	}
	return response.OK(c, rota)
}

// SetMyRota godoc
//
//	@Summary		Replace my working hours at one store
//	@Description	Send the whole week. Days omitted are removed; a day you
//	@Description	do not work is sent with is_working false.
//	@Tags			artists
//	@Accept			json
//	@Produce		json
//	@Param			request	body		SetRotaRequest	true	"The week"
//	@Success		200		{object}	response.Body{data=[]ArtistSchedule}
//	@Failure		404		{object}	response.Body	"STORE_NOT_FOUND"
//	@Router			/artists/me/schedule [put]
func (h *Handler) SetMyRota(c *fiber.Ctx) error {
	var req SetRotaRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	if err := h.svc.validate.Struct(&req); err != nil {
		return validation.MapError(err)
	}

	userID := middleware.UserIDFromContext(c)
	if err := h.svc.SetMyRota(c.UserContext(), userID, req); err != nil {
		return rotaError(err)
	}
	rota, err := h.svc.GetMyRota(c.UserContext(), userID)
	if err != nil {
		return rotaError(err)
	}
	return response.OK(c, rota)
}

// GetMyScheduleExceptions godoc
//
//	@Summary	My upcoming days off and one-off hours
//	@Tags		artists
//	@Produce	json
//	@Success	200	{object}	response.Body{data=[]ArtistScheduleException}
//	@Router		/artists/me/schedule/exceptions [get]
func (h *Handler) GetMyScheduleExceptions(c *fiber.Ctx) error {
	out, err := h.svc.GetMyScheduleExceptions(c.UserContext(), middleware.UserIDFromContext(c))
	if err != nil {
		return rotaError(err)
	}
	return response.OK(c, out)
}

// SetMyScheduleException godoc
//
//	@Summary	Add or replace a personal date override
//	@Tags		artists
//	@Accept		json
//	@Produce	json
//	@Param		request	body		CreateScheduleExceptionRequest	true	"Override"
//	@Success	200		{object}	response.Body{data=[]ArtistScheduleException}
//	@Router		/artists/me/schedule/exceptions [post]
func (h *Handler) SetMyScheduleException(c *fiber.Ctx) error {
	var req CreateScheduleExceptionRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	if err := h.svc.validate.Struct(&req); err != nil {
		return validation.MapError(err)
	}

	userID := middleware.UserIDFromContext(c)
	if err := h.svc.SetMyScheduleException(c.UserContext(), userID, req); err != nil {
		return rotaError(err)
	}
	out, err := h.svc.GetMyScheduleExceptions(c.UserContext(), userID)
	if err != nil {
		return rotaError(err)
	}
	return response.OK(c, out)
}

// DeleteMyScheduleException godoc
//
//	@Summary	Remove a personal date override
//	@Tags		artists
//	@Param		id	path	string	true	"Exception UUID"
//	@Success	204
//	@Router		/artists/me/schedule/exceptions/{id} [delete]
func (h *Handler) DeleteMyScheduleException(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		// A malformed id and another artist's exception answer identically.
		return apperror.NotFound("EXCEPTION_NOT_FOUND", "No such exception")
	}
	if err := h.svc.DeleteMyScheduleException(c.UserContext(),
		middleware.UserIDFromContext(c), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperror.NotFound("EXCEPTION_NOT_FOUND", "No such exception")
		}
		return rotaError(err)
	}
	return response.NoContent(c)
}
