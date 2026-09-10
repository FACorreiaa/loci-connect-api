package aicreds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/ai/providers"
	"github.com/FACorreiaa/loci-connect-api/pkg/secret"
)

// ErrSealingUnavailable means ENCRYPTION_KEY is not configured. Bringing a key
// is refused rather than stored in the clear, and the settings page says so.
var ErrSealingUnavailable = errors.New("aicreds: bring-your-own-key is unavailable because no encryption key is configured")

// ErrUnknownProvider means the provider is not one in the catalogue. Loci
// would have no address or dialect for it.
var ErrUnknownProvider = errors.New("aicreds: not a provider a key can be brought for")

// ErrKeyRequired means a save arrived with no key and none stored to keep.
var ErrKeyRequired = errors.New("aicreds: an API key is required")

// InputError is a validation failure the user can fix: the message names what
// to change and never contains the key.
//
// Its own type so the handler can tell "you typed it wrong" from "the database
// is down" without matching on strings — the first is InvalidArgument with the
// message shown as-is, the second is Internal with the message hidden.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return "aicreds: " + e.Msg }

func invalid(msg string) error { return &InputError{Msg: msg} }

// hintLen is how much of the key is kept in clear. Enough to answer "is the
// stored key the one I am looking at", short enough to be useless to a reader.
const hintLen = 4

// maxKeyLen bounds what will be sealed. Real keys are under a hundred bytes;
// this is only here so a paste accident cannot become an 8KB ciphertext that
// the column's own CHECK would then reject with a worse message.
const maxKeyLen = 1024

// Input is one save from the settings page.
type Input struct {
	Provider string

	// APIKey empty means "keep the key already stored", which is what the form
	// does when somebody edits the model and leaves the key field untouched.
	APIKey string

	// Model empty means the catalogue's default for the provider.
	Model string

	// BaseURL is read only for providers that require one — Hermes. A value
	// here for any other provider is ignored rather than rejected, because the
	// form keeps the field populated when somebody switches provider back and
	// forth and refusing that would be confusing.
	BaseURL string
}

// Resolved is a credential opened for use.
//
// It holds the plaintext key, and it is the only type in this package that
// does. Build a client from it and let it go: do not log it, do not put it in
// an error, do not return it from a handler.
type Resolved struct {
	Provider string
	// Model is resolved: the user's choice, or the catalogue's default.
	Model string
	// BaseURL is set only for providers that require one, and has already been
	// through ParseGatewayURL.
	BaseURL string
	APIKey  string

	// Version is the credential's updated_at. Callers that cache a client
	// built from this compare it to decide whether the cached one is stale,
	// which is what makes a saved change take effect on the next request
	// rather than the next deploy.
	Version time.Time
}

// Service validates, seals and opens provider credentials.
//
// A nil Sealer is a supported state and means bring-your-own-key is off; every
// method that would store or open a secret refuses with ErrSealingUnavailable
// rather than degrading to plaintext.
type Service struct {
	repo   Repository
	sealer *secret.Sealer

	// verifier is optional; see WithVerifier.
	verifier KeyVerifier
	logger   *slog.Logger
}

func NewService(repo Repository, sealer *secret.Sealer) *Service {
	return &Service{repo: repo, sealer: sealer, logger: slog.Default()}
}

// Enabled reports whether credentials can be stored at all. The settings page
// asks so it can explain the absence instead of offering a form that fails.
func (s *Service) Enabled() bool { return s != nil && s.sealer != nil }

// Get returns the stored credential's metadata, never its key.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Credential, error) {
	return s.repo.Get(ctx, userID)
}

// Delete removes the credential, returning the account to Loci's own provider.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID) error {
	return s.repo.Delete(ctx, userID)
}

// RecordFailure notes why a call on this credential was rejected.
func (s *Service) RecordFailure(ctx context.Context, userID uuid.UUID, reason string) error {
	return s.repo.RecordError(ctx, userID, reason)
}

