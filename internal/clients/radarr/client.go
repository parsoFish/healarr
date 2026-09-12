package radarr

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

const (
	apiBase       = "/api/v3"
	queuePageSize = 250
	historyPage   = 500
)

// HTTPClient talks to a real Radarr.
type HTTPClient struct{ h *httpx.Client }

var _ Client = (*HTTPClient)(nil)

// New builds an HTTPClient; apiKey is sent as X-Api-Key.
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error) {
	all := append([]httpx.Option{httpx.WithHeader("X-Api-Key", apiKey)}, opts...)
	h, err := httpx.New(baseURL, all...)
	if err != nil {
		return nil, fmt.Errorf("radarr: %w", err)
	}
	return &HTTPClient{h: h}, nil
}

func (c *HTTPClient) Health(ctx context.Context) ([]HealthItem, error) {
	var out []HealthItem
	if err := c.h.GetJSON(ctx, apiBase+"/health", nil, &out); err != nil {
		return nil, fmt.Errorf("radarr health: %w", err)
	}
	return out, nil
}

// raw wire shapes kept private; public types stay stable.
type rawQueuePage struct {
	TotalRecords int `json:"totalRecords"`
	Records      []struct {
		ID                    int64     `json:"id"`
		MovieID               int64     `json:"movieId"`
		Title                 string    `json:"title"`
		Status                string    `json:"status"`
		TrackedDownloadStatus string    `json:"trackedDownloadStatus"`
		TrackedDownloadState  string    `json:"trackedDownloadState"`
		DownloadID            string    `json:"downloadId"`
		OutputPath            string    `json:"outputPath"`
		Size                  float64   `json:"size"`
		SizeLeft              float64   `json:"sizeleft"`
		Added                 time.Time `json:"added"`
		StatusMessages        []struct {
			Messages []string `json:"messages"`
		} `json:"statusMessages"`
	} `json:"records"`
}

func (c *HTTPClient) Queue(ctx context.Context) ([]QueueItem, error) {
	var items []QueueItem
	for page := 1; ; page++ {
		q := url.Values{
			"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(queuePageSize)},
			"includeUnknownMovieItems": {"true"},
		}
		var p rawQueuePage
		if err := c.h.GetJSON(ctx, apiBase+"/queue", q, &p); err != nil {
			return nil, fmt.Errorf("radarr queue page %d: %w", page, err)
		}
		for _, r := range p.Records {
			var msgs []string
			for _, sm := range r.StatusMessages {
				msgs = append(msgs, sm.Messages...)
			}
			items = append(items, QueueItem{
				ID: r.ID, MovieID: r.MovieID, Title: r.Title, Status: r.Status,
				TrackedDownloadStatus: r.TrackedDownloadStatus, TrackedDownloadState: r.TrackedDownloadState,
				DownloadID: r.DownloadID, OutputPath: r.OutputPath, Size: r.Size, SizeLeft: r.SizeLeft,
				Messages: msgs, Added: r.Added,
			})
		}
		if len(items) >= p.TotalRecords || len(p.Records) == 0 {
			return items, nil
		}
	}
}

func (c *HTTPClient) Movies(ctx context.Context) ([]Movie, error) {
	var out []Movie
	if err := c.h.GetJSON(ctx, apiBase+"/movie", nil, &out); err != nil {
		return nil, fmt.Errorf("radarr movies: %w", err)
	}
	return out, nil
}

func (c *HTTPClient) RootFolders(ctx context.Context) ([]RootFolder, error) {
	var out []RootFolder
	if err := c.h.GetJSON(ctx, apiBase+"/rootfolder", nil, &out); err != nil {
		return nil, fmt.Errorf("radarr rootfolders: %w", err)
	}
	return out, nil
}

