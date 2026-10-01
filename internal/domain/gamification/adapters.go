package gamification

import (
	"context"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/travelhistory"
)

// ScoreOnTheSpot lets travel history score a visit without depending on this
// package (travelhistory.VisitScorer).
func (s *Service) ScoreOnTheSpot(ctx context.Context, userID uuid.UUID, v travelhistory.ScoredVisit) int {
	visit := Visit{UserID: userID, POIID: v.POIID}
	if v.Device != nil {
		visit.Fix = &Fix{
			Latitude:   v.Device.Latitude,
			Longitude:  v.Device.Longitude,
			AccuracyM:  v.Device.AccuracyM,
			ObservedAt: v.Device.ObservedAt,
		}
	}
	if c := v.NewCity; c != nil {
		visit.NewCityKey = c.ID.String()
		visit.CityName = c.CityName
		visit.CityLat = c.Latitude
		visit.CityLon = c.Longitude
	}
	return s.ScoreVisit(ctx, visit)
}

// ScoutCredited awards each user whose field report others just
// corroborated, keyed by that report (placeintel.ContributionScorer).
func (s *Service) ScoutCredited(ctx context.Context, credits map[uuid.UUID]uuid.UUID) {
	for u, claimID := range credits {
		s.AwardQuietly(ctx, Award{UserID: u, Kind: KindScoutClaim, RefKey: "claim:" + claimID.String(), Label: "Scout report confirmed"})
	}
}

// PlaceConfirmed awards the submitter and confirmers of a place that just
// went live.
func (s *Service) PlaceConfirmed(ctx context.Context, users []uuid.UUID, submissionID uuid.UUID, placeName string) {
	label := "Added a place"
	if placeName != "" {
		label = "Added " + placeName
	}
	for _, u := range users {
		s.AwardQuietly(ctx, Award{UserID: u, Kind: KindPlaceSubmission, RefKey: "submission:" + submissionID.String(), Label: label})
	}
}
