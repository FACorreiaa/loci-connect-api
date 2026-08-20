package presenter

import (
	"testing"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The client has to distinguish "Loci checked and this is unverified" from
// "this response predates grounding". The first is grounded=false; the second
// is the field being absent. Setting it only when true would collapse them.
func TestPOIToProtoAlwaysSetsGrounded(t *testing.T) {
	for _, grounded := range []bool{true, false} {
		poi := locitypes.POIDetailedInfo{
			ID: uuid.New(), Name: "Bar Alta", Grounded: grounded,
		}

		got := ToPOIDetailedInfo(poi)

		if got.Grounded == nil {
			t.Fatalf("grounded=%v was not set on the wire", grounded)
		}
		if got.GetGrounded() != grounded {
			t.Errorf("GetGrounded() = %v, want %v", got.GetGrounded(), grounded)
		}
	}
}
