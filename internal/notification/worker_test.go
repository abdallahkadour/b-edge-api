// Package notification tests.
//
// Worker talks to *pgxpool.Pool directly with no Repository interface to
// mock (unlike every other domain in this codebase) - this codebase has no
// bedge_test database, so anything that reaches w.db is not unit-testable
// here. What IS testable without a database: buildMessageBody (pure),
// resolveRecipientPhone's branches that return before touching the DB, and
// sendWhatsApp, which only ever touches w.client, never w.db.
package notification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// redirectTransport rewrites every outgoing request's scheme/host to point
// at a local httptest server, regardless of what URL the code under test
// actually built. sendWhatsApp hardcodes the real Twilio host, so this is
// the only way to intercept its request without changing production code.
type redirectTransport struct {
	target *url.URL
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	req.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func testWorker(t *testing.T, handler http.Handler) *Worker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)

	return &Worker{
		db:  nil,
		log: zap.NewNop(),
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &redirectTransport{target: target},
		},
	}
}

// ── NewWorker ────────────────────────────────────────────────────────────────

func TestNewWorker_ConfiguresTenSecondClientTimeout(t *testing.T) {
	w := NewWorker(nil, zap.NewNop())

	require.NotNil(t, w)
	assert.Equal(t, 10*time.Second, w.client.Timeout)
}

// ── buildMessageBody ─────────────────────────────────────────────────────────

func TestBuildMessageBody_EmptyPayload_ReturnsTemplateName(t *testing.T) {
	body, err := buildMessageBody("booking_confirmed", nil)

	require.NoError(t, err)
	assert.Equal(t, "booking_confirmed", body)
}

func TestBuildMessageBody_MessageField_ReturnsMessage(t *testing.T) {
	body, err := buildMessageBody("booking_confirmed", []byte(`{"message":"Your booking is confirmed"}`))

	require.NoError(t, err)
	assert.Equal(t, "Your booking is confirmed", body)
}

func TestBuildMessageBody_NoMessageField_FallsBackToTemplateName(t *testing.T) {
	body, err := buildMessageBody("booking_confirmed", []byte(`{"booking_id":"abc123"}`))

	require.NoError(t, err)
	assert.Equal(t, "booking_confirmed", body)
}

func TestBuildMessageBody_EmptyMessageField_FallsBackToTemplateName(t *testing.T) {
	// An empty string is present but not usable - must fall back rather
	// than send a blank WhatsApp message.
	body, err := buildMessageBody("booking_confirmed", []byte(`{"message":""}`))

	require.NoError(t, err)
	assert.Equal(t, "booking_confirmed", body)
}

func TestBuildMessageBody_InvalidJSON_ReturnsError(t *testing.T) {
	_, err := buildMessageBody("booking_confirmed", []byte(`not json`))

	require.Error(t, err)
}

// ── resolveRecipientPhone ────────────────────────────────────────────────────

func TestResolveRecipientPhone_ExplicitPhone_ReturnsItWithoutTouchingDB(t *testing.T) {
	// w.db is nil - if this path fell through to getPhoneNumber, it would
	// panic on the nil pool. Reaching the assertion at all proves the
	// early return worked.
	w := &Worker{db: nil, log: zap.NewNop()}
	phone := "+96170123456"

	got, err := w.resolveRecipientPhone(context.Background(), &PendingNotification{
		RecipientPhone: &phone,
	})

	require.NoError(t, err)
	assert.Equal(t, phone, got)
}

func TestResolveRecipientPhone_NoPhoneNoUserID_ReturnsErrorWithoutTouchingDB(t *testing.T) {
	w := &Worker{db: nil, log: zap.NewNop()}

	_, err := w.resolveRecipientPhone(context.Background(), &PendingNotification{})

	require.Error(t, err)
}

func TestResolveRecipientPhone_EmptyPhoneStringTreatedAsUnset(t *testing.T) {
	// A non-nil pointer to an empty string must not be treated as a real
	// phone number - falls through to the UserID branch, which here is
	// also nil, so it must still error rather than "succeed" with "".
	w := &Worker{db: nil, log: zap.NewNop()}
	empty := ""

	_, err := w.resolveRecipientPhone(context.Background(), &PendingNotification{
		RecipientPhone: &empty,
	})

	require.Error(t, err)
}

// ── sendWhatsApp ─────────────────────────────────────────────────────────────

