package integrations

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

type key struct {
	userID   uuid.UUID
	provider string
}

type row struct {
	conn   Connection
	sealed Sealed
}

type fakeRepo struct {
	rows map[key]*row
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[key]*row{}} }

func (f *fakeRepo) List(_ context.Context, userID uuid.UUID) ([]Connection, error) {
	var out []Connection
	for k, r := range f.rows {
		if k.userID == userID {
			out = append(out, r.conn)
		}
	}
	return out, nil
}

func (f *fakeRepo) Get(_ context.Context, userID uuid.UUID, provider string) (Connection, error) {
	r, ok := f.rows[key{userID, provider}]
	if !ok {
		return Connection{}, ErrNotFound
	}
	return r.conn, nil
}

func (f *fakeRepo) SealedToken(_ context.Context, userID uuid.UUID, provider string) (Connection, Sealed, error) {
	r, ok := f.rows[key{userID, provider}]
	if !ok {
		return Connection{}, nil, ErrNotFound
	}
	return r.conn, r.sealed, nil
}

func (f *fakeRepo) Upsert(_ context.Context, userID uuid.UUID, provider, endpoint string, token Sealed) (Connection, error) {
	c := Connection{
		UserID: userID, Provider: provider, Endpoint: endpoint,
		HasToken: len(token) > 0, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.rows[key{userID, provider}] = &row{conn: c, sealed: token}
	return c, nil
}

func (f *fakeRepo) Delete(_ context.Context, userID uuid.UUID, provider string) error {
	k := key{userID, provider}
	if _, ok := f.rows[k]; !ok {
		return ErrNotFound
	}
	delete(f.rows, k)
	return nil
}

func (f *fakeRepo) MarkSeen(_ context.Context, userID uuid.UUID, provider string) error {
	r, ok := f.rows[key{userID, provider}]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	r.conn.LastSeenAt = &now
	r.conn.LastError = ""
	return nil
}

func (f *fakeRepo) RecordError(_ context.Context, userID uuid.UUID, provider, reason string) error {
	r, ok := f.rows[key{userID, provider}]
	if !ok {
		return ErrNotFound
	}
	r.conn.LastError = reason
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
	return NewService(repo, NewClient(), sealerWith(t, 1)), repo
}

const (
	testToken    = "gw_sk_9f3c1a77e2b04d16"
	testEndpoint = "http://hermes-vps-2.tail562587.ts.net:8642/mcp"
)

func TestTheStoredTokenIsCiphertext(t *testing.T) {
	svc, repo := newService(t)
	user := uuid.New()

	if _, err := svc.Connect(t.Context(), user, "hermes", testEndpoint, testToken); err != nil {
		t.Fatalf("connect: %v", err)
	}

	stored := repo.rows[key{user, "hermes"}].sealed
	if len(stored) == 0 {
		t.Fatal("nothing was stored")
	}
	if strings.Contains(string(stored), testToken) {
		t.Fatal("the stored column contains the plaintext token")
	}
}

// Connection is what List returns and what the settings page renders. A token
// reaching any field of it reaches both.
func TestNoFieldOfAConnectionCanHoldTheToken(t *testing.T) {
	svc, _ := newService(t)
	user := uuid.New()

	conn, err := svc.Connect(t.Context(), user, "hermes", testEndpoint, testToken)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	v := reflect.ValueOf(conn)
	for i := range v.NumField() {
		field := v.Field(i)
		if field.Kind() != reflect.String {
			continue
		}
		if strings.Contains(field.String(), testToken) {
			t.Errorf("%s holds the token", v.Type().Field(i).Name)
		}
	}
	if !conn.HasToken {
		t.Error("HasToken is false after storing one")
	}
}

func TestConnectValidatesBeforeStoring(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown provider", func(t *testing.T) {
		svc, _ := newService(t)
		_, err := svc.Connect(ctx, uuid.New(), "dropbox", testEndpoint, testToken)
		if !errors.Is(err, ErrUnknownProvider) {
			t.Errorf("error = %v, want ErrUnknownProvider", err)
		}
	})

	// The address is stored only if Loci would dial it. Storing first and
	// checking later would leave rows nothing can ever use.
	for name, endpoint := range map[string]string{
		"loopback": "http://127.0.0.1:9000/mcp",
		"metadata": "http://169.254.169.254/mcp",
		"empty":    "",
		"scheme":   "file:///etc/passwd",
	} {
		t.Run(name, func(t *testing.T) {
			svc, repo := newService(t)
			user := uuid.New()
			if _, err := svc.Connect(ctx, user, "hermes", endpoint, testToken); err == nil {
				t.Fatal("the endpoint was accepted")
			}
			if len(repo.rows) != 0 {
				t.Error("a row was written for an address Loci will not dial")
			}
		})
	}
}

