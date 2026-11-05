package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// BackgroundLoopUp is 1 while a background loop is running. A loop that is
	// switched off by configuration returns at once and reads 0, so alert on
	// BackgroundLoopStopsTotal rather than on this.
	BackgroundLoopUp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "loci_background_loop_up",
			Help: "Whether a background loop is running (1) or not (0)",
		},
		[]string{"loop"},
	)

	// BackgroundLoopStopsTotal counts a loop ending other than by shutdown.
	// reason is "panic", after which the loop restarts, or "error", after which
	// it stays stopped until the next deploy.
	BackgroundLoopStopsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_background_loop_stops_total",
			Help: "Background loops that ended other than by shutdown, by loop and reason",
		},
		[]string{"loop", "reason"},
	)
)