func TestSendWhatsApp_MissingCredentials_ReturnsErrorWithoutRequest(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "")
	t.Setenv("TWILIO_AUTH_TOKEN", "")
	t.Setenv("TWILIO_WHATSAPP_FROM", "")

	called := false
	w := testWorker(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		called = true
		rw.WriteHeader(http.StatusOK)
	}))

	_, _, err := w.sendVia("+96170123456", "hello")

	require.Error(t, err)
	assert.False(t, called, "must not attempt delivery when Twilio isn't configured")
}

func TestSendWhatsApp_TwilioAccepts_Succeeds(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "AC_test")
	t.Setenv("TWILIO_AUTH_TOKEN", "token_test")
	t.Setenv("TWILIO_WHATSAPP_FROM", "+15005550006")

	var gotUser, gotPass string
	var ok bool
	w := testWorker(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, ok = r.BasicAuth()
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "whatsapp:+96170123456", r.FormValue("To"))
		assert.Equal(t, "whatsapp:+15005550006", r.FormValue("From"))
		assert.Equal(t, "hello", r.FormValue("Body"))
		rw.WriteHeader(http.StatusCreated)
	}))

	_, _, err := w.sendVia("+96170123456", "hello")

	require.NoError(t, err)
	assert.True(t, ok, "request must carry basic auth")
	assert.Equal(t, "AC_test", gotUser)
	assert.Equal(t, "token_test", gotPass)
}

func TestSendWhatsApp_TwilioRejects_ReturnsErrorWithBody(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "AC_test")
	t.Setenv("TWILIO_AUTH_TOKEN", "token_test")
	t.Setenv("TWILIO_WHATSAPP_FROM", "+15005550006")

	w := testWorker(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusBadRequest)
		_, _ = rw.Write([]byte(`{"message":"invalid number"}`))
	}))

	_, _, err := w.sendVia("+96170123456", "hello")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "invalid number")
}

func TestSendWhatsApp_TransportFailure_ReturnsError(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "AC_test")
	t.Setenv("TWILIO_AUTH_TOKEN", "token_test")
	t.Setenv("TWILIO_WHATSAPP_FROM", "+15005550006")

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {}))
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	srv.Close() // closed before use - every request must now fail to connect

	w := &Worker{
		db:  nil,
		log: zap.NewNop(),
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &redirectTransport{target: target},
		},
	}

	_, _, err = w.sendVia("+96170123456", "hello")

	require.Error(t, err)
}

// SEC: outbound message bodies carry text the recipient did not write.
//
// CLAUDE.md's rule is that user-supplied text rendered to a DIFFERENT person
// goes through internal/pkg/bidi. Share cards, inbox notifications and .ics
// fields honoured it; the outbound WhatsApp body did not, and it is the one
// that reaches a customer's phone. A service name containing U+202E reverses
// the remainder of the sentence, and escaping cannot help - an override is
// not markup.
func TestBuildMessageBody_StripsBidiOverrides(t *testing.T) {
	// U+202E RIGHT-TO-LEFT OVERRIDE, as an artist could put in a service name.
	payload := []byte(`{"message":"Today: your ‮Glam‬ appointment is at 10:00am."}`)

	body, err := buildMessageBody("booking_reminder_morning", payload)

	require.NoError(t, err)
	assert.NotContains(t, body, "‮", "RLO must not reach the recipient")
	assert.NotContains(t, body, "‬", "PDF must not reach the recipient")
	assert.Contains(t, body, "Glam", "the readable text itself must survive")
	assert.Contains(t, body, "10:00am")
}

// Ordinary text, including Arabic, must pass through untouched. Stripping
// legitimate script would be a worse bug than the one being fixed, in a
// product whose customers write Arabic.
func TestBuildMessageBody_LeavesLegitimateTextAlone(t *testing.T) {
	payload := []byte(`{"message":"موعدك اليوم الساعة 10:00 صباحاً - Rania"}`)

	body, err := buildMessageBody("booking_reminder_morning", payload)

	require.NoError(t, err)
	assert.Equal(t, "موعدك اليوم الساعة 10:00 صباحاً - Rania", body)
}

// ── SMS fallback ──────────────────────────────────────────────────────────
//
// TWILIO_WHATSAPP_FROM needs Meta business verification, pending since error
// 63051. Until it clears every notification this platform queues is
// undeliverable - 61 of 61 at last count, including customer login codes.
// That is survivable while nobody pays and indefensible the moment they do:
// "it doesn't message my clients" would be both the reason artists leave and
// entirely true.

