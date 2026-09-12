// Package speech turns recordings into words.
//
// It exists so somebody walking around a city can ask for an itinerary without
// stopping to type, and get back exactly what typing would have got them.
//
// The provider is named for the wire format it speaks rather than for a
// vendor, so moving between the cluster's own transcription service and a
// hosted one is configuration and never code. That matters more than it looks:
// the cluster runs its own service, free per request and with no audio leaving
// it, and a type called GeminiTranscriber would have made going back a rewrite.
package speech

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// ErrDisabled reports that transcription is not configured.
var ErrDisabled = errors.New("speech: not configured")

// ErrNothingHeard reports that a recording carried no speech.
//
// A normal outcome rather than a failure — somebody's pocket, or a minute of
// traffic noise — and worth telling apart so the reply can say "I could not
// hear anything" instead of "something went wrong".
var ErrNothingHeard = errors.New("speech: nothing was said")

// Failure is why transcription did not produce words.
//
// Split out because these read very differently to whoever is waiting: telling
// somebody their audio was unclear when the service is actually out of
// capacity sends them back to re-record a clip that was never going to work.
type Failure int

const (
	// FailureUnavailable is the service being off, out of credit, or refusing
	// the credential. The feature is down, not fussy.
	FailureUnavailable Failure = iota
	// FailureBusy is rate limiting or saturation. Worth another go shortly.
	//
	// Not hypothetical: the cluster's transcription service is CPU-bound on a
	// single replica shared with other apps, so a queue is an ordinary
	// afternoon rather than an incident.
	FailureBusy
	// FailureFailed is a genuine inability to turn this audio into words.
	FailureFailed
)

// Error is a transcription failure, carrying what to say about it.
type Error struct {
	Failure Failure
	err     error
}

func (e *Error) Error() string {
	if e.err == nil {
		return e.Message()
	}
	return e.err.Error()
}

func (e *Error) Unwrap() error { return e.err }

// Message is what the person waiting should be told.
func (e *Error) Message() string {
	switch e.Failure {
	case FailureUnavailable:
		return "Voice is switched off at the moment. Type it and I will answer the same way."
	case FailureBusy:
		return "I am behind on voice notes right now. Try that again in a moment, or type it."
	default:
		return "I could not make that out. Try again, or type it."
	}
}

// failureFor maps a provider's status onto what to say about it.
func failureFor(status int) Failure {
	switch status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden:
		return FailureUnavailable
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return FailureBusy
	default:
		return FailureFailed
	}
}

// Transcriber turns spoken audio into text.
//
// An interface so the adapter states what it needs and its tests need no
// provider at all.
type Transcriber interface {
	// Transcribe returns the words in a recording.
	//
	// hint is vocabulary to bias decoding toward. It is not decoration: a
	// recogniser with no idea what words to expect turns "Cais do Sodré" into
	// "Case 2 Soda", and an itinerary is then planned for somewhere that does
	// not exist.
	Transcribe(ctx context.Context, audio []byte, mimeType, hint string) (string, error)
}

// Disabled stands in when nothing is configured, so a missing setting turns off
// one feature rather than stopping the app from booting.
type Disabled struct{}

func (Disabled) Transcribe(context.Context, []byte, string, string) (string, error) {
	return "", ErrDisabled
}

// Client is the configured transcriber.
type Client struct {
	provider Transcriber
	logger   *slog.Logger
}

// Enabled reports whether recordings can be understood at all.
func (c *Client) Enabled() bool {
	if c == nil || c.provider == nil {
		return false
	}
	_, disabled := c.provider.(Disabled)
	return !disabled
}

// Transcribe returns the words in a recording.
func (c *Client) Transcribe(ctx context.Context, audio []byte, mimeType, hint string) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	if len(audio) == 0 {
		return "", ErrNothingHeard
	}

	text, err := c.provider.Transcribe(ctx, audio, mimeType, hint)
	if err != nil {
		return "", err
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrNothingHeard
	}
	return text, nil
}

// MessageFor is what to tell somebody whose recording did not become words.
//
// Anything unrecognised gets the vaguest of the three messages, which is the
// right way round: "I could not make that out" is true of every failure, and
// the more specific ones are only worth saying when they are known.
func MessageFor(err error) string {
	var failure *Error
	if errors.As(err, &failure) {
		return failure.Message()
	}
	if errors.Is(err, ErrDisabled) {
		return (&Error{Failure: FailureUnavailable}).Message()
	}
	return (&Error{Failure: FailureFailed}).Message()
}

// FailureOf reports which kind of failure an error is, for metrics.
func FailureOf(err error) string {
	var failure *Error
	if errors.As(err, &failure) {
		switch failure.Failure {
		case FailureUnavailable:
			return "unavailable"
		case FailureBusy:
			return "busy"
		}
	}
	if errors.Is(err, ErrDisabled) {
		return "unavailable"
	}
	return "failed"
}

func wrap(failure Failure, err error) error {
	return &Error{Failure: failure, err: err}
}

func errorf(failure Failure, format string, args ...any) error {
	return &Error{Failure: failure, err: fmt.Errorf(format, args...)}
}

// NewClient builds the configured transcriber, or a disabled one.
//
// Disabled is a supported state rather than a boot failure: a deployment with
// nothing configured answers text and says so when a recording arrives, which
// is the same shape an absent bot token gives the Telegram bridge.
func NewClient(cfg config.VoiceConfig, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}

	provider := providerFor(cfg, logger)
	if _, disabled := provider.(Disabled); disabled {
		logger.Info("transcription is disabled; no provider is configured")
		return &Client{provider: provider, logger: logger}
	}

	// The base URL and model are logged because they are the two things that
	// are wrong when every request 404s, and neither is a secret.
	logger.Info("transcription is ready",
		slog.String("provider", cfg.Provider),
		slog.String("base_url", cfg.BaseURL),
		slog.String("model", cfg.Model))

	return &Client{provider: provider, logger: logger}
}

func providerFor(cfg config.VoiceConfig, logger *slog.Logger) Transcriber {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "openai_compatible", "openai", "selfhosted", "self_hosted":
		return NewOpenAICompatibleProvider(cfg.BaseURL, cfg.Model, cfg.APIKey, providerTimeout, nil)
	default:
		// Named rather than guessed at: a provider nobody implemented is a
		// typo in configuration, and quietly falling back to one that happens
		// to be configured would hide it.
		logger.Warn("transcription is disabled; the configured provider is not one this server implements",
			slog.String("provider", cfg.Provider))
		return Disabled{}
	}
}

// providerTimeout is the transport-level ceiling. Each request is bounded by
// its own context well inside this; it exists so a provider that accepts a
// connection and then says nothing cannot hold one forever.
const providerTimeout = 10 * time.Minute
