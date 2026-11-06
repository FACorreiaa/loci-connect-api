package profiles

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func wheelchairNeeds() *locitypes.AccessibilityNeeds {
	return &locitypes.AccessibilityNeeds{
		Mobility:             locitypes.MobilityManualWheelchair,
		MaxWalkMinutesPerLeg: 10,
		StepFreeOnly:         true,
	}
}

func TestCreateSearchProfile_AccessibilityConsent(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("needs without consent are rejected before the repository", func(t *testing.T) {
		service, repo, _, _ := setupprofilessServiceTest()
		_, err := service.CreateSearchProfile(ctx, userID, locitypes.CreateUserPreferenceProfileParams{
			ProfileName:        "Lisbon",
			AccessibilityNeeds: wheelchairNeeds(),
		})
		require.ErrorIs(t, err, ErrAccessibilityConsentRequired)
		require.ErrorIs(t, err, locitypes.ErrBadRequest)
		repo.AssertNotCalled(t, "GetSearchProfiles", mock.Anything, mock.Anything)
		repo.AssertNotCalled(t, "CreateSearchProfile", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("needs with consent reach the repository", func(t *testing.T) {
		service, repo, _, _ := setupprofilessServiceTest()
		params := locitypes.CreateUserPreferenceProfileParams{
			ProfileName:               "Lisbon",
			AccessibilityNeeds:        wheelchairNeeds(),
			AccessibilityConsentGiven: true,
		}
		repo.On("GetSearchProfiles", mock.Anything, userID).Return([]locitypes.UserPreferenceProfileResponse{{}}, nil).Once()
		repo.On("CreateSearchProfile", mock.Anything, userID, params).Return(&locitypes.UserPreferenceProfileResponse{}, nil).Once()
		_, err := service.CreateSearchProfile(ctx, userID, params)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("empty needs need no consent", func(t *testing.T) {
		service, repo, _, _ := setupprofilessServiceTest()
		params := locitypes.CreateUserPreferenceProfileParams{
			ProfileName:        "Lisbon",
			AccessibilityNeeds: &locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityNone},
		}
		repo.On("GetSearchProfiles", mock.Anything, userID).Return([]locitypes.UserPreferenceProfileResponse{{}}, nil).Once()
		repo.On("CreateSearchProfile", mock.Anything, userID, params).Return(&locitypes.UserPreferenceProfileResponse{}, nil).Once()
		_, err := service.CreateSearchProfile(ctx, userID, params)
		require.NoError(t, err)
	})

	t.Run("out-of-range needs are rejected", func(t *testing.T) {
		service, _, _, _ := setupprofilessServiceTest()
		_, err := service.CreateSearchProfile(ctx, userID, locitypes.CreateUserPreferenceProfileParams{
			ProfileName:               "Lisbon",
			AccessibilityNeeds:        &locitypes.AccessibilityNeeds{MaxWalkMinutesPerLeg: 500},
			AccessibilityConsentGiven: true,
		})
		require.ErrorIs(t, err, locitypes.ErrBadRequest)
	})
}

func TestUpdateSearchProfile_AccessibilityConsent(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	profileID := uuid.New()
	consentAt := time.Now().Add(-time.Hour)

	t.Run("first save without consent is rejected", func(t *testing.T) {
		service, repo, _, _ := setupprofilessServiceTest()
		repo.On("GetSearchProfile", mock.Anything, userID, profileID).
			Return(&locitypes.UserPreferenceProfileResponse{ID: profileID}, nil).Once()
		err := service.UpdateSearchProfile(ctx, userID, profileID, locitypes.UpdateSearchProfileParams{
			AccessibilityNeeds: wheelchairNeeds(),
		})
		require.ErrorIs(t, err, ErrAccessibilityConsentRequired)
		repo.AssertNotCalled(t, "UpdateSearchProfile", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("edit of already-consented needs needs no fresh consent", func(t *testing.T) {
		service, repo, _, _ := setupprofilessServiceTest()
		existing := wheelchairNeeds()
		existing.ConsentAt = &consentAt
		params := locitypes.UpdateSearchProfileParams{AccessibilityNeeds: &locitypes.AccessibilityNeeds{
			Mobility: locitypes.MobilityPowerWheelchair,
		}}
		repo.On("GetSearchProfile", mock.Anything, userID, profileID).
			Return(&locitypes.UserPreferenceProfileResponse{ID: profileID, AccessibilityNeeds: existing}, nil).Once()
		repo.On("UpdateSearchProfile", mock.Anything, userID, profileID, params).Return(nil).Once()
		require.NoError(t, service.UpdateSearchProfile(ctx, userID, profileID, params))
		repo.AssertExpectations(t)
	})

	t.Run("fresh consent skips the lookup", func(t *testing.T) {
		service, repo, _, _ := setupprofilessServiceTest()
		params := locitypes.UpdateSearchProfileParams{
			AccessibilityNeeds:        wheelchairNeeds(),
			AccessibilityConsentGiven: true,
		}
		repo.On("UpdateSearchProfile", mock.Anything, userID, profileID, params).Return(nil).Once()
		require.NoError(t, service.UpdateSearchProfile(ctx, userID, profileID, params))
		repo.AssertNotCalled(t, "GetSearchProfile", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("clearing never needs consent", func(t *testing.T) {
		service, repo, _, _ := setupprofilessServiceTest()
		params := locitypes.UpdateSearchProfileParams{
			AccessibilityNeeds:      wheelchairNeeds(),
			ClearAccessibilityNeeds: true,
		}
		repo.On("UpdateSearchProfile", mock.Anything, userID, profileID, params).Return(nil).Once()
		require.NoError(t, service.UpdateSearchProfile(ctx, userID, profileID, params))
		repo.AssertNotCalled(t, "GetSearchProfile", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("lookup failure is surfaced", func(t *testing.T) {
		service, repo, _, _ := setupprofilessServiceTest()
		repo.On("GetSearchProfile", mock.Anything, userID, profileID).
			Return(nil, locitypes.ErrNotFound).Once()
		err := service.UpdateSearchProfile(ctx, userID, profileID, locitypes.UpdateSearchProfileParams{
			AccessibilityNeeds: wheelchairNeeds(),
		})
		require.ErrorIs(t, err, locitypes.ErrNotFound)
	})
}

func TestAccessibilityNeedsCodec(t *testing.T) {
	consentAt := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	raw, err := encodeAccessibilityNeeds(wheelchairNeeds())
	require.NoError(t, err)
	assert.JSONEq(t, `{"mobility":"manual_wheelchair","max_walk_minutes_per_leg":10,"step_free_only":true}`, string(raw))

	got := decodeAccessibilityNeeds(raw, &consentAt)
	require.NotNil(t, got)
	assert.Equal(t, locitypes.MobilityManualWheelchair, got.Mobility)
	require.NotNil(t, got.ConsentAt)
	assert.True(t, got.ConsentAt.Equal(consentAt))

	t.Run("inactive needs encode as NULL", func(t *testing.T) {
		raw, err := encodeAccessibilityNeeds(&locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityNone})
		require.NoError(t, err)
		assert.Nil(t, raw)
		raw, err = encodeAccessibilityNeeds(nil)
		require.NoError(t, err)
		assert.Nil(t, raw)
	})

	t.Run("needs without consent are never returned", func(t *testing.T) {
		assert.Nil(t, decodeAccessibilityNeeds(raw, nil))
	})

	t.Run("unreadable blob is dropped", func(t *testing.T) {
		assert.Nil(t, decodeAccessibilityNeeds([]byte(`{not json`), &consentAt))
	})
}
