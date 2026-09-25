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
// Platform names as push_devices stores them.
const (
	PlatformWebPush = "web_push"
	PlatformAPNS    = "apns"
)

// platformOrder is the delivery order: fixed, so a run's log lines and
// metrics read the same way every time.
var platformOrder = []string{PlatformWebPush, PlatformAPNS}

type Notifier struct {
	claims   runs.Store
	settings SettingsReader
	devices  DeviceStore
	senders  map[string]Sender
	logger   *slog.Logger
	wg       sync.WaitGroup
}

// NewNotifier delivers finished runs to browsers through `sender`; nil means
// web push is off. WithSender adds the other platforms.
func NewNotifier(claims runs.Store, settings SettingsReader, devices DeviceStore, sender Sender, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	n := &Notifier{claims: claims, settings: settings, devices: devices, senders: map[string]Sender{}, logger: logger}
	if sender != nil {
		n.senders[PlatformWebPush] = sender
	}
	return n
}

// WithSender registers the sender for one platform (PlatformAPNS for iPhones).
func (n *Notifier) WithSender(platform string, sender Sender) *Notifier {
	if sender != nil {
		n.senders[platform] = sender
	}
	return n
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
	// A run with no session has no result to open. The tracker never
	// reports one; this keeps a stray call from spending the one claim.
	if run.SessionID == uuid.Nil {
		return
	}
	claimed, err := n.claims.ClaimNotification(ctx, run.ID)
	if err != nil || !claimed {
		if err != nil {
			n.logger.Warn("claim run notification", "run_id", run.ID, "error", err)
		} else {
			n.logger.Info("push skipped: run already announced", "run_id", run.ID)
		}
		return
	}
	settings, err := n.settings.GetNotificationSettings(ctx, run.UserID)
	if err != nil {
		n.logger.Warn("read notification settings", "user_id", run.UserID, "error", err)
		return
	}
	if !settings.SearchFinished {
		n.logger.Info("push skipped: search_finished is off", "run_id", run.ID, "user_id", run.UserID)
		return
	}
	body, err := json.Marshal(BuildPayload(run))
	if err != nil {
		return
	}
	for _, platform := range platformOrder {
		sender, ok := n.senders[platform]
		if !ok {
			continue
		}
		devices, err := n.devices.ForUser(ctx, run.UserID, platform)
		if err != nil {
			n.logger.Warn("list push devices", "run_id", run.ID, "platform", platform, "error", err)
			continue
		}
		// Every silent outcome used to look the same from the outside; the
		// device count is what tells "never registered" from "sent and dropped".
		n.logger.Info("push: delivering", "run_id", run.ID, "platform", platform, "devices", len(devices), "status", run.Status)
		for _, d := range devices {
			n.send(ctx, run, platform, sender, d, body)
		}
	}
}

func (n *Notifier) send(ctx context.Context, run runs.Run, platform string, sender Sender, d Device, body []byte) {
	// Defence in depth: registration already enforces the SSRF allow-list,
	// but a row can predate that check or a bug could let one through it.
	// Never dial an endpoint the allow-list would now reject.
	if platform == PlatformWebPush && !ValidWebPushEndpoint(d.Endpoint) {
		n.logger.Warn("skipping push device with disallowed endpoint", "run_id", run.ID, "device_id", d.ID)
		observability.PushSentTotal.WithLabelValues(platform, "invalid_endpoint").Inc()
		return
	}
	n.sendOne(ctx, platform, sender, d, body, "run_id", run.ID)
}

// sendOne delivers one body to one device, records the outcome, and removes
// the device when its push service says it is gone for good (a 410 web
// endpoint; an APNs BadDeviceToken / Unregistered token). logKey/logValue
// name what is being announced, for the failure log line.
func (n *Notifier) sendOne(ctx context.Context, platform string, sender Sender, d Device, body []byte, logKey string, logValue any) {
	gone, err := sender.Send(ctx, d, body)
	switch {
	case gone:
		observability.PushSentTotal.WithLabelValues(platform, "gone").Inc()
		// The push service disowned this device: the row goes, and nothing on
		// the phone knows. Say so, with Apple's reason when there is one.
		n.logger.Warn("push endpoint gone; removing device", logKey, logValue, "platform", platform, "device_id", d.ID, "reason", err)
		if rErr := n.devices.RemoveEndpoint(ctx, d.Endpoint); rErr != nil {
			n.logger.Warn("remove gone push endpoint", "error", rErr)
		}
	case err != nil:
		observability.PushSentTotal.WithLabelValues(platform, "error").Inc()
		n.logger.Warn("push send failed", logKey, logValue, "platform", platform, "device_id", d.ID, "error", err)
	default:
		observability.PushSentTotal.WithLabelValues(platform, "sent").Inc()
	}
}
