package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// KeyPrefix is the fixed prefix identifying Loci secret keys.
const KeyPrefix = "loci_sk_"

// displayPrefixLen is how many characters of the full key are stored and
// shown so users can tell keys apart without exposing the secret.
const displayPrefixLen = 12

// secretBytes yields a 256-bit secret; base64url encodes it to 43 chars.
const secretBytes = 32

type Service interface {
	// Create mints a new key and returns its metadata plus the plaintext,
	// which is never persisted and cannot be recovered later.
	Create(ctx context.Context, userID uuid.UUID, name string, expiresAt *time.Time) (key *Key, plaintext string, err error)
	List(ctx context.Context, userID uuid.UUID) ([]Key, error)
	Revoke(ctx context.Context, userID, keyID uuid.UUID) error
	// Authenticate resolves a presented plaintext key to its owning key
	// record, rejecting unknown, revoked, and expired keys with ErrNotFound.
	Authenticate(ctx context.Context, plaintext string) (*Key, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

// HashKey derives the storage/lookup digest for a plaintext key. Keys are
// 256-bit random values, so an unsalted SHA-256 is preimage-safe and keeps
// lookups O(1) by unique index.
func HashKey(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

func generatePlaintext() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate key material: %w", err)
	}
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *service) Create(ctx context.Context, userID uuid.UUID, name string, expiresAt *time.Time) (*Key, string, error) {
	plaintext, err := generatePlaintext()
	if err != nil {
		return nil, "", err
	}
	key, err := s.repo.Create(ctx, userID, name, plaintext[:displayPrefixLen], HashKey(plaintext), expiresAt)
	if err != nil {
		return nil, "", err
	}
	return key, plaintext, nil
}

func (s *service) List(ctx context.Context, userID uuid.UUID) ([]Key, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *service) Revoke(ctx context.Context, userID, keyID uuid.UUID) error {
	return s.repo.Revoke(ctx, userID, keyID)
}

func (s *service) Authenticate(ctx context.Context, plaintext string) (*Key, error) {
	if len(plaintext) <= len(KeyPrefix) || plaintext[:len(KeyPrefix)] != KeyPrefix {
		return nil, ErrNotFound
	}
	return s.repo.LookupActiveByHash(ctx, HashKey(plaintext))
}
