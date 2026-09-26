package offering

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/clientip"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

type Handler struct{ svc *Service }

// RegisterRoutes mounts the offering endpoints under /artists/salon/, where
// routecoverage_test.go demands a capability guard on every write.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, _ *zap.Logger) {
	h := &Handler{svc: NewService(NewRepository(pool), audit.NewRepository(pool))}
	g := app.Group("/api/v1/artists/salon", middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))
	canWriteOwnServices := middleware.RequireSalonCapability(salonrole.OwnServicesWrite)
	canWriteMemberServices := middleware.RequireSalonCapability(salonrole.MemberServicesWrite)

	g.Get("/my-services", canWriteOwnServices, h.ListMine)
	g.Put("/my-services/:serviceId", canWriteOwnServices, h.UpdateMine)
	g.Get("/members/:artistId/services", canWriteMemberServices, h.ListForMember)
	g.Put("/members/:artistId/services/:serviceId", canWriteMemberServices, h.UpdateForMember)
}

func param(c *fiber.Ctx, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params(name))
	if err != nil {
		return uuid.Nil, apperror.BadRequest("INVALID_ID", "Invalid "+name)
	}
	return id, nil
}

// ListMine godoc
// @Summary  The salon menu with my switches, prices and deposits
// @Tags     offerings
// @Security BearerAuth
// @Success  200 {object} response.Body{data=[]Offering}
// @Failure  404 {object} response.ErrorBody "MEMBER_NOT_FOUND - no longer in the token's salon"
// @Router   /artists/salon/my-services [get]
func (h *Handler) ListMine(c *fiber.Ctx) error {
	out, err := h.svc.ListMine(c.UserContext(), *middleware.SalonIDFromContext(c), middleware.UserIDFromContext(c))
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// UpdateMine godoc
// @Summary  Switch one of my services on or off, and set my price and deposit
// @Tags     offerings
// @Security BearerAuth
// @Param    serviceId path string true "Service ID"
// @Param    body body UpdateRequest true "Switch, price, deposit"
// @Success  200 {object} response.Body{data=Offering}
// @Failure  400 {object} response.ErrorBody "INVALID_ID, INVALID_PRICE, INVALID_DEPOSIT_AMOUNT"
// @Failure  404 {object} response.ErrorBody "MEMBER_NOT_FOUND - no longer in the token's salon; SERVICE_NOT_FOUND"
// @Failure  422 {object} response.ErrorBody "VALIDATION_ERROR - offered missing, or deposit above the price"
// @Router   /artists/salon/my-services/{serviceId} [put]
func (h *Handler) UpdateMine(c *fiber.Ctx) error {
	serviceID, err := param(c, "serviceId")
	if err != nil {
		return err
	}
	var req UpdateRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	out, err := h.svc.UpdateMine(c.UserContext(), *middleware.SalonIDFromContext(c),
		middleware.UserIDFromContext(c), serviceID, req, clientip.From(c))
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// ListForMember godoc
// @Summary  A member's switches, prices and deposits (owner)
// @Tags     offerings
// @Security BearerAuth
// @Param    artistId path string true "Member artist ID"
// @Success  200 {object} response.Body{data=[]Offering}
// @Failure  400 {object} response.ErrorBody "INVALID_ID"
// @Failure  404 {object} response.ErrorBody "MEMBER_NOT_FOUND"
// @Router   /artists/salon/members/{artistId}/services [get]
func (h *Handler) ListForMember(c *fiber.Ctx) error {
	artistID, err := param(c, "artistId")
	if err != nil {
		return err
	}
	out, err := h.svc.ListForMember(c.UserContext(), *middleware.SalonIDFromContext(c), artistID)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// UpdateForMember godoc
// @Summary  Change a member's switch, price or deposit (owner)
// @Tags     offerings
// @Security BearerAuth
// @Param    artistId path string true "Member artist ID"
// @Param    serviceId path string true "Service ID"
// @Param    body body UpdateRequest true "Switch, price, deposit"
// @Success  200 {object} response.Body{data=Offering}
// @Failure  400 {object} response.ErrorBody "INVALID_ID, INVALID_PRICE, INVALID_DEPOSIT_AMOUNT"
// @Failure  404 {object} response.ErrorBody "MEMBER_NOT_FOUND, SERVICE_NOT_FOUND"
// @Failure  422 {object} response.ErrorBody "VALIDATION_ERROR - offered missing, or deposit above the price"
// @Router   /artists/salon/members/{artistId}/services/{serviceId} [put]
func (h *Handler) UpdateForMember(c *fiber.Ctx) error {
	artistID, err := param(c, "artistId")
	if err != nil {
		return err
	}
	serviceID, err := param(c, "serviceId")
	if err != nil {
		return err
	}
	var req UpdateRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	out, err := h.svc.UpdateForMember(c.UserContext(), *middleware.SalonIDFromContext(c),
		middleware.UserIDFromContext(c), artistID, serviceID, req, clientip.From(c))
	if err != nil {
		return err
	}
	return response.OK(c, out)
}
