package aicreds

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/ai/providers"
)

// stubVerifier answers with whatever it was told to and remembers the ask.
type stubVerifier struct {
	err error

	mu    sync.Mutex
	calls int
	last  providers.BYOProvider
	key   string
}

func (v *stubVerifier) Verify(_ context.Context, entry providers.BYOProvider, key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	v.last = entry
	v.key = key
	return v.err
}

// fakeProvider is a stand-in for a vendor's API that answers one status.
func fakeProvider(t *testing.T, status int) (*httptest.Server, *http.Request) {
	t.Helper()
	var seen http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r
		w.WriteHeader(status)
		// A body that would be dangerous to echo anywhere. The verifier must
		// not read it, and certainly must not put it in an error.
		_, _ = w.Write([]byte(`{"error":"bad key ` + r.Header.Get("Authorization") + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func entryAt(base string) providers.BYOProvider {
	return providers.BYOProvider{Name: "openrouter", Label: "OpenRouter", BaseURL: base, VerifyPath: "/key"}
}

// The one outcome the user can act on, and the only one that blocks a save.
func TestAProviderSayingNoIsARejection(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv, _ := fakeProvider(t, status)

		err := NewHTTPVerifier(nil).Verify(t.Context(), entryAt(srv.URL), "sk-bad")
		if !errors.Is(err, ErrKeyRejected) {
			t.Errorf("status %d: error = %v, want ErrKeyRejected", status, err)
		}
	}
}

// A 404 from a backend that routes /models differently is not a bad key, and
// refusing on it would block a working one.
func TestAnyOtherAnswerIsAcceptance(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest, http.StatusTooManyRequests} {
		srv, _ := fakeProvider(t, status)

		if err := NewHTTPVerifier(nil).Verify(t.Context(), entryAt(srv.URL), "sk-good"); err != nil {
			t.Errorf("status %d: error = %v, want acceptance", status, err)
		}
	}
}

// A provider having a bad minute is not evidence about the key.
func TestAProviderThatCannotAnswerIsNotARejection(t *testing.T) {
	t.Run("5xx", func(t *testing.T) {
		srv, _ := fakeProvider(t, http.StatusBadGateway)
		err := NewHTTPVerifier(nil).Verify(t.Context(), entryAt(srv.URL), "sk-good")
		if err == nil {
			t.Fatal("a 502 was read as acceptance; it is unknown")
		}
		if errors.Is(err, ErrKeyRejected) {
			t.Fatal("a 502 was read as rejection")
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		srv, _ := fakeProvider(t, http.StatusOK)
		srv.Close()
		err := NewHTTPVerifier(nil).Verify(t.Context(), entryAt(srv.URL), "sk-good")
		if err == nil || errors.Is(err, ErrKeyRejected) {
			t.Fatalf("error = %v; an unreachable provider is unknown, not a verdict", err)
		}
	})
}

func TestTheCheckAsksTheRightQuestion(t *testing.T) {
	srv, seen := fakeProvider(t, http.StatusOK)

	if err := NewHTTPVerifier(nil).Verify(t.Context(), entryAt(srv.URL), "sk-good"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if seen.URL.Path != "/key" {
		t.Errorf("asked %s, want the catalogue's verify path", seen.URL.Path)
	}
	if seen.Method != http.MethodGet {
		t.Errorf("method = %s", seen.Method)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer sk-good" {
		t.Errorf("Authorization = %q", got)
	}
}

// No endpoint to ask means stored unverified, not refused.
func TestAProviderWithNoVerifyPathIsNotAsked(t *testing.T) {
	srv, seen := fakeProvider(t, http.StatusUnauthorized)

	entry := entryAt(srv.URL)
	entry.VerifyPath = ""
	if err := NewHTTPVerifier(nil).Verify(t.Context(), entry, "sk-anything"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if seen.Method != "" {
		t.Error("the provider was called despite having no verify path")
	}
}

// The failure that must never appear: an error carrying what the provider
// echoed back, which here is the key.
func TestNoOutcomeQuotesTheProvidersBody(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusBadGateway} {
		srv, _ := fakeProvider(t, status)
		err := NewHTTPVerifier(nil).Verify(t.Context(), entryAt(srv.URL), "sk-secret-value")
		if err == nil {
			t.Fatalf("status %d: want an error", status)
		}
		if got := err.Error(); containsAny(got, "sk-secret-value", "bad key") {
			t.Errorf("status %d: the error carries the response body: %q", status, got)
		}
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// A gateway the user typed is dialled through the guarded client, so an
// address that slipped past parsing and resolves somewhere forbidden is a
// "could not ask", never a call.
func TestAUserGatewayOnLoopbackIsNotDialled(t *testing.T) {
	srv, seen := fakeProvider(t, http.StatusOK)

	entry := providers.BYOProvider{Name: "hermes", BaseURL: srv.URL, VerifyPath: "/models", RequiresBaseURL: true}
	err := NewHTTPVerifier(nil).Verify(t.Context(), entry, "gateway-key")
	if err == nil || errors.Is(err, ErrKeyRejected) {
		t.Fatalf("error = %v; a refused dial is unknown, not a verdict", err)
	}
	if seen.Method != "" {
		t.Error("the loopback gateway was actually called")
	}
}

// --- Save with a verifier -------------------------------------------------

func TestARejectedKeyIsNotStored(t *testing.T) {
	svc, repo := newService(t)
	svc.WithVerifier(&stubVerifier{err: ErrKeyRejected}, nil)
	user := uuid.New()

	_, err := svc.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey})
	if !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("error = %v, want ErrKeyRejected", err)
	}
	if _, ok := repo.rows[user]; ok {
		t.Error("the rejected key was sealed and stored anyway")
	}
}

// The provider being down is not a reason to refuse a save.
func TestAnUnanswerableCheckStillSaves(t *testing.T) {
	svc, repo := newService(t)
	svc.WithVerifier(&stubVerifier{err: errors.New("aicreds: the provider could not answer")}, nil)
	user := uuid.New()

	if _, err := svc.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := repo.rows[user]; !ok {
		t.Error("nothing was stored")
	}
}

func TestTheVerifierSeesTheProviderAndTheKey(t *testing.T) {
	svc, _ := newService(t)
	v := &stubVerifier{}
	svc.WithVerifier(v, nil)

	if _, err := svc.Save(t.Context(), uuid.New(), Input{Provider: "openai", APIKey: "sk-openai-key-value"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if v.calls != 1 {
		t.Fatalf("verifier called %d times", v.calls)
	}
	if v.last.Name != "openai" || v.last.BaseURL == "" {
		t.Errorf("verifier was given %+v, want the openai catalogue entry with its address", v.last)
	}
	if v.key != "sk-openai-key-value" {
		t.Errorf("verifier was given key %q", v.key)
	}
}

// The catalogue cannot name a Hermes address, so the check goes to the one the
// user typed.
func TestAHermesCheckGoesToTheUsersGateway(t *testing.T) {
	svc, _ := newService(t)
	v := &stubVerifier{}
	svc.WithVerifier(v, nil)

	_, err := svc.Save(t.Context(), uuid.New(), Input{
		Provider: "hermes", APIKey: "gw", BaseURL: "https://hermes-vps-2.tail562587.ts.net",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	// Normalised by ParseGatewayURL on the way in, so the check goes to the
	// same address the chat path will dial.
	if v.last.BaseURL != "https://hermes-vps-2.tail562587.ts.net/v1" {
		t.Errorf("verifier was pointed at %q", v.last.BaseURL)
	}
}

// Keeping the stored key is not a new key and must not be re-checked; it
// would turn "change the model" into a network call to the provider.
func TestKeepingTheStoredKeyDoesNotReverify(t *testing.T) {
	svc, _ := newService(t)
	v := &stubVerifier{}
	svc.WithVerifier(v, nil)
	user := uuid.New()

	if _, err := svc.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := svc.Save(t.Context(), user, Input{Provider: "openrouter", Model: "deepseek/deepseek-v4-flash"}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if v.calls != 1 {
		t.Errorf("verifier called %d times, want once", v.calls)
	}
}

// --- VerifyStored ---------------------------------------------------------

func TestVerifyStoredReportsEachOutcome(t *testing.T) {
	cases := []struct {
		name     string
		verifier error
		provider string
		want     Verification
	}{
		{"accepted", nil, "openrouter", Verification{Checked: true}},
		{"rejected", ErrKeyRejected, "openrouter", Verification{Checked: true, Rejected: true}},
		{"provider down", errors.New("aicreds: the provider could not answer"), "openrouter", Verification{}},
		{"no verify path", nil, "xai", Verification{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newService(t)
			user := uuid.New()
			// Saved before the verifier is attached, so the save itself is
			// not what gets refused in the rejected case.
			if _, err := svc.Save(t.Context(), user, Input{Provider: tc.provider, APIKey: testKey}); err != nil {
				t.Fatalf("save: %v", err)
			}
			svc.WithVerifier(&stubVerifier{err: tc.verifier}, nil)

			got, err := svc.VerifyStored(t.Context(), user)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if got.Checked != tc.want.Checked || got.Rejected != tc.want.Rejected {
				t.Errorf("got %+v, want checked=%v rejected=%v", got, tc.want.Checked, tc.want.Rejected)
			}
			if !(got.Checked && !got.Rejected) && got.Detail == "" {
				t.Error("no detail for the person on a non-clean outcome")
			}
			if containsAny(got.Detail, testKey) {
				t.Error("the detail carries the key")
			}
		})
	}
}

func TestVerifyStoredWithNothingStoredIsNotFound(t *testing.T) {
	svc, _ := newService(t)
	svc.WithVerifier(&stubVerifier{}, nil)

	if _, err := svc.VerifyStored(t.Context(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
