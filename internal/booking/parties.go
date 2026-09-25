package booking

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// validateBookingParties resolves a booking's service and store and proves
// that they belong to the artist.
//
// # WHY THIS EXISTS
//
// Four entry points take an artist, a store and a service by ID: the guest
// hold, POST /bookings, the slots endpoint and the waitlist. Until 2026-09-25
// each one checked that the IDs EXISTED and none checked that they belonged
// TOGETHER. Measured live, both of these returned 201:
//
//	Rania + her store + another salon's $50 service
//	    -> Rania booked at the other salon's price, and the booking filed
//	       under the OTHER salon, because salon_id is copied from the service
//	Rania + her service + another salon's store
//	    -> Rania booked at a location she does not work at
//
// Service IDs are public - every artist profile lists them - so anyone could
// find the cheapest service anywhere on the platform and book any artist at
// that price.
//
// ONE FUNCTION, FOUR CALLERS. The bug existed because a check that should
// have been written once would have had to be remembered four times, and was
// remembered zero. This is also the seam per-artist service pricing will
// extend: "does this artist offer this service, and at what price" is this
// question with one more condition.
//
// WHAT "BELONG TOGETHER" MEANS
//
//   - the service is in the artist's salon
//   - the store is in the artist's salon
//   - the artist works at that store (artist_stores)
//
// The last is stricter than the salon check and deliberately so. The customer
// funnel only ever lists stores the artist is linked to (artist.repository's
// store query joins artist_stores), so a request naming an unlinked store was
// built by hand. Verified before adding it: every artist on 2026-09-25 was
// linked to all of their salon's stores and to no foreign one, so this blocks
// nothing a customer can legitimately pick.
//
// # FAILURES ARE 404, NOT 403
//
// The house rule: an object you may not use must look exactly like one that
// does not exist, so IDs cannot be probed. A foreign service returns
// errServiceNotFound() - the same constructor the missing-service branch uses
// - so status, code and message are identical by construction rather than by
// two literals that happen to agree.
func (s *Service) validateBookingParties(ctx context.Context,
	artistID, storeID, serviceID uuid.UUID) (*SalonService, *Store, error) {

	// GetService filters on is_active, so an inactive service is not found.
	service, err := s.repo.GetService(ctx, serviceID)
	if err != nil || service == nil {
		return nil, nil, errServiceNotFound()
	}

	store, err := s.repo.GetStore(ctx, storeID)
	if err != nil {
		if errors.Is(err, ErrStoreNotFound) {
			// This used to be wrapped and returned as an internal error, so
			// an unknown store_id answered 500.
			return nil, nil, errStoreNotFound()
		}
		return nil, nil, fmt.Errorf("booking parties: get store: %w", err)
	}
	if store == nil {
		return nil, nil, errStoreNotFound()
	}

	salonID, worksAtStore, err := s.repo.ArtistPlacement(ctx, artistID, storeID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			// No artist, so no salon, so every service is foreign to them.
			return nil, nil, errServiceNotFound()
		}
		return nil, nil, fmt.Errorf("booking parties: artist placement: %w", err)
	}

	// An artist who has not onboarded has no salon. Every service is foreign
	// to her; checked before any comparison so a nil is never dereferenced.
	if salonID == nil || service.SalonID != *salonID {
		return nil, nil, errServiceNotFound()
	}
	if store.SalonID != *salonID || !worksAtStore {
		return nil, nil, errStoreNotFound()
	}

	return service, store, nil
}

// errServiceNotFound is the ONLY way this package reports a service that is
// missing, inactive, or not the artist's to sell. See validateBookingParties
// for why those three must be indistinguishable.
func errServiceNotFound() error {
	return apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
}

// errStoreNotFound is the ONLY way this package reports a store that is
// missing or that the artist does not work at.
func errStoreNotFound() error {
	return apperror.NotFound("STORE_NOT_FOUND", "Store not found")
}
