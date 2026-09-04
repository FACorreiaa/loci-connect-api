package trip

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// reopenAfter is how long a trip must sit untouched before reading it counts
// as coming back to it rather than continuing to work on it.
const reopenAfter = 24 * time.Hour

// tripReopenedMsg is the log message the distinct-user re-open rate is built
// on. Changing it breaks that query.
const tripReopenedMsg = "trip reopened"

// reopened reports whether reading a trip last saved at updatedAt counts as a
// re-open.
//
// A zero time means the trip was never saved, and a future time means the
// clocks disagree; neither is evidence that anyone came back, so both are
// false rather than silently counted.
func reopened(updatedAt, now time.Time) bool {
	if updatedAt.IsZero() || updatedAt.After(now) {
		return false
	}
	return now.Sub(updatedAt) >= reopenAfter
}

// recordReopen notes that a user returned to an older trip.
//
// This is the only place in the system that can observe the retention claim
// the product is sold on, so it is measured here rather than inferred later
// from request logs. The counter carries volume; the log line carries the user
// and trip, because both are unbounded cardinality as metric labels and the
// interesting number is distinct users, not total reads.
func (h *Handler) recordReopen(ctx context.Context, t *Trip, userID uuid.UUID) {
	if t == nil || !reopened(t.UpdatedAt, time.Now()) {
		return
	}
	observability.TripReopenedTotal.Inc()
	if h.log == nil {
		return
	}
	h.log.InfoContext(ctx, tripReopenedMsg,
		slog.String("trip_id", t.ID.String()),
		slog.String("user_id", userID.String()),
		slog.Duration("since_last_save", time.Since(t.UpdatedAt)))
}

// recordReopenEvent sends the re-open to product analytics.
//
// Separate from the counter and the log line on purpose: those answer "how
// many", this answers "who", and only the third can be joined against the
// signup the browser reported.
func (h *Handler) recordReopenEvent(t *Trip, userID uuid.UUID) {
	if t == nil || !reopened(t.UpdatedAt, time.Now()) {
		return
	}
	h.analytics.Capture(userID.String(), "trip_reopened", map[string]any{
		"trip_id":          t.ID.String(),
		"hours_since_save": int(time.Since(t.UpdatedAt).Hours()),
	})
}
