package push

import (
	"context"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ProgressCategory is the notification category points events carry: a badge
// earned, a friend passing you on the week's leaderboard.
const ProgressCategory = "loci_progress"

// BuildBadgePayload announces a badge; it opens the progress screen.
func BuildBadgePayload(title, description string) Payload {
	return Payload{
		Category: ProgressCategory,
		ThreadID: "progress",
		Origin:   "badge_earned",
		Title:    "Badge earned: " + title,
		Body:     description,
		URL:      WebOrigin + "/friends?tab=progress",
	}
}

// BuildPassedPayload tells someone a friend overtook them this week; it opens
// the leaderboard.
func BuildPassedPayload(by *socialv1.PublicUser) Payload {
	name := "A friend"
	if by != nil {
		if by.GetDisplayName() != "" {
			name = by.GetDisplayName()
		} else if by.GetUsername() != "" {
			name = by.GetUsername()
		}
	}
	return Payload{
		Category: ProgressCategory,
		ThreadID: "leaderboard",
		Origin:   "leaderboard_passed",
		Title:    name + " passed you",
		Body:     name + " just moved ahead of you on this week's leaderboard. Get some points back.",
		URL:      WebOrigin + "/friends?tab=leaderboard",
	}
}

func progressAllowed(s *locitypes.NotificationSettings) bool { return s.ProgressUpdates }

// BadgeEarned tells `to` they earned a badge, unless progress updates are off.
func (n *Notifier) BadgeEarned(_ context.Context, to uuid.UUID, title, description string) {
	n.notifyGated(to, BuildBadgePayload(title, description), progressAllowed)
}

// PassedOnLeaderboard tells `to` that `by` overtook them, unless progress
// updates are off.
func (n *Notifier) PassedOnLeaderboard(_ context.Context, to uuid.UUID, by *socialv1.PublicUser) {
	n.notifyGated(to, BuildPassedPayload(by), progressAllowed)
}
