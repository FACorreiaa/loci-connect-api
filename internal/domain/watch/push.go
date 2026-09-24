package watch

import (
	"context"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/push"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ProactivePusher is the slice of push.Notifier a watch needs.
type ProactivePusher interface {
	NotifyProactive(ctx context.Context, m push.ProactiveMessage)
}

// PushNotifier announces a watch's message on the owner's iPhones.
type PushNotifier struct{ pusher ProactivePusher }

// NewPushNotifier returns nil for a nil pusher, which WithNotifier ignores.
func NewPushNotifier(p ProactivePusher) Notifier {
	if p == nil {
		return nil
	}
	return PushNotifier{pusher: p}
}

func (n PushNotifier) WatchPosted(ctx context.Context, w Watch, cityName string, msg locitypes.ConversationMessage) {
	n.pusher.NotifyProactive(ctx, push.ProactiveMessage{
		UserID:      w.UserID,
		SessionID:   w.SessionID,
		MessageID:   msg.ID,
		Title:       w.Title,
		Content:     msg.Content,
		SourceLabel: msg.SourceLabel,
		CityName:    cityName,
	})
}
