package peer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

const (
	// DefaultTimeout is the per-request timeout applied to every attempt
	// (including retries) unless overridden with WithTimeout.
	DefaultTimeout = 5 * time.Second
	// DefaultRetries is the number of retries after the initial attempt,
	// applied unless overridden with WithRetries.
	DefaultRetries = 2
)

const (
	reportPath       = "/v1/report"
	latestReportPath = "/v1/report/latest"
	decisionPath     = "/v1/decision"
	heartbeatPath    = "/v1/heartbeat"
)

// ErrUnauthorized is returned when the peer rejects the bearer token
// (401/403). It is never retried.
var ErrUnauthorized = errors.New("peer: unauthorized")

// ErrPeerUnavailable is returned when every attempt (initial plus retries)
// fails with a transport error or a 5xx response. Callers must treat this
// as non-fatal: log and continue.
var ErrPeerUnavailable = errors.New("peer: unavailable")

// HTTPClient is the default Client implementation: a small retrying
// wrapper around internal/clients/httpx with a static bearer token.
type HTTPClient struct {
	http    *httpx.Client
	retries int
	backoff func(attempt int) time.Duration
}

var _ Client = (*HTTPClient)(nil)

// Option customises an HTTPClient built by New.
type Option func(*httpClientConfig)

// httpClientConfig collects options before New builds the underlying
// httpx.Client, since the timeout option must be applied before that
// client is constructed.
type httpClientConfig struct {
	timeout time.Duration
	retries int
	backoff func(attempt int) time.Duration
}

// WithTimeout overrides the per-request timeout (default DefaultTimeout).
func WithTimeout(d time.Duration) Option {
	return func(c *httpClientConfig) { c.timeout = d }
}

// WithRetries overrides the retry count (default DefaultRetries).
func WithRetries(n int) Option {
	return func(c *httpClientConfig) { c.retries = n }
}

// WithBackoff overrides the retry backoff schedule. attempt is 1 for the
// delay before the first retry, 2 before the second, and so on. Tests
// should supply a schedule that returns 0 so they never sleep for real.
func WithBackoff(f func(attempt int) time.Duration) Option {
	return func(c *httpClientConfig) { c.backoff = f }
}

// defaultBackoff implements the 500 ms -> 1 s schedule from the design
// constraints for up to DefaultRetries retries; later attempts hold at 1 s.
func defaultBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return 500 * time.Millisecond
	}
	return time.Second
}

// New builds an HTTPClient for baseURL, authenticating every request with
// "Authorization: Bearer <token>". opts override the timeout/retry/backoff
// defaults.
func New(baseURL, token string, opts ...Option) (*HTTPClient, error) {
	cfg := &httpClientConfig{timeout: DefaultTimeout, retries: DefaultRetries, backoff: defaultBackoff}
	for _, o := range opts {
		o(cfg)
	}
	hc, err := httpx.New(baseURL,
		httpx.WithHeader("Authorization", "Bearer "+token),
		httpx.WithTimeout(cfg.timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("peer: new client: %w", err)
	}
	return &HTTPClient{http: hc, retries: cfg.retries, backoff: cfg.backoff}, nil
}

// PushReport sends env to the peer's report endpoint.
func (c *HTTPClient) PushReport(ctx context.Context, env ReportEnvelope) (Ack, error) {
	var ack Ack
	if err := c.doWithRetry(ctx, func(ctx context.Context) error {
		return c.http.PostJSON(ctx, reportPath, env, &ack)
	}); err != nil {
		return Ack{}, fmt.Errorf("peer: push report: %w", err)
	}
	return ack, nil
}

// FetchLatest fetches the peer's most recently pushed report. ok is false
// (with a nil error) when the peer has no report yet.
func (c *HTTPClient) FetchLatest(ctx context.Context) (ReportEnvelope, bool, error) {
	var env ReportEnvelope
	err := c.doWithRetry(ctx, func(ctx context.Context) error {
		return c.http.GetJSON(ctx, latestReportPath, nil, &env)
	})
	switch {
	case err == nil:
		return env, true, nil
	case httpx.IsStatus(err, http.StatusNotFound):
		return ReportEnvelope{}, false, nil
	default:
		return ReportEnvelope{}, false, fmt.Errorf("peer: fetch latest: %w", err)
	}
}

// SendDecision records a decision on the peer (observe-only in Phase 3).
func (c *HTTPClient) SendDecision(ctx context.Context, d Decision) (Ack, error) {
	var ack Ack
	if err := c.doWithRetry(ctx, func(ctx context.Context) error {
		return c.http.PostJSON(ctx, decisionPath, d, &ack)
	}); err != nil {
		return Ack{}, fmt.Errorf("peer: send decision: %w", err)
	}
	return ack, nil
}

// Heartbeat tells the peer this node is alive.
func (c *HTTPClient) Heartbeat(ctx context.Context, hb Heartbeat) (Ack, error) {
	var ack Ack
	if err := c.doWithRetry(ctx, func(ctx context.Context) error {
		return c.http.PostJSON(ctx, heartbeatPath, hb, &ack)
	}); err != nil {
		return Ack{}, fmt.Errorf("peer: heartbeat: %w", err)
	}
	return ack, nil
}

// doWithRetry runs fn up to 1+c.retries times. It returns immediately
// (never retrying) on success, once the caller's ctx is done, on
// ErrUnauthorized (401/403), and on any other 4xx response. It retries on
// a transport error or a 5xx response, waiting c.backoff(attempt) between
// attempts; after the final failed attempt it returns ErrPeerUnavailable
// wrapping the last underlying error (via a multi-%w chain), so callers
// can still errors.As into the original *httpx.APIError or transport
// error.
//
// "Did the caller give up?" is answered by ctx.Err(), not by the returned
// error's identity: an attempt that exceeds the per-request timeout
// (http.Client.Timeout) also reports context.DeadlineExceeded while the
// caller's own context is still very much alive, and that is an
// unresponsive peer to retry, not a cancellation to honour.
func (c *HTTPClient) doWithRetry(ctx context.Context, fn func(context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			if err := waitFor(ctx, c.backoff(attempt)); err != nil {
				return err
			}
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err // the caller's own context ended: stop, don't retry
		}
		var ae *httpx.APIError
		if errors.As(err, &ae) {
			if ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden {
				return fmt.Errorf("%w (status %d)", ErrUnauthorized, ae.Status)
			}
			if ae.Status < 500 {
				return err // other 4xx: never retried, surfaced as-is
			}
		}
		lastErr = err // transport error or 5xx: retryable
	}
	return fmt.Errorf("%w: %w", ErrPeerUnavailable, lastErr)
}

// waitFor blocks for d, or returns ctx's error if ctx is done first.
func waitFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
