package handler

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestPaginationMeta(t *testing.T) {
	tests := []struct {
		name                  string
		page, pageSize, total int
		wantTotalPages        int32
		wantHasMore           bool
	}{
		{"empty", 1, 100, 0, 0, false},
		{"single partial page", 1, 100, 7, 1, false},
		{"exactly one page", 1, 100, 100, 1, false},
		{"more to come", 1, 100, 101, 2, true},
		{"last page", 2, 100, 101, 2, false},
		{"zero page size does not divide by zero", 1, 0, 5, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := paginationMeta(tt.page, tt.pageSize, tt.total)
			assert.Equal(t, int32(tt.total), got.TotalRecords)
			assert.Equal(t, int32(tt.page), got.Page)
			assert.Equal(t, int32(tt.pageSize), got.PageSize)
			assert.Equal(t, tt.wantTotalPages, got.TotalPages)
			assert.Equal(t, tt.wantHasMore, got.HasMore)
		})
	}
}

func TestItineraryToProto(t *testing.T) {
	id := uuid.New()
	userID := uuid.New()
	created := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)

	t.Run("minimal record", func(t *testing.T) {
		// What the app actually writes today: no session, no city, no content.
		got := itineraryToProto(&locitypes.UserSavedItinerary{
			ID:        id,
			UserID:    userID,
			Title:     "Three days in Porto",
			CreatedAt: created,
			UpdatedAt: created,
		})

		require.NotNil(t, got)
		assert.Equal(t, id.String(), got.Id)
		assert.Equal(t, userID.String(), got.UserId)
		assert.Equal(t, "Three days in Porto", got.Title)
		assert.Nil(t, got.SessionId)
		assert.Nil(t, got.PrimaryCityId)
		assert.Nil(t, got.Description)
		assert.Empty(t, got.MarkdownContent)
		assert.Equal(t, created.Unix(), got.CreatedAt.AsTime().Unix())
	})

	t.Run("an empty description stays unset", func(t *testing.T) {
		empty := ""
		got := itineraryToProto(&locitypes.UserSavedItinerary{
			ID: id, UserID: userID, Title: "t", Description: &empty,
			CreatedAt: created, UpdatedAt: created,
		})
		assert.Nil(t, got.Description, "an empty string must not become a set optional")
	})

	t.Run("full record", func(t *testing.T) {
		sessionID := uuid.New()
		cityID := uuid.New()
		interactionID := uuid.New()
		desc := "Coastal walk and port houses"
		days := int32(3)
		cost := int32(2)

		got := itineraryToProto(&locitypes.UserSavedItinerary{
			ID:                     id,
			UserID:                 userID,
			SourceLlmInteractionID: &interactionID,
			SessionID:              &sessionID,
			PrimaryCityID:          &cityID,
			Title:                  "Three days in Porto",
			Description:            &desc,
			MarkdownContent:        "# Day 1",
			Tags:                   []string{"food", "walking"},
			EstimatedDurationDays:  &days,
			EstimatedCostLevel:     &cost,
			IsPublic:               true,
			CreatedAt:              created,
			UpdatedAt:              created,
		})

		require.NotNil(t, got.SessionId)
		assert.Equal(t, sessionID.String(), *got.SessionId)
		require.NotNil(t, got.PrimaryCityId)
		assert.Equal(t, cityID.String(), *got.PrimaryCityId)
		require.NotNil(t, got.SourceLlmInteractionId)
		assert.Equal(t, interactionID.String(), *got.SourceLlmInteractionId)
		require.NotNil(t, got.Description)
		assert.Equal(t, desc, *got.Description)
		assert.Equal(t, []string{"food", "walking"}, got.Tags)
		require.NotNil(t, got.EstimatedDurationDays)
		assert.Equal(t, int32(3), *got.EstimatedDurationDays)
		assert.True(t, got.IsPublic)
	})
}
