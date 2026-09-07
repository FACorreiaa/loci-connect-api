package aicreds

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// stubClient is a ChatClient that only reports which one it is and whether it
// was closed. Routing is the whole behaviour under test; generation is not.
type stubClient struct {
	model  string
	closed int
}

func (s *stubClient) Generate(context.Context, string, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return nil, nil
}

func (s *stubClient) GenerateText(context.Context, string, *genai.GenerateContentConfig) (string, error) {
	return s.model, nil
}

func (s *stubClient) GenerateStream(context.Context, string, *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	return nil, nil
}

func (s *stubClient) StartChatSession(context.Context, *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	return nil, nil
}

func (s *stubClient) Model() string { return s.model }

func (s *stubClient) Close() error {
	s.closed++
	return nil
}

func authed(t *testing.T, userID uuid.UUID) context.Context {
	t.Helper()
	return interceptors.ContextWithClaims(t.Context(), &interceptors.Claims{UserID: userID.String()})
}

// answered reports which client the router picked, by the model it names.
//
// It asks clientFor rather than generating: routing is the behaviour under
// test, and calling Generate would dial the real provider.
func answered(t *testing.T, r *Router, ctx context.Context) string {
	t.Helper()
	return r.clientFor(ctx).Model()
}

func newRouter(t *testing.T) (*Router, *fakeRepo, *stubClient) {
	t.Helper()
	repo := newFakeRepo()
	shared := &stubClient{model: "shared-model"}
	return NewRouter(shared, NewService(repo, sealerWith(t, 1)), nil), repo, shared
}

// The ordinary case: almost every account has brought no key.
func TestAnAccountWithNoCredentialRunsOnTheSharedProvider(t *testing.T) {
	r, _, _ := newRouter(t)
	if got := answered(t, r, authed(t, uuid.New())); got != "shared-model" {
		t.Errorf("answered by %q, want the shared provider", got)
	}
}

// Background work and unauthenticated paths have no caller to resolve.
func TestAnUnauthenticatedCallRunsOnTheSharedProvider(t *testing.T) {
	r, _, _ := newRouter(t)
	if got := answered(t, r, t.Context()); got != "shared-model" {
		t.Errorf("answered by %q, want the shared provider", got)
	}
}

func TestWithoutAnEncryptionKeyEverythingRunsOnTheSharedProvider(t *testing.T) {
	shared := &stubClient{model: "shared-model"}
	r := NewRouter(shared, NewService(newFakeRepo(), nil), nil)
	if got := answered(t, r, authed(t, uuid.New())); got != "shared-model" {
		t.Errorf("answered by %q, want the shared provider", got)
	}

	// And a nil service is the same state, reached a different way.
	r = NewRouter(shared, nil, nil)
	if got := answered(t, r, authed(t, uuid.New())); got != "shared-model" {
		t.Errorf("answered by %q with a nil service, want the shared provider", got)
	}
}

func TestACredentialIsUsedForItsOwnerAndNobodyElse(t *testing.T) {
	r, _, _ := newRouter(t)
	alice, bob := uuid.New(), uuid.New()

	if _, err := r.svc.Save(t.Context(), alice, Input{
		Provider: "openrouter", APIKey: testKey, Model: "alice-model",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if got := answered(t, r, authed(t, alice)); got != "alice-model" {
		t.Errorf("Alice was answered by %q, want her own provider", got)
	}
	if got := answered(t, r, authed(t, bob)); got != "shared-model" {
		t.Errorf("Bob was answered by %q, want the shared provider", got)
	}
}

// A key saved in settings must take effect on the next request, not the next
// deploy — and the client built from the old one must not outlive it.
func TestChangingTheCredentialReplacesTheCachedClient(t *testing.T) {
	r, repo, _ := newRouter(t)
	user := uuid.New()
	ctx := authed(t, user)

	if _, err := r.svc.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey, Model: "first"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := answered(t, r, ctx); got != "first" {
		t.Fatalf("answered by %q, want first", got)
	}

	cached := r.cached[user].client
	if _, err := r.svc.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey, Model: "second"}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	// UpdateSettings on the fake does not move updated_at by itself; the real
	// column does. Move it the way the database would.
	repo.rows[user].cred.UpdatedAt = repo.rows[user].cred.UpdatedAt.Add(1)

	if got := answered(t, r, ctx); got != "second" {
		t.Errorf("answered by %q, want the edited model", got)
	}
	if r.cached[user].client == cached {
		t.Error("the stale client was kept")
	}
}

// An unchanged credential must not rebuild its client on every call: a Gemini
// client opens its own transport, and one per request is a leak.
func TestAnUnchangedCredentialReusesItsClient(t *testing.T) {
	r, _, _ := newRouter(t)
	user := uuid.New()
	ctx := authed(t, user)

	if _, err := r.svc.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("save: %v", err)
	}

	answered(t, r, ctx)
	first := r.cached[user].client
	answered(t, r, ctx)
	if r.cached[user].client != first {
		t.Error("a second call rebuilt the client")
	}
}

