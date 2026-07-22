package placeintel

import (
	"testing"
	"time"

	placev1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/place"
	"github.com/stretchr/testify/assert"
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
