package recap

import (
	"strings"
	"testing"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPublicViewLeaksNothingPrivate(t *testing.T) {
	f := lisbon()
	f.Highlights = append(f.Highlights, HighlightFact{POIID: "secret", Name: "My Secret Bar", Reason: HighlightFavorited})
	rec, _, ok := NewBuilder().BuildTrip(f)
	require.True(t, ok)
	rec.Id = "r1"
	rec.IsOwner = true

	pub := PublicView(rec)
	raw, err := protojson.Marshal(pub)
	require.NoError(t, err)
	js := string(raw)

	for _, private := range []string{
		// Place names, neighbourhoods and highlights.
		"Miradouro", "Tasca", "Jerónimos", "Alfama", "Baixa", "Belém", "My Secret Bar",
		// Exact dates.
		"2026-10-11", "2026-10-12", "2026-10-15",
		// Friends, and the owner's surname and ids.
		"Ana", "Rui", "Correia", f.UserID.String(),
	} {
		assert.NotContains(t, js, private, "public view leaks %q", private)
	}

	assert.False(t, pub.GetIsOwner())
	assert.Equal(t, "Fernando", pub.GetOwner().GetDisplayName(), "first name only")
	assert.Equal(t, "Oct 2026", pub.GetPeriodLabel())
	assert.Nil(t, card(pub, CardHighlights), "highlights name places")

	places := card(pub, CardPlaces).GetPlaces()
	assert.Equal(t, int32(4), places.GetPlacesCount(), "counts stay")
	require.Len(t, places.GetGroups(), 1, "neighbourhood stamps become one city stamp")
	assert.Equal(t, "Lisbon", places.GetGroups()[0].GetLabel())
	assert.Equal(t, int32(4), places.GetGroups()[0].GetPlacesCount())
	assert.Equal(t, "Kept across three neighbourhoods of Lisbon.", places.GetHeadline())

	route := card(pub, CardRoute).GetRoute()
	assert.Positive(t, route.GetDistanceKm())
	assert.Empty(t, route.GetPoints(), "a trail between places is where someone went")

	for _, tr := range card(pub, CardTaste).GetTaste().GetTraits() {
		assert.Zero(t, tr.GetEvidenceCount())
	}
	friends := card(pub, CardFriends).GetFriends()
	assert.Equal(t, int32(2), friends.GetFriendsBeenCount())
	assert.Empty(t, friends.GetFriends())
	assert.Empty(t, friends.GetHeadline())

	// The original is untouched.
	assert.NotEmpty(t, card(rec, CardPlaces).GetPlaces().GetGroups())
}

func TestPublicViewKeepsCityRoute(t *testing.T) {
	f := lisbon()
	f.Cities = []City{
		{Name: "Lisbon", Lat: 38.72, Lon: -9.14, HasCoords: true},
		{Name: "Porto", Lat: 41.15, Lon: -8.61, HasCoords: true},
	}
	f.Places[3].CityName = "Porto"
	rec, _, ok := NewBuilder().BuildTrip(f)
	require.True(t, ok)

	pub := PublicView(rec)
	route := card(pub, CardRoute).GetRoute()
	require.Len(t, route.GetPoints(), 2)
	assert.Equal(t, "From Lisbon to Porto.", route.GetHeadline())
	labels := []string{}
	for _, g := range card(pub, CardPlaces).GetPlaces().GetGroups() {
		labels = append(labels, g.GetLabel())
	}
	assert.Equal(t, "Lisbon,Porto", strings.Join(labels, ","), "city stamps stay")
}

func TestPublicViewDropsUnknownCards(t *testing.T) {
	pub := PublicView(&recapv1.Recap{Cards: []*recapv1.RecapCard{{Id: "future"}}})
	assert.Empty(t, pub.GetCards())
	assert.Nil(t, PublicView(nil))
	assert.Equal(t, AnonymousName, pub.GetOwner().GetDisplayName())
}
