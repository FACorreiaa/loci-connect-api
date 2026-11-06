package trip

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
)

// Per-trip accessibility needs must not be stored on a trip: trips are shared,
// and their constraints are copied to everyone they are shared with.
func TestConstraintFromProtoDropsAccessibility(t *testing.T) {
	c := constraintFromProto(&tripv1.TripConstraint{
		Mobility: proto.String("wheelchair"),
		Accessibility: &profilev1.AccessibilityNeeds{
			Mobility: profilev1.MobilityLevel_MOBILITY_LEVEL_MANUAL_WHEELCHAIR,
			Notes:    "private-health-note",
		},
	})
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-health-note") || strings.Contains(string(raw), "accessibility") {
		t.Fatalf("trip constraints persisted accessibility needs: %s", raw)
	}
	if constraintToProto(c).GetAccessibility() != nil {
		t.Fatal("trip constraints echoed accessibility needs back")
	}
}
