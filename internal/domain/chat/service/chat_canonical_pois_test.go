package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestCanonicalizePOIs(t *testing.T) {
	cityID := uuid.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("reuses existing canonical identity", func(t *testing.T) {
		repo := new(MockPOIRepository)
		poiID := uuid.New()
		repo.On("FindPoiByNameAndCity", mock.Anything, "Museum", cityID).
			Return(&locitypes.POIDetailedInfo{ID: poiID, CityID: cityID}, nil).Once()
		result := canonicalizePOIsWithDependencies(
			context.Background(), []locitypes.POIDetailedInfo{{Name: "Museum"}}, cityID,
			repo, logger,
		)

		if len(result) != 1 || result[0].ID != poiID || result[0].CityID != cityID {
			t.Fatalf("unexpected canonical result: %+v", result)
		}
		repo.AssertNotCalled(t, "SavePoi", mock.Anything, mock.Anything, mock.Anything)
		repo.AssertExpectations(t)
	})

	t.Run("creates a new canonical POI", func(t *testing.T) {
		repo := new(MockPOIRepository)
		poiID := uuid.New()
		repo.On("FindPoiByNameAndCity", mock.Anything, "Viewpoint", cityID).
			Return(nil, nil).Once()
		repo.On("SavePoi", mock.Anything, mock.MatchedBy(func(p locitypes.POIDetailedInfo) bool {
			return p.Name == "Viewpoint" && p.DescriptionPOI == "City panorama"
		}), cityID).Return(poiID, nil).Once()
		result := canonicalizePOIsWithDependencies(
			context.Background(), []locitypes.POIDetailedInfo{{
				Name: "Viewpoint", Description: "City panorama", Category: "attraction",
			}}, cityID, repo, logger,
		)

		if len(result) != 1 || result[0].ID != poiID {
			t.Fatalf("unexpected canonical result: %+v", result)
		}
		repo.AssertExpectations(t)
	})
}
