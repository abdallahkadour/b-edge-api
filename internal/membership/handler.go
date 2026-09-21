package membership

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/onboarding"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/clientip"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

type Handler struct {
	svc *Service
	log *zap.Logger
}

func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes wires the membership domain.
//
// Route grouping mirrors the capability boundary exactly:
//
//   - /artists/salon/members     owner writes, member reads
//   - /artists/salon/invitations owner only
//   - /invitations/:token        PUBLIC - see the warning on Preview
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger, inviteBase string) {
	svc := NewService(
		NewRepository(pool),
		onboarding.NewRepository(pool),
		newNotifier(pool),
		newTokenInvalidator(pool),
		audit.NewRepository(pool),
		inviteBase,
	)
	h := NewHandler(svc, log)

	auth := middleware.RequireAuth()
	artistOnly := middleware.RequireRole("artist", "admin")
	canWriteMembers := middleware.RequireSalonCapability(salonrole.MembersWrite)

	g := app.Group("/api/v1/artists/salon", auth, artistOnly)

	// Reads: every member sees who they work with (salonrole.MembersRead).
	g.Get("/members", h.ListMembers)

	// Writes: the owner's.
	g.Post("/members/invite", canWriteMembers, h.Invite)
	g.Delete("/members/:artistId", canWriteMembers, h.RemoveMember)
	g.Get("/invitations", canWriteMembers, h.ListInvitations)
	g.Delete("/invitations/:id", canWriteMembers, h.RevokeInvitation)
	g.Post("/owner/transfer", canWriteMembers, h.TransferOwnership)

	// Leaving is not an owner action - it is the one membership write a
	// member performs on themselves.
	g.Post("/members/leave", h.Leave)

	// ── PUBLIC ───────────────────────────────────────────────────────────
	// Anyone holding a link can call the preview. It returns the salon name
	// and the inviter's name and nothing else; see InvitationPreview.
	app.Get("/api/v1/invitations/:token", h.PreviewInvitation)
	app.Post("/api/v1/invitations/:token/decline", h.DeclineInvitation)

	// Accepting requires an account - the artist row is created for the
	// authenticated caller.
	app.Post("/api/v1/invitations/:token/accept", auth, h.AcceptInvitation)
}

// ── Owner actions ─────────────────────────────────────────────────────────

// Invite godoc
//
//	@Summary		Invite an artist to the salon
//	@Tags			membership
//	@Accept			json
//	@Produce		json
//	@Param			request	body		InviteRequest	true	"Invitation"
//	@Success		201		{object}	response.Body{data=InviteResult}
//	@Failure		403		{object}	response.Body	"SALON_ROLE_FORBIDDEN"
//	@Failure		409		{object}	response.Body	"ALREADY_IN_SALON, INVITATION_EXISTS"
//	@Router			/artists/salon/members/invite [post]
func (h *Handler) Invite(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	var req InviteRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	if err := h.svc.validate.Struct(&req); err != nil {
		return validation.MapError(err)
	}

	res, err := h.svc.Invite(c.UserContext(), *salonID,
		middleware.UserIDFromContext(c), req, clientip.From(c))
	if err != nil {
		return err
	}
	return response.Created(c, res)
}

// ListMembers godoc
//
//	@Summary	The salon roster
//	@Tags		membership
//	@Produce	json
//	@Success	200	{object}	response.Body{data=[]Member}
//	@Router		/artists/salon/members [get]
func (h *Handler) ListMembers(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	members, err := h.svc.ListMembers(c.UserContext(), *salonID)
	if err != nil {
		return err
	}
	return response.OK(c, members)
}

// RemoveMember godoc
//
//	@Summary	Remove a member from the salon
//	@Tags		membership
//	@Produce	json
//	@Param		artistId	path	string	true	"Artist UUID"
//	@Success	204
//	@Failure	409	{object}	response.Body	"CANNOT_REMOVE_OWNER, HAS_FUTURE_BOOKINGS"
//	@Router		/artists/salon/members/{artistId} [delete]
func (h *Handler) RemoveMember(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	artistID, err := uuid.Parse(c.Params("artistId"))
	if err != nil {
		// A malformed id and one belonging to another salon must be
		// indistinguishable, so both take the same constructor.
		return errMemberNotFound()
	}
	if err := h.svc.RemoveMember(c.UserContext(), *salonID,
		middleware.UserIDFromContext(c), artistID, clientip.From(c)); err != nil {
		return err
	}
	return response.NoContent(c)
}

// Leave godoc
//
//	@Summary	Leave the salon you belong to
//	@Tags		membership
//	@Success	204
//	@Failure	409	{object}	response.Body	"OWNER_MUST_TRANSFER, LAST_MEMBER, HAS_FUTURE_BOOKINGS"
//	@Router		/artists/salon/members/leave [post]
func (h *Handler) Leave(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	if err := h.svc.Leave(c.UserContext(), *salonID,
		middleware.UserIDFromContext(c), clientip.From(c)); err != nil {
		return err
	}
	return response.NoContent(c)
}

