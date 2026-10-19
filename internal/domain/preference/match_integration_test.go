//go:build integration

package preference

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

// personalizedUser creates a user outside the control holdout, which never
// gets a match.
func personalizedUser(ctx context.Context, t *testing.T) uuid.UUID {
	t.Helper()
	for {
		id := uuid.New()
		if ExperimentVariant(id) == "control" {
			continue
		}
		_, err := testsupport.MustPool().Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)",
			id, "match-"+uuid.NewString()+"@loci.test")
		require.NoError(t, err)
		return id
	}
}

func insertCategorizedPOI(ctx context.Context, t *testing.T, name, category string, axis int) uuid.UUID {
	t.Helper()
	embedding := any(nil)
	if axis >= 0 {
		embedding = unitVector(axis)
	}
	var id uuid.UUID
	require.NoError(t, testsupport.MustPool().QueryRow(ctx, `
		INSERT INTO points_of_interest (name, category, location, embedding)
		VALUES ($1, $2, ST_SetSRID(ST_MakePoint(-9.14, 38.71), 4326), $3::vector)
		RETURNING id`, fmt.Sprintf("%s-%s", name, uuid.NewString()), category, embedding).Scan(&id))
	return id
}

type matchFixture struct {
	user        uuid.UUID
	liked, a, b uuid.UUID
	likedName   string
	scorer      *MatchScorer
}

// newMatchFixture: a traveller whose taste points along axis 0, who favourited
// a café on axis 0 and has a strong café trait. A is a café on axis 0, B a
// place on axis 1.
func newMatchFixture(ctx context.Context, t *testing.T, feedbackCount int, calibrated bool) matchFixture {
	t.Helper()
	pool := testsupport.MustPool()
	f := matchFixture{user: personalizedUser(ctx, t)}
	f.liked = insertCategorizedPOI(ctx, t, "Liked", "cafe", 0)
	require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM points_of_interest WHERE id = $1`, f.liked).Scan(&f.likedName))
	f.a = insertCategorizedPOI(ctx, t, "A", "cafe", 0)
	f.b = insertCategorizedPOI(ctx, t, "B", "museum", 1)

	_, err := pool.Exec(ctx, `
		INSERT INTO preference_feedback (user_id, poi_id, event, weight) VALUES ($1, $2, 'favorited', 1)`,
		f.user, f.liked.String())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO user_taste_traits (user_id, trait_key, label, score, confidence, evidence_count)
		VALUES ($1, 'cafe', 'Cafe', 0.8, 0.6, 4)`, f.user)
	require.NoError(t, err)

	p10, p90 := any(nil), any(nil)
	if calibrated {
		p10, p90 = 0.0, 1.0
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO user_preference_vectors (user_id, embedding, feedback_count, match_p10, match_p90)
		VALUES ($1, $2::vector, $3, $4, $5)`, f.user, unitVector(0), feedbackCount, p10, p90)
	require.NoError(t, err)

	f.scorer = NewMatchScorer(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

func TestScorePOIs_ScoresAndExplains(t *testing.T) {
	ctx := context.Background()
	f := newMatchFixture(ctx, t, 6, true)

	got, err := f.scorer.ScorePOIs(ctx, f.user, []string{f.a.String(), f.b.String(), "not-a-uuid"})
	require.NoError(t, err)

	a, ok := got[f.a.String()]
	require.True(t, ok, "A should be scored")
	require.Greater(t, a.Score, float32(0.9))
	require.Contains(t, a.Reasons, "You tend to like cafe")
	require.Contains(t, a.Reasons, "Similar to "+f.likedName+", one of your picks")
	require.LessOrEqual(t, len(a.Reasons), 3)

	b, ok := got[f.b.String()]
	require.True(t, ok, "B should be scored")
	require.Less(t, b.Score, float32(0.1))
	for _, r := range b.Reasons {
		require.False(t, strings.HasPrefix(r, "Similar to"), "B is nothing like the liked café: %q", r)
	}
}

func TestScorePOIs_RankedPlaceSaysSoFirst(t *testing.T) {
	ctx := context.Background()
	f := newMatchFixture(ctx, t, 6, true)
	_, err := testsupport.MustPool().Exec(ctx, `
		INSERT INTO user_poi_rankings (user_id, poi_id, poi_name, city_key, city_name, verdict, position)
		VALUES ($1, $2, 'A', 'lisbon', 'Lisbon', 1, 0)`, f.user, f.a.String())
	require.NoError(t, err)

	got, err := f.scorer.ScorePOIs(ctx, f.user, []string{f.a.String()})
	require.NoError(t, err)
	require.Equal(t, "You rated it Loved", got[f.a.String()].Reasons[0])
}

func TestScorePOIs_UncalibratedReturnsNothing(t *testing.T) {
	ctx := context.Background()
	f := newMatchFixture(ctx, t, 6, false)
	got, err := f.scorer.ScorePOIs(ctx, f.user, []string{f.a.String()})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestScorePOIs_TooFewSignals(t *testing.T) {
	ctx := context.Background()
	f := newMatchFixture(ctx, t, 4, true)
	got, err := f.scorer.ScorePOIs(ctx, f.user, []string{f.a.String()})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestScorePOIs_SkipsMissingEmbeddings(t *testing.T) {
	ctx := context.Background()
	f := newMatchFixture(ctx, t, 6, true)
	bare := insertCategorizedPOI(ctx, t, "Bare", "cafe", -1)
	got, err := f.scorer.ScorePOIs(ctx, f.user, []string{f.a.String(), bare.String()})
	require.NoError(t, err)
	require.Contains(t, got, f.a.String())
	require.NotContains(t, got, bare.String())
}

func TestScorePOIs_PersonalizationOff(t *testing.T) {
	ctx := context.Background()
	f := newMatchFixture(ctx, t, 6, true)
	_, err := testsupport.MustPool().Exec(ctx, `
		INSERT INTO personalization_settings (user_id, personalization_enabled) VALUES ($1, FALSE)`, f.user)
	require.NoError(t, err)
	got, err := f.scorer.ScorePOIs(ctx, f.user, []string{f.a.String()})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestRecomputeUser_Calibrates(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()
	user := personalizedUser(ctx, t)
	liked := insertCategorizedPOI(ctx, t, "Liked", "cafe", 0)
	insertCategorizedPOI(ctx, t, "Other", "museum", 1)
	_, err := pool.Exec(ctx, `
		INSERT INTO preference_feedback (user_id, poi_id, event, weight) VALUES ($1, $2, 'favorited', 1)`,
		user, liked.String())
	require.NoError(t, err)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	require.NoError(t, NewReranker(pool, NewVectorStore(pool, log), log).RecomputeUser(ctx, user))

	var p10, p90 *float32
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT match_p10, match_p90 FROM user_preference_vectors WHERE user_id = $1`, user).Scan(&p10, &p90))
	require.NotNil(t, p10)
	require.NotNil(t, p90)
	require.Less(t, *p10, *p90)
}
