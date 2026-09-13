package overseerr

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

const (
	apiBase = "/api/v1"

	// requestsPageSize is the "take" sent to GET /api/v1/request.
	requestsPageSize = 100
)

// maxRequestPages bounds Requests' pagination loop so a misbehaving server
// (pageInfo.pages that never advances to meet the current page) cannot spin
// forever; 500 pages at requestsPageSize covers 50,000 requests, far beyond
// any real Overseerr instance's backlog. It is a var (not a const) solely so
// tests can shrink it and exercise the guard without 500 real round-trips.
var maxRequestPages = 500

// HTTPClient talks to a real Overseerr.
type HTTPClient struct{ h *httpx.Client }

var _ Client = (*HTTPClient)(nil)

// New builds an HTTPClient; apiKey is sent as X-Api-Key.
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error) {
	all := append([]httpx.Option{httpx.WithHeader("X-Api-Key", apiKey)}, opts...)
	h, err := httpx.New(baseURL, all...)
	if err != nil {
		return nil, fmt.Errorf("overseerr: %w", err)
	}
	return &HTTPClient{h: h}, nil
}

type rawStatus struct {
	Version string `json:"version"`
}

type rawSettingsPublic struct {
	Initialized bool `json:"initialized"`
}

// Status combines GET /api/v1/status (version) with GET
// /api/v1/settings/public (initialized), since neither endpoint alone
// carries both fields.
func (c *HTTPClient) Status(ctx context.Context) (Status, error) {
	var raw rawStatus
	if err := c.h.GetJSON(ctx, apiBase+"/status", nil, &raw); err != nil {
		return Status{}, fmt.Errorf("overseerr status: %w", err)
	}
	var settings rawSettingsPublic
	if err := c.h.GetJSON(ctx, apiBase+"/settings/public", nil, &settings); err != nil {
		return Status{}, fmt.Errorf("overseerr settings: %w", err)
	}
	return Status{Version: raw.Version, Initialized: settings.Initialized}, nil
}

type rawRequestedBy struct {
	Email        string `json:"email"`
	DisplayName  string `json:"displayName"`
	PlexUsername string `json:"plexUsername"`
}

type rawMedia struct {
	TMDBID int64 `json:"tmdbId"`
	TVDBID int64 `json:"tvdbId"`
	Status int   `json:"status"`
}

type rawRequest struct {
	ID          int64          `json:"id"`
	Status      int            `json:"status"`
	Type        string         `json:"type"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	Media       rawMedia       `json:"media"`
	RequestedBy rawRequestedBy `json:"requestedBy"`
}

type rawPageInfo struct {
	Pages    int `json:"pages"`
	PageSize int `json:"pageSize"`
	Results  int `json:"results"`
	Page     int `json:"page"`
}

type rawRequestsPage struct {
	PageInfo rawPageInfo  `json:"pageInfo"`
	Results  []rawRequest `json:"results"`
}

// requestedByName picks the requester's email, falling back to their
// display name when no email is set.
func requestedByName(u rawRequestedBy) string {
	if u.Email != "" {
		return u.Email
	}
	return u.DisplayName
}

// Requests lists media requests matching filter, paging via take/skip until
// pageInfo.page reaches pageInfo.pages.
func (c *HTTPClient) Requests(ctx context.Context, filter string) ([]Request, error) {
	var out []Request
	skip := 0
	for page := 0; page < maxRequestPages; page++ {
		q := url.Values{}
		q.Set("filter", filter)
		q.Set("take", strconv.Itoa(requestsPageSize))
		q.Set("skip", strconv.Itoa(skip))

		var raw rawRequestsPage
		if err := c.h.GetJSON(ctx, apiBase+"/request", q, &raw); err != nil {
			return nil, fmt.Errorf("overseerr requests: %w", err)
		}
		for _, r := range raw.Results {
			out = append(out, Request{
				ID:          r.ID,
				Status:      r.Status,
				MediaType:   r.Type,
				TMDBID:      r.Media.TMDBID,
				TVDBID:      r.Media.TVDBID,
				MediaStatus: r.Media.Status,
				RequestedBy: requestedByName(r.RequestedBy),
				CreatedAt:   r.CreatedAt,
				UpdatedAt:   r.UpdatedAt,
			})
		}
		if len(raw.Results) == 0 || raw.PageInfo.Page >= raw.PageInfo.Pages {
			return out, nil
		}
		skip += requestsPageSize
	}
	return nil, fmt.Errorf("overseerr requests: exceeded %d pages without reaching pageInfo.pages", maxRequestPages)
}

// DeclineRequest declines a pending request.
func (c *HTTPClient) DeclineRequest(ctx context.Context, id int64) error {
	path := fmt.Sprintf("%s/request/%d/decline", apiBase, id)
	if err := c.h.PostJSON(ctx, path, nil, nil); err != nil {
		return fmt.Errorf("overseerr decline request %d: %w", id, err)
	}
	return nil
}
