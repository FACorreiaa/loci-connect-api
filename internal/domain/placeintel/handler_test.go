package placeintel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
	"github.com/stretchr/testify/assert"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
)

func TestFactLifetime(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 30*24*time.Hour, factLifetime(placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS))
	assert.Equal(t, 180*24*time.Hour, factLifetime(placev1.PlaceFactField_PLACE_FACT_FIELD_ACCESSIBILITY))
}

func TestFieldRoundTrip(t *testing.T) {
	t.Parallel()
	field := placev1.PlaceFactField_PLACE_FACT_FIELD_CHILD_FRIENDLY
	assert.Equal(t, field, parseField(fieldName(field)))
}

func TestMissingFieldsExcludesWhatIsAlreadyKnown(t *testing.T) {
	t.Parallel()
	candidate := candidatePOI{
		id:               "00000000-0000-0000-0000-000000000001",
		name:             "Cafe",
		hasOpeningHours:  true,
		hasPriceLevel:    true,
		hasAccessibility: false,
	}
	covered := coverage{fields: map[string]struct{}{"vibe": {}}}

	missing := missingFields(candidate, covered)

	assert.NotContains(t, missing, placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS,
		"the POI's own opening_hours column already answers this")
	assert.NotContains(t, missing, placev1.PlaceFactField_PLACE_FACT_FIELD_PRICE_LEVEL,
		"the POI's own price_level column already answers this")
	assert.NotContains(t, missing, placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE,
		"a live fact already answers this")
	assert.Contains(t, missing, placev1.PlaceFactField_PLACE_FACT_FIELD_ACCESSIBILITY)
	assert.Contains(t, missing, placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL)
}

func TestMissingFieldsAsksEverythingForAnUnknownPlace(t *testing.T) {
	t.Parallel()
	missing := missingFields(candidatePOI{id: "x", name: "New"}, coverage{})
	assert.Equal(t, contributableFields, missing)
}

// A geocoder outage is not the user's typo. Reporting it as a bad argument
// told people their correctly-spelled city was wrong.
func TestResolveCityErrorKeepsOutagesApartFromBadInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want connect.Code
	}{
		{"geocoder down", fmt.Errorf("lookup: %w", cityrepo.ErrGeocoderUnavailable), connect.CodeUnavailable},
		{"no such city", cityrepo.ErrCityUnresolvable, connect.CodeInvalidArgument},
		{"ambiguous city", &cityrepo.AmbiguousCityError{Query: "Beja"}, connect.CodeInvalidArgument},
		{"cancelled", context.Canceled, connect.CodeCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, connect.CodeOf(resolveCityError("Tasca", tc.err)))
		})
	}
}
