package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// PushSentTotal counts web-push delivery attempts for a finished run, by
// outcome. "sent" is a 2xx from the push service; "gone" is a 404/410 that
// retired the device; "error" is anything else (network failure, 5xx,
// timeout); "invalid_endpoint" is a device whose stored endpoint fails
// ValidWebPushEndpoint before the notifier ever dials it — defence in depth
// against a row that predates the registration-time allow-list, or one a
// bug let through it.
var PushSentTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "loci_push_sent_total",
		Help: "Web push delivery attempts for finished runs, by outcome",
	},
	[]string{"result"},
)
