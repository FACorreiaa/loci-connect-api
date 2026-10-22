package push

import (
	"context"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ProgressCategory is the notification category points events carry: an
// achievement unlocked, a friend passing you on the week's leaderboard.
const ProgressCategory = "loci_progress"

// BuildAchievementPayload announces an unlock; it opens the achievements
// screen.
func BuildAchievementPayload(title, description string) Payload {
	return Payload{
		Category: ProgressCategory,
		ThreadID: "progress",
		Origin:   "achievement_unlocked",
		Title:    "Achievement unlocked: " + title,
		Body:     description,
		URL:      WebOrigin + "/achievements",
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

// AchievementUnlocked tells `to` they unlocked an achievement, unless progress
// updates are off.
func (n *Notifier) AchievementUnlocked(_ context.Context, to uuid.UUID, title, description string) {
	n.notifyGated(to, BuildAchievementPayload(title, description), progressAllowed)
}

// PassedOnLeaderboard tells `to` that `by` overtook them, unless progress
// updates are off.
func (n *Notifier) PassedOnLeaderboard(_ context.Context, to uuid.UUID, by *socialv1.PublicUser) {
	n.notifyGated(to, BuildPassedPayload(by), progressAllowed)
}
