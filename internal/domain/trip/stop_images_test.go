package trip

import (
	"context"
	"errors"
	"testing"

	poipb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type fakeStopImages struct {
	images map[uuid.UUID]*poipb.POIImage
	err    error
	calls  int
	asked  [][]uuid.UUID
}

func (f *fakeStopImages) FirstImages(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*poipb.POIImage, error) {
	f.calls++
	f.asked = append(f.asked, ids)
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]*poipb.POIImage{}
	for _, id := range ids {
		if img, ok := f.images[id]; ok {
			out[id] = img
		}
	}
	return out, nil
}

func credited(url string) *poipb.POIImage {
	return &poipb.POIImage{Url: url, Source: "wikimedia", Licence: "CC BY-SA 4.0", Attribution: "Someone"}
}

// Every stop with a poi_id gets the POI's picture, not only City Pack stops,
// and a whole page of trips costs one lookup with each POI asked for once.
func TestFillStopImagesOneLookupForAPage(t *testing.T) {
	a, b, bare := uuid.New(), uuid.New(), uuid.New()
	images := &fakeStopImages{images: map[uuid.UUID]*poipb.POIImage{a: credited("https://img/a.jpg"), b: credited("https://img/b.jpg")}}
	h := NewHandler(nil, "", nil, nil).WithStopImages(images)

	first := draftWithStops(a.String(), "not-a-uuid", bare.String())
	second := draftWithStops(a.String(), b.String())
	h.fillStopImages(context.Background(), first, second)

	require.Equal(t, 1, images.calls, "one query for every trip in the response")
	require.ElementsMatch(t, []uuid.UUID{a, bare, b}, images.asked[0], "each POI asked for once; a bad id never")
	require.Equal(t, "https://img/a.jpg", first.Days[0].Stops[0].GetImage().GetUrl())
	require.Nil(t, first.Days[0].Stops[1].GetImage(), "a stop without a valid poi_id has no picture")
	require.Nil(t, first.Days[0].Stops[2].GetImage(), "a POI with no stored picture has none")
	require.Equal(t, "https://img/a.jpg", second.Days[0].Stops[0].GetImage().GetUrl())
	require.Equal(t, "https://img/b.jpg", second.Days[0].Stops[1].GetImage().GetUrl())
	require.Equal(t, "CC BY-SA 4.0", second.Days[0].Stops[1].GetImage().GetLicence(), "the credit travels with it")

	// Two stops at one place hold separate messages.
	require.NotSame(t, first.Days[0].Stops[0].GetImage(), second.Days[0].Stops[0].GetImage())
}

// A picture a response already carries (a City Pack stop) is left alone and
// not looked up again; a lookup failure costs pictures, never the response.
func TestFillStopImagesKeepsExistingAndToleratesErrors(t *testing.T) {
	id := uuid.New()
	images := &fakeStopImages{images: map[uuid.UUID]*poipb.POIImage{id: credited("https://img/store.jpg")}}
	h := NewHandler(nil, "", nil, nil).WithStopImages(images)
	draft := draftWithStops(id.String())
	draft.Days[0].Stops[0].Image = credited("https://img/pack.jpg")
	h.fillStopImages(context.Background(), draft)
	require.Equal(t, 0, images.calls)
	require.Equal(t, "https://img/pack.jpg", draft.Days[0].Stops[0].GetImage().GetUrl())

	failing := NewHandler(nil, "", nil, nil).WithStopImages(&fakeStopImages{err: errors.New("db down")})
	draft = draftWithStops(id.String())
	failing.fillStopImages(context.Background(), draft)
	require.Nil(t, draft.Days[0].Stops[0].GetImage())

	// No store attached: nothing happens.
	draft = draftWithStops(id.String())
	NewHandler(nil, "", nil, nil).fillStopImages(context.Background(), draft)
	require.Nil(t, draft.Days[0].Stops[0].GetImage())
}

// The hydrated place picks the picture up too, so components that render a
// POI's image credits show it, as they do for City Pack stops.
func TestRespondPutsThePictureOnTheHydratedPOI(t *testing.T) {
	id := uuid.New()
	h := NewHandler(nil, "", nil, nil).
		WithPlaces(&fakePlaceLookup{places: map[uuid.UUID]*locitypes.POIDetailedInfo{id: {ID: id, Name: "Torre"}}}).
		WithStopImages(&fakeStopImages{images: map[uuid.UUID]*poipb.POIImage{id: credited("https://img/torre.jpg")}})
	out := h.respond(context.Background(), &Trip{ID: uuid.New(), Days: []TripDay{{DayNumber: 1, Stops: []TripStop{{POIID: id.String(), Name: "Torre"}}}}})
	stop := out.GetDays()[0].GetStops()[0]
	require.Equal(t, "https://img/torre.jpg", stop.GetImage().GetUrl())
	require.Equal(t, []string{"https://img/torre.jpg"}, stop.GetPoi().GetImages())
	require.Len(t, stop.GetPoi().GetImageCredits(), 1)
}
