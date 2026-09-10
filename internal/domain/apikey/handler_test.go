package apikey

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	apikeyv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/apikey"
)

// fakeService records what the handler asked it to mint. The real service is
// exercised in service_test.go; this one only has to echo the request back so
// the tests can see what reached it.
type fakeService struct {
	createdKind ClientKind
	createdName string
	createErr   error
}

func (f *fakeService) Create(_ context.Context, userID uuid.UUID, name string, _ *time.Time, scopes []Scope, kind ClientKind) (*Key, string, error) {
	if f.createErr != nil {
		return nil, "", f.createErr
	}
	f.createdKind = kind
	f.createdName = name
	return &Key{
		ID: uuid.New(), UserID: userID, Name: name, KeyPrefix: "loci_sk_abcd",
		CreatedAt: time.Now(), Scopes: scopes, ClientKind: kind,
	}, "loci_sk_abcd0123456789", nil
}

func (f *fakeService) List(context.Context, uuid.UUID) ([]Key, error)     { return nil, nil }
func (f *fakeService) Revoke(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeService) Authenticate(context.Context, string) (*Key, error) {
	return nil, ErrNotFound
}

func authed(t *testing.T) context.Context {
	t.Helper()
	return interceptors.ContextWithClaims(t.Context(), &interceptors.Claims{UserID: uuid.NewString()})
}

func newTestHandler(svc Service) *Handler {
	return NewHandler(svc, slog.New(slog.DiscardHandler)).WithSetup(testWriter())
}

func TestCreateEchoesTheKindAndReturnsTheSetup(t *testing.T) {
	svc := &fakeService{}
	h := newTestHandler(svc)

	resp, err := h.CreateApiKey(authed(t), connect.NewRequest(&apikeyv1.CreateApiKeyRequest{
		Name: "Laptop", ClientKind: "cursor",
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if svc.createdKind != ClientCursor {
		t.Errorf("the service was asked to mint %q, want %q", svc.createdKind, ClientCursor)
	}
	if got := resp.Msg.GetApiKey().GetClientKind(); got != "cursor" {
		t.Errorf("the returned key says %q, want cursor", got)
	}

	// The setup is the one thing the caller cannot ask for later: it is built
	// on the plaintext, which is never stored.
	setup := resp.Msg.GetSetup()
	if setup == nil {
		t.Fatal("no setup instructions came back with the key")
	}
	if setup.GetClientKind() != "cursor" {
		t.Errorf("setup is for %q, want cursor", setup.GetClientKind())
	}
	if !strings.Contains(setup.GetConfig(), resp.Msg.GetPlaintextKey()) {
		t.Error("the setup config does not carry the plaintext key")
	}
	if !strings.Contains(setup.GetPrompt(), resp.Msg.GetPlaintextKey()) {
		t.Error("the setup prompt does not carry the plaintext key")
	}
	if setup.GetEndpoint() != testWriter().Endpoint() {
		t.Errorf("endpoint = %q", setup.GetEndpoint())
	}
}

// Silently minting an "other" key for an unknown kind would tell the caller
// their choice was accepted when it was discarded.
func TestAnUnknownKindIsRefused(t *testing.T) {
	svc := &fakeService{}
	h := newTestHandler(svc)

	_, err := h.CreateApiKey(authed(t), connect.NewRequest(&apikeyv1.CreateApiKeyRequest{
		Name: "Laptop", ClientKind: "emacs",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (err %v)", connect.CodeOf(err), err)
	}
	if svc.createdKind != "" {
		t.Error("a key was minted despite the refusal")
	}
}

// A caller that does not know about client kinds still gets a working key.
func TestAnEmptyKindMeansOther(t *testing.T) {
	svc := &fakeService{}
	h := newTestHandler(svc)

	resp, err := h.CreateApiKey(authed(t), connect.NewRequest(&apikeyv1.CreateApiKeyRequest{Name: "Laptop"}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if svc.createdKind != ClientOther {
		t.Errorf("kind = %q, want other", svc.createdKind)
	}
	if resp.Msg.GetSetup() == nil {
		t.Error("no setup for the generic client")
	}
}

// A handler wired without a writer can still mint keys — the instructions are
// a convenience, the key is the product — so their absence must not fail the
// request that created a credential.
func TestCreateWithoutAWriterStillReturnsTheKey(t *testing.T) {
	h := NewHandler(&fakeService{}, slog.New(slog.DiscardHandler))

	resp, err := h.CreateApiKey(authed(t), connect.NewRequest(&apikeyv1.CreateApiKeyRequest{Name: "Laptop"}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.Msg.GetPlaintextKey() == "" {
		t.Error("no key")
	}
	if resp.Msg.GetSetup() != nil {
		t.Error("setup was returned with nothing to build it from")
	}
}

func TestThePreviewCarriesThePlaceholderNotAKey(t *testing.T) {
	h := newTestHandler(&fakeService{})

	resp, err := h.GetSetupInstructions(authed(t), connect.NewRequest(&apikeyv1.GetSetupInstructionsRequest{
		ClientKind: "claude_desktop",
	}))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	ins := resp.Msg.GetInstructions()
	if ins.GetClientKind() != "claude_desktop" {
		t.Errorf("preview is for %q", ins.GetClientKind())
	}
	blob := ins.GetConfig() + ins.GetSafe() + ins.GetExportLine() + ins.GetPrompt()
	if !strings.Contains(blob, PlaceholderToken) {
		t.Error("the preview does not contain the placeholder")
	}
	if strings.Contains(blob, KeyPrefix) {
		t.Error("the preview contains something shaped like a real key")
	}
	if ins.GetSafeNote() == "" || ins.GetConfigLabel() == "" {
		t.Error("labels and notes were dropped on the way to the wire")
	}
}

func TestThePreviewRefusesAnUnknownKind(t *testing.T) {
	h := newTestHandler(&fakeService{})

	_, err := h.GetSetupInstructions(authed(t), connect.NewRequest(&apikeyv1.GetSetupInstructionsRequest{
		ClientKind: "emacs",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

// Unavailable rather than Unimplemented: the RPC exists, this deployment has
// no address to write into it.
func TestThePreviewWithoutAWriterIsUnavailable(t *testing.T) {
	h := NewHandler(&fakeService{}, slog.New(slog.DiscardHandler))

	_, err := h.GetSetupInstructions(authed(t), connect.NewRequest(&apikeyv1.GetSetupInstructionsRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", connect.CodeOf(err))
	}
}

func TestEveryRPCRequiresACaller(t *testing.T) {
	h := newTestHandler(&fakeService{})

	if _, err := h.CreateApiKey(t.Context(), connect.NewRequest(&apikeyv1.CreateApiKeyRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("create: code = %v", connect.CodeOf(err))
	}
	if _, err := h.GetSetupInstructions(t.Context(), connect.NewRequest(&apikeyv1.GetSetupInstructionsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("preview: code = %v", connect.CodeOf(err))
	}
}

func TestAServiceFailureIsNotEchoedToTheCaller(t *testing.T) {
	h := newTestHandler(&fakeService{createErr: errors.New("pq: relation api_keys does not exist")})

	_, err := h.CreateApiKey(authed(t), connect.NewRequest(&apikeyv1.CreateApiKeyRequest{Name: "Laptop"}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if strings.Contains(err.Error(), "relation") {
		t.Error("the database error reached the caller")
	}
}
