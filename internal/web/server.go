// Package web serves healarr's LAN-only dashboard, decisions and history
// pages (ADR-014): a token-and-cookie authenticated html/template UI with
// no JavaScript and no external assets, mounted under cfg.Web.BasePath.
// It never mutates the store directly for a delete decision — it records
// the decision and hands execution to a DecisionRunner (the agent adapts
// decision.Execute) — so the actions gate (config.Actions.Enabled) is
// enforced exactly once, in internal/decision, not duplicated here.
package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// shutdownTimeout bounds how long Serve/ListenAndServe wait for in-flight
// requests to finish once the caller's context is cancelled, mirroring
// internal/peer's ListenAndServe/Serve pattern.
const shutdownTimeout = 5 * time.Second

// HTTP server timeouts. As in internal/peer, every *http.Server this
// package builds sets all four explicitly via newHTTPServer rather than
// relying on the zero-value default (no timeout at all).
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
)

// Store is the subset of *store.Store the web UI reads and writes. It is
// satisfied by *store.Store (see the compile-time assertion in
// fakes_test.go) so production wiring never needs an adapter for it.
type Store interface {
	OpenFindings(ctx context.Context, node config.Node) ([]store.StoredFinding, error)
	LatestReport(ctx context.Context, node config.Node) (check.Report, bool, error)
	PendingDecisions(ctx context.Context) ([]store.Decision, error)
	CreateDecision(ctx context.Context, entityKey, kind string, at time.Time) (int64, error)
	DecisionByID(ctx context.Context, id int64) (store.Decision, bool, error)
	RecentRemediations(ctx context.Context, since time.Time) ([]store.Remediation, error)
	FindingHistory(ctx context.Context, node config.Node, since time.Time, limit int) ([]store.StoredFinding, error)
	LastPeerMessageAt(ctx context.Context, peer config.Node, kind string) (time.Time, bool, error)
}

// DecisionRunner executes a pending decision by id. Production wiring
// adapts decision.Execute(ctx, decision.Deps, id); tests use a fake.
type DecisionRunner interface {
	Execute(ctx context.Context, id int64) (store.Decision, error)
}

// server holds the dependencies and derived state every route handler
// needs: the store/runner the pages read and act through, the parsed
// template set, the normalised mount path, and the in-memory session
// table (see auth.go). Sessions live only in this map — restarting the
// process signs every browser out, which is an accepted trade-off for a
// LAN-only tool with no persistence requirement on "stay logged in".
type server struct {
	cfg      config.Config
	basePath string
	token    string
	store    Store
	runner   DecisionRunner
	logger   *slog.Logger
	tmpl     *template.Template
	css      template.CSS

	csrfKey []byte

	sessions *sessionTable
}

// New builds the http.Handler serving the dashboard, decisions and
// history pages under cfg.Web.BasePath. token is the shared secret
// GET <base>/login?token=… must match (secrets.toml's web_token); an
// empty token is refused, since a server that accepts any token isn't
// authenticating anything.
func New(cfg config.Config, token string, st Store, runner DecisionRunner, logger *slog.Logger) (http.Handler, error) {
	if token == "" {
		return nil, errors.New("web: New: token must not be empty")
	}
	if st == nil {
		return nil, errors.New("web: New: store must not be nil")
	}
	if runner == nil {
		return nil, errors.New("web: New: runner must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	csrfKey := make([]byte, 32)
	if _, err := rand.Read(csrfKey); err != nil {
		return nil, fmt.Errorf("web: New: generate csrf key: %w", err)
	}

	tmpl, err := parseTemplates()
	if err != nil {
		return nil, fmt.Errorf("web: New: parse templates: %w", err)
	}

	s := &server{
		cfg:      cfg,
		basePath: normalizeBasePath(cfg.Web.BasePath),
		token:    token,
		store:    st,
		runner:   runner,
		logger:   logger,
		tmpl:     tmpl,
		css:      template.CSS(cssBytes),
		csrfKey:  csrfKey,
		sessions: newSessionTable(),
	}

	return s.routes(), nil
}

// normalizeBasePath trims a trailing slash and ensures a leading one, so
// callers can configure "/healarr" or "/healarr/" interchangeably. An
// empty or "/" BasePath mounts the UI at the root ("" here, since every
// route below appends its own leading slash). An empty cfg.Web.BasePath
// (a bare zero-value Config, as in some tests) falls back to the same
// default config.Defaults().Web.BasePath ships, rather than a second
// hardcoded literal.
func normalizeBasePath(raw string) string {
	if raw == "" {
		raw = config.Defaults().Web.BasePath
	}
	if raw == "/" {
		return ""
	}
	trimmed := raw
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 || trimmed[0] != '/' {
		trimmed = "/" + trimmed
	}
	return trimmed
}

// routes builds the mux: /login is the only unauthenticated route, and
// the dashboard root is registered both with and without a trailing
// slash so cfg.Web.BasePath is tolerant of either form (see brief).
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(http.MethodGet+" "+s.basePath+"/login", s.handleLoginGet)
	mux.HandleFunc(http.MethodPost+" "+s.basePath+"/logout", s.requireSession(s.handleLogout))

	dashboardPath := s.basePath + "/"
	mux.HandleFunc(http.MethodGet+" "+dashboardPath, s.requireSession(s.handleDashboard))
	if s.basePath != "" {
		mux.HandleFunc(http.MethodGet+" "+s.basePath, s.requireSession(s.handleDashboard))
	}

	mux.HandleFunc(http.MethodGet+" "+s.basePath+"/decisions", s.requireSession(s.handleDecisionsGet))
	mux.HandleFunc(http.MethodPost+" "+s.basePath+"/decisions", s.requireSession(s.handleDecisionsPost))
	mux.HandleFunc(http.MethodGet+" "+s.basePath+"/history", s.requireSession(s.handleHistory))

	return mux
}

// ListenAndServe binds addr and runs handler on it until ctx is done,
// then shuts down gracefully (see Serve). It mirrors peer.ListenAndServe.
func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("web: listen on %s: %w", addr, err)
	}
	return Serve(ctx, ln, handler)
}

// Serve runs handler on the already-bound listener ln until ctx is done,
// then shuts down gracefully: it waits up to shutdownTimeout for
// in-flight requests to finish before returning. Splitting this out from
// ListenAndServe lets callers (and tests) bind with net.Listen first,
// e.g. "127.0.0.1:0", and learn the assigned port before serving starts.
func Serve(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := newHTTPServer(handler)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("web: serve: %w", err)
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
			return fmt.Errorf("web: shutdown: %w", err)
		}
		return <-errCh
	}
}

// newHTTPServer builds the *http.Server Serve runs, with the package's
// read/write/idle timeouts set explicitly rather than left at the
// zero-value default (see their doc comment above).
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}
