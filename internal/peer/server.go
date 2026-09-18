package peer

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// maxBodyBytes caps every request body (design constraint), so a
// misbehaving or malicious peer cannot exhaust memory decoding JSON.
const maxBodyBytes = 4 << 20 // 4 MiB

// shutdownTimeout bounds how long Serve/ListenAndServe wait for in-flight
// requests to finish once the caller's context is cancelled.
const shutdownTimeout = 5 * time.Second

// HTTP server timeouts. A network-facing server must never rely on the
// http.Server zero-value (no timeout at all), since that leaves it open
// to a slow/malicious peer holding a connection open indefinitely (e.g.
// slowloris). Every *http.Server this package builds sets all four
// explicitly via newHTTPServer.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
)

// Handler is what the server needs from the agent/store to answer peer
// requests. Implementations must not mutate the values they are given.
type Handler interface {
	// ReceiveReport stores env as the peer's report and returns its id.
	ReceiveReport(ctx context.Context, env ReportEnvelope) (int64, error)
	// LatestOwnReport returns this node's own most recent report. ok is
	// false (with a nil error) when there is none yet.
	LatestOwnReport(ctx context.Context) (ReportEnvelope, bool, error)
	// ReceiveDecision records d. Phase 3 is observe-only (ADR-006):
	// nothing acts on it until Phase 4.
	ReceiveDecision(ctx context.Context, d Decision) (int64, error)
	// ReceiveHeartbeat records hb.
	ReceiveHeartbeat(ctx context.Context, hb Heartbeat) error
}

// server holds the dependencies shared by every route handler.
type server struct {
	h      Handler
	logger *slog.Logger
}

// NewServer returns an http.Handler mounting the node-to-node routes
// (/v1/report, /v1/report/latest, /v1/decision, /v1/heartbeat) behind
// bearer auth. token must be non-empty: the server refuses to start with
// an empty peer token (design constraint).
func NewServer(token string, h Handler, logger *slog.Logger) (http.Handler, error) {
	if token == "" {
		return nil, errors.New("peer: NewServer: token must not be empty")
	}
	if h == nil {
		return nil, errors.New("peer: NewServer: handler must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	s := &server{h: h, logger: logger}
	mux := http.NewServeMux()
	// Method-qualified patterns: Go's ServeMux answers a method mismatch on
	// a registered path with 405 (and an Allow header) on its own, so no
	// separate method-checking middleware is needed.
	mux.HandleFunc(http.MethodPost+" "+reportPath, s.handleReport)
	mux.HandleFunc(http.MethodGet+" "+latestReportPath, s.handleLatestReport)
	mux.HandleFunc(http.MethodPost+" "+decisionPath, s.handleDecision)
	mux.HandleFunc(http.MethodPost+" "+heartbeatPath, s.handleHeartbeat)

	return authMiddleware(token, logger)(mux), nil
}

// authMiddleware rejects any request whose bearer token doesn't match
// token, comparing in constant time. It never reads the request body, so
// an unauthenticated request's body is never logged or otherwise touched.
func authMiddleware(token string, logger *slog.Logger) func(http.Handler) http.Handler {
	want := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := bearerToken(r)
			if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
				logger.Warn("peer: unauthorized request", "method", r.Method, "path", r.URL.Path)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. ok is false when the header is missing or malformed.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return "", false
	}
	return h[len(prefix):], true
}

// ListenAndServe binds addr and runs handler on it until ctx is done, then
// shuts down gracefully (see Serve).
func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("peer: listen on %s: %w", addr, err)
	}
	return Serve(ctx, ln, handler)
}

// Serve runs handler on the already-bound listener ln until ctx is done,
// then shuts down gracefully: it waits up to shutdownTimeout for in-flight
// requests to finish before returning. Splitting this out from
// ListenAndServe lets callers (and tests) bind with net.Listen first, e.g.
// "127.0.0.1:0", and learn the assigned port before serving starts.
func Serve(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := newHTTPServer(handler)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("peer: serve: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("peer: shutdown: %w", err)
		}
		return <-errCh
	}
}

// newHTTPServer builds the *http.Server Serve runs, with the package's
// read/write/idle timeouts set explicitly (see their doc comment above)
// rather than left at the zero-value default.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}
