// Package notification implements the async notification worker for B-Edge.
// The worker polls the notifications table every 5 seconds and sends
// WhatsApp messages via Twilio. It never runs inside a database transaction.
package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// pollInterval is how often the worker checks for pending notifications.
const pollInterval = 5 * time.Second

// maxAttempts is the maximum number of send attempts before marking dead.
const maxAttempts = 3

// twilioAPIBase is the Twilio Messages API endpoint.
const twilioAPIBase = "https://api.twilio.com/2010-04-01/Accounts"

// PendingNotification holds the fields needed to send one notification.
type PendingNotification struct {
	ID           string
	UserID       string
	BookingID    *string
	TemplateName string
	Channel      string
	Payload      []byte
	Attempts     int
}

// Worker polls the notifications table and sends WhatsApp messages.
type Worker struct {
	db     *pgxpool.Pool
	log    *zap.Logger
	client *http.Client
}

// NewWorker creates a notification worker.
func NewWorker(db *pgxpool.Pool, log *zap.Logger) *Worker {
	return &Worker{
		db:     db,
		log:    log,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Start begins the polling loop. Blocks until ctx is cancelled.
// Call as a goroutine from main.go.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("Notification worker started",
		zap.Duration("poll_interval", pollInterval),
		zap.Int("max_attempts", maxAttempts),
	)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("Notification worker stopped")
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

// processBatch fetches pending notifications and sends them.
func (w *Worker) processBatch(ctx context.Context) {
	notifications, err := w.fetchPending(ctx)
	if err != nil {
		w.log.Error("notification worker: fetch pending failed", zap.Error(err))
		return
	}

	if len(notifications) == 0 {
		return
	}

	w.log.Info("notification worker: processing batch", zap.Int("count", len(notifications)))

	for _, n := range notifications {
		w.send(ctx, n)
	}
}

// fetchPending returns up to 10 pending notifications ordered by created_at.
func (w *Worker) fetchPending(ctx context.Context) ([]*PendingNotification, error) {
	rows, err := w.db.Query(ctx, `
		SELECT id, user_id, booking_id, template_name, channel, payload, attempts
		FROM notifications
		WHERE status = 'pending'
		AND attempts < $1
		ORDER BY created_at ASC
		LIMIT 10`,
		maxAttempts,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch pending notifications: %w", err)
	}
	defer rows.Close()

	var result []*PendingNotification
	for rows.Next() {
		n := &PendingNotification{}
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.BookingID,
			&n.TemplateName, &n.Channel, &n.Payload, &n.Attempts,
		); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

// send attempts to deliver one notification.
func (w *Worker) send(ctx context.Context, n *PendingNotification) {
	// Get the recipient phone number from the users table
	phone, err := w.getPhoneNumber(ctx, n.UserID)
	if err != nil {
		w.log.Warn("notification worker: cannot get phone number",
			zap.String("notification_id", n.ID),
			zap.Error(err),
		)
		w.markFailed(ctx, n, "phone number not found")
		return
	}

	// Build the message body from the payload
	body, err := buildMessageBody(n.TemplateName, n.Payload)
	if err != nil {
		w.log.Warn("notification worker: cannot build message",
			zap.String("notification_id", n.ID),
			zap.Error(err),
		)
		w.markFailed(ctx, n, "failed to build message body")
		return
	}

	// Send via Twilio WhatsApp
	if err := w.sendWhatsApp(phone, body); err != nil {
		w.log.Warn("notification worker: send failed",
			zap.String("notification_id", n.ID),
			zap.Error(err),
		)
		w.markFailed(ctx, n, err.Error())
		return
	}

	w.markSent(ctx, n)
	w.log.Info("notification worker: sent",
		zap.String("notification_id", n.ID),
		zap.String("template", n.TemplateName),
	)
}

// sendWhatsApp sends a WhatsApp message via Twilio REST API.
func (w *Worker) sendWhatsApp(to, body string) error {
	accountSID := os.Getenv("TWILIO_ACCOUNT_SID")
	authToken := os.Getenv("TWILIO_AUTH_TOKEN")
	from := os.Getenv("TWILIO_WHATSAPP_FROM")

	if accountSID == "" || authToken == "" || from == "" {
		// Twilio not configured — log and skip in development
		return fmt.Errorf("twilio not configured (TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_WHATSAPP_FROM required)")
	}

	endpoint := fmt.Sprintf("%s/%s/Messages.json", twilioAPIBase, accountSID)

	data := url.Values{}
	data.Set("To", "whatsapp:"+to)
	data.Set("From", "whatsapp:"+from)
	data.Set("Body", body)

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("create twilio request: %w", err)
	}
	req.SetBasicAuth(accountSID, authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("twilio request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twilio returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// getPhoneNumber fetches the phone number for a user.
func (w *Worker) getPhoneNumber(ctx context.Context, userID string) (string, error) {
	var phone *string
	err := w.db.QueryRow(ctx, `
		SELECT phone FROM users WHERE id = $1 AND deleted_at IS NULL`,
		userID,
	).Scan(&phone)
	if err != nil {
		return "", fmt.Errorf("get phone: %w", err)
	}
	if phone == nil || *phone == "" {
		return "", fmt.Errorf("user has no phone number")
	}
	return *phone, nil
}

// markSent updates a notification to sent status.
func (w *Worker) markSent(ctx context.Context, n *PendingNotification) {
	_, err := w.db.Exec(ctx, `
		UPDATE notifications
		SET status          = 'sent',
		    sent_at         = NOW(),
		    attempts        = attempts + 1,
		    last_attempted_at = NOW()
		WHERE id = $1`,
		n.ID,
	)
	if err != nil {
		w.log.Error("notification worker: mark sent failed",
			zap.String("notification_id", n.ID),
			zap.Error(err),
		)
	}
}

// markFailed increments attempts and sets status to failed or dead.
func (w *Worker) markFailed(ctx context.Context, n *PendingNotification, errMsg string) {
	newAttempts := n.Attempts + 1
	status := "failed"
	if newAttempts >= maxAttempts {
		status = "dead"
	}

	_, err := w.db.Exec(ctx, `
		UPDATE notifications
		SET status            = $1,
		    attempts          = $2,
		    last_attempted_at = NOW(),
		    error_message     = $3
		WHERE id = $4`,
		status, newAttempts, errMsg, n.ID,
	)
	if err != nil {
		w.log.Error("notification worker: mark failed failed",
			zap.String("notification_id", n.ID),
			zap.Error(err),
		)
	}
}

// buildMessageBody renders a message from a template name and JSON payload.
// In Phase 1 the payload contains a "message" field with the pre-rendered text.
// Phase 3 will add proper template rendering with variable substitution.
func buildMessageBody(templateName string, payload []byte) (string, error) {
	if len(payload) == 0 {
		return templateName, nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return "", fmt.Errorf("unmarshal payload: %w", err)
	}

	// Phase 1: payload contains a pre-rendered "message" field
	if msg, ok := data["message"].(string); ok && msg != "" {
		return msg, nil
	}

	// Fallback — use template name as message
	return templateName, nil
}
