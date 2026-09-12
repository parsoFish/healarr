package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

// baseURL is unversioned: the Pi's daemon tops out at API 1.41 and rejects a
// pinned /vX.Y prefix, so every request rides the daemon's own latest.
const baseURL = "http://docker"

// requestTimeout bounds a single Docker Engine API call over the socket.
const requestTimeout = 15 * time.Second

// HTTPClient talks to a real Docker Engine over its unix socket.
type HTTPClient struct {
	h          *httpx.Client
	socketPath string
	binary     string
}

var _ Client = (*HTTPClient)(nil)

// New builds an HTTPClient that dials socketPath for every request; binary
// is the docker (or compose-compatible) executable Exec shells out to.
func New(socketPath, binary string) (*HTTPClient, error) {
	if binary == "" {
		return nil, errors.New("docker: binary must not be empty")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	h, err := httpx.New(baseURL,
		httpx.WithHTTPClient(&http.Client{Transport: transport}),
		httpx.WithTimeout(requestTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	return &HTTPClient{h: h, socketPath: socketPath, binary: binary}, nil
}

// wrapErr wraps a Docker API error, adding the docker-group hint when the
// underlying cause is a permission-denied on the unix socket.
func (c *HTTPClient) wrapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("docker %s: permission denied on %s: add this user to the docker group: %w", op, c.socketPath, err)
	}
	return fmt.Errorf("docker %s: %w", op, err)
}

// rawContainer mirrors the /containers/json summary wire shape.
type rawContainer struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
}

// rawInspect mirrors the parts of /containers/{id}/json this client reads.
type rawInspect struct {
	State struct {
		StartedAt string `json:"StartedAt"`
		Health    *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
}

// Containers lists all containers, then inspects each to fill in health,
// restart count and start time.
func (c *HTTPClient) Containers(ctx context.Context) ([]Container, error) {
	var raws []rawContainer
	if err := c.h.GetJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &raws); err != nil {
		return nil, c.wrapErr("containers", err)
	}
	out := make([]Container, 0, len(raws))
	for _, r := range raws {
		cont, err := c.mergeInspect(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, cont)
	}
	return out, nil
}

func (c *HTTPClient) mergeInspect(ctx context.Context, r rawContainer) (Container, error) {
	name := r.ID
	if len(r.Names) > 0 {
		name = strings.TrimPrefix(r.Names[0], "/")
	}
	cont := Container{ID: r.ID, Name: name, Image: r.Image, State: r.State, Status: r.Status}

	var insp rawInspect
	if err := c.h.GetJSON(ctx, "/containers/"+r.ID+"/json", nil, &insp); err != nil {
		return Container{}, c.wrapErr(fmt.Sprintf("containers: inspect %s", name), err)
	}
	cont.RestartCount = insp.RestartCount
	if insp.State.Health != nil {
		cont.Health = insp.State.Health.Status
	}
	if insp.State.StartedAt != "" {
		started, err := time.Parse(time.RFC3339Nano, insp.State.StartedAt)
		if err != nil {
			return Container{}, fmt.Errorf("docker containers: inspect %s: parse StartedAt: %w", name, err)
		}
		cont.StartedAt = started
	}
	return cont, nil
}

// dockerFrameHeader is the length of Docker's multiplexed-stream frame
// header: [STREAM_TYPE, 0, 0, 0, SIZE(4 bytes big-endian)].
const dockerFrameHeader = 8

// Logs returns the last tail lines of combined stdout/stderr for name, with
// Docker's multiplexed stream framing stripped when present.
func (c *HTTPClient) Logs(ctx context.Context, name string, tail int) (string, error) {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {strconv.Itoa(tail)}}
	var raw string
	if err := c.h.GetText(ctx, "/containers/"+name+"/logs", q, &raw); err != nil {
		return "", c.wrapErr("logs "+name, err)
	}
	return demultiplex(raw), nil
}

// demultiplex strips Docker's 8-byte stream-frame headers and concatenates
// the payloads. A tty container's log stream carries no framing at all, so
// the raw body is returned unchanged when the first frame's header shape
// doesn't hold (stream type 0/1/2 followed by three zero bytes).
func demultiplex(raw string) string {
	b := []byte(raw)
	if !looksFramed(b) {
		return raw
	}
	var out []byte
	for len(b) >= dockerFrameHeader {
		size := int(uint32(b[4])<<24 | uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7]))
		b = b[dockerFrameHeader:]
		if size > len(b) {
			// Truncated frame: keep what's left rather than drop it.
			break
		}
		out = append(out, b[:size]...)
		b = b[size:]
	}
	out = append(out, b...) // any trailing bytes shorter than a full header
	return string(out)
}

func looksFramed(b []byte) bool {
	return len(b) >= dockerFrameHeader && (b[0] == 0 || b[0] == 1 || b[0] == 2) && b[1] == 0 && b[2] == 0 && b[3] == 0
}

// rawImage mirrors one entry of /system/df's Images array.
type rawImage struct {
	Containers int   `json:"Containers"`
	Size       int64 `json:"Size"`
}

// rawBuildCacheEntry mirrors one entry of /system/df's optional BuildCache array.
type rawBuildCacheEntry struct {
	Size int64 `json:"Size"`
}

// rawDiskUsage mirrors the /system/df wire shape this client reads.
type rawDiskUsage struct {
	LayersSize int64                `json:"LayersSize"`
	Images     []rawImage           `json:"Images"`
	BuildCache []rawBuildCacheEntry `json:"BuildCache"`
}

// DiskUsage reports image and build-cache disk usage from /system/df.
func (c *HTTPClient) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var raw rawDiskUsage
	if err := c.h.GetJSON(ctx, "/system/df", nil, &raw); err != nil {
		return DiskUsage{}, c.wrapErr("disk usage", err)
	}
	out := DiskUsage{ImagesTotal: len(raw.Images), ImagesSize: raw.LayersSize}
	for _, img := range raw.Images {
		if img.Containers > 0 {
			out.ImagesActive++
		} else {
			out.ImagesReclaimable += img.Size
		}
	}
	for _, bc := range raw.BuildCache {
		out.BuildCacheSize += bc.Size
	}
	return out, nil
}

// PruneImages removes unused images and returns the bytes reclaimed.
func (c *HTTPClient) PruneImages(ctx context.Context, dangling bool) (int64, error) {
	filterVal := "false"
	if dangling {
		filterVal = "true"
	}
	q := url.Values{"filters": {fmt.Sprintf(`{"dangling":["%s"]}`, filterVal)}}

	var resp struct {
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	if err := c.postQuery(ctx, "/images/prune", q, &resp); err != nil {
		return 0, c.wrapErr("prune images", err)
	}
	return resp.SpaceReclaimed, nil
}

// postQuery issues a POST with a query string and no body, decoding the
// JSON response into out. httpx's PostJSON resolves its path without query
// support, so image prune (the one Docker endpoint that carries parameters
// as a query string rather than a JSON body) composes the request directly
// against the shared *http.Client httpx already built for the unix socket.
func (c *HTTPClient) postQuery(ctx context.Context, path string, query url.Values, out any) error {
	full := c.h.BaseURL() + path + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, full, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := c.h.HTTP().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &httpx.APIError{Status: resp.StatusCode, Method: http.MethodPost, URL: full, Body: string(body)}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
