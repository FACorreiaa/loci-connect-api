package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// loadDomainPreferences reads the four per-domain preference blobs for a profile
// and attaches them to the response.
//
// These four tables (migration 0018) have been written since they were created
// and never read: every reference in the codebase was an INSERT ... ON CONFLICT
// DO UPDATE. Meanwhile getUserPreferencesPrompt carries a rendering branch for
// each one, all four unreachable because nothing ever populated the fields. The
// user was answering a preferences questionnaire whose answers reached the
// database and stopped there.
//
// One query with four LEFT JOINs rather than four round trips; a profile with no
// row in a given table simply gets a nil pointer, which is what the renderer
// already checks for.
//
// Best-effort by design: a malformed blob for one domain must not cost the
// caller their whole profile, so unmarshal failures are skipped rather than
// returned. A read failure is returned, because that means the database is
// unhealthy rather than one row being odd.
func (r *RepositoryImpl) loadDomainPreferences(
	ctx context.Context,
	profileID uuid.UUID,
	response *locitypes.UserPreferenceProfileResponse,
) error {
	const query = `
		SELECT
			a.accommodation_filters,
			d.dining_filters,
			ac.activity_filters,
			i.itinerary_filters
		FROM user_preference_profiles p
		LEFT JOIN user_accommodation_preferences a ON a.user_preference_profile_id = p.id
		LEFT JOIN user_dining_preferences        d ON d.user_preference_profile_id = p.id
		LEFT JOIN user_activity_preferences     ac ON ac.user_preference_profile_id = p.id
		LEFT JOIN user_itinerary_preferences     i ON i.user_preference_profile_id = p.id
		WHERE p.id = $1`

	var accommodation, dining, activity, itinerary []byte
	if err := r.pgpool.QueryRow(ctx, query, profileID).
		Scan(&accommodation, &dining, &activity, &itinerary); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The profile itself is gone; the caller already has what it read.
			return nil
		}
		return fmt.Errorf("read domain preferences: %w", err)
	}

	decode(ctx, r, "accommodation", accommodation, &response.AccommodationPreferences)
	decode(ctx, r, "dining", dining, &response.DiningPreferences)
	decode(ctx, r, "activity", activity, &response.ActivityPreferences)
	decode(ctx, r, "itinerary", itinerary, &response.ItineraryPreferences)

	return nil
}

// decode unmarshals one preference blob into target, leaving target untouched
// when the column was NULL or the stored JSON no longer matches the struct.
func decode[T any](ctx context.Context, r *RepositoryImpl, domain string, raw []byte, target **T) {
	if len(raw) == 0 {
		return
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		r.logger.WarnContext(ctx, "skipping unreadable domain preferences",
			slog.String("domain", domain), slog.Any("error", err))
		return
	}
	*target = &value
}

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
