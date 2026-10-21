package push

import (
	"context"
	"encoding/json"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// SocialCategory is the notification category people events carry; the app
// groups them under one thread.
const SocialCategory = "loci_social"

// socialKind is what happened between two people.
type socialKind string

const (
	socialFollowed       socialKind = "new_follower"
	socialFollowedBack   socialKind = "followed_back"
	socialFollowRequest  socialKind = "follow_request"
	socialFollowApproved socialKind = "follow_approved"
)

func displayName(u *socialv1.PublicUser, fallback string) string {
	switch {
	case u == nil:
		return fallback
	case u.GetDisplayName() != "":
		return u.GetDisplayName()
	case u.GetUsername() != "":
		return u.GetUsername()
	default:
		return fallback
	}
}

// BuildSocialPayload shapes a people event for web push and APNs. A follow
// request opens the requests list (to answer it); everything else opens the
// other person's profile.
func BuildSocialPayload(kind socialKind, other *socialv1.PublicUser) Payload {
	name := displayName(other, "Someone")
	path := "/friends"
	if other != nil && other.GetUsername() != "" {
		path = "/u/" + other.GetUsername()
	}
	p := Payload{Category: SocialCategory, ThreadID: "friends", Origin: string(kind)}
	switch kind {
	case socialFollowedBack:
		p.Title = "New friend on Loci"
		p.Body = name + " followed you back. You're friends now: see their trips."
	case socialFollowRequest:
		p.Title = "Follow request"
		p.Body = name + " asked to follow you on Loci."
		path = "/friends?tab=requests"
	case socialFollowApproved:
		p.Title = "Request approved"
		p.Body = name + " approved your follow request."
	default:
		p.Title = "New follower"
		p.Body = name + " started following you on Loci."
	}
	p.URL = WebOrigin + path
	return p
}

// NewFollower tells `to` that `from` follows them; friends when `to` already
// followed `from`.
func (n *Notifier) NewFollower(_ context.Context, to uuid.UUID, from *socialv1.PublicUser, friends bool) {
	kind := socialFollowed
	if friends {
		kind = socialFollowedBack
	}
	n.notifySocial(to, BuildSocialPayload(kind, from))
}

// FollowRequested tells a private account's owner someone asked to follow.
func (n *Notifier) FollowRequested(_ context.Context, to uuid.UUID, from *socialv1.PublicUser) {
	n.notifySocial(to, BuildSocialPayload(socialFollowRequest, from))
}

// FollowApproved tells `to` their request to follow `by` was approved.
func (n *Notifier) FollowApproved(_ context.Context, to uuid.UUID, by *socialv1.PublicUser) {
	n.notifySocial(to, BuildSocialPayload(socialFollowApproved, by))
}

// notifySocial sends p to every device `to` registered, on every platform,
// unless they switched people activity (friend_activity) off. Like OnRunFinished it returns at
// once and never fails the caller.
func (n *Notifier) notifySocial(to uuid.UUID, p Payload) {
	n.notifyGated(to, p, func(s *locitypes.NotificationSettings) bool { return s.FriendActivity })
}

// notifyGated sends p to every device `to` registered, on every platform,
// when allow says their switches let it through.
func (n *Notifier) notifyGated(to uuid.UUID, p Payload, allow func(*locitypes.NotificationSettings) bool) {
	if to == uuid.Nil || len(n.senders) == 0 {
		return
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		settings, err := n.settings.GetNotificationSettings(ctx, to)
		if err != nil {
			n.logger.Warn("read notification settings", "user_id", to, "error", err)
			return
		}
		if !allow(settings) {
			return
		}
		body, err := json.Marshal(p)
		if err != nil {
			return
		}
		for _, platform := range platformOrder {
			sender, ok := n.senders[platform]
			if !ok {
				continue
			}
			devices, err := n.devices.ForUser(ctx, to, platform)
			if err != nil {
				n.logger.Warn("list push devices", "user_id", to, "platform", platform, "error", err)
				continue
			}
			for _, d := range devices {
				if platform == PlatformWebPush && !ValidWebPushEndpoint(d.Endpoint) {
					continue
				}
				n.sendOne(ctx, platform, sender, d, body, "user_id", to)
			}
		}
	}()
}
