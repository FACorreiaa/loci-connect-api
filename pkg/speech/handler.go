package speech

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"

	speechv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/speech"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/speech/speechv1connect"
)

// allowedTypes are the recordings this server will send to a model.
//
// An allowlist rather than a guess: the MIME type is what the model is told
// the bytes are, and a wrong one reads as a recording full of noise rather
// than as an error. Every entry here is a format the provider documents.
var allowedTypes = map[string]struct{}{
	"audio/wav":   {},
	"audio/x-wav": {},
	"audio/mpeg":  {},
	"audio/mp3":   {},
	"audio/ogg":   {},
	"audio/opus":  {},
	"audio/aac":   {},
	"audio/flac":  {},
	"audio/aiff":  {},
	"audio/mp4":   {},
	"audio/webm":  {},
}

// Handler implements SpeechService.
//
// Registered whether or not speech is configured. With none it answers
// FailedPrecondition, which is what lets the client hide the microphone rather
// than offer a button that silently does nothing.
// Vocabulary supplies place names to bias decoding toward.
//
// Derived here rather than sent by the client: the server knows who is asking
// and holds the place data, and a client could offer anything. It is also the
// difference between "Cais do Sodré" and "Case 2 Soda", so it is not something
// to leave to a caller that might omit it.
type Vocabulary interface {
	For(ctx context.Context, userID uuid.UUID) string
}

type Handler struct {
	speechv1connect.UnimplementedSpeechServiceHandler
	client     *Client
	vocabulary Vocabulary
	logger     *slog.Logger
}

// NewHandler creates the handler. client and vocabulary may be nil.
func NewHandler(client *Client, vocabulary Vocabulary, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		client:     client,
		vocabulary: vocabulary,
		logger:     logger.With(slog.String("component", "speech-handler")),
	}
}

// hintFor is the vocabulary for whoever is calling, or empty.
//
// A hint improves a transcript; it is not a precondition for one, so an
// unidentifiable caller or a failed lookup transcribes unhinted rather than
// being refused.
func (h *Handler) hintFor(ctx context.Context) string {
	if h.vocabulary == nil {
		return ""
	}
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return ""
	}
	userID, err := uuid.Parse(raw)
	if err != nil {
		return ""
	}
	return h.vocabulary.For(ctx, userID)
}

func (h *Handler) Transcribe(ctx context.Context, req *connect.Request[speechv1.TranscribeRequest]) (*connect.Response[speechv1.TranscribeResponse], error) {
	if !h.client.Enabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("speech is not configured on this server"))
	}

	mimeType := normaliseType(req.Msg.GetMimeType())
	if _, ok := allowedTypes[mimeType]; !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("that is not an audio format this server can read"))
	}

	text, err := h.client.Transcribe(ctx, req.Msg.GetAudio(), mimeType, h.hintFor(ctx))
	switch {
	case errors.Is(err, ErrNothingHeard):
		// Not an error. A recording of a pocket is something the caller should
		// say "I could not hear anything" to, and an error code would make it
		// indistinguishable from the provider being down.
		return connect.NewResponse(&speechv1.TranscribeResponse{}), nil

	case err != nil:
		// The upstream error can name models, hosts and providers, none of
		// which the caller can act on. What does reach them is which of the
		// three kinds of failure it was, because the advice differs: a service
		// that is down is not a reason to re-record.
		h.logger.ErrorContext(ctx, "could not transcribe a recording",
			slog.String("failure", FailureOf(err)),
			slog.String("error", err.Error()))

		code := connect.CodeUnavailable
		if FailureOf(err) == "failed" {
			code = connect.CodeInternal
		}
		return nil, connect.NewError(code, errors.New(MessageFor(err)))
	}

	return connect.NewResponse(&speechv1.TranscribeResponse{Text: text}), nil
}

// normaliseType strips the parameters a browser attaches to a recording's
// type, so "audio/webm;codecs=opus" is recognised as the format it is.
func normaliseType(raw string) string {
	base, _, _ := strings.Cut(raw, ";")
	return strings.ToLower(strings.TrimSpace(base))
}
