package placeintel

import (
	"testing"

	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeValueSingleChoice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		field placev1.PlaceFactField
		raw   string
		want  string
	}{
		{"trims and lowercases", placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL, "  Busy  ", "busy"},
		{"collapses whitespace", placev1.PlaceFactField_PLACE_FACT_FIELD_DOG_FRIENDLY, "outdoor_only", "outdoor_only"},
		{"price token", placev1.PlaceFactField_PLACE_FACT_FIELD_PRICE_LEVEL, "SPLURGE", "splurge"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeValue(testCase.field, testCase.raw)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

// A multi-answer field takes one answer per claim, so that each answer can be
// corroborated on its own rather than only as part of an identical set.
func TestNormalizeValueMultiChoiceTakesOneAnswer(t *testing.T) {
	t.Parallel()
	got, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_DIETARY, "  VEGAN ")
	require.NoError(t, err)
	assert.Equal(t, "vegan", got)
}

func TestNormalizeValueMultiChoiceRejectsASet(t *testing.T) {
	t.Parallel()
	_, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_DIETARY, "vegan,gluten_free")
	require.Error(t, err, "a set arriving as one value is a caller that has not fanned out")
}

func TestNormalizeValueRejectsUnknownTokens(t *testing.T) {
	t.Parallel()
	_, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL, "rammed")
	require.Error(t, err)

	_, err = normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_ACCESSIBILITY, "ramp")
	require.Error(t, err)
}

func TestNormalizeValueRejectsEmpty(t *testing.T) {
	t.Parallel()
	_, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE, "   ")
	require.ErrorIs(t, err, errEmptyValue)
}

// "none" is an answer in its own right: the scout looked and found none of them.
func TestNormalizeValueAcceptsNone(t *testing.T) {
	t.Parallel()
	got, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_DIETARY, "none")
	require.NoError(t, err)
	assert.Equal(t, "none", got)
}

func TestExclusiveFields(t *testing.T) {
	t.Parallel()
	assert.True(t, isExclusiveField(placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL))
	assert.True(t, isExclusiveField(placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS))
	assert.False(t, isExclusiveField(placev1.PlaceFactField_PLACE_FACT_FIELD_DIETARY))
	assert.False(t, isExclusiveField(placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE))
}

func TestNormalizeValueRejectsUnspecifiedField(t *testing.T) {
	t.Parallel()
	_, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_UNSPECIFIED, "quiet")
	require.Error(t, err)
}

func TestNormalizeOpeningHours(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"single span", "mon-fri 09:00-17:00", "mon-fri 09:00-17:00"},
		{"split shift", "sat 10:00-14:00,19:00-23:00", "sat 10:00-14:00,19:00-23:00"},
		{"closed day", "sun closed", "sun closed"},
		{
			"segments sort into day order",
			"sun closed; sat 10:00-14:00; mon-fri 09:00-17:00",
			"mon-fri 09:00-17:00; sat 10:00-14:00; sun closed",
		},
		{"tolerates loose spacing", "  MON-FRI   09:00-17:00 ;  sat 10:00-14:00 ", "mon-fri 09:00-17:00; sat 10:00-14:00"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS, testCase.raw)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestNormalizeOpeningHoursRejectsProse(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"9am-5pm", "mon-fri 9:00-17:00", "everyday 09:00-17:00", "mon-fri 25:00-26:00"} {
		_, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS, raw)
		require.Error(t, err, "expected %q to be rejected", raw)
	}
}

// Every contributable field must be normalisable, or the UI can offer a
// question whose answer the server refuses.
func TestEveryContributableFieldHasAVocabulary(t *testing.T) {
	t.Parallel()
	for _, field := range contributableFields {
		_, ok := claimVocabulary[field]
		assert.True(t, ok, "field %s has no vocabulary", field.String())
	}
	assert.Len(t, contributableFields, len(claimVocabulary))
}

func TestNormalizeOpeningHoursAllowsMidnightClose(t *testing.T) {
	t.Parallel()
	got, err := normalizeValue(placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS, "fri 19:00-24:00")
	require.NoError(t, err)
	assert.Equal(t, "fri 19:00-24:00", got)
}
