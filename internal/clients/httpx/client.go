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
}

// Option customises a Client.
type Option func(*Client)

// WithHeader sets a default header sent with every request.
func WithHeader(k, v string) Option { return func(c *Client) { c.headers.Set(k, v) } }

// WithTimeout overrides the underlying http.Client's timeout.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.http.Timeout = d } }

// WithHTTPClient replaces the underlying http.Client entirely (e.g. to share a cookie jar).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithTLSServerName sets SNI/verification name (Plex's plex.direct certificates).
func WithTLSServerName(name string) Option {
	return func(c *Client) {
		t, ok := c.http.Transport.(*http.Transport)
		if !ok || t == nil {
			t = http.DefaultTransport.(*http.Transport).Clone()
		}
		t.TLSClientConfig = &tls.Config{ServerName: name, MinVersion: tls.VersionTLS12}
		c.http.Transport = t
	}
}

// New builds a client for baseURL.
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("httpx: invalid base URL %q", baseURL)
	}
	c := &Client{base: u, headers: http.Header{}, http: &http.Client{Timeout: defaultTimeout}}
	for _, o := range opts {
		o(c)
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
		return nil, fmt.Errorf("httpx: build %s %s: %w", method, rawURL, err)
	}
	for k, vs := range c.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpx: %s %s: %w", method, rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("httpx: read %s %s: %w", method, rawURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode, Method: method, URL: rawURL, Body: string(raw)}
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

func (c *Client) sendJSON(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return fmt.Errorf("httpx: encode body: %w", err)
		}
	}
	raw, err := c.do(ctx, method, c.resolve(path, nil), &buf, "application/json")
	if err != nil {
		return err
	}
	return decode(raw, out)
}

// PostJSON issues a POST request with a JSON-encoded body and decodes the JSON response into out.
func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPost, path, body, out)
}

// PutJSON issues a PUT request with a JSON-encoded body and decodes the JSON response into out.
func (c *Client) PutJSON(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPut, path, body, out)
}

// Delete issues a DELETE request and discards the response body.
func (c *Client) Delete(ctx context.Context, path string, query url.Values) error {
	_, err := c.do(ctx, http.MethodDelete, c.resolve(path, query), nil, "")
	return err
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
