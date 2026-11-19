package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// PlaceChecksTotal counts each place looked up on the map for a stop's check,
// by how it came out and what kind of place the plan said it was.
//
// outcome="found_with_hours" - found, and the map lists opening hours
// outcome="found_no_hours"   - found, no hours listed
// outcome="not_found"        - no one place on the map fits the name
// outcome="lookup_failed"    - the map did not answer; nothing was recorded
//
// kind is "sight", "food", "shop", "stay" or "mixed".
//
// not_found/(found+not_found) is the share of stops shown as "Couldn't
// check". Read by kind it says where the matching falls short on real plans,
// which fixtures written by hand can only guess at.
var PlaceChecksTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "loci_place_checks_total",
		Help: "Places looked up on the map for stop checks, by outcome and kind of place",
	},
	[]string{"outcome", "kind"},
)

// RecordPlaceCheck records one place's lookup.
func RecordPlaceCheck(outcome, kind string) {
	PlaceChecksTotal.WithLabelValues(outcome, kind).Inc()
}
