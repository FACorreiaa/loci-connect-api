package compare

import "github.com/FACorreiaa/loci-connect-api/pkg/geo"

// These are thin aliases so existing compare code (and its tests) keep reading
// naturally. The implementations live in pkg/geo because the go/no-go scorer in
// localcontext needs them too, and compare already imports localcontext.
var (
	HaversineKm = geo.HaversineKm
	DriveMins   = geo.DriveMins
)
