package handler

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	userpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/user"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/push"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// The proto pattern checks that a timezone looks like a zone name. Only tzdata
// knows whether one exists, and the difference shows up late: an unresolvable
// zone is a scheduled notification that never fires, at 9pm, with nothing in
// the logs pointing at the profile that caused it.
func TestValidateTimezone(t *testing.T) {
	str := func(s string) *string { return &s }

	t.Run("accepts a real zone", func(t *testing.T) {
		for _, tz := range []string{"Atlantic/Madeira", "Europe/Lisbon", "UTC", "America/New_York"} {
			if err := validateTimezone(str(tz)); err != nil {
				t.Errorf("validateTimezone(%q) = %v, want nil", tz, err)
			}
		}
	})

	// A plausible misspelling passes the proto pattern and resolves to nothing.
	t.Run("refuses a zone that does not exist", func(t *testing.T) {
		for _, tz := range []string{"Atlantic/Madiera", "Europe/Lisboa", "Mars/Olympus"} {
			if err := validateTimezone(str(tz)); err == nil {
				t.Errorf("validateTimezone(%q) = nil, want an error", tz)
			}
		}
	})

	// Nil is a partial update that does not mention the timezone: leave it
	// alone. Empty is "clear it", which is how somebody goes back to letting
	// the browser guess. Neither is a validation failure.
	t.Run("nil and empty are not errors", func(t *testing.T) {
		if err := validateTimezone(nil); err != nil {
			t.Errorf("nil timezone = %v, want nil", err)
		}
		if err := validateTimezone(str("")); err != nil {
			t.Errorf("empty timezone = %v, want nil", err)
		}
	})
}

// A field the request does not set must not be written. Everything on
// UpdateProfileParams is a pointer for that reason, and a mapping that turned
// "absent" into "empty string" would silently clear a user's locale every time
// they edited their bio.
func TestFromUpdateProtoLeavesAbsentLocaleAlone(t *testing.T) {
	bio := "likes levadas"
	params := fromUpdateProto(&userpb.UpdateProfileParams{AboutYou: &bio})

	if params.Timezone != nil {
		t.Errorf("timezone = %q, want nil for a request that did not mention it", *params.Timezone)
	}
	if params.Units != nil {
		t.Errorf("units = %q, want nil", *params.Units)
	}
	if params.Currency != nil {
		t.Errorf("currency = %q, want nil", *params.Currency)
	}
}

func TestFromUpdateProtoCarriesLocale(t *testing.T) {
	tz, units, currency := "Atlantic/Madeira", "imperial", "GBP"
	params := fromUpdateProto(&userpb.UpdateProfileParams{
		Timezone: &tz,
		Units:    &units,
		Currency: &currency,
	})

	if params.Timezone == nil || *params.Timezone != tz {
		t.Errorf("timezone = %v, want %q", params.Timezone, tz)
	}
	if params.Units == nil || *params.Units != units {
		t.Errorf("units = %v, want %q", params.Units, units)
	}
	if params.Currency == nil || *params.Currency != currency {
		t.Errorf("currency = %v, want %q", params.Currency, currency)
	}
}

// fakeDeviceStore records the last Upsert call, which is all these tests
// need to know about the write.
type fakeDeviceStore struct {
	upsertedUserID   uuid.UUID
	upsertedPlatform string
	upsertedEndpoint string
	upsertCalled     bool
}

func (f *fakeDeviceStore) Upsert(_ context.Context, userID uuid.UUID, platform, endpoint, _, _, _ string) error {
	f.upsertCalled = true
	f.upsertedUserID = userID
	f.upsertedPlatform = platform
	f.upsertedEndpoint = endpoint
	return nil
}

func (f *fakeDeviceStore) Remove(context.Context, uuid.UUID, string) error { return nil }
func (f *fakeDeviceStore) RemoveEndpoint(context.Context, string) error    { return nil }
func (f *fakeDeviceStore) ForUser(context.Context, uuid.UUID, string) ([]push.Device, error) {
	return nil, nil
}

func authedCtx(userID uuid.UUID) context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, userID.String())
}