// ListInvitations godoc
//
//	@Summary	Pending and historical invitations
//	@Tags		membership
//	@Produce	json
//	@Success	200	{object}	response.Body{data=[]Invitation}
//	@Router		/artists/salon/invitations [get]
func (h *Handler) ListInvitations(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	invs, err := h.svc.ListInvitations(c.UserContext(), *salonID)
	if err != nil {
		return err
	}
	return response.OK(c, invs)
}

// RevokeInvitation godoc
//
//	@Summary	Revoke a pending invitation
//	@Tags		membership
//	@Param		id	path	string	true	"Invitation UUID"
//	@Success	204
//	@Router		/artists/salon/invitations/{id} [delete]
func (h *Handler) RevokeInvitation(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return errInvitationNotFound()
	}
	if err := h.svc.Revoke(c.UserContext(), *salonID,
		middleware.UserIDFromContext(c), id, clientip.From(c)); err != nil {
		return err
	}
	return response.NoContent(c)
}

// TransferOwnership godoc
//
//	@Summary	Hand the salon to another active member
//	@Tags		membership
//	@Accept		json
//	@Param		request	body	TransferRequest	true	"New owner"
//	@Success	204
//	@Failure	409	{object}	response.Body	"TARGET_NOT_ACTIVE_MEMBER, ALREADY_OWNER"
//	@Router		/artists/salon/owner/transfer [post]
func (h *Handler) TransferOwnership(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	var req TransferRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	if err := h.svc.validate.Struct(&req); err != nil {
		return validation.MapError(err)
	}
	if err := h.svc.TransferOwnership(c.UserContext(), *salonID,
		middleware.UserIDFromContext(c), req.ArtistID, clientip.From(c)); err != nil {
		return err
	}
	return response.NoContent(c)
}

// ── Invitee actions ───────────────────────────────────────────────────────

// PreviewInvitation godoc
//
//	@Summary		Look up an invitation by its link token (public)
//	@Description	Unknown, expired, revoked and already-accepted tokens all
//	@Description	answer identically, so this cannot be used to discover
//	@Description	which tokens once existed.
//	@Tags			membership
//	@Produce		json
//	@Param			token	path		string	true	"Invitation token"
//	@Success		200		{object}	response.Body{data=InvitationPreview}
//	@Failure		404		{object}	response.Body	"INVITATION_NOT_FOUND"
//	@Router			/invitations/{token} [get]
func (h *Handler) PreviewInvitation(c *fiber.Ctx) error {
	p, err := h.svc.Preview(c.UserContext(), c.Params("token"))
	if err != nil {
		return err
	}
	return response.OK(c, p)
}

// AcceptInvitation godoc
//
//	@Summary	Accept an invitation and join the salon
//	@Tags		membership
//	@Accept		json
//	@Produce	json
//	@Param		token	path	string						true	"Invitation token"
//	@Param		request	body	onboarding.ArtistProfile	true	"Your artist profile"
//	@Success	201		{object}	response.Body
//	@Failure	404		{object}	response.Body	"INVITATION_NOT_FOUND"
//	@Failure	409		{object}	response.Body	"ALREADY_IN_SALON, HANDLE_TAKEN"
//	@Router		/invitations/{token}/accept [post]
func (h *Handler) AcceptInvitation(c *fiber.Ctx) error {
	var profile onboarding.ArtistProfile
	if err := c.BodyParser(&profile); err != nil {
		return validation.MapBodyError(err)
	}
	if err := h.svc.validate.Struct(&profile); err != nil {
		return validation.MapError(err)
	}

	artistID, err := h.svc.Accept(c.UserContext(), c.Params("token"),
		middleware.UserIDFromContext(c), profile, clientip.From(c))
	if err != nil {
		return err
	}
	return response.Created(c, fiber.Map{
		"artist_id": artistID,
		// Stated rather than implied: an invitation does not bypass
		// platform review (BR-7), and the joiner should not be left
		// wondering why they cannot take bookings yet.
		"status":  "pending",
		"message": "You have joined the salon. An admin will review your profile shortly.",
	})
}

// DeclineInvitation godoc
//
//	@Summary	Decline an invitation (public)
//	@Tags		membership
//	@Param		token	path	string	true	"Invitation token"
//	@Success	204
//	@Router		/invitations/{token}/decline [post]
func (h *Handler) DeclineInvitation(c *fiber.Ctx) error {
	if err := h.svc.Decline(c.UserContext(), c.Params("token")); err != nil {
		return err
	}
	return response.NoContent(c)
}
