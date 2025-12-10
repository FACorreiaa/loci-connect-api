package profiles

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Transaction helper methods for updating domain preferences

func (r *RepositoryImpl) updateAccommodationPreferencesInTx(ctx context.Context, tx pgx.Tx, profileID uuid.UUID, prefs *locitypes.AccommodationPreferences) error {
	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("failed to marshal accommodation preferences: %w", err)
	}

	query := `
        INSERT INTO user_accommodation_preferences (user_preference_profile_id, accommodation_filters)
        VALUES ($1, $2)
        ON CONFLICT (user_preference_profile_id) DO UPDATE SET accommodation_filters = $2, updated_at = NOW()`
	_, err = tx.Exec(ctx, query, profileID, prefsJSON)
	return err
}

func (r *RepositoryImpl) updateDiningPreferencesInTx(ctx context.Context, tx pgx.Tx, profileID uuid.UUID, prefs *locitypes.DiningPreferences) error {
	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("failed to marshal dining preferences: %w", err)
	}

	query := `
        INSERT INTO user_dining_preferences (user_preference_profile_id, dining_filters)
        VALUES ($1, $2)
        ON CONFLICT (user_preference_profile_id) DO UPDATE SET dining_filters = $2, updated_at = NOW()`
	_, err = tx.Exec(ctx, query, profileID, prefsJSON)
	return err
}

func (r *RepositoryImpl) updateActivityPreferencesInTx(ctx context.Context, tx pgx.Tx, profileID uuid.UUID, prefs *locitypes.ActivityPreferences) error {
	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("failed to marshal activity preferences: %w", err)
	}

	query := `
        INSERT INTO user_activity_preferences (user_preference_profile_id, activity_filters)
        VALUES ($1, $2)
        ON CONFLICT (user_preference_profile_id) DO UPDATE SET activity_filters = $2, updated_at = NOW()`
	_, err = tx.Exec(ctx, query, profileID, prefsJSON)
	return err
}

func (r *RepositoryImpl) updateItineraryPreferencesInTx(ctx context.Context, tx pgx.Tx, profileID uuid.UUID, prefs *locitypes.ItineraryPreferences) error {
	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("failed to marshal itinerary preferences: %w", err)
	}

	query := `
        INSERT INTO user_itinerary_preferences (user_preference_profile_id, itinerary_filters)
        VALUES ($1, $2)
        ON CONFLICT (user_preference_profile_id) DO UPDATE SET itinerary_filters = $2, updated_at = NOW()`
	_, err = tx.Exec(ctx, query, profileID, prefsJSON)
	return err
}
