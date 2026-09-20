package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
)

type calendlyAPI struct{}

func (calendlyAPI) accountLabel(ctx context.Context, ts oauth2.TokenSource) (string, error) {
	me, err := calendlyGET(ctx, ts, "https://api.calendly.com/users/me")
	if err != nil {
		return "", err
	}
	var out struct {
		Resource struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(me, &out); err != nil {
		return "", err
	}
	if out.Resource.Email != "" {
		return out.Resource.Email, nil
	}
	return out.Resource.Name, nil
}

func (calendlyAPI) listEvents(ctx context.Context, ts oauth2.TokenSource, from, to time.Time) ([]OverlayEvent, error) {
	me, err := calendlyGET(ctx, ts, "https://api.calendly.com/users/me")
	if err != nil {
		return nil, err
	}
	var user struct {
		Resource struct {
			URI string `json:"uri"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(me, &user); err != nil {
		return nil, err
	}
	q := url.Values{
		"user":           {user.Resource.URI},
		"min_start_time": {from.UTC().Format(time.RFC3339)},
		"max_start_time": {to.UTC().Format(time.RFC3339)},
		"status":         {"active"},
		"count":          {"100"},
	}
	raw, err := calendlyGET(ctx, ts, "https://api.calendly.com/scheduled_events?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var feed struct {
		Collection []struct {
			URI       string `json:"uri"`
			Name      string `json:"name"`
			StartTime string `json:"start_time"`
			EndTime   string `json:"end_time"`
			Location  struct {
				Location string `json:"location"`
			} `json:"location"`
		} `json:"collection"`
	}
	if err := json.Unmarshal(raw, &feed); err != nil {
		return nil, err
	}
	out := make([]OverlayEvent, 0, len(feed.Collection))
	for _, item := range feed.Collection {
		start, err := time.Parse(time.RFC3339, item.StartTime)
		if err != nil {
			continue
		}
		end, err := time.Parse(time.RFC3339, item.EndTime)
		if err != nil {
			end = start.Add(time.Hour)
		}
		out = append(out, OverlayEvent{
			ID:       "calendly:" + item.URI,
			Title:    item.Name,
			Start:    start,
			End:      end,
			Location: item.Location.Location,
		})
	}
	return out, nil
}

func calendlyGET(ctx context.Context, ts oauth2.TokenSource, rawURL string) ([]byte, error) {
	tok, err := ts.Token()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("calendly: %s", res.Status)
	}
	return body, nil
}
