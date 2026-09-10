package aicreds

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/ai/providers"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	aicredsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/aicreds"
)

func asCaller(t *testing.T) context.Context {
	t.Helper()
	return interceptors.ContextWithClaims(t.Context(), &interceptors.Claims{UserID: uuid.NewString()})
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// With no encryption key there is no service. The page must be told that,
// not shown an error it can only render as a spinner.
func TestWithoutAServiceEveryReadSaysDisabled(t *testing.T) {
	h := NewHandler(nil, quiet())
	ctx := asCaller(t)

	get, err := h.GetCredential(ctx, connect.NewRequest(&aicredsv1.GetCredentialRequest{}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if get.Msg.GetEnabled() || get.Msg.GetCredential() != nil {
		t.Errorf("get = %+v, want enabled=false and no credential", get.Msg)
	}

	list, err := h.ListProviders(ctx, connect.NewRequest(&aicredsv1.ListProvidersRequest{}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list.Msg.GetEnabled() {
		t.Error("providers listed as enabled with no service")
	}
	if len(list.Msg.GetProviders()) != len(providers.Catalog) {
		t.Errorf("%d providers listed, want the whole catalogue (%d)", len(list.Msg.GetProviders()), len(providers.Catalog))
	}

	if _, err := h.DeleteCredential(ctx, connect.NewRequest(&aicredsv1.DeleteCredentialRequest{})); err != nil {
		t.Errorf("delete with nothing stored anywhere: %v", err)
	}
}

func TestWithoutAServiceWritesAreFailedPrecondition(t *testing.T) {
	h := NewHandler(nil, quiet())
	ctx := asCaller(t)

	_, err := h.SaveCredential(ctx, connect.NewRequest(&aicredsv1.SaveCredentialRequest{Provider: "openrouter", ApiKey: testKey}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("save: code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "encryption key") {
		t.Errorf("save: the error does not say why: %v", err)
	}

	_, err = h.VerifyCredential(ctx, connect.NewRequest(&aicredsv1.VerifyCredentialRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("verify: code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestSaveThenGetShowsTheHintAndNeverTheKey(t *testing.T) {
	svc, _ := newService(t)
	h := NewHandler(svc, quiet())
	ctx := asCaller(t)

	saved, err := h.SaveCredential(ctx, connect.NewRequest(&aicredsv1.SaveCredentialRequest{
		Provider: "openrouter", ApiKey: testKey, Model: "deepseek/deepseek-v4-flash",
	}))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Msg.GetCredential().GetKeyHint() != testKey[len(testKey)-4:] {
		t.Errorf("hint = %q", saved.Msg.GetCredential().GetKeyHint())
	}
	if strings.Contains(saved.Msg.String(), testKey) {
		t.Fatal("the save response carries the key")
	}

	got, err := h.GetCredential(ctx, connect.NewRequest(&aicredsv1.GetCredentialRequest{}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Msg.GetEnabled() {
		t.Error("enabled = false with a service")
	}
	cred := got.Msg.GetCredential()
	if cred.GetProvider() != "openrouter" || cred.GetModel() != "deepseek/deepseek-v4-flash" {
		t.Errorf("credential = %+v", cred)
	}
	if strings.Contains(got.Msg.String(), testKey) {
		t.Fatal("the get response carries the key")
	}
}

func TestNothingStoredIsAnEmptyAnswerNotAnError(t *testing.T) {
	svc, _ := newService(t)
	h := NewHandler(svc, quiet())

	got, err := h.GetCredential(asCaller(t), connect.NewRequest(&aicredsv1.GetCredentialRequest{}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Msg.GetEnabled() || got.Msg.GetCredential() != nil {
		t.Errorf("got %+v, want enabled and empty", got.Msg)
	}
}

// Everything the person can fix comes back as InvalidArgument with a sentence
// naming what to change.
func TestBadInputIsInvalidArgumentWithAReason(t *testing.T) {
	cases := []struct {
		name string
		req  *aicredsv1.SaveCredentialRequest
		want string
	}{
		{"unknown provider", &aicredsv1.SaveCredentialRequest{Provider: "anthropic", ApiKey: testKey}, "not a provider"},
		{"no key and nothing stored", &aicredsv1.SaveCredentialRequest{Provider: "openrouter"}, "key is required"},
		{"hermes without a gateway", &aicredsv1.SaveCredentialRequest{Provider: "hermes", ApiKey: "gw"}, ""},
		{"hermes on loopback", &aicredsv1.SaveCredentialRequest{Provider: "hermes", ApiKey: "gw", BaseUrl: "http://127.0.0.1:1/v1"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newService(t)
			h := NewHandler(svc, quiet())

			_, err := h.SaveCredential(asCaller(t), connect.NewRequest(tc.req))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (%v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
			if strings.HasPrefix(connect.NewError(connect.CodeInvalidArgument, err).Message(), "aicreds:") {
				t.Errorf("the package prefix leaked into user text: %q", err)
			}
		})
	}
}

func TestARejectedKeyIsInvalidArgumentAndNotStored(t *testing.T) {
	svc, repo := newService(t)
	svc.WithVerifier(&stubVerifier{err: ErrKeyRejected}, nil)
	h := NewHandler(svc, quiet())

	_, err := h.SaveCredential(asCaller(t), connect.NewRequest(&aicredsv1.SaveCredentialRequest{Provider: "openrouter", ApiKey: testKey}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error = %q does not say the provider rejected it", err)
	}
	if len(repo.rows) != 0 {
		t.Error("the rejected key was stored")
	}
}

func TestVerifyMapsEachOutcomeToTheWire(t *testing.T) {
	cases := []struct {
		name        string
		verifier    error
		provider    string
		ok, checked bool
	}{
		{"accepted", nil, "openrouter", true, true},
		{"rejected", ErrKeyRejected, "openrouter", false, true},
		{"provider down", errors.New("aicreds: the provider could not answer"), "openrouter", false, false},
		{"no verify path", nil, "xai", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newService(t)
			// Saved before the verifier is attached so the save itself is not
			// what gets refused in the rejected case.
			ctx := asCaller(t)
			h := NewHandler(svc, quiet())
			if _, err := h.SaveCredential(ctx, connect.NewRequest(&aicredsv1.SaveCredentialRequest{Provider: tc.provider, ApiKey: testKey})); err != nil {
				t.Fatalf("save: %v", err)
			}
			svc.WithVerifier(&stubVerifier{err: tc.verifier}, nil)

			got, err := h.VerifyCredential(ctx, connect.NewRequest(&aicredsv1.VerifyCredentialRequest{}))
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if got.Msg.GetOk() != tc.ok || got.Msg.GetChecked() != tc.checked {
				t.Errorf("got ok=%v checked=%v, want ok=%v checked=%v", got.Msg.GetOk(), got.Msg.GetChecked(), tc.ok, tc.checked)
			}
			if !tc.ok && got.Msg.GetError() == "" {
				t.Error("a non-ok answer with no reason for the person")
			}
		})
	}
}

func TestVerifyWithNothingStoredIsNotFound(t *testing.T) {
	svc, _ := newService(t)
	svc.WithVerifier(&stubVerifier{}, nil)
	h := NewHandler(svc, quiet())

	_, err := h.VerifyCredential(asCaller(t), connect.NewRequest(&aicredsv1.VerifyCredentialRequest{}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestDeleteReturnsTheAccountToLocisProvider(t *testing.T) {
	svc, repo := newService(t)
	h := NewHandler(svc, quiet())
	ctx := asCaller(t)

	if _, err := h.SaveCredential(ctx, connect.NewRequest(&aicredsv1.SaveCredentialRequest{Provider: "openrouter", ApiKey: testKey})); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := h.DeleteCredential(ctx, connect.NewRequest(&aicredsv1.DeleteCredentialRequest{})); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(repo.rows) != 0 {
		t.Error("the credential is still stored")
	}
	// Deleting twice is not an error: the second call finds the state it
	// wanted.
	if _, err := h.DeleteCredential(ctx, connect.NewRequest(&aicredsv1.DeleteCredentialRequest{})); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestEveryRPCRequiresACaller(t *testing.T) {
	h := NewHandler(nil, quiet())
	ctx := t.Context()

	calls := map[string]func() error{
		"get": func() error {
			_, err := h.GetCredential(ctx, connect.NewRequest(&aicredsv1.GetCredentialRequest{}))
			return err
		},
		"save": func() error {
			_, err := h.SaveCredential(ctx, connect.NewRequest(&aicredsv1.SaveCredentialRequest{}))
			return err
		},
		"delete": func() error {
			_, err := h.DeleteCredential(ctx, connect.NewRequest(&aicredsv1.DeleteCredentialRequest{}))
			return err
		},
		"list": func() error {
			_, err := h.ListProviders(ctx, connect.NewRequest(&aicredsv1.ListProvidersRequest{}))
			return err
		},
		"verify": func() error {
			_, err := h.VerifyCredential(ctx, connect.NewRequest(&aicredsv1.VerifyCredentialRequest{}))
			return err
		},
	}
	for name, call := range calls {
		if err := call(); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("%s: code = %v, want Unauthenticated", name, connect.CodeOf(err))
		}
	}
}

func TestSupportsVerificationFollowsTheCatalogue(t *testing.T) {
	h := NewHandler(nil, quiet())

	list, err := h.ListProviders(asCaller(t), connect.NewRequest(&aicredsv1.ListProvidersRequest{}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, p := range list.Msg.GetProviders() {
		entry, _ := providers.ByName(p.GetName())
		if p.GetSupportsVerification() != (entry.VerifyPath != "") {
			t.Errorf("%s: supports_verification = %v, catalogue verify path %q", p.GetName(), p.GetSupportsVerification(), entry.VerifyPath)
		}
		if p.GetRequiresBaseUrl() != entry.RequiresBaseURL {
			t.Errorf("%s: requires_base_url = %v", p.GetName(), p.GetRequiresBaseUrl())
		}
	}
}
