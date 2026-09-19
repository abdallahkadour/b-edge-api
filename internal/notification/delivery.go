// delivery.go — asks the provider what actually happened to a message.
//
// WHY THIS EXISTS
//
// `notifications.status = 'sent'` records that Twilio's API returned 2xx when
// we handed the message over. It has never meant the message arrived, and
// nothing in this system distinguished the two.
//
// On 2026-09-19 that gap was measured. The table reported 19 OTP codes
// `sent`; Twilio's own API reported, for the whole account lifetime, 31
// undelivered, 6 failed, 3 inbound and **zero delivered**. Not one message
// had ever reached a person. Six weeks of total failure, invisible to every
// log, query and dashboard in the product, because the application had no
// memory of which provider message a row became and therefore no question it
// could ask.
//
// This worker asks. It is the difference between a system that is broken and
// a system that is broken *and cannot tell*.
//
// # WHY POLLING RATHER THAN A STATUS CALLBACK
//
// Twilio will POST delivery updates to a StatusCallback URL, which is the
// better design and is what this should become once there is a domain. Until
// then there is no stable public address to register - the tunnels used for
// testing change hostname on every restart - and a callback pointed at a dead
// host fails silently, which is the exact class of problem this file exists
// to end.
//
// Polling needs no inbound address. When the callback lands, keep this as the
// backstop for updates that were missed; nothing here has to change.
package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// deliverySweepInterval is how often the provider is asked about messages it
// has accepted but not yet confirmed.
//
// Two minutes. WhatsApp delivery is normally seconds, so this is unhurried,
// but the point is not speed - it is that the answer arrives at all, and
// arrives without anyone remembering to look.
const deliverySweepInterval = 2 * time.Minute

// deliveryBatchSize bounds one sweep.
//
// Each row costs one HTTP request to Twilio, so an unbounded sweep would turn
// a backlog into a rate-limit incident. Unconfirmed rows persist, so whatever
// is not reached this pass is reached on the next.
const deliveryBatchSize = 25

// deliveryRecheckAfter is how long before a non-final status is asked again.
//
// `queued` and `sent` are transitional and worth re-asking. `delivered`,
// `undelivered`, `failed` and `read` are final, and the query excludes them
// rather than re-asking forever.
const deliveryRecheckAfter = 10 * time.Minute

// finalDeliveryStatuses are the provider verdicts that will not change.
var finalDeliveryStatuses = map[string]bool{
	"delivered":   true,
	"undelivered": true,
	"failed":      true,
	"read":        true,
}

// DeliveryWorker reconciles what we sent against what the provider delivered.
type DeliveryWorker struct {
	db     *pgxpool.Pool
	log    *zap.Logger
	client *http.Client
}

// NewDeliveryWorker creates the reconciler.
func NewDeliveryWorker(db *pgxpool.Pool, log *zap.Logger) *DeliveryWorker {
	return &DeliveryWorker{
		db:     db,
		log:    log.With(zap.String("module", "delivery_worker")),
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

// Start runs the sweep until ctx is cancelled. Supervised by superviseWorker
// in main.go, like every other worker here.
func (w *DeliveryWorker) Start(ctx context.Context) {
	w.log.Info("Delivery reconciler started",
		zap.Duration("interval", deliverySweepInterval),
		zap.Int("batch", deliveryBatchSize),
	)
	w.sweep(ctx)

	t := time.NewTicker(deliverySweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.log.Info("Delivery reconciler stopped")
			return
		case <-t.C:
			w.sweep(ctx)
		}
	}
}

type pendingDelivery struct {
	id  string
	sid string
}

func (w *DeliveryWorker) sweep(ctx context.Context) {
	sid, token := os.Getenv("TWILIO_ACCOUNT_SID"), os.Getenv("TWILIO_AUTH_TOKEN")
	if sid == "" || token == "" {
		return // not configured; the send worker already logs this
	}

	rows, err := w.db.Query(ctx, `
		SELECT id, provider_message_id
		  FROM notifications
		 WHERE provider_message_id IS NOT NULL
		   AND (delivery_status IS NULL OR delivery_status NOT IN ('delivered','undelivered','failed','read'))
		   AND (delivery_checked_at IS NULL OR delivery_checked_at < NOW() - $1::interval)
		 ORDER BY delivery_checked_at NULLS FIRST, sent_at
		 LIMIT $2`, deliveryRecheckAfter, deliveryBatchSize)
	if err != nil {
		w.log.Error("delivery sweep: query failed", zap.Error(err))
		return
	}
	var batch []pendingDelivery
	for rows.Next() {
		var p pendingDelivery
		if err := rows.Scan(&p.id, &p.sid); err == nil {
			batch = append(batch, p)
		}
	}
	rows.Close()
	if len(batch) == 0 {
		return
	}

	var delivered, undelivered int
	for _, p := range batch {
		status, errCode, err := w.fetchStatus(sid, token, p.sid)
		if err != nil {
			w.log.Warn("delivery sweep: lookup failed",
				zap.String("notification_id", p.id), zap.Error(err))
			continue
		}
		w.record(ctx, p.id, status, errCode)
		switch {
		case status == "delivered" || status == "read":
			delivered++
		case finalDeliveryStatuses[status]:
			undelivered++
		}
	}

	// Logged at WARN when nothing is getting through, because a delivery rate
	// of zero is the single most important fact about this system and it
	// previously appeared nowhere at all.
	if undelivered > 0 && delivered == 0 {
		w.log.Warn("delivery reconciled: NOTHING is reaching recipients",
			zap.Int("undelivered", undelivered), zap.Int("checked", len(batch)))
	} else if delivered > 0 || undelivered > 0 {
		w.log.Info("delivery reconciled",
			zap.Int("delivered", delivered),
			zap.Int("undelivered", undelivered),
			zap.Int("checked", len(batch)))
	}
}

// fetchStatus asks Twilio about one message.
func (w *DeliveryWorker) fetchStatus(accountSID, token, messageSID string) (string, *int, error) {
	url := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages/%s.json",
		accountSID, messageSID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(accountSID, token)

	resp, err := w.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("twilio lookup: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("twilio returned %d", resp.StatusCode)
	}

	var parsed struct {
		Status       string `json:"status"`
		ErrorCode    *int   `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", nil, fmt.Errorf("parse twilio response: %w", err)
	}
	return parsed.Status, parsed.ErrorCode, nil
}

// record writes the provider's verdict.
//
// It never touches `status`. That column is this system's record of what IT
// did, and overwriting it with the provider's answer would erase the very
// distinction this file exists to create. The provider's error code is
// appended to error_message only when the message did not arrive, so a failed
// delivery carries the reason a human needs (63016 = outside the 24-hour
// window and a template is required; 63051 = Meta has restricted the sender).
func (w *DeliveryWorker) record(ctx context.Context, id, status string, errCode *int) {
	var note *string
	if errCode != nil && !(status == "delivered" || status == "read") {
		s := fmt.Sprintf("provider status=%s error_code=%d", status, *errCode)
		note = &s
	}
	_, err := w.db.Exec(ctx, `
		UPDATE notifications
		   SET delivery_status     = $2,
		       delivery_checked_at = NOW(),
		       error_message       = COALESCE($3, error_message)
		 WHERE id = $1`, id, status, note)
	if err != nil {
		w.log.Error("delivery sweep: update failed",
			zap.String("notification_id", id), zap.Error(err))
	}
}
