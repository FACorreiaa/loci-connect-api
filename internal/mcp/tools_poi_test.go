package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// A description cut on a byte boundary produces invalid UTF-8, which is exactly
// what happens to accented place names — and Loci is a travel product.
func TestSummarizeTruncatesDescriptionsWithoutBreakingUTF8(t *testing.T) {
	// Multi-byte runes straddling the cut point.
	long := strings.Repeat("é", descriptionLimit+50)

	out := summarize([]locitypes.POIDetailedInfo{
		{ID: uuid.New(), Name: "Café", DescriptionPOI: long},
	}, nil)

	got := out.Results[0].Description
	if !utf8.ValidString(got) {
		t.Fatal("truncated description is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(got); n > descriptionLimit+1 {
		t.Errorf("description is %d runes, want at most %d plus the ellipsis",
			n, descriptionLimit)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncation marker missing")
	}
}

func TestSummarizeKeepsShortDescriptionsIntact(t *testing.T) {
	out := summarize([]locitypes.POIDetailedInfo{
		{ID: uuid.New(), Name: "Bar", DescriptionPOI: "Short and sweet."},
	}, nil)

	if got := out.Results[0].Description; got != "Short and sweet." {
		t.Errorf("Description = %q, want it unchanged", got)
	}
}

func TestSummarizeFallsBackToDescriptionField(t *testing.T) {
	out := summarize([]locitypes.POIDetailedInfo{
		{ID: uuid.New(), Name: "Bar", Description: "fallback text"},
	}, nil)

	if got := out.Results[0].Description; got != "fallback text" {
		t.Errorf("Description = %q, want the fallback", got)
	}
}

// The unit bug: POIDetailedInfo.Distance holds kilometres on spatial paths and a
// cosine similarity score on vector paths. A tool that did not measure a
// distance must report none rather than publish whichever number is in there.
func TestSummarizeOmitsDistanceWhenNoneWasMeasured(t *testing.T) {
	id := uuid.New()

	out := summarize([]locitypes.POIDetailedInfo{
		// Distance here is a similarity score, as the vector search paths leave it.
		{ID: id, Name: "Somewhere", Distance: 0.82},
	}, nil)

	if got := out.Results[0].DistanceKm; got != 0 {
		t.Errorf("DistanceKm = %v; an unmeasured distance must be omitted, not "+
			"filled from a similarity score", got)
	}
}

func TestSummarizeReportsMeasuredDistances(t *testing.T) {
	id := uuid.New()
	pois := []locitypes.POIDetailedInfo{{ID: id, Name: "Nearby Bar", Distance: 1.4}}

	out := summarize(pois, measuredDistances(pois))

	if got := out.Results[0].DistanceKm; got != 1.4 {
		t.Errorf("DistanceKm = %v, want 1.4", got)
	}
}

func TestMeasuredDistancesSkipsUnusableRows(t *testing.T) {
	withID := uuid.New()

	got := measuredDistances([]locitypes.POIDetailedInfo{
		{ID: withID, Distance: 2.5},
		{ID: uuid.Nil, Distance: 9.9}, // uncitable, must not be keyed
		{ID: uuid.New(), Distance: 0}, // no distance measured
	})

	if len(got) != 1 {
		t.Fatalf("measuredDistances returned %d entries, want 1: %+v", len(got), got)
	}
	if got[withID] != 2.5 {
		t.Errorf("distance = %v, want 2.5", got[withID])
	}
}

func TestSummarizeTruncatesAtSharedLimit(t *testing.T) {
	pois := make([]locitypes.POIDetailedInfo, maxToolResults+7)
	for i := range pois {
		pois[i] = locitypes.POIDetailedInfo{ID: uuid.New(), Name: "Place"}
	}

	out := summarize(pois, nil)

	if len(out.Results) != maxToolResults {
		t.Errorf("returned %d results, want the shared cap of %d",
			len(out.Results), maxToolResults)
	}
	if !out.Truncated {
		t.Error("Truncated flag not set on a capped response")
	}
	// Count reports what existed, not what was returned.
	if out.Count != maxToolResults+7 {
		t.Errorf("Count = %d, want the pre-truncation total %d", out.Count, maxToolResults+7)
	}
}

func TestLabelMatchReasonStampsEveryResult(t *testing.T) {
	out := summarize([]locitypes.POIDetailedInfo{
		{ID: uuid.New(), Name: "A"},
		{ID: uuid.New(), Name: "B"},
	}, nil)

	labelMatchReason(&out, "nearby")

	for i, r := range out.Results {
		if r.MatchReason != "nearby" {
			t.Errorf("result %d MatchReason = %q, want %q", i, r.MatchReason, "nearby")
		}
	}
}

func TestSummarizeOmitsNilPOIIdentifiers(t *testing.T) {
	out := summarize([]locitypes.POIDetailedInfo{{ID: uuid.Nil, Name: "Unsaved"}}, nil)

	if got := out.Results[0].ID; got != "" {
		t.Errorf("ID = %q; a nil uuid must render as empty, not as all zeroes", got)
	}
}
