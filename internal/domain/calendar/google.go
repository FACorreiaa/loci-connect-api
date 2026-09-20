package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

const lociCalendarSummary = "Loci"

type googleAPI struct {
	cfg OAuthConfig
}

func (g googleAPI) accountLabel(ctx context.Context, ts oauth2.TokenSource) (string, error) {
	client := oauth2.NewClient(ctx, ts)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("google userinfo: %s", res.Status)
	}
	var out struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Email, nil
}

func (g googleAPI) listEvents(ctx context.Context, ts oauth2.TokenSource, from, to time.Time) ([]OverlayEvent, error) {
	svc, err := gcal.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	call := svc.Events.List("primary").
		TimeMin(from.Format(time.RFC3339)).
		TimeMax(to.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		MaxResults(250)
	feed, err := call.Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	out := make([]OverlayEvent, 0, len(feed.Items))
	for _, item := range feed.Items {
		start, end, ok := googleEventTimes(item)
		if !ok {
			continue
		}
		out = append(out, OverlayEvent{
			ID:       "google:" + item.Id,
			Title:    item.Summary,
			Start:    start,
			End:      end,
			Location: item.Location,
		})
	}
	return out, nil
}

func googleEventTimes(item *gcal.Event) (time.Time, time.Time, bool) {
	parse := func(dt *gcal.EventDateTime) (time.Time, bool) {
		if dt == nil {
			return time.Time{}, false
		}
		if dt.DateTime != "" {
			t, err := time.Parse(time.RFC3339, dt.DateTime)
			return t, err == nil
		}
		if dt.Date != "" {
			t, err := time.Parse("2006-01-02", dt.Date)
			return t, err == nil
		}
		return time.Time{}, false
	}
	start, ok := parse(item.Start)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	end, ok := parse(item.End)
	if !ok {
		end = start.Add(time.Hour)
	}
	return start, end, true
}

func (g googleAPI) pushTrip(ctx context.Context, ts oauth2.TokenSource, t *trip.Trip) ([]string, error) {
	svc, err := gcal.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	calID, err := ensureLociCalendar(ctx, svc)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range t.Days {
		if d.Date == nil {
			continue
		}
		start, end := dayWindow(d)
		title := t.Title
		if title == "" {
			title = "Loci trip"
		}
		loc := d.CityName
		if loc == "" {
			loc = t.CityName
		}
		ev := &gcal.Event{
			Summary:     title,
			Location:    loc,
			Description: fmt.Sprintf("Day %d · Loci", d.DayNumber),
			Start:       &gcal.EventDateTime{DateTime: start.Format(time.RFC3339)},
			End:         &gcal.EventDateTime{DateTime: end.Format(time.RFC3339)},
		}
		created, err := svc.Events.Insert(calID, ev).Context(ctx).Do()
		if err != nil {
			return ids, err
		}
		ids = append(ids, created.Id)
	}
	return ids, nil
}

func ensureLociCalendar(ctx context.Context, svc *gcal.Service) (string, error) {
	list, err := svc.CalendarList.List().Context(ctx).Do()
	if err != nil {
		return "", err
	}
	for _, c := range list.Items {
		if c.Summary == lociCalendarSummary {
			return c.Id, nil
		}
	}
	created, err := svc.Calendars.Insert(&gcal.Calendar{Summary: lociCalendarSummary}).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return created.Id, nil
}
