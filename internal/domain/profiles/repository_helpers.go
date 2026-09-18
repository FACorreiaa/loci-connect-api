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

// rollback aborts tx and returns cause. The rollback error is logged rather than
// returned: the caller needs to know why the write failed, not why the cleanup
// after the failure also failed.
func rollback(ctx context.Context, l *slog.Logger, tx pgx.Tx, cause error) error {
	if err := tx.Rollback(ctx); err != nil {
		l.ErrorContext(ctx, "Failed to rollback transaction", slog.Any("error", err))
	}
	return cause
}

// replaceProfileInterestsInTx makes the profile's interest links exactly the
// given set. Create and update share it, so saving a profile with an interest
// removed actually removes it -- appending would have made deselection
// impossible.
//
// A nil slice means "not supplied" and leaves the existing links alone; an empty
// (non-nil) slice clears them.
func replaceProfileInterestsInTx(ctx context.Context, tx pgx.Tx, profileID uuid.UUID, interestIDs []uuid.UUID) error {
	if interestIDs == nil {
		return nil
	}

	if _, err := tx.Exec(ctx, `DELETE FROM user_profile_interests WHERE profile_id = $1`, profileID); err != nil {
		return fmt.Errorf("clear profile interests: %w", err)
	}
	if len(interestIDs) == 0 {
		return nil
	}

	// preference_level 1 matches AddInterestToProfile's default; the column is
	// not yet exposed on the profile RPCs.
	const link = `
        INSERT INTO user_profile_interests (profile_id, interest_id, preference_level)
        SELECT $1, id, 1 FROM interests WHERE id = ANY($2)
        ON CONFLICT DO NOTHING`
	tag, err := tx.Exec(ctx, link, profileID, interestIDs)
	if err != nil {
		return fmt.Errorf("link interests to profile: %w", err)
	}
	if int(tag.RowsAffected()) != len(interestIDs) {
		return fmt.Errorf("%d of %d interest ids do not exist: %w",
			len(interestIDs)-int(tag.RowsAffected()), len(interestIDs), locitypes.ErrNotFound)
	}
	return nil
}

// replaceProfileTagsInTx makes the profile's tag links exactly the given set.
//
// Only personal tags can be scoped to a profile: user_personal_tags carries a
// single nullable profile_id, and global_tags has no profile column at all. Ids
// that are not one of this user's personal tags are reported rather than
// skipped -- silently dropping part of a saved selection is the bug this whole
// change exists to remove. The caller's picker is filtered to match.
//
// A nil slice means "not supplied"; an empty (non-nil) slice unlinks everything.
func replaceProfileTagsInTx(ctx context.Context, tx pgx.Tx, userID, profileID uuid.UUID, tagIDs []uuid.UUID) error {
	if tagIDs == nil {
		return nil
	}

	const unlink = `
        UPDATE user_personal_tags SET profile_id = NULL, updated_at = NOW()
        WHERE profile_id = $1 AND user_id = $2`
	if _, err := tx.Exec(ctx, unlink, profileID, userID); err != nil {
		return fmt.Errorf("clear profile tags: %w", err)
	}
	if len(tagIDs) == 0 {
		return nil
	}

	const link = `
        UPDATE user_personal_tags SET profile_id = $1, updated_at = NOW()
        WHERE id = ANY($2) AND user_id = $3`
	tag, err := tx.Exec(ctx, link, profileID, tagIDs, userID)
	if err != nil {
		return fmt.Errorf("link tags to profile: %w", err)
	}
	if int(tag.RowsAffected()) != len(tagIDs) {
		return fmt.Errorf("%d of %d tag ids are not personal tags belonging to this user: %w",
			len(tagIDs)-int(tag.RowsAffected()), len(tagIDs), locitypes.ErrNotFound)
	}
	return nil
}