// Save validates and stores a credential.
//
// A blank key keeps the stored one, but only when the provider has not
// changed: a key issued by OpenRouter is not a key for xAI, and silently
// carrying it across would store a credential guaranteed to fail.
func (s *Service) Save(ctx context.Context, userID uuid.UUID, in Input) (Credential, error) {
	if !s.Enabled() {
		return Credential{}, ErrSealingUnavailable
	}

	entry, ok := providers.ByName(strings.TrimSpace(in.Provider))
	if !ok {
		return Credential{}, ErrUnknownProvider
	}

	model := strings.TrimSpace(in.Model)
	if len(model) > 500 {
		return Credential{}, invalid("the model name is too long")
	}

	baseURL := ""
	if entry.RequiresBaseURL {
		parsed, err := providers.ParseGatewayURL(in.BaseURL)
		if err != nil {
			// The gateway URL is the user's own text and safe to report on;
			// the message names what to fix.
			return Credential{}, invalid(err.Error())
		}
		baseURL = parsed
	}

	key := strings.TrimSpace(in.APIKey)
	if key == "" {
		return s.keepStoredKey(ctx, userID, entry.Name, model, baseURL)
	}
	if len(key) > maxKeyLen {
		return Credential{}, invalid("that does not look like an API key")
	}

	// Checked before it is sealed, so a mistyped key is refused while the
	// person is still looking at the form rather than discovered through an
	// itinerary that quietly ran on Loci's provider.
	if err := s.verify(ctx, userID, entry, baseURL, key); err != nil {
		return Credential{}, err
	}

	sealed, err := s.sealer.Seal(userID[:], []byte(key))
	if err != nil {
		// Deliberately not wrapped: the only error worth reporting is that
		// sealing failed, and the input is the key.
		return Credential{}, errors.New("aicreds: could not seal the API key")
	}

	return s.repo.Upsert(ctx, userID, entry.Name, sealed, hint(key), model, baseURL)
}

// keepStoredKey handles a save with no key in it.
func (s *Service) keepStoredKey(ctx context.Context, userID uuid.UUID, provider, model, baseURL string) (Credential, error) {
	existing, err := s.repo.Get(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return Credential{}, ErrKeyRequired
	}
	if err != nil {
		return Credential{}, err
	}
	if existing.Provider != provider {
		return Credential{}, fmt.Errorf("%w: the stored key belongs to %s", ErrKeyRequired, existing.Provider)
	}
	return s.repo.UpdateSettings(ctx, userID, model, baseURL)
}

// Resolve opens the stored credential for use.
//
// ErrNotFound is the ordinary answer for most accounts and means "run on
// Loci's own provider". Callers must treat it as a branch, not a failure.
func (s *Service) Resolve(ctx context.Context, userID uuid.UUID) (Resolved, error) {
	if !s.Enabled() {
		return Resolved{}, ErrNotFound
	}

	c, sealed, err := s.repo.SealedKey(ctx, userID)
	if err != nil {
		return Resolved{}, err
	}

	entry, ok := providers.ByName(c.Provider)
	if !ok {
		// A provider removed from the catalogue after somebody stored a key
		// for it. Their account falls back rather than dialling an address
		// this build no longer describes.
		return Resolved{}, ErrUnknownProvider
	}

	plaintext, err := s.sealer.Open(userID[:], sealed)
	if err != nil {
		// The key cannot be opened: the encryption key was rotated out from
		// under it, or the row was tampered with. Either way this credential
		// is not usable again, and saying so is what stops the account failing
		// every request in silence.
		return Resolved{}, fmt.Errorf("aicreds: the stored key cannot be opened: %w", err)
	}

	model := c.Model
	if model == "" {
		model = entry.DefaultModel
	}

	baseURL := ""
	if entry.RequiresBaseURL {
		// Re-validated on the way out: the stored value passed this check when
		// it was saved, but the rules can tighten between then and now.
		parsed, err := providers.ParseGatewayURL(c.BaseURL)
		if err != nil {
			return Resolved{}, fmt.Errorf("aicreds: the stored gateway URL is no longer one Loci will dial: %w", err)
		}
		baseURL = parsed
	}

	return Resolved{
		Provider: entry.Name,
		Model:    model,
		BaseURL:  baseURL,
		APIKey:   string(plaintext),
		Version:  c.UpdatedAt,
	}, nil
}

// hint is the tail of the key, for "a key ending a203 is stored".
func hint(key string) string {
	if len(key) <= hintLen {
		// Too short to be a real key, and returning it whole would print the
		// entire secret on the settings page.
		return ""
	}
	return key[len(key)-hintLen:]
}
