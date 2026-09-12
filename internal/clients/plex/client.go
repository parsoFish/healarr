package plex

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

// HTTPClient talks to a real Plex Media Server.
type HTTPClient struct{ h *httpx.Client }

var _ Client = (*HTTPClient)(nil)

// New builds an HTTPClient. token is sent as X-Plex-Token on every request
// except when empty (Identity works without a token). Accept is always set
// to application/json so Plex returns JSON instead of XML.
func New(baseURL, token string, opts ...httpx.Option) (*HTTPClient, error) {
	all := []httpx.Option{httpx.WithHeader("Accept", "application/json")}
	if token != "" {
		all = append(all, httpx.WithHeader("X-Plex-Token", token))
	}
	all = append(all, opts...)
	h, err := httpx.New(baseURL, all...)
	if err != nil {
		return nil, fmt.Errorf("plex: %w", err)
	}
	return &HTTPClient{h: h}, nil
}

// rawIdentity mirrors the wire shape of GET /identity.
type rawIdentity struct {
	MediaContainer struct {
		MachineIdentifier string `json:"machineIdentifier"`
		Version           string `json:"version"`
	} `json:"MediaContainer"`
}

func (c *HTTPClient) Identity(ctx context.Context) (Identity, error) {
	var raw rawIdentity
	if err := c.h.GetJSON(ctx, "/identity", nil, &raw); err != nil {
		return Identity{}, fmt.Errorf("plex identity: %w", err)
	}
	return Identity{
		MachineIdentifier: raw.MediaContainer.MachineIdentifier,
		Version:           raw.MediaContainer.Version,
	}, nil
}

// rawDirectory mirrors one entry of GET /library/sections' Directory array.
type rawDirectory struct {
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`
	ScannedAt epochTime `json:"scannedAt"`
	UpdatedAt epochTime `json:"updatedAt"`
}

type rawSections struct {
	MediaContainer struct {
		Directory []rawDirectory `json:"Directory"`
	} `json:"MediaContainer"`
}

func (c *HTTPClient) Libraries(ctx context.Context) ([]Library, error) {
	var raw rawSections
	if err := c.h.GetJSON(ctx, "/library/sections", nil, &raw); err != nil {
		return nil, fmt.Errorf("plex libraries: %w", err)
	}
	out := make([]Library, 0, len(raw.MediaContainer.Directory))
	for _, d := range raw.MediaContainer.Directory {
		out = append(out, Library{
			Key:       d.Key,
			Title:     d.Title,
			Type:      d.Type,
			ScannedAt: d.ScannedAt.Time,
			UpdatedAt: d.UpdatedAt.Time,
		})
	}
	return out, nil
}

// rawMetadata mirrors one entry of a library's Metadata array.
// LibrarySectionID is a bare JSON integer that identifies the owning
// library; it is converted to a string for Item.LibraryKey.
type rawMetadata struct {
	RatingKey        string    `json:"ratingKey"`
	Title            string    `json:"title"`
	Type             string    `json:"type"`
	AddedAt          epochTime `json:"addedAt"`
	LibrarySectionID int64     `json:"librarySectionID"`
}

type rawMetadataContainer struct {
	MediaContainer struct {
		Metadata []rawMetadata `json:"Metadata"`
	} `json:"MediaContainer"`
}

// RecentlyAdded lists the most recently added items in the given library,
// sending X-Plex-Container-Size and X-Plex-Container-Start as query params
// (Plex's pagination convention).
func (c *HTTPClient) RecentlyAdded(ctx context.Context, libraryKey string, limit int) ([]Item, error) {
	q := url.Values{}
	q.Set("X-Plex-Container-Size", strconv.Itoa(limit))
	q.Set("X-Plex-Container-Start", "0")

	var raw rawMetadataContainer
	path := fmt.Sprintf("/library/sections/%s/recentlyAdded", libraryKey)
	if err := c.h.GetJSON(ctx, path, q, &raw); err != nil {
		return nil, fmt.Errorf("plex recently added %s: %w", libraryKey, err)
	}
	out := make([]Item, 0, len(raw.MediaContainer.Metadata))
	for _, m := range raw.MediaContainer.Metadata {
		out = append(out, Item{
			RatingKey:  m.RatingKey,
			Title:      m.Title,
			Type:       m.Type,
			AddedAt:    m.AddedAt.Time,
			LibraryKey: strconv.FormatInt(m.LibrarySectionID, 10),
		})
	}
	return out, nil
}

// RefreshLibrary triggers a rescan of the given library. Plex returns 200
// with an empty body, so the response is decoded into nil (discarded).
func (c *HTTPClient) RefreshLibrary(ctx context.Context, libraryKey string) error {
	path := fmt.Sprintf("/library/sections/%s/refresh", libraryKey)
	if err := c.h.GetJSON(ctx, path, nil, nil); err != nil {
		return fmt.Errorf("plex refresh library %s: %w", libraryKey, err)
	}
	return nil
}
