package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Trip-sizing metrics answer: are we asking for the right number of places, and
// are we getting them?
//
// The count used to be whatever the model felt like producing — roughly ten for
// a weekend and roughly ten for a month. Now it is derived from a duration
// parsed out of the traveller's own words, which makes two new things worth
// watching: how often that parse misses, and how often the answer falls short
// of what was asked for.
var (
	// TripDurationParseTotal counts requests by how their length was decided.
	//
	// source="default" means nothing in the text said how long the trip was and
	// a two-day sample was assumed. A rising share of those is a parser gap —
	// and the raw text behind them is the copy for a future "did you mean 4
	// days?" prompt.
	TripDurationParseTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_trip_duration_parse_total",
			Help: "Requests by how the trip duration was determined",
		},
		[]string{"source"},
	)

	// TripDays is the horizon each request was planned over, after clamping.
	TripDays = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "loci_trip_days",
			Help:    "Planning horizon in days, after clamping",
			Buckets: []float64{1, 2, 3, 4, 5, 7, 10, 14, 21, 30},
		},
	)

	// POITarget is how many places a request asked the model for.
	POITarget = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "loci_poi_target",
			Help:    "Places requested from the model for one answer",
			Buckets: []float64{8, 12, 18, 24, 30, 35, 40, 50},
		},
	)

	// POIShortfallTotal counts places asked for but not delivered.
	//
	// This is the metric that makes an honest short answer distinguishable from
	// a padded one. A city whose corpus holds fifteen places cannot ground forty,
	// and the prompt tells the model to return fewer rather than invent; a
	// persistent shortfall for one city is therefore an ingest ticket, not a
	// prompt bug. Without this counter that distinction is invisible.
	POIShortfallTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "loci_poi_shortfall_total",
			Help: "Places requested from the model but not returned",
		},
	)

	// POIDelivered is how many places an answer actually carried.
	POIDelivered = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "loci_poi_delivered",
			Help:    "Places actually returned in one answer",
			Buckets: []float64{0, 5, 8, 12, 18, 24, 30, 35, 40, 50},
		},
	)
)

// RecordTripDurationParse records how one request's length was decided. Called
// for every request, not only misses: the source label is only meaningful as a
// share of the whole.
func RecordTripDurationParse(source string, days int) {
	TripDurationParseTotal.WithLabelValues(source).Inc()
	TripDays.Observe(float64(days))
}

// RecordPOITarget records how many places a request asked for.
func RecordPOITarget(target int) {
	POITarget.Observe(float64(target))
}

// RecordPOIShortfall records what an answer delivered against what it asked
// for. A surplus is not a shortfall and is not counted as one.
func RecordPOIShortfall(target, delivered int) {
	POIDelivered.Observe(float64(delivered))
	if target > delivered {
		POIShortfallTotal.Add(float64(target - delivered))
	}
}
