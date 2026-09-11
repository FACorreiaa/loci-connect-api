package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type stubImageRepo struct {
	images map[uuid.UUID][]locitypes.POIImage
	asked  []uuid.UUID
	err    error
}

func (s *stubImageRepo) ImagesForPOIs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]locitypes.POIImage, error) {
	s.asked = append(s.asked, ids...)
	return s.images, s.err
}

func TestAttachImagesDecoratesOnlyResolvedPlaces(t *testing.T) {
	resolved := uuid.New()
	repo := &stubImageRepo{images: map[uuid.UUID][]locitypes.POIImage{
		resolved: {{
			POIID:       resolved,
			URL:         "https://upload.wikimedia.org/cabo-girao.jpg",
			Licence:     "CC BY-SA 3.0",
			Attribution: "H. Zell",
		}},
	}}

	data := &locitypes.AiCityResponse{
		PointsOfInterest: []locitypes.POIDetailedInfo{
			{ID: resolved, Name: "Cabo Girão"},
			{Name: "Somewhere The Model Imagined"}, // no id: no row, no pictures
		},
	}

	attachImagesWithDependencies(context.Background(), data, repo, nil)

	first := data.PointsOfInterest[0]
	if len(first.ImageCredits) != 1 || first.ImageCredits[0].Attribution != "H. Zell" {
		t.Errorf("a resolved place should carry its credit: %+v", first.ImageCredits)
	}
	// Both forms describe the same pictures: existing clients read Images.
	if len(first.Images) != 1 || first.Images[0] != "https://upload.wikimedia.org/cabo-girao.jpg" {
		t.Errorf("the URL list should mirror the credits: %+v", first.Images)
	}
	if second := data.PointsOfInterest[1]; len(second.ImageCredits) != 0 || len(second.Images) != 0 {
		t.Errorf("an unresolved place must not gain pictures: %+v", second)
	}
	if len(repo.asked) != 1 || repo.asked[0] != resolved {
		t.Errorf("only resolved ids should be looked up: %v", repo.asked)
	}
}

// One query for the whole answer: the same place in two lists is asked for once.
func TestAttachImagesAsksOnceForAPlaceInTwoLists(t *testing.T) {
	id := uuid.New()
	repo := &stubImageRepo{images: map[uuid.UUID][]locitypes.POIImage{}}

	data := &locitypes.AiCityResponse{
		PointsOfInterest: []locitypes.POIDetailedInfo{{ID: id, Name: "Sé Cathedral"}},
		AIItineraryResponse: locitypes.AIItineraryResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{{ID: id, Name: "Sé Cathedral"}},
		},
	}

	attachImagesWithDependencies(context.Background(), data, repo, nil)

	if len(repo.asked) != 1 {
		t.Errorf("expected one lookup for one place, got %d", len(repo.asked))
	}
}

// Pictures are decoration. A turn that cannot fetch them is still an answer.
func TestAttachImagesSurvivesAFailingRepository(t *testing.T) {
	repo := &stubImageRepo{err: errors.New("database is unwell")}
	data := &locitypes.AiCityResponse{
		PointsOfInterest: []locitypes.POIDetailedInfo{{ID: uuid.New(), Name: "Cabo Girão"}},
	}

	attachImagesWithDependencies(context.Background(), data, repo, nil)

	if len(data.PointsOfInterest[0].ImageCredits) != 0 {
		t.Error("a failed lookup should leave the place undecorated, not broken")
	}
}
