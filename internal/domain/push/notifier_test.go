package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// --- fakes ---

type fakeRunStore struct {
	claimed   bool
	claimErr  error
	claimArgs []uuid.UUID
}

func (f *fakeRunStore) Reserve(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (f *fakeRunStore) Release(ctx context.Context, runID uuid.UUID) error { return nil }
func (f *fakeRunStore) Attach(ctx context.Context, runID, sessionID uuid.UUID, domain, city string) error {
	return nil
}

func (f *fakeRunStore) Finish(ctx context.Context, runID uuid.UUID, status runs.Status, errorCode string) (runs.Run, bool, error) {
	return runs.Run{}, false, nil
}

func (f *fakeRunStore) Statuses(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID) ([]runs.Run, error) {
	return nil, nil
}

func (f *fakeRunStore) FindBySession(ctx context.Context, userID, sessionID uuid.UUID) (runs.Run, bool, error) {
	return runs.Run{}, false, nil
}

func (f *fakeRunStore) ClaimNotification(ctx context.Context, runID uuid.UUID) (bool, error) {
	f.claimArgs = append(f.claimArgs, runID)
	return f.claimed, f.claimErr
}

type fakeSettingsReader struct {
	mu       sync.Mutex
	settings *locitypes.NotificationSettings
	err      error
	calls    int
}

func (f *fakeSettingsReader) GetNotificationSettings(ctx context.Context, userID uuid.UUID) (*locitypes.NotificationSettings, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.settings, f.err
}

func (f *fakeSettingsReader) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeDeviceStore struct {
	mu      sync.Mutex
	devices []Device
	// byPlatform, when set, answers ForUser per platform; devices is the
	// answer for every platform otherwise.
	byPlatform map[string][]Device
	err        error
	removed    []string
}

func (f *fakeDeviceStore) Upsert(ctx context.Context, d Device, userAgent string) error {
	return nil
}

func (f *fakeDeviceStore) Remove(ctx context.Context, userID uuid.UUID, endpoint string) error {
	return nil
}

func (f *fakeDeviceStore) RemoveEndpoint(ctx context.Context, endpoint string) error {
	f.mu.Lock()
	f.removed = append(f.removed, endpoint)
	f.mu.Unlock()
	return nil
}

func (f *fakeDeviceStore) ForUser(ctx context.Context, userID uuid.UUID, platform string) ([]Device, error) {
	if f.byPlatform != nil {
		return f.byPlatform[platform], f.err
	}
	return f.devices, f.err
}

func (f *fakeDeviceStore) removedEndpoints() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.removed))
	copy(out, f.removed)
	return out
}

type sendResult struct {
	gone bool
	err  error
}

type fakeSender struct {
	mu      sync.Mutex
	results map[string]sendResult // keyed by device endpoint
	sent    []Device
	bodies  map[string][]byte
}

func newFakeSender() *fakeSender {
	return &fakeSender{results: map[string]sendResult{}, bodies: map[string][]byte{}}
}

func (f *fakeSender) Send(ctx context.Context, d Device, body []byte) (bool, error) {
	f.mu.Lock()
	f.sent = append(f.sent, d)
	f.bodies[d.Endpoint] = body
	res := f.results[d.Endpoint]
	f.mu.Unlock()
	return res.gone, res.err
}

