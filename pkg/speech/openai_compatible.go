package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatibleProvider transcribes through any service serving OpenAI's
// POST /v1/audio/transcriptions.
//
// Named for the wire format rather than for a vendor on purpose. The cluster
// runs its own transcription service — free per request, no key, no audio
// leaving the cluster — and hosted providers serve the same shape, so moving
// between them is a base URL and nothing else. A type named after whichever
// one is in use today would have made that a rewrite.
type OpenAICompatibleProvider struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
}

// NewOpenAICompatibleProvider builds a provider, or Disabled when there is
// nothing to call.
//
// No guessed default for the base URL: a guess would point at a vendor nobody
// asked for, and billing somebody by accident is worse than the feature being
// off.
func NewOpenAICompatibleProvider(baseURL, model, apiKey string, timeout time.Duration, client *http.Client) Transcriber {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || strings.TrimSpace(model) == "" {
		return Disabled{}
	}
	if client == nil {
		// The transcription service is CPU-bound, so this is generous: a
		// minute of audio is minutes of work on a shared node, and the caller
		// bounds each request with a context anyway.
		client = &http.Client{Timeout: timeout}
	}
	return &OpenAICompatibleProvider{
		baseURL: baseURL,
		model:   strings.TrimSpace(model),
		apiKey:  strings.TrimSpace(apiKey),
		http:    client,
	}
}

// maxTranscriptBytes bounds what is read back. A transcript of a minute of
// speech is a few hundred bytes; this is far above that.
const maxTranscriptBytes = 1 << 20

// maxHintBytes bounds the vocabulary hint.
//
// The prompt field is fed to the decoder as if it were preceding speech, and
// these models only look back a couple of hundred tokens — past that the hint
// is silently ignored, so a long one is not more help, it is less.
const maxHintBytes = 800

func (p *OpenAICompatibleProvider) Transcribe(ctx context.Context, audio []byte, mimeType, hint string) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", fileNameFor(mimeType))
	if err != nil {
		return "", errorf(FailureFailed, "speech: could not build the request")
	}
	if _, err := part.Write(audio); err != nil {
		return "", errorf(FailureFailed, "speech: could not build the request")
	}
	if err := writer.WriteField("model", p.model); err != nil {
		return "", errorf(FailureFailed, "speech: could not build the request")
	}
	if hint = truncate(strings.TrimSpace(hint), maxHintBytes); hint != "" {
		if err := writer.WriteField("prompt", hint); err != nil {
			return "", errorf(FailureFailed, "speech: could not build the request")
		}
	}
	if err := writer.Close(); err != nil {
		return "", errorf(FailureFailed, "speech: could not build the request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", errorf(FailureFailed, "speech: could not build the request")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", wrap(FailureBusy, ctxErr)
		}
		// Not wrapped with the URL: a key may travel in the header, but the
		// base URL is still worth keeping out of a chat reply, and this error
		// reaches a log either way.
		return "", errorf(FailureUnavailable, "speech: the transcription service could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTranscriptBytes))
	if err != nil {
		return "", errorf(FailureFailed, "speech: could not read the transcript")
	}

	if resp.StatusCode != http.StatusOK {
		// The body can echo back the request, so only the status is reported.
		return "", errorf(failureFor(resp.StatusCode),
			"speech: the transcription service answered %d", resp.StatusCode)
	}

	var answer struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return "", errorf(FailureFailed, "speech: the transcription service answered with something that is not a transcript")
	}
	return answer.Text, nil
}

// fileNameFor gives the upload an extension matching its type.
//
// Multipart uploads to these services are routed on the filename as much as on
// the content type, and an extensionless "file" is refused by some of them.
func fileNameFor(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	switch strings.ToLower(strings.TrimSpace(base)) {
	case "audio/ogg", "audio/opus":
		return "clip.ogg"
	case "audio/mpeg", "audio/mp3":
		return "clip.mp3"
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/aac":
		return "clip.m4a"
	case "audio/webm", "video/webm":
		return "clip.webm"
	case "video/mp4":
		return "clip.mp4"
	default:
		return "clip.wav"
	}
}

// truncate shortens text to at most limit bytes, on a rune boundary.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !isRuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut])
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
