package trip

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func TestReopenedNeedsAFullDay(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		updatedAt time.Time
		want      bool
	}{
		{"just saved", now, false},
		{"same session, an hour later", now.Add(-time.Hour), false},
		{"just under a day", now.Add(-23 * time.Hour), false},
		{"a day later", now.Add(-25 * time.Hour), true},
		{"a week later", now.Add(-7 * 24 * time.Hour), true},
		{"clock skew puts the trip in the future", now.Add(time.Hour), false},
		{"never saved", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reopened(tc.updatedAt, now); got != tc.want {
				t.Fatalf("reopened(%v) = %v, want %v", tc.updatedAt, got, tc.want)
			}
		})
	}
}

func getTripLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if m["msg"] == tripReopenedMsg {
			out = append(out, m)
		}
	}
	return out
}

func getTrip(t *testing.T, h *Handler, userID, tripID uuid.UUID) {
	t.Helper()
	ctx := interceptors.ContextWithClaims(context.Background(), &interceptors.Claims{UserID: userID.String()})
	_, err := h.GetTrip(ctx, connect.NewRequest(&tripv1.GetTripRequest{TripId: tripID.String()}))
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
}

// The persistence claim is that people come back to a plan they made
// yesterday. This is the only place that can observe it, so a trip fetched a
// day or more after it was last saved is recorded as a re-open.
func TestGetTripRecordsAReopen(t *testing.T) {
	var buf bytes.Buffer
	userID, tripID := uuid.New(), uuid.New()
	repo := &fakeTripRepo{trip: &Trip{
		ID:        tripID,
		UserID:    userID,
		UpdatedAt: time.Now().Add(-48 * time.Hour),
	}}
	h := NewHandler(repo, "", nil, nil).WithLogger(slog.New(slog.NewJSONHandler(&buf, nil)))

	getTrip(t, h, userID, tripID)

	lines := getTripLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("logged %d re-opens, want 1", len(lines))
	}
	if lines[0]["trip_id"] != tripID.String() || lines[0]["user_id"] != userID.String() {
		t.Fatalf("logged %+v, want trip %s and user %s", lines[0], tripID, userID)
	}
}

func TestGetTripDoesNotRecordASameSessionRead(t *testing.T) {
	var buf bytes.Buffer
	userID, tripID := uuid.New(), uuid.New()
	repo := &fakeTripRepo{trip: &Trip{
		ID:        tripID,
		UserID:    userID,
		UpdatedAt: time.Now().Add(-30 * time.Minute),
	}}
	h := NewHandler(repo, "", nil, nil).WithLogger(slog.New(slog.NewJSONHandler(&buf, nil)))

	getTrip(t, h, userID, tripID)

	if lines := getTripLogLines(t, &buf); len(lines) != 0 {
		t.Fatalf("logged %d re-opens for a trip saved 30 minutes ago, want 0", len(lines))
	}
}

// The logger is optional everywhere else in this handler; a re-open must not
// be the thing that panics without one.
func TestGetTripSurvivesWithoutALogger(t *testing.T) {
	userID, tripID := uuid.New(), uuid.New()
	repo := &fakeTripRepo{trip: &Trip{
		ID:        tripID,
		UserID:    userID,
		UpdatedAt: time.Now().Add(-48 * time.Hour),
	}}
	getTrip(t, NewHandler(repo, "", nil, nil), userID, tripID)
}
