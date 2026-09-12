package docker

import (
	"context"
	"errors"
	"fmt"
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
	Config       struct {
		Tty bool `json:"Tty"`
	} `json:"Config"`
}

// inspect fetches and decodes /containers/{name}/json for name (an id or a
// container name both work against the Docker API).
func (c *HTTPClient) inspect(ctx context.Context, name string) (rawInspect, error) {
	var insp rawInspect
	err := c.h.GetJSON(ctx, "/containers/"+url.PathEscape(name)+"/json", nil, &insp)
	return insp, err
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

	insp, err := c.inspect(ctx, r.ID)
	if err != nil {
		if httpx.IsStatus(err, http.StatusNotFound) {
			// The container was removed between listing and inspecting it;
			// keep the summary row (zero Health/StartedAt/RestartCount)
			// rather than failing the whole listing over one race.
			return cont, nil
		}
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

// Logs returns the last tail lines of combined stdout/stderr for name.
//
// Whether the log stream is framed depends on how the container was
// started: a tty container's output carries no multiplexed-stream framing
// at all, while a non-tty container's does. Logs consults the container's
// own inspect data (Config.Tty) to decide which case applies, rather than
// guessing from the body's shape, and returns the inspect error unchanged
// if that lookup fails (no log fetch is attempted). Docker's framing is
// stripped only when Tty is false.
func (c *HTTPClient) Logs(ctx context.Context, name string, tail int) (string, error) {
	insp, err := c.inspect(ctx, name)
	if err != nil {
		return "", c.wrapErr("logs: inspect "+name, err)
	}

	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {strconv.Itoa(tail)}}
	var raw string
	if err := c.h.GetText(ctx, "/containers/"+url.PathEscape(name)+"/logs", q, &raw); err != nil {
		return "", c.wrapErr("logs "+name, err)
	}
	if insp.Config.Tty {
		return raw, nil
	}
	return demultiplex(raw), nil
}

// demultiplex strips Docker's 8-byte stream-frame headers and concatenates
// the payloads. It is only reached when inspect reports Tty == false, but
// stays defensive: if the body doesn't actually look framed (stream type
// 0/1/2 followed by three zero bytes), it is returned unchanged rather than
// mangled, as a fallback for a body that doesn't match what Tty promised.
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
	if err := c.h.PostJSONQuery(ctx, "/images/prune", q, nil, &resp); err != nil {
		return 0, c.wrapErr("prune images", err)
	}
	return resp.SpaceReclaimed, nil
}
