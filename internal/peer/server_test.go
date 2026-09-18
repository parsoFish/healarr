package peer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// FakeHandler is an in-memory Handler for tests: it records every call and
// returns caller-configured results. ReportBlock, when non-nil, makes
// ReceiveReport block until the channel is closed or sent on, so tests can
// exercise an in-flight request during graceful shutdown (see
// server_http_test.go).
type FakeHandler struct {
	ReportID    int64
	ReportErr   error
	ReportBlock chan struct{}

	LatestEnvelope ReportEnvelope
	LatestOK       bool
	LatestErr      error

	DecisionID  int64
	DecisionErr error

	HeartbeatErr error

	Calls []string
}

var _ Handler = (*FakeHandler)(nil)

func (f *FakeHandler) ReceiveReport(_ context.Context, env ReportEnvelope) (int64, error) {
	f.Calls = append(f.Calls, fmt.Sprintf("ReceiveReport(%s)", env.Node))
	if f.ReportBlock != nil {
		<-f.ReportBlock
	}
	return f.ReportID, f.ReportErr
}

func (f *FakeHandler) LatestOwnReport(_ context.Context) (ReportEnvelope, bool, error) {
	f.Calls = append(f.Calls, "LatestOwnReport()")
	return f.LatestEnvelope, f.LatestOK, f.LatestErr
}

func (f *FakeHandler) ReceiveDecision(_ context.Context, d Decision) (int64, error) {
	f.Calls = append(f.Calls, fmt.Sprintf("ReceiveDecision(%s,%s)", d.Kind, d.EntityKey))
	return f.DecisionID, f.DecisionErr
}

func (f *FakeHandler) ReceiveHeartbeat(_ context.Context, hb Heartbeat) error {
	f.Calls = append(f.Calls, fmt.Sprintf("Heartbeat(%s)", hb.Node))
	return f.HeartbeatErr
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// failingResponseWriter wraps an httptest.ResponseRecorder but makes every
// Write fail, so tests can exercise writeJSON's error-logging path without
// a real network failure.
type failingResponseWriter struct {
	*httptest.ResponseRecorder
}

func (f *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("boom: simulated write failure")
}

func TestNewHTTPServerSetsNonZeroTimeouts(t *testing.T) {
	srv := newHTTPServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout != readHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, readHeaderTimeout)
	}
	if srv.ReadTimeout != readTimeout {
		t.Errorf("ReadTimeout = %v, want %v", srv.ReadTimeout, readTimeout)
	}
	if srv.WriteTimeout != writeTimeout {
		t.Errorf("WriteTimeout = %v, want %v", srv.WriteTimeout, writeTimeout)
	}
	if srv.IdleTimeout != idleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", srv.IdleTimeout, idleTimeout)
	}
	for name, got := range map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	} {
		if got <= 0 {
			t.Errorf("%s = %v, want a positive timeout (never the zero-value default)", name, got)
		}
	}
}

func TestWriteJSONLogsWriteFailure(t *testing.T) {
	var logBuf bytes.Buffer
	s := &server{h: &FakeHandler{}, logger: slog.New(slog.NewTextHandler(&logBuf, nil))}
	w := &failingResponseWriter{httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodPost, "/v1/report", nil)

	s.writeJSON(w, r, http.StatusOK, Ack{Status: "ok"})

	logged := logBuf.String()
	if !strings.Contains(logged, "write response failed") {
		t.Fatalf("expected a write-failure log line, got %q", logged)
	}
	if !strings.Contains(logged, "/v1/report") || !strings.Contains(logged, http.MethodPost) {
		t.Fatalf("expected method/path in the log line, got %q", logged)
	}
}

// newRoundTripServer builds a NewServer-backed httptest.Server plus a real
// peer.Client pointed at it, so tests exercise the client<->server wire
// format rather than hand-rolled requests.
func newRoundTripServer(t *testing.T, fh *FakeHandler, token string) (*httptest.Server, *HTTPClient) {
	t.Helper()
	h, err := NewServer(token, fh, testLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, token, WithBackoff(noBackoff))
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	return srv, c
}

