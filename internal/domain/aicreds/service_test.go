package aicreds

import (
	"context"
	"crypto/rand"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/secret"
)

// row is what the fake keeps: the same split the real table has, with the
// sealed key beside the metadata rather than inside it.
type row struct {
	cred   Credential
	sealed Sealed
}

type fakeRepo struct {
	rows map[uuid.UUID]*row
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[uuid.UUID]*row{}} }

func (f *fakeRepo) Get(_ context.Context, userID uuid.UUID) (Credential, error) {
	r, ok := f.rows[userID]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return r.cred, nil
}

func (f *fakeRepo) SealedKey(_ context.Context, userID uuid.UUID) (Credential, Sealed, error) {
	r, ok := f.rows[userID]
	if !ok {
		return Credential{}, nil, ErrNotFound
	}
	return r.cred, r.sealed, nil
}

func (f *fakeRepo) Upsert(_ context.Context, userID uuid.UUID, provider string, key Sealed, hint, model, baseURL string) (Credential, error) {
	c := Credential{
		UserID: userID, Provider: provider, KeyHint: hint,
		Model: model, BaseURL: baseURL,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.rows[userID] = &row{cred: c, sealed: key}
	return c, nil
}

func (f *fakeRepo) UpdateSettings(_ context.Context, userID uuid.UUID, model, baseURL string) (Credential, error) {
	r, ok := f.rows[userID]
	if !ok {
		return Credential{}, ErrNotFound
	}
	r.cred.Model = model
	r.cred.BaseURL = baseURL
	r.cred.LastError = ""
	r.cred.LastErrorAt = nil
	return r.cred, nil
}

func (f *fakeRepo) Delete(_ context.Context, userID uuid.UUID) error {
	if _, ok := f.rows[userID]; !ok {
		return ErrNotFound
	}
	delete(f.rows, userID)
	return nil
}

func (f *fakeRepo) RecordError(_ context.Context, userID uuid.UUID, reason string) error {
	r, ok := f.rows[userID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	r.cred.LastError = reason
	r.cred.LastErrorAt = &now
	return nil
}

func sealerWith(t *testing.T, ids ...uint8) *secret.Sealer {
	t.Helper()
	keys := make([]secret.Key, 0, len(ids))
	for _, id := range ids {
		material := make([]byte, secret.KeySize)
		if _, err := rand.Read(material); err != nil {
			t.Fatalf("random key: %v", err)
		}
		keys = append(keys, secret.Key{ID: id, Material: material})
	}
	s, err := secret.NewSealer(keys...)
	if err != nil {
		t.Fatalf("new sealer: %v", err)
	}
	return s
}

func newService(t *testing.T) (*Service, *fakeRepo) {
	t.Helper()
	repo := newFakeRepo()
	return NewService(repo, sealerWith(t, 1)), repo
}

const testKey = "sk-or-v1-0123456789abcdef0123456789abcdefa203"

// The point of the whole package. Cheap, and the only test that catches a
// service which quietly stopped sealing.
func TestTheStoredKeyIsCiphertext(t *testing.T) {
	svc, repo := newService(t)
	user := uuid.New()

	if _, err := svc.Save(context.Background(), user, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("save: %v", err)
	}

	stored := repo.rows[user].sealed
	if len(stored) == 0 {
		t.Fatal("nothing was stored")
	}
	if strings.Contains(string(stored), testKey) {
		t.Fatal("the stored column contains the plaintext key")
	}
}

// Credential is what Get returns, what the handler renders and what the
// settings page shows. A key reaching any field of it reaches all three.
func TestNoFieldOfACredentialCanHoldTheKey(t *testing.T) {
	svc, _ := newService(t)
	user := uuid.New()

	cred, err := svc.Save(context.Background(), user, Input{Provider: "openrouter", APIKey: testKey})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	v := reflect.ValueOf(cred)
	for i := range v.NumField() {
		field := v.Field(i)
		if field.Kind() != reflect.String {
			continue
		}
		if s := field.String(); s != "" && strings.Contains(testKey, s) && len(s) > hintLen {
			t.Errorf("%s = %q, which is a slice of the key", v.Type().Field(i).Name, s)
		}
	}
	if cred.KeyHint != "a203" {
		t.Errorf("KeyHint = %q, want the last four characters", cred.KeyHint)
	}
}

func TestSaveThenResolveRoundTrips(t *testing.T) {
	svc, _ := newService(t)
	user := uuid.New()
	ctx := context.Background()

	if _, err := svc.Save(ctx, user, Input{Provider: "openrouter", APIKey: testKey, Model: "anthropic/claude-opus-4"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := svc.Resolve(ctx, user)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.APIKey != testKey {
		t.Error("the resolved key differs from the saved one")
	}
	if got.Provider != "openrouter" || got.Model != "anthropic/claude-opus-4" {
		t.Errorf("resolved %s/%s, want openrouter/anthropic/claude-opus-4", got.Provider, got.Model)
	}
}

// The row-swap: this is why the user id is sealed in as additional data.
func TestACredentialCopiedToAnotherUserWillNotOpen(t *testing.T) {
	svc, repo := newService(t)
	alice, bob := uuid.New(), uuid.New()
	ctx := context.Background()

	if _, err := svc.Save(ctx, alice, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A support query, a bad restore, a copied row.
	copied := *repo.rows[alice]
	copied.cred.UserID = bob
	repo.rows[bob] = &copied

	if _, err := svc.Resolve(ctx, bob); err == nil {
		t.Fatal("Alice's key opened for Bob; a copied row would bill one account through another's provider")
	}
}

// Rotating the encryption key out from under a stored credential must be an
// error the account can see, not a fallback that looks like it worked.
func TestAKeySealedUnderADroppedKeyFailsLoudly(t *testing.T) {
	repo := newFakeRepo()
	user := uuid.New()
	ctx := context.Background()

	old := NewService(repo, sealerWith(t, 1))
	if _, err := old.Save(ctx, user, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The old key is gone; only key 2 is configured.
	rotated := NewService(repo, sealerWith(t, 2))
	if _, err := rotated.Resolve(ctx, user); err == nil {
		t.Fatal("a credential sealed under a dropped key resolved successfully")
	}
}

func TestWithoutASealerNothingCanBeStored(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	ctx := context.Background()

	if svc.Enabled() {
		t.Error("Enabled reports true with no sealer")
	}
	if _, err := svc.Save(ctx, uuid.New(), Input{Provider: "openrouter", APIKey: testKey}); !errors.Is(err, ErrSealingUnavailable) {
		t.Errorf("save error = %v, want ErrSealingUnavailable", err)
	}
	// Resolve is the read path every request takes. With no sealer it must say
	// "no credential" so the account falls back, not raise an error that would
	// fail requests for everyone.
	if _, err := svc.Resolve(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("resolve error = %v, want ErrNotFound", err)
	}
}

// The form leaves the key field blank when somebody only edits the model.
func TestSavingWithABlankKeyKeepsTheStoredOne(t *testing.T) {
	svc, _ := newService(t)
	user := uuid.New()
	ctx := context.Background()

	if _, err := svc.Save(ctx, user, Input{Provider: "openrouter", APIKey: testKey, Model: "a/b"}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := svc.Save(ctx, user, Input{Provider: "openrouter", Model: "c/d"}); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := svc.Resolve(ctx, user)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.APIKey != testKey {
		t.Error("the stored key was lost when the model was edited")
	}
	if got.Model != "c/d" {
		t.Errorf("Model = %q, want the edited c/d", got.Model)
	}
}

// A key issued by OpenRouter is not a key for xAI. Carrying it across would
// store a credential guaranteed to fail, with nothing saying why.
func TestABlankKeyCannotCarryAcrossAProviderChange(t *testing.T) {
	svc, _ := newService(t)
	user := uuid.New()
	ctx := context.Background()

	if _, err := svc.Save(ctx, user, Input{Provider: "openrouter", APIKey: testKey}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := svc.Save(ctx, user, Input{Provider: "xai"}); !errors.Is(err, ErrKeyRequired) {
		t.Errorf("error = %v, want ErrKeyRequired", err)
	}
}

func TestSaveValidates(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown provider", func(t *testing.T) {
		svc, _ := newService(t)
		_, err := svc.Save(ctx, uuid.New(), Input{Provider: "anthropic", APIKey: testKey})
		if !errors.Is(err, ErrUnknownProvider) {
			t.Errorf("error = %v, want ErrUnknownProvider", err)
		}
	})

	t.Run("no key and nothing stored", func(t *testing.T) {
		svc, _ := newService(t)
		_, err := svc.Save(ctx, uuid.New(), Input{Provider: "openrouter"})
		if !errors.Is(err, ErrKeyRequired) {
			t.Errorf("error = %v, want ErrKeyRequired", err)
		}
	})

	t.Run("hermes without a gateway URL", func(t *testing.T) {
		svc, _ := newService(t)
		if _, err := svc.Save(ctx, uuid.New(), Input{Provider: "hermes", APIKey: testKey}); err == nil {
			t.Error("a Hermes credential saved with no gateway URL")
		}
	})

	t.Run("hermes pointed at loopback", func(t *testing.T) {
		svc, _ := newService(t)
		_, err := svc.Save(ctx, uuid.New(), Input{Provider: "hermes", APIKey: testKey, BaseURL: "http://127.0.0.1:8642/v1"})
		if err == nil {
			t.Error("a gateway URL on loopback was stored")
		}
	})
}

func TestHermesCredentialKeepsItsOwnURL(t *testing.T) {
	svc, _ := newService(t)
	user := uuid.New()
	ctx := context.Background()

	// No path: ParseGatewayURL defaults it to /v1.
	if _, err := svc.Save(ctx, user, Input{
		Provider: "hermes", APIKey: testKey,
		BaseURL: "http://hermes-vps-2.tail562587.ts.net:8642",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := svc.Resolve(ctx, user)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.BaseURL != "http://hermes-vps-2.tail562587.ts.net:8642/v1" {
		t.Errorf("BaseURL = %q, want the normalised gateway URL", got.BaseURL)
	}
	if got.Model != "hermes-3" {
		t.Errorf("Model = %q, want the catalogue default", got.Model)
	}
}

// A base URL supplied for a provider whose address we already know must not be
// dialled, whatever the form happened to still hold.
func TestABaseURLIsIgnoredForProvidersThatDoNotTakeOne(t *testing.T) {
	svc, _ := newService(t)
	user := uuid.New()
	ctx := context.Background()

	if _, err := svc.Save(ctx, user, Input{
		Provider: "openrouter", APIKey: testKey,
		BaseURL: "http://169.254.169.254/latest/meta-data",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := svc.Resolve(ctx, user)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty; OpenRouter's address is not the user's to set", got.BaseURL)
	}
}

func TestResolveReportsNoCredentialRatherThanFailing(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.Resolve(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound; most accounts run on Loci's own key", err)
	}
}

func TestAKeyTooShortToHintLeavesTheHintEmpty(t *testing.T) {
	if got := hint("abc"); got != "" {
		t.Errorf("hint(%q) = %q, want empty rather than the whole value", "abc", got)
	}
	if got := hint(testKey); got != "a203" {
		t.Errorf("hint = %q, want a203", got)
	}
}
