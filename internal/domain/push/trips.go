package push

import (
	"context"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// TripCategory is the notification category a shared trip's events carry;
// the app groups each trip's under one thread.
const TripCategory = "loci_trip"

// BuildTripPayload shapes a planning-together event (a suggestion, an applied
// change) for web push and APNs. It opens the trip.
func BuildTripPayload(tripID uuid.UUID, title, body string) Payload {
	return Payload{
		Category: TripCategory, ThreadID: "trip-" + tripID.String(), Origin: "trip_change",
		Title: title, Body: body, URL: WebOrigin + "/trips/" + tripID.String(),
	}
}

// TripChanged tells each of `to` about a change to a trip they plan together,
// gated on their trip-updates switch.
func (n *Notifier) TripChanged(_ context.Context, to []uuid.UUID, tripID uuid.UUID, title, body string) {
	p := BuildTripPayload(tripID, title, body)
	for _, id := range to {
		n.notifyGated(id, p, func(s *locitypes.NotificationSettings) bool { return s.TripReminders })
	}
}
