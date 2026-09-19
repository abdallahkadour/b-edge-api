package notification

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTestDeliveryWorker points the reconciler at a stub instead of Twilio.
// The URL is built inside fetchStatus, so the stub is installed as the HTTP
// client's transport rather than by rewriting the address.
func newTestDeliveryWorker(h http.HandlerFunc) (*DeliveryWorker, *httptest.Server) {
	srv := httptest.NewServer(h)
	w := &DeliveryWorker{
		log: zap.NewNop(),
		client: &http.Client{Transport: rewriteHost{base: srv.URL, inner: srv.Client().Transport}},
	}
	return w, srv
}

// rewriteHost sends every request to the stub, whatever host was asked for.
type rewriteHost struct {
	base  string
	inner http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL
	u.Scheme = "http"
	u.Host = strings.TrimPrefix(r.base, "http://")
	rt := r.inner
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(req)
}

// The whole point of this worker: a message the API accepted can still have
// failed to arrive, and the provider is the only source of that truth.
func TestFetchStatus_UndeliveredIsReported(t *testing.T) {
	w, srv := newTestDeliveryWorker(func(rw http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"sid": "SM123", "status": "undelivered", "error_code": 63016,
		})
	})
	defer srv.Close()

	status, code, err := w.fetchStatus("AC1", "tok", "SM123")

	require.NoError(t, err)
	assert.Equal(t, "undelivered", status)
	require.NotNil(t, code)
	assert.Equal(t, 63016, *code, "the error code is what tells a human WHY")
}

func TestFetchStatus_DeliveredHasNoErrorCode(t *testing.T) {
	w, srv := newTestDeliveryWorker(func(rw http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"sid": "SM124", "status": "delivered", "error_code": nil,
		})
	})
	defer srv.Close()

	status, code, err := w.fetchStatus("AC1", "tok", "SM124")

	require.NoError(t, err)
	assert.Equal(t, "delivered", status)
	assert.Nil(t, code)
}

// A provider error must not be mistaken for "delivered". Returning a zero
// value with no error here would record silence as success, which is the
// exact failure this worker exists to end.
func TestFetchStatus_ProviderErrorIsAnError(t *testing.T) {
	w, srv := newTestDeliveryWorker(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusUnauthorized)
		_, _ = rw.Write([]byte(`{"message":"authenticate"}`))
	})
	defer srv.Close()

	status, _, err := w.fetchStatus("AC1", "tok", "SM125")

	require.Error(t, err)
	assert.Empty(t, status)
}

func TestFetchStatus_MalformedBodyIsAnError(t *testing.T) {
	w, srv := newTestDeliveryWorker(func(rw http.ResponseWriter, _ *http.Request) {
		_, _ = rw.Write([]byte("not json"))
	})
	defer srv.Close()

	_, _, err := w.fetchStatus("AC1", "tok", "SM126")

	require.Error(t, err)
}

// Which verdicts are final decides what the sweep stops re-asking about.
// Getting this wrong either polls forever or freezes a transitional status
// as though it were the answer.
func TestFinalDeliveryStatuses_CoverTerminalVerdictsOnly(t *testing.T) {
	for _, s := range []string{"delivered", "undelivered", "failed", "read"} {
		assert.True(t, finalDeliveryStatuses[s], "%s should be final", s)
	}
	for _, s := range []string{"queued", "sent", "sending", "accepted", ""} {
		assert.False(t, finalDeliveryStatuses[s], "%s must stay re-checkable", s)
	}
}
