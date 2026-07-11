package apikey

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRepo struct {
	created struct {
		userID    uuid.UUID
		name      string
		keyPrefix string
		keyHash   []byte
		expiresAt *time.Time
	}
	lookupHash []byte
	lookupKey  *Key
	lookupErr  error
}

func (f *fakeRepo) Create(_ context.Context, userID uuid.UUID, name, keyPrefix string, keyHash []byte, expiresAt *time.Time) (*Key, error) {
	f.created.userID = userID
	f.created.name = name
	f.created.keyPrefix = keyPrefix
	f.created.keyHash = keyHash
	f.created.expiresAt = expiresAt
	return &Key{ID: uuid.New(), UserID: userID, Name: name, KeyPrefix: keyPrefix, CreatedAt: time.Now()}, nil
}

func (f *fakeRepo) ListByUser(context.Context, uuid.UUID) ([]Key, error) { return nil, nil }

func (f *fakeRepo) Revoke(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f *fakeRepo) LookupActiveByHash(_ context.Context, keyHash []byte) (*Key, error) {
	f.lookupHash = keyHash
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.lookupKey, nil
}

func TestCreateKeyFormat(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	userID := uuid.New()

	_, plaintext, err := svc.Create(context.Background(), userID, "test key", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !strings.HasPrefix(plaintext, KeyPrefix) {
		t.Fatalf("plaintext %q missing prefix %q", plaintext, KeyPrefix)
	}
	if wantLen := len(KeyPrefix) + 43; len(plaintext) != wantLen {
		t.Fatalf("plaintext length = %d, want %d", len(plaintext), wantLen)
	}
	if repo.created.keyPrefix != plaintext[:displayPrefixLen] {
		t.Fatalf("stored prefix %q != plaintext[:%d] %q", repo.created.keyPrefix, displayPrefixLen, plaintext[:displayPrefixLen])
	}
	if !bytes.Equal(repo.created.keyHash, HashKey(plaintext)) {
		t.Fatal("stored hash does not match HashKey(plaintext)")
	}
	if bytes.Contains(repo.created.keyHash, []byte(plaintext)) {
		t.Fatal("plaintext leaked into stored hash")
	}
}

func TestCreateKeysAreUnique(t *testing.T) {
	svc := NewService(&fakeRepo{})
	seen := map[string]bool{}
	for range 5 {
		_, plaintext, err := svc.Create(context.Background(), uuid.New(), "k", nil)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if seen[plaintext] {
			t.Fatal("duplicate key generated")
		}
		seen[plaintext] = true
	}
}

func TestAuthenticate(t *testing.T) {
	key := &Key{ID: uuid.New(), UserID: uuid.New()}
	repo := &fakeRepo{lookupKey: key}
	svc := NewService(repo)

	// Wrong prefix or too-short input never reaches the repository.
	for _, bad := range []string{"", "loci_sk_", "sk_abc123", "Bearer loci"} {
		if _, err := svc.Authenticate(context.Background(), bad); err != ErrNotFound {
			t.Fatalf("Authenticate(%q) err = %v, want ErrNotFound", bad, err)
		}
		if repo.lookupHash != nil {
			t.Fatalf("Authenticate(%q) hit the repository", bad)
		}
	}

	got, err := svc.Authenticate(context.Background(), KeyPrefix+"abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got != key {
		t.Fatal("Authenticate returned wrong key")
	}
	if repo.lookupHash == nil {
		t.Fatal("Authenticate did not hash the key for lookup")
	}
}
