package push

import (
	"context"
	"encoding/json"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// SocialCategory is the notification category friend events carry; the app
// groups them under one thread.
const SocialCategory = "loci_social"

// socialKind is what happened between two people.
type socialKind string

const (
	socialRequest  socialKind = "friend_request"
	socialAccepted socialKind = "friend_accepted"
)

// BuildSocialPayload shapes a friend event for web push and APNs. A request
// opens /friends (to answer it); an acceptance opens the friend's profile.
func BuildSocialPayload(kind socialKind, other *socialv1.PublicUser) Payload {
	name := "Someone"
	path := "/friends"
	if other != nil {
		if other.GetDisplayName() != "" {
			name = other.GetDisplayName()
		} else if other.GetUsername() != "" {
			name = other.GetUsername()
		}
	}
	p := Payload{Category: SocialCategory, ThreadID: "friends", Origin: string(kind)}
	switch kind {
	case socialAccepted:
		p.Title = "New friend on Loci"
		p.Body = name + " is now your friend. See their trips."
		if other != nil && other.GetUsername() != "" {
			path = "/u/" + other.GetUsername()
		}
	default:
		p.Title = "Friend request"
		p.Body = name + " wants to be friends on Loci."
	}
	p.URL = WebOrigin + path
	return p
}

// FriendRequest announces a new request to its recipient.
func (n *Notifier) FriendRequest(_ context.Context, to uuid.UUID, from *socialv1.PublicUser) {
	n.notifySocial(to, BuildSocialPayload(socialRequest, from))
}

// FriendAccepted tells `to` they have a new friend.
func (n *Notifier) FriendAccepted(_ context.Context, to uuid.UUID, by *socialv1.PublicUser) {
	n.notifySocial(to, BuildSocialPayload(socialAccepted, by))
}

// notifySocial sends p to every device `to` registered, on every platform,
// unless they switched friend activity off. Like OnRunFinished it returns at
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