// One bad credential must degrade that account to the shared provider, not
// fail its requests — and the reason must be visible in settings afterwards.
func TestABrokenCredentialFallsBackAndIsRecorded(t *testing.T) {
	repo := newFakeRepo()
	user := uuid.New()

	old := NewService(repo, sealerWith(t, 1))
	if _, err := old.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The encryption key was rotated out from under the stored credential.
	rotated := NewService(repo, sealerWith(t, 2))
	r := NewRouter(&stubClient{model: "shared-model"}, rotated, nil)

	if got := answered(t, r, authed(t, user)); got != "shared-model" {
		t.Errorf("answered by %q, want the shared provider", got)
	}

	cred, err := repo.Get(t.Context(), user)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !cred.Failing() {
		t.Error("the credential is not marked failing; the owner would have no way to learn " +
			"their key stopped working, because the account goes on answering on ours")
	}
}

// A credential that resolves but cannot become a client is the case where the
// failure is worth writing down, since nothing else would show it.
func TestAnUnbuildableCredentialIsRecordedAsFailing(t *testing.T) {
	r, repo, _ := newRouter(t)
	user := uuid.New()

	if _, err := r.svc.Save(t.Context(), user, Input{
		Provider: "hermes", APIKey: testKey,
		BaseURL: "http://hermes-vps-2.tail562587.ts.net:8642/v1",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// The row now names an address this build will not dial — a tightened rule,
	// or a hand-edited row.
	repo.rows[user].cred.BaseURL = "http://127.0.0.1:8642/v1"

	if got := answered(t, r, authed(t, user)); got != "shared-model" {
		t.Errorf("answered by %q, want the shared provider", got)
	}

	cred, err := repo.Get(t.Context(), user)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !cred.Failing() {
		t.Error("the credential is not marked failing; settings would still show it as in use")
	}
	if strings.Contains(cred.LastError, testKey) {
		t.Error("the recorded reason contains the key")
	}
}

func TestCloseClosesTheSharedClientAndEveryUsersOwn(t *testing.T) {
	r, _, shared := newRouter(t)
	user := uuid.New()

	if _, err := r.svc.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("save: %v", err)
	}
	answered(t, r, authed(t, user))

	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if shared.closed != 1 {
		t.Errorf("shared closed %d times, want 1", shared.closed)
	}
	if len(r.cached) != 0 {
		t.Error("cached clients survived Close")
	}
}

// Deleting a credential in settings must stop it serving immediately, not at
// its next change — there will not be one.
func TestDeletingACredentialReleasesItsClient(t *testing.T) {
	r, _, _ := newRouter(t)
	user := uuid.New()

	if _, err := r.svc.Save(t.Context(), user, Input{Provider: "openrouter", APIKey: testKey, Model: "mine"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	answered(t, r, authed(t, user))

	if err := r.svc.Delete(t.Context(), user); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// No Forget call: the next request is what notices, so deleting a
	// credential does not depend on a handler remembering to tell the router.
	if got := answered(t, r, authed(t, user)); got != "shared-model" {
		t.Errorf("answered by %q after deletion, want the shared provider", got)
	}
	if len(r.cached) != 0 {
		t.Error("the client outlived the credential it was built from")
	}
}

func TestModelReportsTheSharedProvider(t *testing.T) {
	r, _, _ := newRouter(t)
	if r.Model() != "shared-model" {
		t.Errorf("Model = %q", r.Model())
	}
}
