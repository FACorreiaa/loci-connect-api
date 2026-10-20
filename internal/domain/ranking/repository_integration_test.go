//go:build integration

package ranking

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

type noKick struct{}

func (noKick) Kick(uuid.UUID) {}

func newRankingService(t *testing.T) (*Service, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.MustPool()
	user := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)", user, "rank-"+uuid.NewString()+"@loci.test")
	require.NoError(t, err)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(NewRepository(pool), nil, preference.NewRecorder(pool, log), noKick{}, log), user
}

// rate gives poi a verdict and answers every comparison with answer(other).
func rate(t *testing.T, svc *Service, user uuid.UUID, poi, city string, v Verdict, answer func(other string) bool) Place {
	t.Helper()
	ctx := context.Background()
	place, next, err := svc.RatePlace(ctx, user, RateInput{POIID: poi, POIName: poi, CityName: city, Verdict: v})
	require.NoError(t, err)
	for i := 0; next != nil; i++ {
		require.Less(t, i, 10, "binary insertion should finish")
		place, next, err = svc.ComparePlaces(ctx, user, poi, next.POIID, answer(next.POIID))
		require.NoError(t, err)
	}
	return place
}

func ids(places []Place) []string {
	out := make([]string, len(places))
	for i, p := range places {
		out[i] = p.POIID
	}
	return out
}

func TestRatePlace_FirstInBucketNeedsNoComparison(t *testing.T) {
	svc, user := newRankingService(t)
	a := uuid.NewString()
	place, next, err := svc.RatePlace(context.Background(), user, RateInput{POIID: a, POIName: "A", CityName: "Lisbon", Verdict: Loved})
	require.NoError(t, err)
	require.Nil(t, next)
	require.False(t, place.Pending)
	require.Equal(t, 1, place.Rank)
	require.InDelta(t, 10, place.Score, 1e-4)
}

func TestBinaryInsertion_PlacesCorrectly(t *testing.T) {
	svc, user := newRankingService(t)
	a, b, c, d := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	order := map[string]int{b: 0, a: 1, c: 2} // the traveller's true preference
	better := func(poi string) func(string) bool {
		return func(other string) bool { return order[poi] < order[other] }
	}
	rate(t, svc, user, a, "Lisbon", Loved, better(a))
	rate(t, svc, user, b, "Lisbon", Loved, better(b))
	rate(t, svc, user, c, "Lisbon", Loved, better(c))
	top := rate(t, svc, user, d, "Lisbon", Loved, func(string) bool { return true })

	got, err := svc.ListMine(context.Background(), user, "Lisbon", 0)
	require.NoError(t, err)
	require.Equal(t, []string{d, b, a, c}, ids(got))
	for i, p := range got {
		require.Equal(t, i+1, p.Rank)
		require.False(t, p.Pending)
	}
	require.InDelta(t, 10, top.Score, 1e-4)
	require.Equal(t, 1, top.Rank)
}

func TestRatePlace_ReRateMovesBucket(t *testing.T) {
	ctx := context.Background()
	svc, user := newRankingService(t)
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	rate(t, svc, user, b, "Lisbon", Loved, nil)
	rate(t, svc, user, a, "Lisbon", Loved, func(string) bool { return true })
	rate(t, svc, user, c, "Lisbon", Loved, func(string) bool { return false })
	rate(t, svc, user, a, "Lisbon", NotForMe, nil)

	got, err := svc.ListMine(ctx, user, "Lisbon", 0)
	require.NoError(t, err)
	require.Equal(t, []string{b, c, a}, ids(got))
	require.Equal(t, NotForMe, got[2].Verdict)

	var positions []int
	rows, err := testsupport.MustPool().Query(ctx, `
		SELECT position FROM user_poi_rankings WHERE user_id = $1 AND verdict = 1 ORDER BY position`, user)
	require.NoError(t, err)
	for rows.Next() {
		var p int
		require.NoError(t, rows.Scan(&p))
		positions = append(positions, p)
	}
	rows.Close()
	require.Equal(t, []int{0, 1}, positions, "the Loved bucket closes the gap A left")

	var signals int
	var event string
	require.NoError(t, testsupport.MustPool().QueryRow(ctx, `
		SELECT COUNT(*), MAX(event) FROM preference_feedback WHERE user_id = $1 AND metadata->>'ranking_poi' = $2`,
		user, a).Scan(&signals, &event))
	require.Equal(t, 1, signals)
	require.Equal(t, preference.EventSkipped, event)
}

func TestComparePlaces_StaleOtherFinalises(t *testing.T) {
	ctx := context.Background()
	svc, user := newRankingService(t)
	a, b := uuid.NewString(), uuid.NewString()
	rate(t, svc, user, a, "Lisbon", Loved, nil)
	_, next, err := svc.RatePlace(ctx, user, RateInput{POIID: b, POIName: "B", CityName: "Lisbon", Verdict: Loved})
	require.NoError(t, err)
	require.NotNil(t, next)

	// An id not in the bucket teaches nothing; the next probe is the same.
	_, again, err := svc.ComparePlaces(ctx, user, b, uuid.NewString(), true)
	require.NoError(t, err)
	require.NotNil(t, again)
	require.Equal(t, next.POIID, again.POIID)

	place, done, err := svc.ComparePlaces(ctx, user, b, a, true)
	require.NoError(t, err)
	require.Nil(t, done)
	require.Equal(t, 1, place.Rank)
}

func TestComparePlaces_NotPending(t *testing.T) {
	svc, user := newRankingService(t)
	a := uuid.NewString()
	rate(t, svc, user, a, "Lisbon", Fine, nil)
	_, _, err := svc.ComparePlaces(context.Background(), user, a, uuid.NewString(), true)
	require.True(t, errors.Is(err, ErrNotPending), "err = %v", err)
	_, _, err = svc.ComparePlaces(context.Background(), user, uuid.NewString(), a, true)
	require.True(t, errors.Is(err, ErrNotFound), "err = %v", err)
}

func TestDelete_RenumbersAndForgets(t *testing.T) {
	ctx := context.Background()
	svc, user := newRankingService(t)
	a, b := uuid.NewString(), uuid.NewString()
	rate(t, svc, user, a, "Lisbon", Loved, nil)
	rate(t, svc, user, b, "Lisbon", Loved, func(string) bool { return false })

	deleted, err := svc.Delete(ctx, user, a)
	require.NoError(t, err)
	require.True(t, deleted)
	got, err := svc.ListMine(ctx, user, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{b}, ids(got))
	require.Equal(t, 1, got[0].Rank)

	var signals int
	require.NoError(t, testsupport.MustPool().QueryRow(ctx, `
		SELECT COUNT(*) FROM preference_feedback WHERE user_id = $1 AND metadata->>'ranking_poi' = $2`, user, a).Scan(&signals))
	require.Zero(t, signals)

	again, err := svc.Delete(ctx, user, a)
	require.NoError(t, err)
	require.False(t, again)
}

func TestListMine_FiltersCityCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	svc, user := newRankingService(t)
	a, b := uuid.NewString(), uuid.NewString()
	rate(t, svc, user, a, "Lisbon", Loved, nil)
	rate(t, svc, user, b, "Porto", Loved, nil)

	got, err := svc.ListMine(ctx, user, " lisbon ", 0)
	require.NoError(t, err)
	require.Equal(t, []string{a}, ids(got))
	require.Equal(t, "Lisbon", got[0].CityName)

	all, err := svc.ListMine(ctx, user, "", 0)
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, 1, all[0].Rank)
	require.Equal(t, 1, all[1].Rank, "rank restarts in each city")
}
