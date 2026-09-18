package peer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// noBackoff makes retry tests instantaneous: they must never sleep for
// real, per the design constraints.
func noBackoff(int) time.Duration { return 0 }

func newTestClient(t *testing.T, srv *httptest.Server, token string, opts ...Option) *HTTPClient {
	t.Helper()
	c, err := New(srv.URL, token, append([]Option{WithBackoff(noBackoff)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestPushReportSendsMethodPathAuthAndDecodesAck(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","id":7}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ack, err := c.PushReport(context.Background(), ReportEnvelope{Node: config.NodePi})
	if err != nil {
		t.Fatalf("PushReport: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != reportPath || gotAuth != "Bearer tok" {
		t.Fatalf("got method=%q path=%q auth=%q", gotMethod, gotPath, gotAuth)
	}
	if ack.Status != "ok" || ack.ID != 7 {
		t.Fatalf("decoded ack: %+v", ack)
	}
}

func TestFetchLatestSendsMethodPathAuthAndDecodesEnvelope(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"node":"nas","version":"1.2.3"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	env, ok, err := c.FetchLatest(context.Background())
	if err != nil {
		t.Fatalf("FetchLatest: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != latestReportPath || gotAuth != "Bearer tok" {
		t.Fatalf("got method=%q path=%q auth=%q", gotMethod, gotPath, gotAuth)
	}
	if !ok || env.Node != config.NodeNAS || env.Version != "1.2.3" {
		t.Fatalf("decoded env: %+v ok=%v", env, ok)
	}
}

func TestSendDecisionSendsMethodPathAuthAndDecodesAck(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ack, err := c.SendDecision(context.Background(), Decision{Kind: "nudge", EntityKey: "x"})
	if err != nil {
		t.Fatalf("SendDecision: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != decisionPath || gotAuth != "Bearer tok" {
		t.Fatalf("got method=%q path=%q auth=%q", gotMethod, gotPath, gotAuth)
	}
	if ack.Status != "accepted" {
		t.Fatalf("decoded ack: %+v", ack)
	}
}

func TestHeartbeatSendsMethodPathAuthAndDecodesAck(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ack, err := c.Heartbeat(context.Background(), Heartbeat{Node: config.NodePi, UptimeSeconds: 42})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != heartbeatPath || gotAuth != "Bearer tok" {
		t.Fatalf("got method=%q path=%q auth=%q", gotMethod, gotPath, gotAuth)
	}
	if ack.Status != "ok" {
		t.Fatalf("decoded ack: %+v", ack)
	}
}

func TestUnauthorizedReturnsSentinelWithoutRetry(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "bad")
	_, err := c.PushReport(context.Background(), ReportEnvelope{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt, got %d", got)
	}
}

func TestForbiddenReturnsSentinelWithoutRetry(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "bad")
	_, err := c.Heartbeat(context.Background(), Heartbeat{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt, got %d", got)
	}
}

func TestServiceUnavailableTwiceThenSuccessRetriesToSuccess(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ack, err := c.PushReport(context.Background(), ReportEnvelope{})
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if ack.Status != "ok" {
		t.Fatalf("decoded ack: %+v", ack)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", got)
	}
}

func TestServiceUnavailableThreeTimesReturnsPeerUnavailable(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	_, err := c.PushReport(context.Background(), ReportEnvelope{})
	if !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("expected ErrPeerUnavailable, got %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected exactly 3 attempts (1 + 2 retries), got %d", got)
	}
}

func TestTransportErrorRetriesThenReturnsPeerUnavailable(t *testing.T) {
	// A closed listener produces a transport-level connection-refused error
	// on every attempt, exercising the non-APIError retry branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	c := newTestClient(t, srv, "tok")
	srv.Close()

	_, err := c.PushReport(context.Background(), ReportEnvelope{})
	if !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("expected ErrPeerUnavailable, got %v", err)
	}
}

func TestBadRequestIsNotRetriedAndHasNoSentinel(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	_, err := c.PushReport(context.Background(), ReportEnvelope{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("expected a plain 400 error, got sentinel: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt (no retry on 4xx), got %d", got)
	}
}

func TestFetchLatestNotFoundReturnsOkFalseNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	env, ok, err := c.FetchLatest(context.Background())
	if err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false on 404")
	}
	if !reflect.DeepEqual(env, ReportEnvelope{}) {
		t.Fatalf("expected zero-value envelope, got %+v", env)
	}
}

func TestContextCancellationIsRespectedWithoutRetry(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.PushReport(ctx, ReportEnvelope{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestContextCancelledDuringBackoffStopsRetryLoop(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	backoffCalls := 0
	c := newTestClient(t, srv, "tok", WithBackoff(func(attempt int) time.Duration {
		backoffCalls++
		cancel() // cancel once the loop starts waiting to retry
		return time.Hour
	}))
	_, err := c.PushReport(ctx, ReportEnvelope{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 request attempt before cancellation, got %d", got)
	}
	if backoffCalls != 1 {
		t.Fatalf("expected exactly 1 backoff call, got %d", backoffCalls)
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	if _, err := New("://bad", "tok"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestWithRetriesOverridesDefault(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok", WithRetries(0))
	_, err := c.PushReport(context.Background(), ReportEnvelope{})
	if !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("expected ErrPeerUnavailable, got %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt with WithRetries(0), got %d", got)
	}
}

func TestDefaultBackoffScheduleMatchesDesign(t *testing.T) {
	if got := defaultBackoff(1); got != 500*time.Millisecond {
		t.Fatalf("attempt 1: expected 500ms, got %v", got)
	}
	if got := defaultBackoff(2); got != time.Second {
		t.Fatalf("attempt 2: expected 1s, got %v", got)
	}
}