func (c *HTTPClient) WantedMissingCount(ctx context.Context) (int, error) {
	var p struct {
		TotalRecords int `json:"totalRecords"`
	}
	q := url.Values{"pageSize": {"1"}, "monitored": {"true"}}
	if err := c.h.GetJSON(ctx, apiBase+"/wanted/missing", q, &p); err != nil {
		return 0, fmt.Errorf("radarr wanted/missing: %w", err)
	}
	return p.TotalRecords, nil
}

func (c *HTTPClient) History(ctx context.Context, since time.Time, eventType string) ([]HistoryRecord, error) {
	var out []HistoryRecord
	for page := 1; ; page++ {
		q := url.Values{
			"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(historyPage)},
			"sortKey": {"date"}, "sortDirection": {"descending"},
		}
		if eventType != "" {
			q.Set("eventType", eventType)
		}
		var p struct {
			TotalRecords int             `json:"totalRecords"`
			Records      []HistoryRecord `json:"records"`
		}
		if err := c.h.GetJSON(ctx, apiBase+"/history", q, &p); err != nil {
			return nil, fmt.Errorf("radarr history page %d: %w", page, err)
		}
		for _, r := range p.Records {
			if !since.IsZero() && r.Date.Before(since) {
				return out, nil // sorted descending: everything after is older
			}
			out = append(out, r)
		}
		if len(p.Records) == 0 || page*historyPage >= p.TotalRecords {
			return out, nil
		}
	}
}

func (c *HTTPClient) DeleteQueueItem(ctx context.Context, id int64, removeFromClient, blocklist bool) error {
	q := url.Values{
		"removeFromClient": {strconv.FormatBool(removeFromClient)},
		"blocklist":        {strconv.FormatBool(blocklist)},
		"skipRedownload":   {"false"},
	}
	if err := c.h.Delete(ctx, fmt.Sprintf("%s/queue/%d", apiBase, id), q); err != nil {
		return fmt.Errorf("radarr delete queue %d: %w", id, err)
	}
	return nil
}

func (c *HTTPClient) UpdateMovieMonitored(ctx context.Context, id int64, monitored bool) error {
	// Radarr requires the full object on PUT; fetch, patch, send.
	var full map[string]any
	if err := c.h.GetJSON(ctx, fmt.Sprintf("%s/movie/%d", apiBase, id), nil, &full); err != nil {
		return fmt.Errorf("radarr get movie %d: %w", id, err)
	}
	patched := make(map[string]any, len(full))
	for k, v := range full {
		patched[k] = v
	}
	patched["monitored"] = monitored
	// moveFiles only affects a changed path, which we never touch here, and
	// httpx.PutJSON has no query parameter: appending "?moveFiles=false" to
	// path would be treated as a literal path segment (percent-encoded) by
	// httpx's resolve, not a query string, so it is omitted rather than sent
	// broken. Radarr's default for moveFiles is false, which matches intent.
	if err := c.h.PutJSON(ctx, fmt.Sprintf("%s/movie/%d", apiBase, id), patched, nil); err != nil {
		return fmt.Errorf("radarr update movie %d: %w", id, err)
	}
	return nil
}

func (c *HTTPClient) DeleteMovie(ctx context.Context, id int64, deleteFiles, addExclusion bool) error {
	q := url.Values{
		"deleteFiles":        {strconv.FormatBool(deleteFiles)},
		"addImportExclusion": {strconv.FormatBool(addExclusion)},
	}
	if err := c.h.Delete(ctx, fmt.Sprintf("%s/movie/%d", apiBase, id), q); err != nil {
		return fmt.Errorf("radarr delete movie %d: %w", id, err)
	}
	return nil
}

func (c *HTTPClient) RunCommand(ctx context.Context, name string, params map[string]any) (int64, error) {
	body := map[string]any{"name": name}
	for k, v := range params {
		body[k] = v
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.h.PostJSON(ctx, apiBase+"/command", body, &out); err != nil {
		return 0, fmt.Errorf("radarr command %s: %w", name, err)
	}
	return out.ID, nil
}