// loadAssociations fetches the interest and tag links for a set of profiles in
// two queries rather than two per profile, and returns them keyed by profile id.
//
// Neither was ever read back on the profile RPCs: ToProtoProfile emitted no
// interests or tags and no read query joined them, so a saved profile always
// came back with the chips the user had picked missing.
func (r *RepositoryImpl) loadAssociations(
	ctx context.Context,
	profileIDs []uuid.UUID,
) (map[uuid.UUID][]*locitypes.Interest, map[uuid.UUID][]*locitypes.Tags, error) {
	interests := make(map[uuid.UUID][]*locitypes.Interest, len(profileIDs))
	tags := make(map[uuid.UUID][]*locitypes.Tags, len(profileIDs))
	if len(profileIDs) == 0 {
		return interests, tags, nil
	}

	const interestQuery = `
        SELECT upi.profile_id, i.id, i.name, i.description, i.active, i.created_at, i.updated_at
        FROM user_profile_interests upi
        JOIN interests i ON i.id = upi.interest_id
        WHERE upi.profile_id = ANY($1)
        ORDER BY i.name`

	rows, err := r.pgpool.Query(ctx, interestQuery, profileIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("read profile interests: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var profileID uuid.UUID
		var i locitypes.Interest
		if err := rows.Scan(&profileID, &i.ID, &i.Name, &i.Description, &i.Active, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan profile interest: %w", err)
		}
		i.Source = "global"
		interests[profileID] = append(interests[profileID], &i)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read profile interests: %w", err)
	}

	// Straight off user_personal_tags. GetTagsForProfile joins global_tags to
	// user_personal_tags on id, which are separate id spaces and so match
	// nothing -- that is why the prompt's "Tags to Avoid" branch never fired.
	const tagQuery = `
        SELECT profile_id, id, name, tag_type, description, active, created_at, updated_at
        FROM user_personal_tags
        WHERE profile_id = ANY($1)
        ORDER BY name`

	tagRows, err := r.pgpool.Query(ctx, tagQuery, profileIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("read profile tags: %w", err)
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var profileID uuid.UUID
		var t locitypes.Tags
		if err := tagRows.Scan(&profileID, &t.ID, &t.Name, &t.TagType, &t.Description, &t.Active, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan profile tag: %w", err)
		}
		source := "personal"
		t.Source = &source
		tags[profileID] = append(tags[profileID], &t)
	}
	if err := tagRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read profile tags: %w", err)
	}

	return interests, tags, nil
}

// attachAssociations fills in the interests and tags for profiles already read.
func (r *RepositoryImpl) attachAssociations(ctx context.Context, profiles []*locitypes.UserPreferenceProfileResponse) error {
	if len(profiles) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(profiles))
	for _, p := range profiles {
		ids = append(ids, p.ID)
	}
	interests, tags, err := r.loadAssociations(ctx, ids)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		p.Interests = interests[p.ID]
		p.Tags = tags[p.ID]
	}
	return nil
}

// loadDomainPreferencesBatch is loadDomainPreferences for many profiles at once.
// The list endpoint is the only read the client actually calls, so doing this
// per row would be an N+1 on the hot path.
func (r *RepositoryImpl) loadDomainPreferencesBatch(ctx context.Context, profiles []*locitypes.UserPreferenceProfileResponse) error {
	if len(profiles) == 0 {
		return nil
	}
	byID := make(map[uuid.UUID]*locitypes.UserPreferenceProfileResponse, len(profiles))
	ids := make([]uuid.UUID, 0, len(profiles))
	for _, p := range profiles {
		byID[p.ID] = p
		ids = append(ids, p.ID)
	}

	const query = `
		SELECT
			p.id,
			a.accommodation_filters,
			d.dining_filters,
			ac.activity_filters,
			i.itinerary_filters
		FROM user_preference_profiles p
		LEFT JOIN user_accommodation_preferences a ON a.user_preference_profile_id = p.id
		LEFT JOIN user_dining_preferences        d ON d.user_preference_profile_id = p.id
		LEFT JOIN user_activity_preferences     ac ON ac.user_preference_profile_id = p.id
		LEFT JOIN user_itinerary_preferences     i ON i.user_preference_profile_id = p.id
		WHERE p.id = ANY($1)`

	rows, err := r.pgpool.Query(ctx, query, ids)
	if err != nil {
		return fmt.Errorf("read domain preferences: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var accommodation, dining, activity, itinerary []byte
		if err := rows.Scan(&id, &accommodation, &dining, &activity, &itinerary); err != nil {
			return fmt.Errorf("scan domain preferences: %w", err)
		}
		p, ok := byID[id]
		if !ok {
			continue
		}
		decode(ctx, r, "accommodation", accommodation, &p.AccommodationPreferences)
		decode(ctx, r, "dining", dining, &p.DiningPreferences)
		decode(ctx, r, "activity", activity, &p.ActivityPreferences)
		decode(ctx, r, "itinerary", itinerary, &p.ItineraryPreferences)
	}
	return rows.Err()
}
