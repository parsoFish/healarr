// Package httpx is a minimal JSON/form HTTP client shared by all service clients.
package httpx

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 15 * time.Second

// APIError is returned for any non-2xx response.
type APIError struct {
	Status int
	Method string
	URL    string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, truncate(e.Body, 200))
}

// redactURL returns a copy of rawURL with every query parameter value
// replaced by "REDACTED" (parameter names are preserved). It is the single
// choke point every error path in this package must send a request URL
// through before it can appear in an error message, since query strings
// routinely carry secrets (e.g. Tautulli's apikey parameter). Unparsable
// input is returned unchanged, since it cannot contain a decodable query.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.RawQuery == "" {
		return rawURL
	}
	q := u.Query()
	for k, vs := range q {
		redacted := make([]string, len(vs))
		for i := range vs {
			redacted[i] = "REDACTED"
		}
		q[k] = redacted
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// hasQuery reports whether rawURL carries a query string. Unparsable input
// is treated as having none.
func hasQuery(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && u.RawQuery != ""
}

// redactTransportError guards against a secret leaking through the standard
// library's own error message: net/http and net/url both report failures as
// a *url.Error whose Error() method embeds the full request URL verbatim
// (e.g. `Get "http://host/x?apikey=...": dial tcp ...`), bypassing
// redactURL on our own wrapping text entirely. It returns a copy of err with
// any embedded *url.Error's URL field redacted, preserving the error chain
// (errors.Is/As, Unwrap) and leaving err itself unmodified; err is returned
// unchanged if it contains no *url.Error.
func redactTransportError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	redacted := *ue
	redacted.URL = redactURL(ue.URL)
	return &redacted
}

// IsStatus reports whether err is an APIError with the given status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// Client wraps an *http.Client with a base URL and default headers.
type Client struct {
	base    *url.URL
	headers http.Header
	http    *http.Client
	timeout time.Duration
}

// Option customises a Client.
type Option func(*Client)

// WithHeader sets a default header sent with every request. A per-call
// Content-Type (GetJSON/PostJSON/PutJSON/PostForm each set their own) always
// overrides a default Content-Type set here, since the per-call value is more
// specific; a default Accept set here is preserved instead of being replaced
// by the client's built-in Accept default.
func WithHeader(k, v string) Option { return func(c *Client) { c.headers.Set(k, v) } }

// WithTimeout overrides the underlying http.Client's timeout. The value is
// applied after all options have run, so it takes effect regardless of
// whether WithHTTPClient is supplied before or after it in the option list.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithHTTPClient replaces the underlying http.Client with a shallow copy of h
// (e.g. to share a cookie jar). The caller's original *http.Client is never
// mutated by later options (such as WithTimeout or WithTLSServerName) or by
// requests made through this Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		cp := *h
		c.http = &cp
	}
}

// WithTLSServerName sets SNI/verification name (Plex's plex.direct
// certificates). It never mutates a transport or TLS config supplied by the
// caller (e.g. via WithHTTPClient): both are cloned before being changed, so
// the caller's original *http.Transport keeps its own settings (including
// RootCAs and MinVersion) untouched.
func WithTLSServerName(name string) Option {
	return func(c *Client) {
		t, ok := c.http.Transport.(*http.Transport)
		if !ok || t == nil {
			t = http.DefaultTransport.(*http.Transport).Clone()
		} else {
			t = t.Clone()
		}
		var cfg *tls.Config
		if t.TLSClientConfig != nil {
			cfg = t.TLSClientConfig.Clone()
		} else {
			cfg = &tls.Config{}
		}
		cfg.ServerName = name
		if cfg.MinVersion == 0 {
			cfg.MinVersion = tls.VersionTLS12
		}
		t.TLSClientConfig = cfg
		c.http.Transport = t
	}
}

// New builds a client for baseURL.
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("httpx: invalid base URL %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("httpx: invalid base URL %q", baseURL)
	}
	c := &Client{base: u, headers: http.Header{}, http: &http.Client{Timeout: defaultTimeout}}
	for _, o := range opts {
		o(c)
	}
	if c.timeout != 0 {
		c.http.Timeout = c.timeout
	}
	return c, nil
}

// HTTP returns the underlying http.Client, e.g. for cookie-jar sharing.
func (c *Client) HTTP() *http.Client { return c.http }

// BaseURL returns the client's configured base URL.
func (c *Client) BaseURL() string { return c.base.String() }

func (c *Client) resolve(path string, query url.Values) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func (c *Client) do(ctx context.Context, method, rawURL string, body io.Reader, contentType string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, fmt.Errorf("httpx: build %s %s: %w", method, redactURL(rawURL), redactTransportError(err))
	}
	for k, vs := range c.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpx: %s %s: %w", method, redactURL(rawURL), redactTransportError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("httpx: read %s %s: %w", method, redactURL(rawURL), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody := string(raw)
		if hasQuery(rawURL) {
			respBody = "(body omitted: request carried query parameters)"
		}
		return nil, &APIError{Status: resp.StatusCode, Method: method, URL: redactURL(rawURL), Body: respBody}
	}
	return raw, nil
}

func decode(raw []byte, out any) error {
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("httpx: decode response: %w (body: %s)", err, truncate(string(raw), 200))
	}
	return nil
}

// GetJSON issues a GET request and decodes the JSON response body into out.
// out == nil discards the body.
func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, out any) error {
	raw, err := c.do(ctx, http.MethodGet, c.resolve(path, query), nil, "")
	if err != nil {
		return err
	}
	return decode(raw, out)
}

func (c *Client) sendJSON(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return fmt.Errorf("httpx: encode body: %w", err)
		}
	}
	raw, err := c.do(ctx, method, c.resolve(path, query), &buf, "application/json")
	if err != nil {
		return err
	}
	return decode(raw, out)
}

// PostJSON issues a POST request with a JSON-encoded body and decodes the JSON response into out.
func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	return c.PostJSONQuery(ctx, path, nil, body, out)
}

// PostJSONQuery issues a POST request carrying both a query string and a
// JSON-encoded body (body may be nil to send an empty body), decoding the
// JSON response into out. It exists for endpoints that put their
// parameters on the query string rather than in the body (e.g. Docker's
// /images/prune?filters=...); it shares do()'s response-size cap and
// non-2xx -> APIError handling with every other method on Client.
func (c *Client) PostJSONQuery(ctx context.Context, path string, query url.Values, body, out any) error {
	return c.sendJSON(ctx, http.MethodPost, path, query, body, out)
}

// PutJSON issues a PUT request with a JSON-encoded body and decodes the JSON response into out.
func (c *Client) PutJSON(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPut, path, nil, body, out)
}

// Delete issues a DELETE request and discards the response body.
func (c *Client) Delete(ctx context.Context, path string, query url.Values) error {
	_, err := c.do(ctx, http.MethodDelete, c.resolve(path, query), nil, "")
	return err
}

// GetText issues a GET request and returns the raw response body as text,
// for endpoints (e.g. qBittorrent's plain-text API) that don't return JSON.
// out == nil discards the body.
func (c *Client) GetText(ctx context.Context, path string, query url.Values, out *string) error {
	raw, err := c.do(ctx, http.MethodGet, c.resolve(path, query), nil, "")
	if err != nil {
		return err
	}
	if out != nil {
		*out = string(raw)
	}
	return nil
}

// PostForm sends application/x-www-form-urlencoded and returns the raw text body.
func (c *Client) PostForm(ctx context.Context, path string, form url.Values, out *string) error {
	raw, err := c.do(ctx, http.MethodPost, c.resolve(path, nil), strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if out != nil {
		*out = string(raw)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
