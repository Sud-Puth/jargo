package deepgram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gojargo/jargo/frames"
	errs "github.com/gojargo/jargo/utils/errors"
)

// refusingServer refuses every WebSocket handshake with status, explaining it
// in the dg-error header when reason is set.
func refusingServer(t *testing.T, status int, reason string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if reason != "" {
			w.Header().Set("dg-error", reason)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// TestFluxSTTRefusalReportsDeepgramsReason checks Deepgram's reason for
// refusing the handshake reaches the error, which keeps the status it is
// classified by.
func TestFluxSTTRefusalReportsDeepgramsReason(t *testing.T) {
	reason := "eot_threshold must be between 0.5 and 1, inclusive."
	c := newFluxConnector(FluxConfig{
		APIKey: "k", Model: defaultFluxModel,
		ListenURL: refusingServer(t, http.StatusBadRequest, reason),
	})

	_, err := c.Connect(t.Context(), 16000)

	if want := "Deepgram refused the connection (HTTP 400): " + reason; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
	if got := errs.ClassifyError(err); !got.IsPermanent() {
		t.Errorf("category = %v, want a permanent one", got)
	}
}

// runFluxTTS runs one synthesis against url and returns its error.
func runFluxTTS(t *testing.T, url string) error {
	t.Helper()
	s := &fluxSynth{cfg: FluxTTSConfig{APIKey: "k", SpeakURL: url}.withDefaults()}
	return s.RunTTS(context.Background(), "hello", "", func(frames.Frame) error { return nil })
}

// TestFluxTTSRefusedSettingIsPermanent checks a setting Deepgram refuses is
// reported with Deepgram's reason, and as a permanent failure, since a retry
// would be refused the same way.
func TestFluxTTSRefusedSettingIsPermanent(t *testing.T) {
	reason := "'expressivity' must be between -2 and 2. Got: 99."

	err := runFluxTTS(t, refusingServer(t, http.StatusBadRequest, reason))

	if want := "Deepgram refused the connection (HTTP 400): " + reason; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
	if got := errs.ClassifyError(err); !got.IsPermanent() {
		t.Errorf("category = %v, want a permanent one", got)
	}
}

// TestFluxTTSServerErrorIsNotPermanent checks a server error is retried, and
// that a refusal with no reason keeps the dial error.
func TestFluxTTSServerErrorIsNotPermanent(t *testing.T) {
	err := runFluxTTS(t, refusingServer(t, http.StatusServiceUnavailable, ""))

	if err == nil || !strings.Contains(err.Error(), "deepgram flux tts: dial") {
		t.Errorf("error = %v, want the dial error", err)
	}
	if got := errs.ClassifyError(err); got.IsPermanent() {
		t.Errorf("category = %v, want a transient one", got)
	}
}
