package aicreds

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/ai/providers"
)

// verifyTimeout bounds the check. Somebody is watching a form submit, so this
// is short: a provider that cannot answer in ten seconds is not a reason to
// refuse to store a key that may be perfectly good.
const verifyTimeout = 10 * time.Second

// ErrKeyRejected means the provider itself said no. Distinct from every other
// failure, because it is the only one the user can act on — and the only one
// that blocks a save.
var ErrKeyRejected = errors.New("aicreds: the provider rejected this key")

// KeyVerifier checks a credential against its provider before it is stored.
//
// An interface so the check can be stubbed: the alternative is tests that make
// real calls to six vendors, which would be slow, flaky, and would need real
// keys to mean anything.
type KeyVerifier interface {
	// Verify returns ErrKeyRejected when the provider refuses the credential,
	// nil when it accepts it, and any other error when the question could not
	// be asked. That third case must not block a save — a provider having a
	// bad minute is not evidence about the key.
	//
	// entry.BaseURL is where to ask. For Hermes the catalogue has none and the
	// caller fills in the user's gateway; see verifyTarget.
	Verify(ctx context.Context, entry providers.BYOProvider, key string) error
}

// NewHTTPVerifier is the real check: a cheap authenticated GET against the
// provider's own API, using whichever path the catalogue says answers 401 to a
// bad key.
//
// Which path that is had to be probed per provider rather than assumed — see
// providers.BYOProvider.VerifyPath. Three of the six have one.
//
// A nil client gets one with the verify timeout. Exported so the behaviour can
// be tested against a stand-in provider without a shim.
func NewHTTPVerifier(client *http.Client) KeyVerifier {
	if client == nil {
		client = &http.Client{Timeout: verifyTimeout}
	}
	return httpVerifier{
		client: client,
		// A user-supplied gateway is dialled through the same guarded client
		// the chat path uses, so a hostname that passed ParseGatewayURL and
		// then resolves to loopback or cloud metadata is refused here too.
		gateway: providers.GatewayHTTPClient(&http.Client{Timeout: verifyTimeout}),
	}
}

type httpVerifier struct {
	client  *http.Client
	gateway *http.Client
}

func (v httpVerifier) Verify(ctx context.Context, entry providers.BYOProvider, key string) error {
	if entry.VerifyPath == "" || entry.BaseURL == "" {
		// Nothing to ask. Storing unverified is the honest outcome; the
		// router's own fallback and last_error still cover a bad key.
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.BaseURL+entry.VerifyPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)

	client := v.client
	if entry.RequiresBaseURL {
		client = v.gateway
	}

	res, err := client.Do(req)
	if err != nil {
		// Network trouble is not the key's fault. Not wrapped: a *url.Error
		// prints the URL, which is fine, but the pattern of never wrapping
		// on a path that saw a credential is worth keeping uniform.
		return errors.New("aicreds: the provider could not be reached")
	}
	defer func() { _ = res.Body.Close() }()

	switch {
	case res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden:
		return ErrKeyRejected
	case res.StatusCode >= 500:
		// The provider is unwell. Not evidence either way.
		return errors.New("aicreds: the provider could not answer")
	default:
		// Anything else — including a 404 from a backend that routes /models
		// differently — is treated as acceptance rather than rejection. The
		// check exists to catch a mistyped key, and refusing on an unexpected
		// status would block a working one.
		//
		// The body is deliberately not read: it can echo the credential back,
		// and nothing here needs it.
		return nil
	}
}

// WithVerifier makes Save check a key with its provider before sealing it, and
// gives the service somewhere to log a check that could not be made.
//
// Optional. Without one every key is stored unverified, which is what the
// package did before the check existed and is still correct: a bad key falls
// back to Loci's provider on first use and is recorded in last_error.
func (s *Service) WithVerifier(v KeyVerifier, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s.verifier = v
	s.logger = logger
	return s
}

// verifyTarget is the catalogue entry with the address to ask filled in.
//
// The catalogue cannot name a Hermes address, so the user's own is used; every
// other provider's address is the catalogue's and the user's value is ignored.
func verifyTarget(entry providers.BYOProvider, baseURL string) providers.BYOProvider {
	if entry.RequiresBaseURL {
		entry.BaseURL = baseURL
	}
	return entry
}

// verify asks the provider about a key on the way into Save.
//
// Only a rejection is returned. Every other failure is logged and swallowed,
// because "the provider was down for ten seconds" is not a reason to refuse
// a save — the key goes in unverified, which is where it would have been
// without a verifier at all.
func (s *Service) verify(ctx context.Context, userID uuid.UUID, entry providers.BYOProvider, baseURL, key string) error {
	if s.verifier == nil {
		return nil
	}

	err := s.verifier.Verify(ctx, verifyTarget(entry, baseURL), key)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrKeyRejected):
		return ErrKeyRejected
	default:
		s.logger.WarnContext(ctx, "could not verify a provider key; storing it unverified",
			slog.String("user_id", userID.String()),
			slog.String("provider", entry.Name),
			slog.String("error", err.Error()))
		return nil
	}
}

// Verification is the answer to "does the stored key work".
type Verification struct {
	// Checked is false when the question could not be asked: the provider has
	// no endpoint Loci can check against, or did not answer. Rejected means
	// nothing then.
	Checked bool
	// Rejected is meaningful only when Checked.
	Rejected bool
	// Detail is a sentence for the person whenever the answer is not a clean
	// "yes". It never contains the key.
	Detail string
}

// VerifyStored asks the provider about the credential already on the account.
//
// ErrNotFound when there is none. Other errors mean the credential could not
// be opened, which is the same failure Resolve reports and worth showing for
// the same reason.
func (s *Service) VerifyStored(ctx context.Context, userID uuid.UUID) (Verification, error) {
	resolved, err := s.Resolve(ctx, userID)
	if err != nil {
		return Verification{}, err
	}

	entry, _ := providers.ByName(resolved.Provider)
	if entry.VerifyPath == "" {
		return Verification{
			Detail: entry.Label + " has no endpoint Loci can check a key against; " +
				"the key will be tried on your first request.",
		}, nil
	}
	if s.verifier == nil {
		return Verification{Detail: "Key checks are not configured on this server."}, nil
	}

	err = s.verifier.Verify(ctx, verifyTarget(entry, resolved.BaseURL), resolved.APIKey)
	switch {
	case err == nil:
		return Verification{Checked: true}, nil
	case errors.Is(err, ErrKeyRejected):
		return Verification{Checked: true, Rejected: true, Detail: "The provider rejected this key."}, nil
	default:
		return Verification{Detail: "The provider could not be reached to check the key. Try again in a moment."}, nil
	}
}
