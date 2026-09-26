package artist

// createServiceWithOwnerOfferSQL inserts a service AND the salon owner's
// artist_services row for it, in ONE statement (PP-7: "a service the owner
// adds later: on for the owner, off for members"). The owner_offers CTE
// finds the owner's artist through salons.owner_id; being one statement, a
// service can never exist with an empty owner menu, and a failure of either
// half leaves neither.
//
// # WHY THIS HAS ITS OWN FILE
//
// internal/pkg/salonrole/nodrift_test.go (TestNoInlineOwnerChecks) forbids
// any file outside its allowlist from tying the salons table to owner_id,
// because authorisation must go through salonrole.Can. This statement's use
// is a data lookup - WHICH artist to seed - not an authorisation decision,
// so it is allowlisted. It used to sit inside artist/repository.go, which
// meant the WHOLE repository was exempt and an owner check written anywhere
// in it would have passed unseen. Moving the one statement here keeps the
// exemption to exactly the code that needs it.
//
// Parameters, in order: id, salon_id, category_id, name, name_ar,
// description, duration_min, buffer_min, active_duration_min, price,
// deposit_amount, deposit_deadline_hours, is_active, is_custom. Returns
// created_at, updated_at.
const createServiceWithOwnerOfferSQL = `
		WITH svc AS (
			INSERT INTO services (
				id, salon_id, category_id, name, name_ar, description,
				duration_min, buffer_min, active_duration_min, price,
				deposit_amount, deposit_deadline_hours,
				is_active, is_custom
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9, $10,
				$11, $12,
				$13, $14
			)
			RETURNING id, salon_id, created_at, updated_at
		), owner_offers AS (
			-- PP-7: the owner offers what she creates; members switch it on.
			INSERT INTO artist_services (artist_id, service_id)
			SELECT a.id, svc.id
			  FROM svc
			  JOIN salons sa ON sa.id = svc.salon_id
			  JOIN artists a ON a.user_id = sa.owner_id AND a.salon_id = sa.id
		)
		SELECT created_at, updated_at FROM svc`
