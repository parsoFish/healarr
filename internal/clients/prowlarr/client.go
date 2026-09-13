package prowlarr

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

const apiBase = "/api/v1"

// HTTPClient talks to a real Prowlarr.
type HTTPClient struct{ h *httpx.Client }

var _ Client = (*HTTPClient)(nil)

// New builds an HTTPClient; apiKey is sent as X-Api-Key.
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error) {
	all := append([]httpx.Option{httpx.WithHeader("X-Api-Key", apiKey)}, opts...)
	h, err := httpx.New(baseURL, all...)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: %w", err)
	}
	return &HTTPClient{h: h}, nil
}

func (c *HTTPClient) Health(ctx context.Context) ([]HealthItem, error) {
	var out []HealthItem
	if err := c.h.GetJSON(ctx, apiBase+"/health", nil, &out); err != nil {
		return nil, fmt.Errorf("prowlarr health: %w", err)
	}
	return out, nil
}

func (c *HTTPClient) Indexers(ctx context.Context) ([]Indexer, error) {
	var out []Indexer
	if err := c.h.GetJSON(ctx, apiBase+"/indexer", nil, &out); err != nil {
		return nil, fmt.Errorf("prowlarr indexers: %w", err)
	}
	return out, nil
}

// rawIndexerStatus mirrors the wire shape; MostRecentFailure and DisabledTill
// may be JSON null, which nullTime decodes as the zero time.Time.
type rawIndexerStatus struct {
	IndexerID         int64    `json:"indexerId"`
	MostRecentFailure nullTime `json:"mostRecentFailure"`
	DisabledTill      nullTime `json:"disabledTill"`
}

func (c *HTTPClient) IndexerStatus(ctx context.Context) ([]IndexerStatus, error) {
	var raw []rawIndexerStatus
	if err := c.h.GetJSON(ctx, apiBase+"/indexerstatus", nil, &raw); err != nil {
		return nil, fmt.Errorf("prowlarr indexerstatus: %w", err)
	}
	out := make([]IndexerStatus, 0, len(raw))
	for _, r := range raw {
		out = append(out, IndexerStatus{
			IndexerID:         r.IndexerID,
			MostRecentFailure: r.MostRecentFailure.Time,
			DisabledTill:      r.DisabledTill.Time,
		})
	}
	return out, nil
}

func (c *HTTPClient) DeleteIndexer(ctx context.Context, id int64) error {
	if err := c.h.Delete(ctx, fmt.Sprintf("%s/indexer/%d", apiBase, id), nil); err != nil {
		return fmt.Errorf("prowlarr delete indexer %d: %w", id, err)
	}
	return nil
}

// TestIndexer fetches the indexer's full definition and POSTs it back to the
// test endpoint. Prowlarr requires the complete object (including its
// implementation-specific fields, which healarr never inspects) rather than
// just the id, so the fetched map is forwarded unmodified.
func (c *HTTPClient) TestIndexer(ctx context.Context, id int64) error {
	var def map[string]any
	if err := c.h.GetJSON(ctx, fmt.Sprintf("%s/indexer/%d", apiBase, id), nil, &def); err != nil {
		return fmt.Errorf("prowlarr get indexer %d: %w", id, err)
	}
	if err := c.h.PostJSON(ctx, apiBase+"/indexer/test", def, nil); err != nil {
		return fmt.Errorf("prowlarr test indexer %d: %w", id, err)
	}
	return nil
}
