package activity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository reads a salon's activity.
type Repository interface {
	// List returns up to limit rows of salonID's activity older than
	// cursor, newest first.
	List(ctx context.Context, salonID uuid.UUID, f Filter, cursor time.Time, limit int) ([]Entry, error)
}

type pgRepo struct{ db *pgxpool.Pool }

// NewRepository creates the activity repository.
func NewRepository(db *pgxpool.Pool) Repository { return &pgRepo{db: db} }

// listSQL joins, per entity type, the names the screen shows. Each LEFT JOIN
// is gated on entity_type, so a row only ever picks up names from the table
// its entity_id actually belongs to - entity_id is a bare UUID with no
// foreign key, and joining it against every table would match by accident.
//
// The offering row's artist sits in new_values (its entity_id is the
// service); compared as text so a malformed value joins nothing rather than
// failing the whole feed on a cast.
//
// The salon condition is the first and only parameter the caller cannot
// leave out: idx_audit_events_salon_id (salon_id, created_at DESC) serves it.
const listSQL = `
	SELECT e.id, e.created_at, e.actor_id, COALESCE(u.name, ''), COALESCE(e.actor_role, ''),
	       e.entity_type, e.entity_id, e.action, e.old_values, e.new_values,
	       COALESCE(bs.name, os.name, ''),
	       COALESCE(bc.name, oc.name, ''),
	       COALESCE(bau.name, aau.name, oau.name, ''),
	       COALESCE(st.name, ''),
	       b.start_time
	  FROM audit_events e
	  LEFT JOIN users    u   ON u.id = e.actor_id
	  LEFT JOIN bookings b   ON e.entity_type = 'booking' AND b.id = e.entity_id
	  LEFT JOIN services bs  ON bs.id = b.service_id
	  LEFT JOIN users    bc  ON bc.id = b.customer_id
	  LEFT JOIN artists  ba  ON ba.id = b.artist_id
	  LEFT JOIN users    bau ON bau.id = ba.user_id
	  LEFT JOIN orders   o   ON e.entity_type = 'order' AND o.id = e.entity_id
	  LEFT JOIN users    oc  ON oc.id = o.customer_id
	  LEFT JOIN artists  aa  ON e.entity_type = 'artist' AND aa.id = e.entity_id
	  LEFT JOIN users    aau ON aau.id = aa.user_id
	  LEFT JOIN stores   st  ON e.entity_type = 'store' AND st.id = e.entity_id
	  LEFT JOIN services os  ON e.entity_type = 'artist_service' AND os.id = e.entity_id
	  LEFT JOIN artists  oa  ON e.entity_type = 'artist_service' AND oa.id::text = e.new_values->>'artist_id'
	  LEFT JOIN users    oau ON oau.id = oa.user_id
	 WHERE e.salon_id = $1 AND e.created_at < $2`

func (r *pgRepo) List(ctx context.Context, salonID uuid.UUID, f Filter, cursor time.Time, limit int) ([]Entry, error) {
	q := listSQL
	args := []any{salonID, cursor}
	if cond, ok := kinds[f.Kind]; ok {
		q += ` AND ` + cond
	}
	if f.ActorID != nil {
		args = append(args, *f.ActorID)
		q += fmt.Sprintf(` AND e.actor_id = $%d`, len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(` ORDER BY e.created_at DESC, e.id DESC LIMIT $%d`, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()

	out := make([]Entry, 0)
	for rows.Next() {
		var e Entry
		var oldV, newV []byte
		if err := rows.Scan(&e.ID, &e.At, &e.Actor.ID, &e.Actor.Name, &e.Actor.Role,
			&e.EntityType, &e.EntityID, &e.Action, &oldV, &newV,
			&e.Subject.ServiceName, &e.Subject.CustomerName, &e.Subject.ArtistName,
			&e.Subject.StoreName, &e.Subject.StartTime); err != nil {
			return nil, fmt.Errorf("list activity: scan: %w", err)
		}
		if len(oldV) > 0 {
			e.Old = oldV
		}
		if len(newV) > 0 {
			e.New = newV
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
