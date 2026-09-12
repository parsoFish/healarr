package sonarr

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

// HTTPClient talks to a real Sonarr.
type HTTPClient struct{ h *httpx.Client }

var _ Client = (*HTTPClient)(nil)

// New builds an HTTPClient; apiKey is sent as X-Api-Key.
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error) {
	all := append([]httpx.Option{httpx.WithHeader("X-Api-Key", apiKey)}, opts...)
	h, err := httpx.New(baseURL, all...)
	if err != nil {
		return nil, fmt.Errorf("sonarr: %w", err)
	}
	return &HTTPClient{h: h}, nil
}

func (c *HTTPClient) Health(ctx context.Context) ([]HealthItem, error) {
	var out []HealthItem
	if err := c.h.GetJSON(ctx, apiBase+"/health", nil, &out); err != nil {
		return nil, fmt.Errorf("sonarr health: %w", err)
	}
	return out, nil
}

// raw wire shapes kept private; public types stay stable.
type rawQueuePage struct {
	TotalRecords int `json:"totalRecords"`
	Records      []struct {
		ID                    int64     `json:"id"`
		SeriesID              int64     `json:"seriesId"`
		EpisodeID             int64     `json:"episodeId"`
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
			"includeUnknownSeriesItems": {"true"},
		}
		var p rawQueuePage
		if err := c.h.GetJSON(ctx, apiBase+"/queue", q, &p); err != nil {
			return nil, fmt.Errorf("sonarr queue page %d: %w", page, err)
		}
		for _, r := range p.Records {
			var msgs []string
			for _, sm := range r.StatusMessages {
				msgs = append(msgs, sm.Messages...)
			}
			items = append(items, QueueItem{
				ID: r.ID, SeriesID: r.SeriesID, EpisodeID: r.EpisodeID, Title: r.Title, Status: r.Status,
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

type rawSeries struct {
	ID         int64     `json:"id"`
	TVDBID     int64     `json:"tvdbId"`
	Title      string    `json:"title"`
	Monitored  bool      `json:"monitored"`
	Status     string    `json:"status"`
	Path       string    `json:"path"`
	Added      time.Time `json:"added"`
	Statistics struct {
		EpisodeFileCount int   `json:"episodeFileCount"`
		EpisodeCount     int   `json:"episodeCount"`
		SizeOnDisk       int64 `json:"sizeOnDisk"`
	} `json:"statistics"`
}

func (c *HTTPClient) Series(ctx context.Context) ([]Series, error) {
	var raw []rawSeries
	if err := c.h.GetJSON(ctx, apiBase+"/series", nil, &raw); err != nil {
		return nil, fmt.Errorf("sonarr series: %w", err)
	}
	out := make([]Series, 0, len(raw))
	for _, r := range raw {
		out = append(out, Series{
			ID: r.ID, TVDBID: r.TVDBID, Title: r.Title, Monitored: r.Monitored, Status: r.Status, Path: r.Path, Added: r.Added,
			EpisodeFileCount: r.Statistics.EpisodeFileCount, EpisodeCount: r.Statistics.EpisodeCount, SizeOnDisk: r.Statistics.SizeOnDisk,
		})
	}
	return out, nil
}

func (c *HTTPClient) RootFolders(ctx context.Context) ([]RootFolder, error) {
	var out []RootFolder
	if err := c.h.GetJSON(ctx, apiBase+"/rootfolder", nil, &out); err != nil {
		return nil, fmt.Errorf("sonarr rootfolders: %w", err)
	}
	return out, nil
}

func (c *HTTPClient) WantedMissingCount(ctx context.Context) (int, error) {
	var p struct {
		TotalRecords int `json:"totalRecords"`
	}
	q := url.Values{"pageSize": {"1"}, "monitored": {"true"}}
	if err := c.h.GetJSON(ctx, apiBase+"/wanted/missing", q, &p); err != nil {
		return 0, fmt.Errorf("sonarr wanted/missing: %w", err)
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
			return nil, fmt.Errorf("sonarr history page %d: %w", page, err)
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
		return fmt.Errorf("sonarr delete queue %d: %w", id, err)
	}
	return nil
}

func (c *HTTPClient) UpdateSeriesMonitored(ctx context.Context, id int64, monitored bool) error {
	// Sonarr requires the full object on PUT; fetch, patch, send.
	var full map[string]any
	if err := c.h.GetJSON(ctx, fmt.Sprintf("%s/series/%d", apiBase, id), nil, &full); err != nil {
		return fmt.Errorf("sonarr get series %d: %w", id, err)
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
	// broken.
	if err := c.h.PutJSON(ctx, fmt.Sprintf("%s/series/%d", apiBase, id), patched, nil); err != nil {
		return fmt.Errorf("sonarr update series %d: %w", id, err)
	}
	return nil
}

func (c *HTTPClient) DeleteSeries(ctx context.Context, id int64, deleteFiles, addExclusion bool) error {
	q := url.Values{
		"deleteFiles":            {strconv.FormatBool(deleteFiles)},
		"addImportListExclusion": {strconv.FormatBool(addExclusion)},
	}
	if err := c.h.Delete(ctx, fmt.Sprintf("%s/series/%d", apiBase, id), q); err != nil {
		return fmt.Errorf("sonarr delete series %d: %w", id, err)
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
		return 0, fmt.Errorf("sonarr command %s: %w", name, err)
	}
	return out.ID, nil
}