// The form leaves the token field blank when somebody only edits the endpoint.
func TestABlankTokenKeepsTheStoredOne(t *testing.T) {
	svc, repo := newService(t)
	user := uuid.New()

	if _, err := svc.Connect(t.Context(), user, "hermes", testEndpoint, testToken); err != nil {
		t.Fatalf("connect: %v", err)
	}
	first := repo.rows[key{user, "hermes"}].sealed

	const moved = "http://hermes-vps-3.tail562587.ts.net:8642/mcp"
	conn, err := svc.Connect(t.Context(), user, "hermes", moved, "")
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}

	if conn.Endpoint != moved {
		t.Errorf("Endpoint = %q, want the edited address", conn.Endpoint)
	}
	if string(repo.rows[key{user, "hermes"}].sealed) != string(first) {
		t.Error("the stored token was lost when the endpoint was edited")
	}
	if !conn.HasToken {
		t.Error("HasToken went false while a token was still stored")
	}
}

// Some MCP servers need no credential at all.
func TestAServerCanBeConnectedWithNoToken(t *testing.T) {
	svc, repo := newService(t)
	user := uuid.New()

	conn, err := svc.Connect(t.Context(), user, "calendar", testEndpoint, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if conn.HasToken {
		t.Error("HasToken is true with no token given")
	}
	if len(repo.rows[key{user, "calendar"}].sealed) != 0 {
		t.Error("something was stored as a token")
	}
}

// Reconnecting replaces: two endpoints for one provider would need a third
// concept — which of them is live.
func TestReconnectingReplacesRatherThanAccumulates(t *testing.T) {
	svc, repo := newService(t)
	user := uuid.New()

	for _, endpoint := range []string{testEndpoint, "http://other.tail562587.ts.net:8642/mcp"} {
		if _, err := svc.Connect(t.Context(), user, "hermes", endpoint, testToken); err != nil {
			t.Fatalf("connect: %v", err)
		}
	}
	if len(repo.rows) != 1 {
		t.Fatalf("%d rows, want 1", len(repo.rows))
	}
}

func TestWithoutAnEncryptionKeyNothingCanBeConnected(t *testing.T) {
	svc := NewService(newFakeRepo(), NewClient(), nil)

	if svc.Enabled() {
		t.Error("Enabled reports true with no sealer")
	}
	if _, err := svc.Connect(t.Context(), uuid.New(), "hermes", testEndpoint, testToken); !errors.Is(err, ErrSealingUnavailable) {
		t.Errorf("error = %v, want ErrSealingUnavailable", err)
	}
	// Open is on the planning path. With no sealer it must report "not
	// connected" so the plan proceeds without this server, rather than failing.
	if _, err := svc.Open(t.Context(), uuid.New(), "hermes"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// A token sealed under a key that is no longer configured cannot be recovered.
// Saying so is what stops the itinerary silently arriving without whatever this
// server contributes.
func TestATokenSealedUnderADroppedKeyIsReportedAsFailing(t *testing.T) {
	repo := newFakeRepo()
	user := uuid.New()

	old := NewService(repo, NewClient(), sealerWith(t, 1))
	if _, err := old.Connect(t.Context(), user, "hermes", testEndpoint, testToken); err != nil {
		t.Fatalf("connect: %v", err)
	}

	rotated := NewService(repo, NewClient(), sealerWith(t, 2))
	if _, err := rotated.Open(t.Context(), user, "hermes"); err == nil {
		t.Fatal("the connection opened with the key gone")
	}

	conn, err := repo.Get(t.Context(), user, "hermes")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !conn.Failing() {
		t.Error("the connection is not marked failing")
	}
	if strings.Contains(conn.LastError, testToken) {
		t.Error("the recorded reason contains the token")
	}
}

func TestDisconnect(t *testing.T) {
	svc, repo := newService(t)
	user := uuid.New()

	if _, err := svc.Connect(t.Context(), user, "hermes", testEndpoint, testToken); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := svc.Disconnect(t.Context(), user, "hermes"); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if len(repo.rows) != 0 {
		t.Error("the row survived disconnection")
	}
	if err := svc.Disconnect(t.Context(), user, "hermes"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second disconnect = %v, want ErrNotFound", err)
	}
}

func TestCatalogEntriesAreRenderable(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Catalog {
		if p.Name == "" || p.Label == "" {
			t.Errorf("%+v: an entry needs a stored name and a written label", p)
		}
		if seen[p.Name] {
			t.Errorf("%q appears twice", p.Name)
		}
		seen[p.Name] = true

		if _, ok := ProviderByName(p.Name); !ok {
			t.Errorf("ProviderByName(%q) did not find its own entry", p.Name)
		}
	}
	if _, ok := ProviderByName("anything"); ok {
		t.Error("an unlisted integration resolved; this list is closed on purpose")
	}
}
