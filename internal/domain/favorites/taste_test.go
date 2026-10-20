package favorites

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	favoritesv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/favorites/v1"
)

type stubScorer struct {
	matches map[string]preference.Match
	err     error
	asked   []string
}

func (s *stubScorer) ScorePOIs(_ context.Context, _ uuid.UUID, ids []string) (map[string]preference.Match, error) {
	s.asked = append(s.asked, ids...)
	return s.matches, s.err
}

func savedList() *fakeRepo {
	return &fakeRepo{list: []locitypes.FavoriteItem{
		{ID: uuid.New(), ItemID: "11111111-1111-1111-1111-111111111111", ItemName: "Manteigaria", ContentType: "poi"},
		{ID: uuid.New(), ItemID: "22222222-2222-2222-2222-222222222222", ItemName: "Tram 28", ContentType: "poi"},
		{ID: uuid.New(), ItemID: "33333333-3333-3333-3333-333333333333", ItemName: "Lisbon weekend", ContentType: "itinerary"},
	}}
}

func TestGetFavorites_AttachesTasteMatch(t *testing.T) {
	scorer := &stubScorer{matches: map[string]preference.Match{
		"11111111-1111-1111-1111-111111111111": {Score: 0.87, Reasons: []string{"You like bakeries"}},
	}}
	h := newTestHandler(savedList(), &fakePlaces{}).WithTaste(scorer)

	resp, err := h.GetFavorites(signedIn(), connect.NewRequest(&favoritesv1.GetFavoritesRequest{}))
	require.NoError(t, err)

	favs := resp.Msg.GetFavorites()
	require.Len(t, favs, 3)
	require.InDelta(t, 0.87, favs[0].GetTasteMatch().GetScore(), 1e-6)
	require.Equal(t, []string{"You like bakeries"}, favs[0].GetTasteMatch().GetReasons())
	require.Nil(t, favs[1].GetTasteMatch(), "a place the scorer skipped shows no match")
	require.Nil(t, favs[2].GetTasteMatch())
	// An itinerary is not a place: it is never sent for scoring.
	require.ElementsMatch(t, []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"}, scorer.asked)
}

func TestGetFavorites_ScorerErrorStillReturnsFavorites(t *testing.T) {
	h := newTestHandler(savedList(), &fakePlaces{}).WithTaste(&stubScorer{err: errors.New("db down")})

	resp, err := h.GetFavorites(signedIn(), connect.NewRequest(&favoritesv1.GetFavoritesRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetFavorites(), 3)
	for _, f := range resp.Msg.GetFavorites() {
		require.Nil(t, f.GetTasteMatch())
	}
}
