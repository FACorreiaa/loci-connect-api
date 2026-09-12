package service

import (
	"context"
	"errors"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/repository"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

func namedPOIs(n int) []locitypes.POIDetailedInfo {
	out := make([]locitypes.POIDetailedInfo, n)
	for i := range out {
		out[i] = locitypes.POIDetailedInfo{ID: uuid.New(), Name: string(rune('A' + i%26))}
	}
	return out
}

// sessionRepoStub answers GetSession and nothing else. The embedded interface
// is nil on purpose: reading a page must not touch the repository for anything
// beyond the session, and any other call panics loudly rather than passing.
type sessionRepoStub struct {
	repository.Repository
	session *locitypes.ChatSession
}

func (s sessionRepoStub) GetSession(context.Context, uuid.UUID) (*locitypes.ChatSession, error) {
	return s.session, nil
}

// sessionWith builds a stored answer and the service that reads it.
func sessionWith(t *testing.T, owner uuid.UUID, data *locitypes.AiCityResponse) (*ServiceImpl, uuid.UUID) {
	t.Helper()
	sessionID := uuid.New()
	return &ServiceImpl{llmInteractionRepo: sessionRepoStub{
		session: &locitypes.ChatSession{
			ID:               sessionID,
			UserID:           owner,
			CurrentItinerary: data,
		},
	}}, sessionID
}

// A page is a read of somebody's own answer. Reading it must be no easier than
// reading the session it lives in.
func TestGetSessionPOIsChecksOwnership(t *testing.T) {
	owner := uuid.New()
	svc, sessionID := sessionWith(t, owner, &locitypes.AiCityResponse{PointsOfInterest: namedPOIs(5)})

	if _, err := svc.GetSessionPOIs(context.Background(), uuid.New(), sessionID,
		locitypes.SectionGeneral, 1, 12); !errors.Is(err, common.ErrUnauthorized) {
		t.Fatalf("another user read the page: err = %v", err)
	}

	if _, err := svc.GetSessionPOIs(context.Background(), owner, sessionID,
		locitypes.SectionGeneral, 1, 12); err != nil {
		t.Fatalf("the owner could not read their own page: %v", err)
	}
}

// Pages must tile the list exactly once: no gaps, no repeats, and has_more
// false on the last one. The boundary case is a total that divides evenly,
// where an off-by-one shows up as a phantom empty page the client would
// happily ask for.
func TestPagesTileTheListExactlyOnce(t *testing.T) {
	owner := uuid.New()
	all := namedPOIs(24)
	svc, sessionID := sessionWith(t, owner, &locitypes.AiCityResponse{PointsOfInterest: all})

	seen := map[uuid.UUID]int{}
	page := 1
	for {
		got, err := svc.GetSessionPOIs(context.Background(), owner, sessionID,
			locitypes.SectionGeneral, page, 12)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, poi := range got.POIs {
			seen[poi.ID]++
		}
		if got.Total != 24 {
			t.Errorf("page %d reports a total of %d, want 24", page, got.Total)
		}
		if !got.HasMore {
			// 24 items at 12 a page is exactly two pages.
			if page != 2 {
				t.Errorf("ran out after %d pages, want 2", page)
			}
			break
		}
		page++
		if page > 5 {
			t.Fatal("has_more never went false")
		}
	}

	if len(seen) != 24 {
		t.Errorf("saw %d distinct places across the pages, want 24", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("place %s appeared %d times", id, n)
		}
	}
}

// Pressing "more" once too often is a thing clients do. It must be an empty
// page, not an error.
func TestPastTheEndIsAnEmptyPage(t *testing.T) {
	owner := uuid.New()
	svc, sessionID := sessionWith(t, owner, &locitypes.AiCityResponse{PointsOfInterest: namedPOIs(3)})

	got, err := svc.GetSessionPOIs(context.Background(), owner, sessionID,
		locitypes.SectionGeneral, 9, 12)
	if err != nil {
		t.Fatalf("page past the end failed: %v", err)
	}
	if len(got.POIs) != 0 || got.HasMore {
		t.Errorf("page past the end returned %d places, has_more=%v", len(got.POIs), got.HasMore)
	}
}

func TestPageSizeIsClamped(t *testing.T) {
	owner := uuid.New()
	svc, sessionID := sessionWith(t, owner, &locitypes.AiCityResponse{PointsOfInterest: namedPOIs(200)})

	for _, c := range []struct{ ask, want int }{
		{0, defaultSessionPOIPageSize},
		{-5, defaultSessionPOIPageSize},
		{500, maxSessionPOIPageSize},
		{12, 12},
	} {
		got, err := svc.GetSessionPOIs(context.Background(), owner, sessionID,
			locitypes.SectionGeneral, 1, c.ask)
		if err != nil {
			t.Fatalf("page size %d: %v", c.ask, err)
		}
		if got.PageSize != c.want || len(got.POIs) != c.want {
			t.Errorf("asked for %d a page, got page size %d with %d places, want %d",
				c.ask, got.PageSize, len(got.POIs), c.want)
		}
	}

	// A page below one is page one, not an offset into nowhere.
	got, err := svc.GetSessionPOIs(context.Background(), owner, sessionID,
		locitypes.SectionGeneral, 0, 12)
	if err != nil || got.Page != 1 || len(got.POIs) != 12 {
		t.Errorf("page 0 gave page %d with %d places (err %v)", got.Page, len(got.POIs), err)
	}
}

// The default section is "the plan". When a turn produced no itinerary the
// general list is the plan, and the response has to say so rather than claim
// an itinerary that does not exist.
func TestDefaultSectionFallsBackAndSaysSo(t *testing.T) {
	owner := uuid.New()

	svc, sessionID := sessionWith(t, owner, &locitypes.AiCityResponse{
		PointsOfInterest: namedPOIs(4),
	})
	got, err := svc.GetSessionPOIs(context.Background(), owner, sessionID, locitypes.SectionItinerary, 1, 12)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if got.Section != locitypes.SectionGeneral || len(got.POIs) != 4 {
		t.Errorf("fell back to %q with %d places, want general with 4", got.Section, len(got.POIs))
	}

	withItinerary, sessionID2 := sessionWith(t, owner, &locitypes.AiCityResponse{
		PointsOfInterest:    namedPOIs(4),
		AIItineraryResponse: locitypes.AIItineraryResponse{PointsOfInterest: namedPOIs(7), PlannedDays: 3},
	})
	got, err = withItinerary.GetSessionPOIs(context.Background(), owner, sessionID2, locitypes.SectionItinerary, 1, 12)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if got.Section != locitypes.SectionItinerary || len(got.POIs) != 7 {
		t.Errorf("read %q with %d places, want itinerary with 7", got.Section, len(got.POIs))
	}
	if got.PlannedDays != 3 {
		t.Errorf("planned days = %d, want 3", got.PlannedDays)
	}
}

// A session that never produced an answer is an empty page, not a crash.
func TestNoStoredAnswerIsAnEmptyPage(t *testing.T) {
	owner := uuid.New()
	svc, sessionID := sessionWith(t, owner, nil)

	got, err := svc.GetSessionPOIs(context.Background(), owner, sessionID, locitypes.SectionItinerary, 1, 12)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(got.POIs) != 0 || got.Total != 0 || got.HasMore {
		t.Errorf("empty session returned %+v", got)
	}
}