func TestNewServerRejectsEmptyToken(t *testing.T) {
	if _, err := NewServer("", &FakeHandler{}, testLogger()); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestNewServerRejectsNilHandler(t *testing.T) {
	if _, err := NewServer("tok", nil, testLogger()); err == nil {
		t.Fatal("expected error for nil handler")
	}
}

func TestNewServerAcceptsNilLoggerAndUsesDefault(t *testing.T) {
	if _, err := NewServer("tok", &FakeHandler{}, nil); err != nil {
		t.Fatalf("expected NewServer to accept a nil logger, got %v", err)
	}
}

func TestPushReportRoundTripsThroughRealClient(t *testing.T) {
	fh := &FakeHandler{ReportID: 42}
	_, c := newRoundTripServer(t, fh, "tok")

	ack, err := c.PushReport(context.Background(), ReportEnvelope{Node: config.NodePi, Version: "1.0.0"})
	if err != nil {
		t.Fatalf("PushReport: %v", err)
	}
	if ack.Status != "ok" || ack.ID != 42 {
		t.Fatalf("got ack %+v", ack)
	}
	if len(fh.Calls) != 1 || fh.Calls[0] != "ReceiveReport(pi)" {
		t.Fatalf("handler calls: %v", fh.Calls)
	}
}

func TestFetchLatestRoundTripsThroughRealClient(t *testing.T) {
	want := ReportEnvelope{Node: config.NodeNAS, Version: "2.0.0"}
	fh := &FakeHandler{LatestEnvelope: want, LatestOK: true}
	_, c := newRoundTripServer(t, fh, "tok")

	env, ok, err := c.FetchLatest(context.Background())
	if err != nil {
		t.Fatalf("FetchLatest: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if env.Node != want.Node || env.Version != want.Version {
		t.Fatalf("got %+v, want %+v", env, want)
	}
}

func TestFetchLatestNotFoundRoundTripsToOkFalse(t *testing.T) {
	fh := &FakeHandler{LatestOK: false}
	_, c := newRoundTripServer(t, fh, "tok")

	env, ok, err := c.FetchLatest(context.Background())
	if err != nil {
		t.Fatalf("expected nil error on not-found, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false")
	}
	if !reflect.DeepEqual(env, ReportEnvelope{}) {
		t.Fatalf("expected zero-value envelope, got %+v", env)
	}
}

func TestSendDecisionRoundTripsThroughRealClientWithAccepted(t *testing.T) {
	fh := &FakeHandler{DecisionID: 9}
	_, c := newRoundTripServer(t, fh, "tok")

	ack, err := c.SendDecision(context.Background(), Decision{Kind: "nudge", EntityKey: "x"})
	if err != nil {
		t.Fatalf("SendDecision: %v", err)
	}
	if ack.Status != "accepted" || ack.ID != 9 {
		t.Fatalf("got ack %+v", ack)
	}
	if len(fh.Calls) != 1 || fh.Calls[0] != "ReceiveDecision(nudge,x)" {
		t.Fatalf("handler calls: %v", fh.Calls)
	}
}

func TestHeartbeatRoundTripsThroughRealClient(t *testing.T) {
	fh := &FakeHandler{}
	_, c := newRoundTripServer(t, fh, "tok")

	ack, err := c.Heartbeat(context.Background(), Heartbeat{Node: config.NodePi, UptimeSeconds: 5})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if ack.Status != "ok" {
		t.Fatalf("got ack %+v", ack)
	}
	if len(fh.Calls) != 1 || fh.Calls[0] != "Heartbeat(pi)" {
		t.Fatalf("handler calls: %v", fh.Calls)
	}
}

func TestHandlerErrorReturns500WithoutLeakingDetails(t *testing.T) {
	secret := "db path /var/lib/healarr/state.db is corrupt"
	fh := &FakeHandler{ReportErr: errors.New(secret)}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	h, err := NewServer("tok", fh, logger)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	c, err := New(srv.URL, "tok", WithBackoff(noBackoff), WithRetries(0))
	if err != nil {
		t.Fatalf("New client: %v", err)
	}

	_, err = c.PushReport(context.Background(), ReportEnvelope{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("client-visible error leaked handler error text: %v", err)
	}
	if !strings.Contains(logBuf.String(), secret) {
		t.Fatalf("expected handler error to be logged via slog, got log: %q", logBuf.String())
	}
}

func TestLatestOwnReportHandlerErrorReturns500(t *testing.T) {
	fh := &FakeHandler{LatestErr: errors.New("boom")}
	_, c := newRoundTripServer(t, fh, "tok")

	_, _, err := c.FetchLatest(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestDecisionHandlerErrorReturns500(t *testing.T) {
	fh := &FakeHandler{DecisionErr: errors.New("boom")}
	_, c := newRoundTripServer(t, fh, "tok")

	_, err := c.SendDecision(context.Background(), Decision{})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestHeartbeatHandlerErrorReturns500(t *testing.T) {
	fh := &FakeHandler{HeartbeatErr: errors.New("boom")}
	_, c := newRoundTripServer(t, fh, "tok")

	_, err := c.Heartbeat(context.Background(), Heartbeat{})
	if err == nil {
		t.Fatal("expected an error")
	}
}