// UNSPECIFIED is not a platform the store knows how to save.
func TestRegisterPushDeviceRejectsUnspecifiedPlatform(t *testing.T) {
	store := &fakeDeviceStore{}
	h := NewUserHandler(&recordingUserService{}).WithPush(store, config.PushConfig{})

	_, err := h.RegisterPushDevice(authedCtx(uuid.New()), connect.NewRequest(&userpb.RegisterPushDeviceRequest{
		Platform: userpb.PushPlatform_PUSH_PLATFORM_UNSPECIFIED,
		Endpoint: "https://push.example.test/x",
	}))
	if err == nil {
		t.Fatal("RegisterPushDevice with UNSPECIFIED = nil error, want InvalidArgument")
	}
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want %v", code, connect.CodeInvalidArgument)
	}
	if store.upsertCalled {
		t.Error("store.Upsert was called for a rejected platform")
	}
}

// Proto validation does not require p256dh/auth, but a web push subscription
// is useless without both — the handler is the only place left to reject it.
func TestRegisterPushDeviceRejectsWebPushWithoutKeys(t *testing.T) {
	store := &fakeDeviceStore{}
	h := NewUserHandler(&recordingUserService{}).WithPush(store, config.PushConfig{})

	_, err := h.RegisterPushDevice(authedCtx(uuid.New()), connect.NewRequest(&userpb.RegisterPushDeviceRequest{
		Platform: userpb.PushPlatform_PUSH_PLATFORM_WEB_PUSH,
		Endpoint: "https://push.example.test/x",
	}))
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want %v", code, connect.CodeInvalidArgument)
	}
	if store.upsertCalled {
		t.Error("store.Upsert was called for web push with no p256dh/auth")
	}
}

// A valid web push registration reaches the store with the right platform name.
func TestRegisterPushDeviceValidCallUpserts(t *testing.T) {
	store := &fakeDeviceStore{}
	h := NewUserHandler(&recordingUserService{}).WithPush(store, config.PushConfig{})

	caller := uuid.New()
	_, err := h.RegisterPushDevice(authedCtx(caller), connect.NewRequest(&userpb.RegisterPushDeviceRequest{
		Platform: userpb.PushPlatform_PUSH_PLATFORM_WEB_PUSH,
		Endpoint: "https://push.example.test/x",
		P256Dh:   "p256dh-key",
		Auth:     "auth-key",
	}))
	if err != nil {
		t.Fatalf("RegisterPushDevice: %v", err)
	}
	if !store.upsertCalled {
		t.Fatal("store.Upsert was not called for a valid registration")
	}
	if store.upsertedPlatform != "web_push" {
		t.Errorf("platform = %q, want %q", store.upsertedPlatform, "web_push")
	}
	if store.upsertedUserID != caller {
		t.Errorf("upserted user = %s, want caller %s", store.upsertedUserID, caller)
	}
	if store.upsertedEndpoint != "https://push.example.test/x" {
		t.Errorf("upserted endpoint = %q", store.upsertedEndpoint)
	}
}

// With no VAPID key configured, GetPushConfig has nothing to hand out.
func TestGetPushConfigDisabledReturnsEmptyKey(t *testing.T) {
	h := NewUserHandler(&recordingUserService{}).WithPush(&fakeDeviceStore{}, config.PushConfig{})

	resp, err := h.GetPushConfig(authedCtx(uuid.New()), connect.NewRequest(&userpb.GetPushConfigRequest{}))
	if err != nil {
		t.Fatalf("GetPushConfig: %v", err)
	}
	if got := resp.Msg.GetVapidPublicKey(); got != "" {
		t.Errorf("VapidPublicKey = %q, want empty when push is disabled", got)
	}
}

// With all three VAPID values set, GetPushConfig hands out the public key.
func TestGetPushConfigEnabledReturnsKey(t *testing.T) {
	cfg := config.PushConfig{
		VAPIDPublicKey:  "pub-key",
		VAPIDPrivateKey: "priv-key",
		VAPIDSubject:    "mailto:push@example.test",
	}
	h := NewUserHandler(&recordingUserService{}).WithPush(&fakeDeviceStore{}, cfg)

	resp, err := h.GetPushConfig(authedCtx(uuid.New()), connect.NewRequest(&userpb.GetPushConfigRequest{}))
	if err != nil {
		t.Fatalf("GetPushConfig: %v", err)
	}
	if got := resp.Msg.GetVapidPublicKey(); got != "pub-key" {
		t.Errorf("VapidPublicKey = %q, want %q", got, "pub-key")
	}
}
