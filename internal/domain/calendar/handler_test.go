package calendar

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	calendarv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/calendar"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type fakeTripRepo struct {
	trips []*trip.Trip
}

func (f *fakeTripRepo) GetTrip(context.Context, uuid.UUID, uuid.UUID) (*trip.Trip, error) {
	if len(f.trips) == 0 {
		return nil, trip.ErrNotFound
	}
	return f.trips[0], nil
}

func (f *fakeTripRepo) SaveTrip(context.Context, *trip.Trip, int64) (*trip.Trip, error) {
	return f.trips[0], nil
}

func (f *fakeTripRepo) ListTrips(context.Context, uuid.UUID, int, int) ([]*trip.Trip, int, error) {
	return f.trips, len(f.trips), nil
}

func (f *fakeTripRepo) SetShare(context.Context, uuid.UUID, uuid.UUID, bool, string) (*trip.Trip, error) {
	return f.trips[0], nil
}

type memStore struct {
	token string
	uid   uuid.UUID
}

func (m *memStore) GetOrCreateFeedToken(_ context.Context, userID uuid.UUID, mint func() string) (string, error) {
	if m.token == "" {
		m.token = mint()
		m.uid = userID
	}
	return m.token, nil
}

func (m *memStore) UserIDForFeedToken(context.Context, string) (uuid.UUID, error) { return m.uid, nil }

func (m *memStore) UpsertConnection(context.Context, uuid.UUID, string, string, []byte) (*Connection, error) {
	return nil, nil
}

func (m *memStore) ListConnections(context.Context, uuid.UUID) ([]*Connection, error) {
	return nil, nil
}

func (m *memStore) GetConnection(context.Context, uuid.UUID, uuid.UUID) (*Connection, error) {
	return nil, errNotFound
}

func (m *memStore) DeleteConnection(context.Context, uuid.UUID, uuid.UUID) error { return errNotFound }

func authed(ctx context.Context, id uuid.UUID) context.Context {
	return interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: id.String()})
}

func TestListCalendarEventsIncludesDatedTrips(t *testing.T) {
	t.Parallel()
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	h := NewHandler(&fakeTripRepo{trips: []*trip.Trip{{
		ID:    uid,
		Title: "Lisbon",
		Days:  []trip.TripDay{{ID: uuid.New(), DayNumber: 1, Date: &day}},
	}}}, &memStore{}, "https://api.example")

	from := timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	to := timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	res, err := h.ListCalendarEvents(authed(context.Background(), uid), connect.NewRequest(&calendarv1.ListCalendarEventsRequest{
		From: from, To: to,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Msg.Events) != 1 {
		t.Fatalf("got %d events", len(res.Msg.Events))
	}
	if res.Msg.Events[0].Title != "Lisbon" {
		t.Fatalf("title %q", res.Msg.Events[0].Title)
	}
}

func TestStartCalendarConnectRequiresConfig(t *testing.T) {
	t.Parallel()
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	h := NewHandler(&fakeTripRepo{}, &memStore{}, "https://api.example")
	_, err := h.StartCalendarConnect(authed(context.Background(), uid), connect.NewRequest(&calendarv1.StartCalendarConnectRequest{
		Provider: calendarv1.CalendarProvider_CALENDAR_PROVIDER_GOOGLE,
	}))
	if err == nil {
		t.Fatal("expected failed precondition")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code %v", connect.CodeOf(err))
	}
}

func TestGetTripCalendarFeedUrl(t *testing.T) {
	t.Parallel()
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	h := NewHandler(&fakeTripRepo{}, &memStore{}, "https://api.example")
	res, err := h.GetTripCalendarFeedUrl(authed(context.Background(), uid), connect.NewRequest(&calendarv1.GetTripCalendarFeedUrlRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.Url == "" || res.Msg.Url[:8] != "https://" {
		t.Fatalf("url %q", res.Msg.Url)
	}
}
