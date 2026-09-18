package tautulli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

const apiPath = "/api/v2"

// HTTPClient talks to a real Tautulli.
type HTTPClient struct {
	h      *httpx.Client
	apiKey string
}

var _ Client = (*HTTPClient)(nil)

// New builds an HTTPClient. Every call is a GET to /api/v2 with apikey and
// cmd as query parameters.
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error) {
	h, err := httpx.New(baseURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("tautulli: %w", err)
	}
	return &HTTPClient{h: h, apiKey: apiKey}, nil
}

// envelope mirrors Tautulli's response wrapper. Data is left raw since its
// shape depends on cmd.
type envelope struct {
	Response struct {
		Result  string          `json:"result"`
		Message *string         `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"response"`
}

// call issues cmd with any extra query parameters, checks result == success,
// and (when out is non-nil) decodes response.data into out.
func (c *HTTPClient) call(ctx context.Context, cmd string, extra url.Values, out any) error {
	q := url.Values{}
	q.Set("apikey", c.apiKey)
	q.Set("cmd", cmd)
	for k, vs := range extra {
		for _, v := range vs {
			q.Add(k, v)
		}
	}

	var env envelope
	if err := c.h.GetJSON(ctx, apiPath, q, &env); err != nil {
		return fmt.Errorf("tautulli %s: %w", cmd, err)
	}
	if env.Response.Result != "success" {
		msg := "unknown error"
		if env.Response.Message != nil {
			msg = *env.Response.Message
		}
		return fmt.Errorf("tautulli %s: %s", cmd, msg)
	}
	if out != nil {
		if err := json.Unmarshal(env.Response.Data, out); err != nil {
			return fmt.Errorf("tautulli %s: decode data: %w", cmd, err)
		}
	}
	return nil
}

// Ping verifies the API key and connectivity via cmd=status.
func (c *HTTPClient) Ping(ctx context.Context) error {
	return c.call(ctx, "status", nil, nil)
}

// rawHistoryRow mirrors one row of get_history's data.data array.
// rating_key/parent_rating_key/grandparent_rating_key are JSON-integer-ish
// fields, but Tautulli sends some of them as "" instead (e.g. a movie row
// has no parent hierarchy, so parent_rating_key/grandparent_rating_key
// come back as ""); watched_status/percent_complete/date can likewise
// arrive as quoted numeric strings on some rows. The loose* wrapper types
// tolerate all of that instead of failing the whole decode.
type rawHistoryRow struct {
	RatingKey            looseNumberString `json:"rating_key"`
	ParentRatingKey      looseNumberString `json:"parent_rating_key"`
	GrandparentRatingKey looseNumberString `json:"grandparent_rating_key"`
	Title                string            `json:"title"`
	GrandparentTitle     string            `json:"grandparent_title"`
	MediaType            string            `json:"media_type"`
	User                 string            `json:"user"`
	Date                 epochTime         `json:"date"`
	WatchedStatus        looseFloat        `json:"watched_status"`
	PercentComplete      looseInt          `json:"percent_complete"`
}

type rawHistoryData struct {
	Data []rawHistoryRow `json:"data"`
}

// History fetches watch history since the given time via cmd=get_history,
// sending after=YYYY-MM-DD and length as query parameters.
func (c *HTTPClient) History(ctx context.Context, since time.Time, length int) ([]HistoryRow, error) {
	q := url.Values{}
	q.Set("after", since.Format("2006-01-02"))
	q.Set("length", strconv.Itoa(length))

	var data rawHistoryData
	if err := c.call(ctx, "get_history", q, &data); err != nil {
		return nil, err
	}
	out := make([]HistoryRow, 0, len(data.Data))
	for _, r := range data.Data {
		out = append(out, HistoryRow{
			RatingKey:            r.RatingKey.String(),
			GrandparentRatingKey: r.GrandparentRatingKey.String(),
			ParentRatingKey:      r.ParentRatingKey.String(),
			Title:                r.Title,
			GrandparentTitle:     r.GrandparentTitle,
			MediaType:            r.MediaType,
			User:                 r.User,
			Date:                 r.Date.Time,
			WatchedStatus:        float64(r.WatchedStatus),
			PercentComplete:      int(r.PercentComplete),
		})
	}
	return out, nil
}

// rawActivityData mirrors get_activity's data object. stream_count is a
// quoted string in Tautulli's response, which json.Number also accepts.
type rawActivityData struct {
	StreamCount json.Number `json:"stream_count"`
}

// ActivityCount returns the current number of active streams via
// cmd=get_activity.
func (c *HTTPClient) ActivityCount(ctx context.Context) (int, error) {
	var data rawActivityData
	if err := c.call(ctx, "get_activity", nil, &data); err != nil {
		return 0, err
	}
	n, err := data.StreamCount.Int64()
	if err != nil {
		return 0, fmt.Errorf("tautulli get_activity: parse stream_count %q: %w", data.StreamCount, err)
	}
	return int(n), nil
}
