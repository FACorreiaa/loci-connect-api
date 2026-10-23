package push

import (
	"context"
	"strings"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
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

// maxCommentPreview keeps a comment's banner to about two lines.
const maxCommentPreview = 140

// BuildTripCommentPayload is a new comment on a shared trip: who wrote it
// and on which trip, then the start of what they said.
func BuildTripCommentPayload(tripID uuid.UUID, tripTitle string, from *socialv1.PublicUser, body string) Payload {
	p := BuildTripPayload(tripID, displayName(from, "Someone")+" on "+tripTitle, clipRunes(strings.TrimSpace(body), maxCommentPreview))
	p.Origin = "trip_comment"
	return p
}

// TripCommented tells each of `to` about a new comment on a trip they plan
// together, gated on their friend-activity switch.
func (n *Notifier) TripCommented(_ context.Context, to []uuid.UUID, tripID uuid.UUID, tripTitle string, from *socialv1.PublicUser, body string) {
	p := BuildTripCommentPayload(tripID, tripTitle, from, body)
	for _, id := range to {
		n.notifySocial(id, p)
	}
}

func clipRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return strings.TrimRight(string(r[:limit-1]), " ,.") + "…"
}
