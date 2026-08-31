// Package inbox implements the in-app notification centre.
//
// Deliberately named `inbox` rather than `notification` because
// internal/notification already exists and does something different: it is
// the OUTBOUND delivery queue that sends WhatsApp messages to phones. This
// package is the INBOUND feed a signed-in user reads inside the product.
// Two things called "notification" in one codebase would be a standing
// invitation to wire the wrong one.
//
// Why it exists: outbound delivery fails permanently and invisibly. Every
// notification queued before this was written is `dead` - 58 of them,
// including 37 customer login codes - because Twilio was never configured,
// and nobody was ever told. An artist had no way to learn that a customer
// was never reached, so the customer simply turned up at the wrong time.
//
// This is also the only channel that works with no external dependency, no
// Meta template approval, and no per-message cost. It cannot replace
// WhatsApp for guests, who have no account and therefore no inbox.
package inbox

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotificationNotFound is returned when no notification matches, or when
// it belongs to someone else. Deliberately one error for both: a caller
// probing IDs must not be able to tell a foreign notification from a
// nonexistent one.
var ErrNotificationNotFound = errors.New("notification not found")

// Levels. action_required is the load-bearing one - it means a human must
// DO something (call a customer who was never reached, send a refund), as
// opposed to merely being informed.
const (
	LevelInfo           = "info"
	LevelWarning        = "warning"
	LevelActionRequired = "action_required"
)

// Kinds currently produced. Kept as constants rather than a database CHECK
// so adding one is product work, not a migration.
const (
	// KindDeliveryFailed - a WhatsApp message to this artist's customer
	// was permanently undeliverable. The single most important kind: it is
	// the only way an artist learns a customer was never told something.
	KindDeliveryFailed = "delivery_failed"
	// KindRefundDue - a cancelled booking has a paid deposit that has not
	// been refunded.
	KindRefundDue = "refund_due"
)

// maxFeedLimit caps a page of the feed. The centre is for scanning recent
// activity, not for browsing history.
const maxFeedLimit = 50

// defaultFeedLimit is returned when the caller does not specify.
const defaultFeedLimit = 20

// Notification is one entry in a user's feed.
type Notification struct {
	ID     uuid.UUID `db:"id"      json:"id"`
	UserID uuid.UUID `db:"user_id" json:"-"`
	Kind   string    `db:"kind"    json:"kind"`
	Level  string    `db:"level"   json:"level"`
	Title  string    `db:"title"   json:"title"`
	Body   *string   `db:"body"    json:"body,omitempty"`
	// Link is a relative in-app path. A notification that cannot be acted
	// on from itself mostly gets ignored.
	Link *string `db:"link" json:"link,omitempty"`
	// ItemCount is how many occurrences this row represents. Greater than
	// one means several identical events were bundled - a bulk action
	// touching twelve bookings is one row saying twelve, not twelve rows.
	ItemCount  int        `db:"item_count"  json:"item_count"`
	ReadAt     *time.Time `db:"read_at"     json:"read_at,omitempty"`
	ArchivedAt *time.Time `db:"archived_at" json:"-"`
	CreatedAt  time.Time  `db:"created_at"  json:"created_at"`
}

// IsUnread reports whether this still needs the user's attention.
func (n Notification) IsUnread() bool { return n.ReadAt == nil }

// CreateParams is a new notification to file.
//
// GroupKey is optional. When set, a second occurrence while the first is
// still unread bumps ItemCount instead of inserting a new row - see
// migration 030's partial unique index, which is what enforces it. Once the
// user has read the row it stops participating, so a later occurrence
// correctly starts fresh rather than silently reusing something already
// dealt with.
type CreateParams struct {
	UserID   uuid.UUID
	Kind     string
	Level    string
	Title    string
	Body     *string
	Link     *string
	GroupKey *string
}

// FeedResponse is the notification centre's payload.
type FeedResponse struct {
	// Notifications is always a slice, never null, so clients can iterate
	// without a nil check.
	Notifications []Notification `json:"notifications"`
	// UnreadCount counts ROWS, not occurrences: a bundled row with
	// item_count 12 contributes 1. The badge answers "how many things do
	// I need to look at", not "how many events happened".
	UnreadCount int `json:"unread_count"`
}

// UnreadCountResponse backs the bell badge on its own.
//
// Separate from the feed because it is polled far more often and must stay
// cheap - fetching twenty rows to render a number would be wasteful at the
// interval a badge wants.
type UnreadCountResponse struct {
	UnreadCount int `json:"unread_count"`
}

// clampLimit keeps a caller from requesting an unbounded page.
func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultFeedLimit
	case limit > maxFeedLimit:
		return maxFeedLimit
	default:
		return limit
	}
}
