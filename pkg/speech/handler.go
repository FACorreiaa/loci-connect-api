package speech

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"

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
type Handler struct {
	speechv1connect.UnimplementedSpeechServiceHandler
	client *Client
	logger *slog.Logger
}

// NewHandler creates the handler. client may be nil.
func NewHandler(client *Client, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		client: client,
		logger: logger.With(slog.String("component", "speech-handler")),
	}
}

func (h *Handler) Transcribe(ctx context.Context, req *connect.Request[speechv1.TranscribeRequest]) (*connect.Response[speechv1.TranscribeResponse], error) {
	if h.client == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("speech is not configured on this server"))
	}

	mimeType := normaliseType(req.Msg.GetMimeType())
	if _, ok := allowedTypes[mimeType]; !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("that is not an audio format this server can read"))
	}

	text, err := h.client.Transcribe(ctx, req.Msg.GetAudio(), mimeType)
	switch {
	case errors.Is(err, ErrNothingHeard):
		// Not an error. A recording of a pocket is something the caller should
		// say "I could not hear anything" to, and an error code would make it
		// indistinguishable from the provider being down.
		return connect.NewResponse(&speechv1.TranscribeResponse{}), nil

	case err != nil:
		// The upstream error can name models and providers, none of which the
		// caller can act on.
		h.logger.ErrorContext(ctx, "could not transcribe a recording",
			slog.String("error", err.Error()))
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("could not understand that recording"))
	}

	return connect.NewResponse(&speechv1.TranscribeResponse{Text: text}), nil
}

// normaliseType strips the parameters a browser attaches to a recording's
// type, so "audio/webm;codecs=opus" is recognised as the format it is.
func normaliseType(raw string) string {
	base, _, _ := strings.Cut(raw, ";")
	return strings.ToLower(strings.TrimSpace(base))
}
