package push

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

type SettingsReader interface {
	GetNotificationSettings(ctx context.Context, userID uuid.UUID) (*locitypes.NotificationSettings, error)
}

// Notifier announces a finished run once, to every web-push device its
// owner registered, unless they switched "search finished" off. It never
// blocks the stream that finished and never fails it.
type Notifier struct {
	claims   runs.Store
	settings SettingsReader
	devices  DeviceStore
	sender   Sender
	logger   *slog.Logger
	wg       sync.WaitGroup
}

func NewNotifier(claims runs.Store, settings SettingsReader, devices DeviceStore, sender Sender, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{claims: claims, settings: settings, devices: devices, sender: sender, logger: logger}
}

func (n *Notifier) OnRunFinished(_ context.Context, run runs.Run) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n.deliver(ctx, run)
	}()
}

// wait is for tests.
func (n *Notifier) wait() { n.wg.Wait() }

func (n *Notifier) deliver(ctx context.Context, run runs.Run) {
	claimed, err := n.claims.ClaimNotification(ctx, run.ID)
	if err != nil || !claimed {
		if err != nil {
			n.logger.Warn("claim run notification", "run_id", run.ID, "error", err)
		}
		return
	}
	settings, err := n.settings.GetNotificationSettings(ctx, run.UserID)
	if err != nil {
		n.logger.Warn("read notification settings", "user_id", run.UserID, "error", err)
		return
	}
	if !settings.SearchFinished {
		return
	}
	devices, err := n.devices.ForUser(ctx, run.UserID, "web_push")
	if err != nil || len(devices) == 0 {
		return
	}
	body, err := json.Marshal(BuildPayload(run))
	if err != nil {
		return
	}
	for _, d := range devices {
		// Defence in depth: registration already enforces the SSRF
		// allow-list, but a row can predate that check or a bug could let
		// one through it. Never dial an endpoint the allow-list would now
		// reject.
		if !ValidWebPushEndpoint(d.Endpoint) {
			n.logger.Warn("skipping push device with disallowed endpoint", "run_id", run.ID, "device_id", d.ID)
			observability.PushSentTotal.WithLabelValues("invalid_endpoint").Inc()
			continue
		}
		gone, err := n.sender.Send(ctx, d, body)
		switch {
		case gone:
			observability.PushSentTotal.WithLabelValues("gone").Inc()
			if rErr := n.devices.RemoveEndpoint(ctx, d.Endpoint); rErr != nil {
				n.logger.Warn("remove gone push endpoint", "error", rErr)
			}
		case err != nil:
			observability.PushSentTotal.WithLabelValues("error").Inc()
			n.logger.Warn("push send failed", "run_id", run.ID, "device_id", d.ID, "error", err)
		default:
			observability.PushSentTotal.WithLabelValues("sent").Inc()
		}
	}
}