func (f *fakeSender) sentTo() []Device {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Device, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *fakeSender) bodyFor(endpoint string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[endpoint]
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testRun() runs.Run {
	return runs.Run{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		SessionID: uuid.New(),
		Domain:    "itinerary",
		CityName:  "Lisbon",
		Status:    runs.StatusDone,
	}
}

// --- tests ---

func TestNotifier_ClaimFailed_NoSendNoSettingsRead(t *testing.T) {
	claims := &fakeRunStore{claimed: false}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: true}}
	devices := &fakeDeviceStore{devices: []Device{{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/x"}}}
	sender := newFakeSender()

	n := NewNotifier(claims, settings, devices, sender, testLogger())
	n.OnRunFinished(context.Background(), testRun())
	n.wait()

	require.Equal(t, 0, settings.callCount())
	require.Empty(t, sender.sentTo())
}

func TestNotifier_SearchFinishedOff_NoSend(t *testing.T) {
	claims := &fakeRunStore{claimed: true}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: false}}
	devices := &fakeDeviceStore{devices: []Device{{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/x"}}}
	sender := newFakeSender()

	n := NewNotifier(claims, settings, devices, sender, testLogger())
	n.OnRunFinished(context.Background(), testRun())
	n.wait()

	require.Empty(t, sender.sentTo())
}

func TestNotifier_OneDeviceGone_RemovedOnlyThatOne(t *testing.T) {
	claims := &fakeRunStore{claimed: true}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: true}}
	goneDevice := Device{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/gone"}
	okDevice := Device{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/ok"}
	devices := &fakeDeviceStore{devices: []Device{goneDevice, okDevice}}
	sender := newFakeSender()
	sender.results[goneDevice.Endpoint] = sendResult{gone: true}
	sender.results[okDevice.Endpoint] = sendResult{gone: false}

	n := NewNotifier(claims, settings, devices, sender, testLogger())
	n.OnRunFinished(context.Background(), testRun())
	n.wait()

	require.Equal(t, []string{goneDevice.Endpoint}, devices.removedEndpoints())
	require.Len(t, sender.sentTo(), 2)
}

func TestNotifier_SenderError_LoggedNoPanicOtherDeviceStillSent(t *testing.T) {
	claims := &fakeRunStore{claimed: true}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: true}}
	failing := Device{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/fail"}
	ok := Device{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/ok"}
	devices := &fakeDeviceStore{devices: []Device{failing, ok}}
	sender := newFakeSender()
	sender.results[failing.Endpoint] = sendResult{err: errors.New("boom")}

	n := NewNotifier(claims, settings, devices, sender, testLogger())
	require.NotPanics(t, func() {
		n.OnRunFinished(context.Background(), testRun())
		n.wait()
	})

	sent := sender.sentTo()
	require.Len(t, sent, 2)
	require.Empty(t, devices.removedEndpoints())
}

func TestNotifier_SentBodyUnmarshalsToPayloadWithExpectedTitle(t *testing.T) {
	claims := &fakeRunStore{claimed: true}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: true}}
	d := Device{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/x"}
	devices := &fakeDeviceStore{devices: []Device{d}}
	sender := newFakeSender()

	run := testRun()
	n := NewNotifier(claims, settings, devices, sender, testLogger())
	n.OnRunFinished(context.Background(), run)
	n.wait()

	body := sender.bodyFor(d.Endpoint)
	require.NotEmpty(t, body)
	var p Payload
	require.NoError(t, json.Unmarshal(body, &p))
	require.Equal(t, "Your Lisbon itinerary is ready", p.Title)
}

// TestNotifier_InvalidEndpoint_NeverSent is defence in depth: a device row
// whose endpoint fails ValidWebPushEndpoint (e.g. it predates the
// registration-time allow-list, or points at an internal host) must never
// reach the sender, even though registration already enforces the allow-list.
func TestNotifier_InvalidEndpoint_NeverSent(t *testing.T) {
	claims := &fakeRunStore{claimed: true}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: true}}
	bad := Device{ID: uuid.New(), Endpoint: "http://internal.svc.cluster.local/push"}
	good := Device{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/ok"}
	devices := &fakeDeviceStore{devices: []Device{bad, good}}
	sender := newFakeSender()

	n := NewNotifier(claims, settings, devices, sender, testLogger())
	n.OnRunFinished(context.Background(), testRun())
	n.wait()

	sent := sender.sentTo()
	require.Len(t, sent, 1)
	require.Equal(t, good.Endpoint, sent[0].Endpoint)
}

func TestNotifier_NilSession_NoClaimNoSend(t *testing.T) {
	claims := &fakeRunStore{claimed: true}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: true}}
	devices := &fakeDeviceStore{devices: []Device{{ID: uuid.New(), Endpoint: "https://fcm.googleapis.com/x"}}}
	sender := newFakeSender()

	run := testRun()
	run.SessionID = uuid.Nil
	n := NewNotifier(claims, settings, devices, sender, testLogger())
	n.OnRunFinished(context.Background(), run)
	n.wait()

	require.Empty(t, claims.claimArgs, "the one claim must not be spent")
	require.Equal(t, 0, settings.callCount())
	require.Empty(t, sender.sentTo())
}

// Each platform's devices go to that platform's sender, and a platform with
// no sender is skipped without touching its devices.
func TestNotifier_RoutesEachPlatformToItsSender(t *testing.T) {
	claims := &fakeRunStore{claimed: true}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: true}}
	web := Device{ID: uuid.New(), Platform: PlatformWebPush, Endpoint: "https://fcm.googleapis.com/x"}
	phone := Device{ID: uuid.New(), Platform: PlatformAPNS, Endpoint: apnsDevice().Endpoint, APNSTopic: "com.example.app"}
	devices := &fakeDeviceStore{byPlatform: map[string][]Device{PlatformWebPush: {web}, PlatformAPNS: {phone}}}
	webSender := newFakeSender()
	apnsSender := newFakeSender()

	n := NewNotifier(claims, settings, devices, webSender, testLogger()).WithSender(PlatformAPNS, apnsSender)
	n.OnRunFinished(context.Background(), testRun())
	n.wait()

	require.Equal(t, []Device{web}, webSender.sentTo())
	require.Equal(t, []Device{phone}, apnsSender.sentTo())

	// APNs only: the web devices are never listed as sendable.
	apnsOnly := newFakeSender()
	n = NewNotifier(claims, settings, devices, nil, testLogger()).WithSender(PlatformAPNS, apnsOnly)
	n.OnRunFinished(context.Background(), testRun())
	n.wait()
	require.Equal(t, []Device{phone}, apnsOnly.sentTo())
}

// A gone APNs token is removed the same way a gone web endpoint is.
func TestNotifier_GoneAPNSTokenRemoved(t *testing.T) {
	claims := &fakeRunStore{claimed: true}
	settings := &fakeSettingsReader{settings: &locitypes.NotificationSettings{SearchFinished: true}}
	phone := Device{ID: uuid.New(), Platform: PlatformAPNS, Endpoint: apnsDevice().Endpoint, APNSTopic: "com.example.app"}
	devices := &fakeDeviceStore{byPlatform: map[string][]Device{PlatformAPNS: {phone}}}
	apnsSender := newFakeSender()
	apnsSender.results[phone.Endpoint] = sendResult{gone: true}

	n := NewNotifier(claims, settings, devices, nil, testLogger()).WithSender(PlatformAPNS, apnsSender)
	n.OnRunFinished(context.Background(), testRun())
	n.wait()

	require.Equal(t, []string{phone.Endpoint}, devices.removedEndpoints())
}
