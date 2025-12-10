package profiles

import (
	"context"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestGetSearchProfiles(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID1 := uuid.New()
	profileID2 := uuid.New()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, user_id, profile_name, is_default, search_radius_km, preferred_time,
               budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
               prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs,
               created_at, updated_at
        FROM user_preference_profiles
        WHERE user_id = $1
        ORDER BY is_default DESC, profile_name`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_name", "is_default", "search_radius_km",
				"preferred_time", "budget_level", "preferred_pace", "prefer_accessible_pois",
				"prefer_outdoor_seating", "prefer_dog_friendly", "preferred_vibes",
				"preferred_transport", "dietary_needs", "created_at", "updated_at",
			}).
				AddRow(profileID1, userID, "Default Profile", true, 5.0,
					"any", 2, "moderate", false, false, false,
					[]string{"cozy", "quiet"}, "walk", []string{"vegetarian"}, now, now).
				AddRow(profileID2, userID, "Adventure Profile", false, 10.0,
					"day", 3, "fast", true, true, true,
					[]string{"lively"}, "public", []string{}, now, now),
		)

	repo := NewPostgresUserRepo(mock, slog.Default())
	profiles, err := repo.GetSearchProfiles(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetSearchProfiles: %v", err)
	}

	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}

	if profiles[0].ProfileName != "Default Profile" {
		t.Errorf("expected first profile name 'Default Profile', got %s", profiles[0].ProfileName)
	}
	if !profiles[0].IsDefault {
		t.Error("expected first profile to be default")
	}
	if profiles[1].ProfileName != "Adventure Profile" {
		t.Errorf("expected second profile name 'Adventure Profile', got %s", profiles[1].ProfileName)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetSearchProfilesEmpty(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, user_id, profile_name, is_default, search_radius_km, preferred_time,
               budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
               prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs,
               created_at, updated_at
        FROM user_preference_profiles
        WHERE user_id = $1
        ORDER BY is_default DESC, profile_name`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_name", "is_default", "search_radius_km",
				"preferred_time", "budget_level", "preferred_pace", "prefer_accessible_pois",
				"prefer_outdoor_seating", "prefer_dog_friendly", "preferred_vibes",
				"preferred_transport", "dietary_needs", "created_at", "updated_at",
			}),
		)

	repo := NewPostgresUserRepo(mock, slog.Default())
	profiles, err := repo.GetSearchProfiles(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetSearchProfiles: %v", err)
	}

	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(profiles))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetSearchProfile(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, user_id, profile_name, is_default, search_radius_km, preferred_time,
               budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
               prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs,
               created_at, updated_at
        FROM user_preference_profiles
        WHERE id = $1 AND user_id = $2`)).
		WithArgs(profileID, userID).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_name", "is_default", "search_radius_km",
				"preferred_time", "budget_level", "preferred_pace", "prefer_accessible_pois",
				"prefer_outdoor_seating", "prefer_dog_friendly", "preferred_vibes",
				"preferred_transport", "dietary_needs", "created_at", "updated_at",
			}).
				AddRow(profileID, userID, "Test Profile", true, 5.0,
					"night", 1, "relaxed", false, true, false,
					[]string{"romantic"}, "car", []string{"gluten_free"}, now, now),
		)

	repo := NewPostgresUserRepo(mock, slog.Default())
	profile, err := repo.GetSearchProfile(context.Background(), userID, profileID)
	if err != nil {
		t.Fatalf("GetSearchProfile: %v", err)
	}

	if profile.ID != profileID {
		t.Errorf("expected profile ID %s, got %s", profileID, profile.ID)
	}
	if profile.ProfileName != "Test Profile" {
		t.Errorf("expected profile name 'Test Profile', got %s", profile.ProfileName)
	}
	if profile.PreferredTime != locitypes.DayPreferenceNight {
		t.Errorf("expected preferred time 'night', got %s", profile.PreferredTime)
	}
	if profile.PreferredPace != locitypes.SearchPaceRelaxed {
		t.Errorf("expected preferred pace 'relaxed', got %s", profile.PreferredPace)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetSearchProfileNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, user_id, profile_name, is_default, search_radius_km, preferred_time,
               budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
               prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs,
               created_at, updated_at
        FROM user_preference_profiles
        WHERE id = $1 AND user_id = $2`)).
		WithArgs(profileID, userID).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_name", "is_default", "search_radius_km",
				"preferred_time", "budget_level", "preferred_pace", "prefer_accessible_pois",
				"prefer_outdoor_seating", "prefer_dog_friendly", "preferred_vibes",
				"preferred_transport", "dietary_needs", "created_at", "updated_at",
			}),
		)

	repo := NewPostgresUserRepo(mock, slog.Default())
	_, err = repo.GetSearchProfile(context.Background(), userID, profileID)
	if err == nil {
		t.Fatal("expected error for not found profile")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetDefaultSearchProfile(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, user_id, profile_name, is_default, search_radius_km, preferred_time,
               budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
               prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs,
               created_at, updated_at
        FROM user_preference_profiles
        WHERE user_id = $1 AND is_default = TRUE`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_name", "is_default", "search_radius_km",
				"preferred_time", "budget_level", "preferred_pace", "prefer_accessible_pois",
				"prefer_outdoor_seating", "prefer_dog_friendly", "preferred_vibes",
				"preferred_transport", "dietary_needs", "created_at", "updated_at",
			}).
				AddRow(profileID, userID, "Default Profile", true, 5.0,
					"any", 2, "moderate", false, false, false,
					[]string{}, "any", []string{}, now, now),
		)

	repo := NewPostgresUserRepo(mock, slog.Default())
	profile, err := repo.GetDefaultSearchProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetDefaultSearchProfile: %v", err)
	}

	if !profile.IsDefault {
		t.Error("expected profile to be default")
	}
	if profile.ProfileName != "Default Profile" {
		t.Errorf("expected profile name 'Default Profile', got %s", profile.ProfileName)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetDefaultSearchProfileNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id, user_id, profile_name, is_default, search_radius_km, preferred_time,
               budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
               prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs,
               created_at, updated_at
        FROM user_preference_profiles
        WHERE user_id = $1 AND is_default = TRUE`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_name", "is_default", "search_radius_km",
				"preferred_time", "budget_level", "preferred_pace", "prefer_accessible_pois",
				"prefer_outdoor_seating", "prefer_dog_friendly", "preferred_vibes",
				"preferred_transport", "dietary_needs", "created_at", "updated_at",
			}),
		)

	repo := NewPostgresUserRepo(mock, slog.Default())
	_, err = repo.GetDefaultSearchProfile(context.Background(), userID)
	if err == nil {
		t.Fatal("expected error for not found default profile")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCreateSearchProfile(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
        INSERT INTO user_preference_profiles (
            user_id, profile_name, is_default, search_radius_km, preferred_time,
            budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
            prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
        ) RETURNING id, user_id, profile_name, is_default, search_radius_km, preferred_time,
                   budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
                   prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs,
                   created_at, updated_at`)).
		WithArgs(
			userID, "New Profile", false, 5.0, locitypes.DayPreferenceAny,
			0, locitypes.SearchPaceAny, false, false, false,
			[]string{}, locitypes.TransportPreferenceAny, []string{},
		).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_name", "is_default", "search_radius_km",
				"preferred_time", "budget_level", "preferred_pace", "prefer_accessible_pois",
				"prefer_outdoor_seating", "prefer_dog_friendly", "preferred_vibes",
				"preferred_transport", "dietary_needs", "created_at", "updated_at",
			}).
				AddRow(profileID, userID, "New Profile", false, 5.0,
					"any", 0, "any", false, false, false,
					[]string{}, "any", []string{}, now, now),
		)
	mock.ExpectCommit()

	repo := NewPostgresUserRepo(mock, slog.Default())
	params := locitypes.CreateUserPreferenceProfileParams{
		ProfileName: "New Profile",
	}

	profile, err := repo.CreateSearchProfile(context.Background(), userID, params)
	if err != nil {
		t.Fatalf("CreateSearchProfile: %v", err)
	}

	if profile.ID != profileID {
		t.Errorf("expected profile ID %s, got %s", profileID, profile.ID)
	}
	if profile.ProfileName != "New Profile" {
		t.Errorf("expected profile name 'New Profile', got %s", profile.ProfileName)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCreateSearchProfileWithDefaultFlag(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()
	now := time.Now()
	isDefault := true

	mock.ExpectBegin()
	// Expect reset of existing defaults
	mock.ExpectExec(regexp.QuoteMeta("UPDATE user_preference_profiles SET is_default = FALSE WHERE user_id = $1 AND id != $2")).
		WithArgs(userID, uuid.Nil).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	mock.ExpectQuery(regexp.QuoteMeta(`
        INSERT INTO user_preference_profiles (
            user_id, profile_name, is_default, search_radius_km, preferred_time,
            budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
            prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
        ) RETURNING id, user_id, profile_name, is_default, search_radius_km, preferred_time,
                   budget_level, preferred_pace, prefer_accessible_pois, prefer_outdoor_seating,
                   prefer_dog_friendly, preferred_vibes, preferred_transport, dietary_needs,
                   created_at, updated_at`)).
		WithArgs(
			userID, "Default Profile", true, 5.0, locitypes.DayPreferenceAny,
			0, locitypes.SearchPaceAny, false, false, false,
			[]string{}, locitypes.TransportPreferenceAny, []string{},
		).
		WillReturnRows(
			pgxmock.NewRows([]string{
				"id", "user_id", "profile_name", "is_default", "search_radius_km",
				"preferred_time", "budget_level", "preferred_pace", "prefer_accessible_pois",
				"prefer_outdoor_seating", "prefer_dog_friendly", "preferred_vibes",
				"preferred_transport", "dietary_needs", "created_at", "updated_at",
			}).
				AddRow(profileID, userID, "Default Profile", true, 5.0,
					"any", 0, "any", false, false, false,
					[]string{}, "any", []string{}, now, now),
		)
	mock.ExpectCommit()

	repo := NewPostgresUserRepo(mock, slog.Default())
	params := locitypes.CreateUserPreferenceProfileParams{
		ProfileName: "Default Profile",
		IsDefault:   &isDefault,
	}

	profile, err := repo.CreateSearchProfile(context.Background(), userID, params)
	if err != nil {
		t.Fatalf("CreateSearchProfile: %v", err)
	}

	if !profile.IsDefault {
		t.Error("expected profile to be default")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestDeleteSearchProfile(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()

	// Check if profile is default
	mock.ExpectQuery(regexp.QuoteMeta("SELECT is_default FROM user_preference_profiles WHERE id = $1 AND user_id = $2")).
		WithArgs(profileID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"is_default"}).AddRow(false))

	// Delete profile
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_preference_profiles WHERE id = $1 AND user_id = $2")).
		WithArgs(profileID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	repo := NewPostgresUserRepo(mock, slog.Default())
	err = repo.DeleteSearchProfile(context.Background(), userID, profileID)
	if err != nil {
		t.Fatalf("DeleteSearchProfile: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestDeleteSearchProfileNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()

	// Check if profile is default - not found
	mock.ExpectQuery(regexp.QuoteMeta("SELECT is_default FROM user_preference_profiles WHERE id = $1 AND user_id = $2")).
		WithArgs(profileID, userID).
		WillReturnError(pgx.ErrNoRows)

	repo := NewPostgresUserRepo(mock, slog.Default())
	err = repo.DeleteSearchProfile(context.Background(), userID, profileID)
	if err == nil {
		t.Fatal("expected error for not found profile")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestDeleteSearchProfileCannotDeleteDefault(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()

	// Check if profile is default - yes it is
	mock.ExpectQuery(regexp.QuoteMeta("SELECT is_default FROM user_preference_profiles WHERE id = $1 AND user_id = $2")).
		WithArgs(profileID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"is_default"}).AddRow(true))

	repo := NewPostgresUserRepo(mock, slog.Default())
	err = repo.DeleteSearchProfile(context.Background(), userID, profileID)
	if err == nil {
		t.Fatal("expected error when trying to delete default profile")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSetDefaultSearchProfile(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()

	// Query to check user exists
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id FROM user_preference_profiles WHERE user_id = $1")).
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(userID))

	mock.ExpectBegin()
	// Reset all defaults for user
	mock.ExpectExec(regexp.QuoteMeta("UPDATE user_preference_profiles SET is_default = FALSE WHERE user_id = $1")).
		WithArgs(userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	// Set new default
	mock.ExpectExec(regexp.QuoteMeta("UPDATE user_preference_profiles SET is_default = TRUE WHERE id = $1 AND user_id = $2")).
		WithArgs(profileID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	mock.ExpectCommit()

	repo := NewPostgresUserRepo(mock, slog.Default())
	err = repo.SetDefaultSearchProfile(context.Background(), userID, profileID)
	if err != nil {
		t.Fatalf("SetDefaultSearchProfile: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSetDefaultSearchProfileNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	profileID := uuid.New()

	// Query to check user exists
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id FROM user_preference_profiles WHERE user_id = $1")).
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(userID))

	mock.ExpectBegin()
	// Reset all defaults for user
	mock.ExpectExec(regexp.QuoteMeta("UPDATE user_preference_profiles SET is_default = FALSE WHERE user_id = $1")).
		WithArgs(userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	// Set new default - profile not found
	mock.ExpectExec(regexp.QuoteMeta("UPDATE user_preference_profiles SET is_default = TRUE WHERE id = $1 AND user_id = $2")).
		WithArgs(profileID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	mock.ExpectRollback()

	repo := NewPostgresUserRepo(mock, slog.Default())
	err = repo.SetDefaultSearchProfile(context.Background(), userID, profileID)
	if err == nil {
		t.Fatal("expected error for not found profile")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
