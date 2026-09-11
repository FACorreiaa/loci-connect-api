// Package speech hears recordings and says replies aloud.
//
// It exists so somebody walking around a city can ask for an itinerary without
// stopping to type, and get back exactly what typing would have got them. Both
// directions run on Gemini, and deliberately not through the chat provider:
// chat runs on OpenRouter in production and has to keep running on it, so this
// reads its own credential and its own models.
package speech

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// transcribePrompt is what the model is told to do with a recording.
//
// Verbatim and nothing else, because the transcript is echoed back to the
// person who spoke it: a model that helpfully tidies "erm, three days in
// Lisbon" into a polished sentence makes the echo useless as a check on what
// was actually heard.
const transcribePrompt = `Transcribe the speech in this recording word for word.
Reply with the transcript and nothing else — no preamble, no quotation marks, no commentary.
If there is no speech in it, reply with nothing at all.`

// shortenPrompt reduces an answer to something worth hearing.
const shortenPrompt = `Rewrite the following for someone listening rather than reading.
Two or three sentences, spoken plainly, covering only what matters most.
Leave out links, addresses and lists — they are unusable aloud and the reader has them already.
Reply with the rewritten text and nothing else.

`

// shortenFloor is the length below which an answer is already speakable.
//
// Shortening a paragraph that is already two sentences is a model call that
// costs money and adds a second of latency to say the same thing.
const shortenFloor = 400

// defaultSampleRate is assumed when a synthesis response does not say.
// Gemini's speech models answer at 24 kHz.
const defaultSampleRate = 24000

// ErrDisabled reports that speech is not configured.
var ErrDisabled = errors.New("speech: not configured")

// ErrNothingHeard reports that a recording carried no speech.
//
// A normal outcome rather than a failure — somebody's pocket, or a note that
// was all background noise — and worth telling apart from a broken call so the
// reply can say "I could not hear anything" instead of "something went wrong".
var ErrNothingHeard = errors.New("speech: nothing was said")

// Client hears recordings and says replies aloud.
type Client struct {
	gemini          *generativeAI.GeminiChatClient
	transcribeModel string
	speakModel      string
	voiceName       string
	logger          *slog.Logger

	// canEncode records whether the Opus encoder was on PATH at startup.
	//
	// Checked once, at construction, so an image built without opus-tools is
	// one line in the boot log rather than an identical failure on every
	// spoken reply for as long as nobody reads the logs.
	canEncode bool
}

// New builds the speech client, or returns nil when speech is not configured.
//
// Nil and no error is the supported disabled state, the same shape the
// Telegram bridge uses for a missing bot token: a deployment without a key
// answers in text and ignores recordings, rather than refusing to boot.
func New(ctx context.Context, cfg config.VoiceConfig, logger *slog.Logger) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if !cfg.Enabled() {
		logger.Info("speech is disabled; no GEMINI_API_KEY is set")
		return nil, nil
	}

	gemini, err := generativeAI.NewGeminiClient(ctx, cfg.GeminiAPIKey, cfg.TranscribeModel)
	if err != nil {
		return nil, fmt.Errorf("speech: could not build the provider client: %w", err)
	}

	c := &Client{
		gemini:          gemini.WithLogger(logger),
		transcribeModel: cfg.TranscribeModel,
		speakModel:      cfg.SpeakModel,
		voiceName:       cfg.VoiceName,
		logger:          logger,
	}

	if _, err := exec.LookPath(encoderBinary); err != nil {
		// Transcription still works; only spoken replies are lost. Warned
		// rather than fatal for that reason.
		logger.Warn("spoken replies are disabled; the opus encoder is not installed",
			slog.String("binary", encoderBinary))
	} else {
		c.canEncode = true
	}

	// The models are named in the log because they are preview-tagged and get
	// retired: when one disappears, this line is what says which one was asked
	// for, without reading the ConfigMap.
	logger.Info("speech is ready",
		slog.String("transcribe_model", cfg.TranscribeModel),
		slog.String("speak_model", cfg.SpeakModel),
		slog.String("voice", cfg.VoiceName),
		slog.Bool("can_speak", c.canEncode))

	return c, nil
}

// CanSpeak reports whether spoken replies are possible.
func (c *Client) CanSpeak() bool { return c != nil && c.canEncode }

// Transcribe returns the words in a recording.
//
// A recording with no speech in it returns ErrNothingHeard, which the caller
// should answer rather than report.
func (c *Client) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	if c == nil {
		return "", ErrDisabled
	}
	if len(audio) == 0 {
		return "", ErrNothingHeard
	}

	// Temperature zero: this is a transcription, and there is nothing to be
	// creative about.
	var zero float32
	config := &genai.GenerateContentConfig{Temperature: &zero}

	text, err := c.gemini.GenerateTextFromParts(ctx, transcribePrompt,
		[]generativeAI.Blob{{Data: audio, MIMEType: mimeType}}, config)
	if err != nil {
		if errors.Is(err, generativeAI.ErrNoContent) {
			return "", ErrNothingHeard
		}
		return "", fmt.Errorf("speech: could not transcribe the recording: %w", err)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrNothingHeard
	}
	return text, nil
}

// Shorten reduces a reply to something worth listening to.
//
// An answer already short enough is returned unchanged rather than sent to the
// model: rewriting two sentences into two sentences costs a call and a second.
func (c *Client) Shorten(ctx context.Context, text string) (string, error) {
	if c == nil {
		return "", ErrDisabled
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("speech: nothing to shorten")
	}
	if len(text) < shortenFloor {
		return text, nil
	}

	short, err := c.gemini.GenerateText(ctx, shortenPrompt+text, nil)
	if err != nil {
		return "", fmt.Errorf("speech: could not shorten the reply: %w", err)
	}
	short = strings.TrimSpace(short)
	if short == "" {
		return "", fmt.Errorf("speech: the shortened reply came back empty")
	}
	return short, nil
}

// Say renders text as an OGG/Opus voice note.
//
// Two steps, because Gemini answers with headerless PCM and Telegram plays
// only Opus in an Ogg container: the samples are given a WAV header, then
// encoded.
func (c *Client) Say(ctx context.Context, text string, encodeTimeout time.Duration) ([]byte, error) {
	if c == nil {
		return nil, ErrDisabled
	}
	if !c.canEncode {
		return nil, ErrEncoderMissing
	}

	audio, err := c.gemini.GenerateSpeech(ctx, text, generativeAI.SpeechOptions{
		Model:     c.speakModel,
		VoiceName: c.voiceName,
	})
	if err != nil {
		return nil, fmt.Errorf("speech: could not synthesise the reply: %w", err)
	}

	wav, err := wrapPCM(audio.Data, sampleRateOf(audio.MIMEType), 1, 16)
	if err != nil {
		return nil, err
	}

	encodeCtx, cancel := context.WithTimeout(ctx, encodeTimeout)
	defer cancel()
	return encodeOgg(encodeCtx, wav)
}

// sampleRateOf reads the rate out of a PCM MIME type.
//
// Gemini describes its audio as "audio/L16;codec=pcm;rate=24000". Getting this
// wrong does not fail — it produces a reply played at the wrong speed, which
// is worse than an error, so an unreadable rate falls back to the documented
// one rather than to zero.
func sampleRateOf(mimeType string) int {
	for _, part := range strings.Split(mimeType, ";") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "rate") {
			continue
		}
		if rate, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && rate > 0 {
			return rate
		}
	}
	return defaultSampleRate
}
