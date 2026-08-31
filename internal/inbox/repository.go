package inbox

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines the database operations for the notification centre.
type Repository interface {
	// Create files a notification. When GroupKey is set and an unread,
	// unarchived notification already exists for that (user, group_key),
	// this bumps its item_count and refreshes its content instead of
	// inserting - so repeats collapse into one row.
	Create(ctx context.Context, p CreateParams) error

	// ListFeed returns a user's notifications, newest first, excluding
	// archived ones. unreadOnly narrows to those still needing attention.
	ListFeed(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit int) ([]Notification, error)

	// CountUnread returns the badge number: unread, unarchived ROWS.
	CountUnread(ctx context.Context, userID uuid.UUID) (int, error)

	// MarkRead marks one notification read. Returns
	// ErrNotificationNotFound if it does not exist OR belongs to another
	// user - the caller must not be able to tell those apart.
	MarkRead(ctx context.Context, userID, notificationID uuid.UUID) error

	// MarkAllRead clears the badge in one call and returns how many rows
	// changed.
	MarkAllRead(ctx context.Context, userID uuid.UUID) (int64, error)

	// Archive removes a notification from the feed without deleting it.
	Archive(ctx context.Context, userID, notificationID uuid.UUID) error
}

type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a PostgreSQL-backed inbox Repository.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

const notificationCols = `
	id, user_id, kind, level, title, body, link, item_count,
	read_at, archived_at, created_at`

// Create inserts or bundles.
//
// The ON CONFLICT target is the partial unique index from migration 030,
// which only covers unread, unarchived rows with a group key. That is what
// makes bundling stop at the right moment: once the user has read the row,
// it leaves the index and the next occurrence inserts fresh rather than
// resurrecting something already dealt with.
//
// Title and body are refreshed on bundle so the row can say "3 customers
// could not be reached" rather than still claiming one.
func (r *pgRepo) Create(ctx context.Context, p CreateParams) error {
	level := p.Level
	if level == "" {
		level = LevelInfo
	}

	_, err := r.db.Exec(ctx, `
		INSERT INTO user_notifications
			(user_id, kind, level, title, body, link, group_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, group_key)
			WHERE group_key IS NOT NULL AND read_at IS NULL AND archived_at IS NULL
		DO UPDATE SET
			item_count = user_notifications.item_count + 1,
			title      = EXCLUDED.title,
			body       = EXCLUDED.body,
			link       = EXCLUDED.link,
			level      = EXCLUDED.level,
			created_at = NOW()`,
		p.UserID, p.Kind, level, p.Title, p.Body, p.Link, p.GroupKey,
	)
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	return nil
}

func (r *pgRepo) ListFeed(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit int) ([]Notification, error) {
	q := `SELECT ` + notificationCols + `
		FROM user_notifications
		WHERE user_id = $1 AND archived_at IS NULL`
	if unreadOnly {
		q += ` AND read_at IS NULL`
	}
	q += ` ORDER BY created_at DESC LIMIT $2`

	rows, err := r.db.Query(ctx, q, userID, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("list feed: %w", err)
	}
	defer rows.Close()

	// Non-nil so JSON carries [] rather than null.
	out := make([]Notification, 0)
	for rows.Next() {
		var n Notification
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.Kind, &n.Level, &n.Title, &n.Body,
			&n.Link, &n.ItemCount, &n.ReadAt, &n.ArchivedAt, &n.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list feed rows: %w", err)
	}
	return out, nil
}

func (r *pgRepo) CountUnread(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM user_notifications
		WHERE user_id = $1 AND read_at IS NULL AND archived_at IS NULL`,
		userID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return n, nil
}

// MarkRead is scoped by user_id in the WHERE clause, not checked after the
// fact - a foreign notification simply matches no rows, so ownership is
// enforced by the query rather than by a comparison someone can forget.
func (r *pgRepo) MarkRead(ctx context.Context, userID, notificationID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE user_notifications SET read_at = NOW()
		WHERE id = $1 AND user_id = $2 AND read_at IS NULL`,
		notificationID, userID,
	)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either it does not exist, is not theirs, or is already read.
		// Distinguish the last case so re-reading is idempotent rather
		// than a 404 on a double-tap.
		var exists bool
		if err := r.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM user_notifications WHERE id=$1 AND user_id=$2)`,
			notificationID, userID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("mark read: verify: %w", err)
		}
		if !exists {
			return ErrNotificationNotFound
		}
	}
	return nil
}

func (r *pgRepo) MarkAllRead(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE user_notifications SET read_at = NOW()
		WHERE user_id = $1 AND read_at IS NULL AND archived_at IS NULL`,
		userID,
	)
	if err != nil {
		return 0, fmt.Errorf("mark all read: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Archive also marks read. A dismissed notification that still counted
// toward the badge would be a permanently stuck number.
func (r *pgRepo) Archive(ctx context.Context, userID, notificationID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE user_notifications
		SET archived_at = NOW(), read_at = COALESCE(read_at, NOW())
		WHERE id = $1 AND user_id = $2 AND archived_at IS NULL`,
		notificationID, userID,
	)
	if err != nil {
		return fmt.Errorf("archive notification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotificationNotFound
	}
	return nil
}
