package push

import (
	"context"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// BriefCategory is the notification category a trip-day brief carries; the
// app opens the trip from it.
const BriefCategory = "loci_brief"

// BuildBriefPayload shapes a trip-day brief. One thread per trip keeps a
// week's briefs stacked together on the lock screen.
func BuildBriefPayload(tripID uuid.UUID, title, body, url string) Payload {
	return Payload{
		Category: BriefCategory,
		ThreadID: "brief-" + tripID.String(),
		Origin:   "trip_brief",
		Title:    title,
		Body:     body,
		URL:      url,
	}
}

// TripBrief sends a trip-day brief (the morning one, or the weather turn) to
// every device `to` registered, on every platform, when trip reminders are
// on. It returns at once and never fails the caller.
func (n *Notifier) TripBrief(_ context.Context, to, tripID uuid.UUID, title, body, url string) {
	n.notifyGated(to, BuildBriefPayload(tripID, title, body, url), func(s *locitypes.NotificationSettings) bool {
		return s.TripReminders
	})
}
