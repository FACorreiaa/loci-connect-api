//go:build integration

package retrieval

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

// treatmentUser returns a fresh user outside the personalization holdout.
func treatmentUser(ctx context.Context, t *testing.T, wantControl bool) uuid.UUID {
	t.Helper()
	for {
		id := uuid.New()
		if (preference.ExperimentVariant(id) == "control") != wantControl {
			continue
		}
		_, err := testsupport.MustPool().Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)",
			id, "history-"+uuid.NewString()+"@loci.test")
		require.NoError(t, err)
		return id
	}
}

func seedHistory(ctx context.Context, t *testing.T, userID uuid.UUID) {
	t.Helper()
	pool := testsupport.MustPool()
	for i := range 7 {
		_, err := pool.Exec(ctx, `INSERT INTO trips (user_id, city_name, title) VALUES ($1, $2, $3)`,
			userID, fmt.Sprintf("City %d", i), fmt.Sprintf("Trip %d", i))
		require.NoError(t, err)
	}
	for i := range 10 {
		_, err := pool.Exec(ctx, `
			INSERT INTO user_favorites (user_id, item_id, item_name, content_type, city_name)
			VALUES ($1, $2, $3, 'poi', 'Lisbon')`, userID, uuid.NewString(), fmt.Sprintf("Lisbon spot %d", i))
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO user_favorites (user_id, item_id, item_name, content_type, city_name)
		VALUES ($1, $2, 'Porto spot', 'poi', 'Porto')`, userID, uuid.NewString())
	require.NoError(t, err)
	for i := range 6 {
		_, err := pool.Exec(ctx, `INSERT INTO lists (user_id, name) VALUES ($1, $2)`,
			userID, fmt.Sprintf("List %d", i))
		require.NoError(t, err)
	}
}

func newHistoryAssembler() *Assembler {
	return NewAssembler(testsupport.MustPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestUserHistory_CapsAndFiltersByCity(t *testing.T) {
	ctx := context.Background()
	user := treatmentUser(ctx, t, false)
	seedHistory(ctx, t, user)

	h := newHistoryAssembler().userHistory(ctx, user, "lisbon")
	require.NotNil(t, h)
	require.Len(t, h.PastTrips, MaxHistoryTrips)
	require.Len(t, h.SavedHere, MaxHistorySaved)
	for _, s := range h.SavedHere {
		require.NotEqual(t, "Porto spot", s, "saves from other cities must not appear")
	}
	require.Len(t, h.Lists, MaxHistoryLists)
}

func TestUserHistory_NoneWhenPersonalizationOff(t *testing.T) {
	ctx := context.Background()
	user := treatmentUser(ctx, t, false)
	seedHistory(ctx, t, user)
	_, err := testsupport.MustPool().Exec(ctx,
		`INSERT INTO personalization_settings (user_id, personalization_enabled) VALUES ($1, FALSE)`, user)
	require.NoError(t, err)

	require.Nil(t, newHistoryAssembler().userHistory(ctx, user, "Lisbon"))
}

func TestUserHistory_NoneForControlGroup(t *testing.T) {
	ctx := context.Background()
	user := treatmentUser(ctx, t, true)
	seedHistory(ctx, t, user)

	require.Nil(t, newHistoryAssembler().userHistory(ctx, user, "Lisbon"))
}

func TestUserHistory_NoneForAnonymous(t *testing.T) {
	require.Nil(t, newHistoryAssembler().userHistory(context.Background(), uuid.Nil, "Lisbon"))
}
