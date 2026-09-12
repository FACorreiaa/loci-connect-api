package compare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	comparev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/compare/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func request(origin string, candidates ...string) *connect.Request[comparev1.CompareWeekendRequest] {
	start := time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)
	return connect.NewRequest(&comparev1.CompareWeekendRequest{
		OriginCity:         &origin,
		CandidateCityNames: candidates,
		StartDate:          timestamppb.New(start),
		EndDate:            timestamppb.New(start.Add(48 * time.Hour)),
	})
}

func handlerWith(r CityResolver, pois POIFinder) *Handler {
	return NewHandler(newTestService(r, pois))
}

func TestHandler_ComparesSuccessfully(t *testing.T) {
	r := &fakeResolver{cities: map[string]*cityrepo.Resolved{
		"Porto": city("Porto", 41.14961, -8.61099),
		"Évora": city("Évora", 38.5714, -7.9135),
		"Beja":  city("Beja", 38.0151, -7.8632),
	}}

	resp, err := handlerWith(r, fakePOIs{}).CompareWeekend(context.Background(), request("Porto", "Évora", "Beja"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Msg.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(resp.Msg.Columns))
	}
}

// The reported bug's inverse, and the single most important assertion here. A
// provider outage must tell the client to retry, not to fix input that was
// never wrong — and must be visible to anything counting server failures.
func TestHandler_GeocoderOutageIsUnavailable(t *testing.T) {
	r := &fakeResolver{err: fmt.Errorf("%w: status 503", cityrepo.ErrGeocoderUnavailable)}

	_, err := handlerWith(r, fakePOIs{}).CompareWeekend(context.Background(), request("Porto", "Évora", "Beja"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Errorf("code = %v, want Unavailable", got)
	}
}

func TestHandler_UnknownCityIsInvalidArgument(t *testing.T) {
	r := &fakeResolver{cities: map[string]*cityrepo.Resolved{}}

	_, err := handlerWith(r, fakePOIs{}).CompareWeekend(context.Background(), request("Zzzqqx", "Qqqzzx", "Xxzzqq"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestHandler_FewerThanTwoCandidatesIsInvalidArgument(t *testing.T) {
	r := &fakeResolver{cities: map[string]*cityrepo.Resolved{"Porto": city("Porto", 41.1, -8.6)}}

	_, err := handlerWith(r, fakePOIs{}).CompareWeekend(context.Background(), request("Porto", "Évora"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

// A cancelled request is not a bad request. It used to be logged as one, which
// made ordinary navigation look like client error in the metrics.
func TestHandler_CancelledRequestIsNotInvalidArgument(t *testing.T) {
	r := &fakeResolver{err: context.Canceled}

	_, err := handlerWith(r, fakePOIs{}).CompareWeekend(context.Background(), request("Porto", "Évora", "Beja"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := connect.CodeOf(err); got == connect.CodeInvalidArgument {
		t.Errorf("code = InvalidArgument, want Canceled or similar")
	}
}

// Near-misses ride back on the error so the page can offer them instead of just
// refusing. They travel base64-encoded because Connect metadata is ASCII and
// "Évora" is not.
func TestHandler_SuggestionsRideOnErrorMetadata(t *testing.T) {
	r := &fakeResolver{err: &cityrepo.AmbiguousCityError{
		Query: "Evoraa",
		Suggestions: []cityrepo.Suggestion{
			{Name: "Évora", Country: "Portugal", CountryCode: "PT", Lat: 38.5714, Lon: -7.9135},
		},
	}}

	_, err := handlerWith(r, fakePOIs{}).CompareWeekend(context.Background(), request("Evoraa", "Beja", "Faro"))
	if err == nil {
		t.Fatal("expected an error")
	}

	var ce *connect.Error
	if !asConnectError(err, &ce) {
		t.Fatalf("not a connect error: %v", err)
	}
	raw := ce.Meta().Get(suggestionsHeader)
	if raw == "" {
		t.Fatal("no suggestions on the error metadata")
	}

	decoded, decErr := base64.RawURLEncoding.DecodeString(raw)
	if decErr != nil {
		t.Fatalf("metadata was not base64: %v", decErr)
	}
	var got []cityrepo.Suggestion
	if jsonErr := json.Unmarshal(decoded, &got); jsonErr != nil {
		t.Fatalf("metadata was not JSON: %v", jsonErr)
	}
	if len(got) != 1 || got[0].Name != "Évora" {
		t.Errorf("suggestions: got %+v", got)
	}
	if got[0].Lat != 38.5714 {
		t.Errorf("coordinates lost in transit: %+v", got[0])
	}
}

func asConnectError(err error, target **connect.Error) bool {
	ce, ok := err.(*connect.Error)
	if ok {
		*target = ce
	}
	return ok
}