// The case that matters today: WhatsApp unconfigured, SMS configured. It
// must deliver rather than fail.
func TestSendVia_NoWhatsAppButSMSConfigured_SendsSMS(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "AC_test")
	t.Setenv("TWILIO_AUTH_TOKEN", "token_test")
	t.Setenv("TWILIO_WHATSAPP_FROM", "")
	t.Setenv("TWILIO_SMS_FROM", "+15005550006")

	var to, from string
	w := testWorker(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		to, from = r.FormValue("To"), r.FormValue("From")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"sid":"SM_test"}`))
	}))

	sid, transport, err := w.sendVia("+96170123456", "hello")

	require.NoError(t, err)
	assert.Equal(t, "SM_test", sid)
	assert.Equal(t, transportSMS, transport)
	assert.Equal(t, "+96170123456", to, "an SMS address carries no whatsapp: prefix")
	assert.Equal(t, "+15005550006", from)
}

// WhatsApp is preferred whenever it works: free to the recipient, threaded,
// and where this market already lives. SMS is the floor, not a preference.
func TestSendVia_WhatsAppConfigured_PrefersWhatsApp(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "AC_test")
	t.Setenv("TWILIO_AUTH_TOKEN", "token_test")
	t.Setenv("TWILIO_WHATSAPP_FROM", "+15005550001")
	t.Setenv("TWILIO_SMS_FROM", "+15005550006")

	var to string
	w := testWorker(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		to = r.FormValue("To")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"sid":"SM_wa"}`))
	}))

	_, transport, err := w.sendVia("+96170123456", "hello")

	require.NoError(t, err)
	assert.Equal(t, transportWhatsApp, transport)
	assert.Equal(t, "whatsapp:+96170123456", to)
}

// A WhatsApp failure must fall through rather than strand the message. This
// is the path that runs on the day Meta verification lapses or the sender is
// rate-limited.
func TestSendVia_WhatsAppRejected_FallsBackToSMS(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "AC_test")
	t.Setenv("TWILIO_AUTH_TOKEN", "token_test")
	t.Setenv("TWILIO_WHATSAPP_FROM", "+15005550001")
	t.Setenv("TWILIO_SMS_FROM", "+15005550006")

	var attempts []string
	w := testWorker(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		attempts = append(attempts, r.FormValue("To"))
		if strings.HasPrefix(r.FormValue("To"), "whatsapp:") {
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte(`{"code":63051,"message":"not verified"}`))
			return
		}
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"sid":"SM_fallback"}`))
	}))

	sid, transport, err := w.sendVia("+96170123456", "hello")

	require.NoError(t, err, "a WhatsApp rejection must not strand the message")
	assert.Equal(t, "SM_fallback", sid)
	assert.Equal(t, transportSMS, transport)
	assert.Equal(t, []string{"whatsapp:+96170123456", "+96170123456"}, attempts,
		"WhatsApp must be tried first, then SMS - in that order, once each")
}

// No fallback configured: the WhatsApp error must surface as itself, not be
// replaced by a confusing SMS error.
func TestSendVia_WhatsAppFailsWithNoSMSConfigured_ReturnsTheRealError(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "AC_test")
	t.Setenv("TWILIO_AUTH_TOKEN", "token_test")
	t.Setenv("TWILIO_WHATSAPP_FROM", "+15005550001")
	t.Setenv("TWILIO_SMS_FROM", "")

	calls := 0
	w := testWorker(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		calls++
		rw.WriteHeader(http.StatusBadRequest)
		_, _ = rw.Write([]byte(`{"code":63051}`))
	}))

	_, _, err := w.sendVia("+96170123456", "hello")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "63051", "the real reason must survive")
	assert.Equal(t, 1, calls, "no pointless second attempt without a fallback")
}

// Neither transport configured is a configuration error, and must say so
// rather than silently doing nothing - which is the state the platform has
// been in.
func TestSendVia_NoTransportConfigured_SaysSo(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "AC_test")
	t.Setenv("TWILIO_AUTH_TOKEN", "token_test")
	t.Setenv("TWILIO_WHATSAPP_FROM", "")
	t.Setenv("TWILIO_SMS_FROM", "")

	called := false
	w := testWorker(t, http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		called = true
		rw.WriteHeader(http.StatusOK)
	}))

	_, _, err := w.sendVia("+96170123456", "hello")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "TWILIO_SMS_FROM",
		"the error must name the way out")
	assert.False(t, called)
}
