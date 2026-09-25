package offering

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/pricing"
)

type pgRepo struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) Repository { return &pgRepo{db: db} }

// offeringCols is the settings-screen row. pricing.Detail supplies the seven
// money columns so this package never names services.price itself.
var offeringCols = `s.id, s.name, s.duration_min, (os.artist_id IS NOT NULL), ` +
	pricing.Detail("s", "os") + `, uu.name`

const offeringFrom = `
	  FROM services s
	  LEFT JOIN artist_services os ON os.service_id = s.id AND os.artist_id = $2
	  LEFT JOIN users uu ON uu.id = os.updated_by
	 WHERE s.salon_id = $1 AND s.is_active = TRUE`

func scanOffering(row pgx.Row) (*Offering, error) {
	o := &Offering{}
	err := row.Scan(&o.ServiceID, &o.ServiceName, &o.DurationMin, &o.Offered,
		&o.SalonPrice, &o.SalonDeposit, &o.OwnPrice, &o.OwnDeposit,
		&o.EffectivePrice, &o.EffectiveDeposit, &o.DepositCapped, &o.UpdatedByName)
	return o, err
}

func (r *pgRepo) List(ctx context.Context, salonID, artistID uuid.UUID) ([]*Offering, error) {
	rows, err := r.db.Query(ctx, `SELECT `+offeringCols+offeringFrom+` ORDER BY s.name`, salonID, artistID)
	if err != nil {
		return nil, fmt.Errorf("list offerings: %w", err)
	}
	defer rows.Close()
	out := make([]*Offering, 0)
	for rows.Next() {
		o, err := scanOffering(rows)
		if err != nil {
			return nil, fmt.Errorf("scan offering: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *pgRepo) Get(ctx context.Context, salonID, artistID, serviceID uuid.UUID) (*Offering, error) {
	o, err := scanOffering(r.db.QueryRow(ctx,
		`SELECT `+offeringCols+offeringFrom+` AND s.id = $3`, salonID, artistID, serviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get offering: %w", err)
	}
	return o, nil
}

// Upsert switches the service on and applies whichever of price and deposit
// were SENT. An absent field keeps the stored value; an explicit null clears
// it to the salon's - the presence/value pair house pattern.
func (r *pgRepo) Upsert(ctx context.Context, p UpsertParams) error {
	var actor any
	if p.ActorUserID != uuid.Nil {
		actor = p.ActorUserID
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO artist_services (artist_id, service_id, price, deposit_amount, updated_by)
		VALUES ($1, $2, CASE WHEN $3 THEN $4::numeric END, CASE WHEN $5 THEN $6::numeric END, $7)
		ON CONFLICT (artist_id, service_id) DO UPDATE SET
		    price          = CASE WHEN $3 THEN $4::numeric ELSE artist_services.price END,
		    deposit_amount = CASE WHEN $5 THEN $6::numeric ELSE artist_services.deposit_amount END,
		    updated_by     = $7,
		    updated_at     = now()`,
		p.ArtistID, p.ServiceID, p.PriceSet, p.Price, p.DepositSet, p.Deposit, actor)
	if err != nil {
		return fmt.Errorf("upsert offering: %w", err)
	}
	return nil
}

func (r *pgRepo) Delete(ctx context.Context, artistID, serviceID uuid.UUID) error {
	if _, err := r.db.Exec(ctx,
		`DELETE FROM artist_services WHERE artist_id = $1 AND service_id = $2`, artistID, serviceID); err != nil {
		return fmt.Errorf("delete offering: %w", err)
	}
	return nil
}

func (r *pgRepo) ArtistIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT id FROM artists WHERE user_id = $1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (r *pgRepo) ArtistInSalon(ctx context.Context, artistID, salonID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM artists WHERE id = $1 AND salon_id = $2)`, artistID, salonID).Scan(&ok)
	return ok, err
}
