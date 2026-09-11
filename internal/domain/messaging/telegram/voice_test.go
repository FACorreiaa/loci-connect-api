package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
)

// fakeVoice stands in for the speech client, recording what it was asked.
type fakeVoice struct {
	mu sync.Mutex

	transcript    string
	transcribeErr error
	heard         [][]byte
	heardTypes    []string

	shortened   []string
	shortenErr  error
	spoken      []string
	sayErr      error
	cannotSpeak bool
}

func (v *fakeVoice) Transcribe(_ context.Context, audio []byte, mimeType string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.heard = append(v.heard, audio)
	v.heardTypes = append(v.heardTypes, mimeType)
	return v.transcript, v.transcribeErr
}

func (v *fakeVoice) Shorten(_ context.Context, text string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.shortened = append(v.shortened, text)
	if v.shortenErr != nil {
		return "", v.shortenErr
	}
	return "short: " + text, nil
}

func (v *fakeVoice) Say(_ context.Context, text string, _ time.Duration) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.spoken = append(v.spoken, text)
	if v.sayErr != nil {
		return nil, v.sayErr
	}
	return []byte("OggSOpusHead"), nil
}

func (v *fakeVoice) CanSpeak() bool { return !v.cannotSpeak }

func (v *fakeVoice) said() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.spoken...)
}

func (v *fakeVoice) recordings() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.heard)
}

// echoingHandler answers whatever the transcript turned out to be, which is
// how these tests see that the closure ran and what it produced.
type echoingHandler struct {
	mu sync.Mutex

	reply   messaging.OutboundMessage
	spoken  bool
	asked   bool
	lastErr error
}

func (h *echoingHandler) Handle(ctx context.Context, in messaging.InboundMessage) (messaging.OutboundMessage, error) {
	h.mu.Lock()
	h.spoken = in.SpokenBy()
	h.mu.Unlock()

	if in.Audio != nil {
		transcript, err := in.Audio(ctx)
		h.mu.Lock()
		h.asked = true
		h.lastErr = err
		h.mu.Unlock()
		if err != nil {
			return messaging.OutboundMessage{Text: "could not make that out"}, nil
		}
		if transcript == "" {
			return messaging.OutboundMessage{Text: "heard nothing"}, nil
		}
		return messaging.OutboundMessage{Text: "plan for: " + transcript}, nil
	}
	return h.reply, nil
}

func (h *echoingHandler) fetched() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.asked
}

func voiceUpdate(id, chatID int64, clip *Recording, video bool) map[string]any {
	kind := "voice"
	if video {
		kind = "video_note"
	}
	recording := map[string]any{"file_id": clip.FileID, "duration": clip.Duration, "file_size": clip.FileSize}
	if clip.MIMEType != "" {
		recording["mime_type"] = clip.MIMEType
	}
	return map[string]any{
		"update_id": id,
		"message": map[string]any{
			"chat": map[string]any{"id": chatID},
			"from": map[string]any{"first_name": "Fernando"},
			kind:   recording,
		},
	}
}

func voiceBridge(t *testing.T, api *fakeAPI, handler Handler, voice Voice, opts VoiceOptions) bridge {
	t.Helper()
	b := newBridge(api.client(), handler, newTestLogger())
	if voice != nil {
		b = b.withVoice(voice, opts)
	}
	return b
}

func defaultOptions() VoiceOptions {
	return VoiceOptions{
		MaxDuration:       60 * time.Second,
		MaxVideoDuration:  30 * time.Second,
		MaxBytes:          20 << 20,
		RepliesEnabled:    true,
		VideoNotesEnabled: true,
	}
}

func TestAVoiceNoteIsFetchedTranscribedAndAnswered(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga", "file_size": 2048}
	api.fileContent = []byte("OggS the recording")

	voice := &fakeVoice{transcript: "three days in Lisbon"}
	handler := &echoingHandler{}
	b := voiceBridge(t, api, handler, voice, defaultOptions())

	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 8, MIMEType: "audio/ogg", FileSize: 2048}, false)))

	if !handler.fetched() {
		t.Fatal("the handler was never given a recording to fetch")
	}
	if voice.recordings() != 1 {
		t.Fatalf("transcribed %d times, want once", voice.recordings())
	}
	if got := voice.heardTypes[0]; got != "audio/ogg" {
		t.Errorf("the model was told the recording was %q", got)
	}
	if string(voice.heard[0]) != "OggS the recording" {
		t.Errorf("the model heard %q", voice.heard[0])
	}

	texts := sentTexts(api)
	// The transcript is echoed before the answer, because speech recognition
	// mangles place names and an itinerary takes minutes to arrive.
	if len(texts) < 2 {
		t.Fatalf("sent %d messages, want an echo and an answer: %v", len(texts), texts)
	}
	if !strings.Contains(texts[0], "three days in Lisbon") || !strings.HasPrefix(texts[0], "Heard:") {
		t.Errorf("first message was %q, want the transcript echoed", texts[0])
	}
	if !strings.Contains(texts[1], "plan for: three days in Lisbon") {
		t.Errorf("second message was %q, want the answer", texts[1])
	}
}

// The single most important assertion here: a recording past the limit costs
// nothing at all. Refusing it after the download costs a download and a
// transcription for an answer nobody gets.
func TestALongRecordingIsRefusedWithoutBeingFetched(t *testing.T) {
	tests := []struct {
		name  string
		clip  *Recording
		video bool
	}{
		{"a voice note past a minute", &Recording{FileID: "abc", Duration: 90, MIMEType: "audio/ogg"}, false},
		{"a video message past thirty seconds", &Recording{FileID: "abc", Duration: 45}, true},
		{"a recording too big to fetch", &Recording{FileID: "abc", Duration: 5, FileSize: 64 << 20}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFakeAPI(t)
			voice := &fakeVoice{transcript: "should never be reached"}
			handler := &echoingHandler{}
			b := voiceBridge(t, api, handler, voice, defaultOptions())

			b.handle(context.Background(), decodeUpdate(t, voiceUpdate(1, 4242, tt.clip, tt.video)))

			if got := api.callsTo("getFile"); len(got) != 0 {
				t.Errorf("the file was resolved %d times for a refused recording", len(got))
			}
			if got := api.callsTo("download"); len(got) != 0 {
				t.Errorf("the file was downloaded %d times for a refused recording", len(got))
			}
			if voice.recordings() != 0 {
				t.Error("a refused recording was still transcribed")
			}
			if handler.fetched() {
				t.Error("a refused recording still reached the handler")
			}

			// And the sender is told why, rather than met with silence.
			if texts := sentTexts(api); len(texts) != 1 {
				t.Errorf("sent %d messages, want one refusal: %v", len(texts), texts)
			}
		})
	}
}

func TestVideoMessagesCanBeTurnedOffOnTheirOwn(t *testing.T) {
	api := newFakeAPI(t)
	opts := defaultOptions()
	opts.VideoNotesEnabled = false

	voice := &fakeVoice{transcript: "unused"}
	b := voiceBridge(t, api, &echoingHandler{}, voice, opts)

	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 5}, true)))

	if len(api.callsTo("getFile")) != 0 {
		t.Error("a video message was fetched while they were turned off")
	}
	texts := sentTexts(api)
	if len(texts) != 1 || !strings.Contains(texts[0], "voice note") {
		t.Errorf("reply was %v, want one pointing at voice notes", texts)
	}

	// A voice note still works, which is the point of the separate switch.
	api.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga"}
	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(2, 9999, &Recording{FileID: "abc", Duration: 5, MIMEType: "audio/ogg"}, false)))
	if len(api.callsTo("getFile")) != 1 {
		t.Error("a voice note was refused along with video messages")
	}
}

func TestWithoutSpeechARecordingIsRefusedPolitely(t *testing.T) {
	api := newFakeAPI(t)
	b := voiceBridge(t, api, &echoingHandler{}, nil, VoiceOptions{})

	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 5, MIMEType: "audio/ogg"}, false)))

	texts := sentTexts(api)
	if len(texts) != 1 || !strings.Contains(texts[0], "type it") {
		t.Errorf("reply was %v, want one offering the keyboard", texts)
	}
}

// Everything after the written answer is additive. A synthesis that failed is
// not a reason to tell somebody their itinerary did not work.
func TestAFailureToSpeakStillDeliversTheWrittenAnswer(t *testing.T) {
	for _, tt := range []struct {
		name  string
		spoil func(*fakeVoice)
	}{
		{"synthesis failed", func(v *fakeVoice) { v.sayErr = errors.New("the provider went away") }},
		{"shortening failed", func(v *fakeVoice) { v.shortenErr = errors.New("the provider went away") }},
		{"there is no encoder", func(v *fakeVoice) { v.cannotSpeak = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := newFakeAPI(t)
			api.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga"}
			voice := &fakeVoice{transcript: "three days in Lisbon"}
			tt.spoil(voice)

			b := voiceBridge(t, api, &echoingHandler{}, voice, defaultOptions())
			b.handle(context.Background(),
				decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 8, MIMEType: "audio/ogg"}, false)))

			texts := sentTexts(api)
			var answered bool
			for _, text := range texts {
				if strings.Contains(text, "plan for: three days in Lisbon") {
					answered = true
				}
				// Nothing about the failure reaches the chat.
				if strings.Contains(strings.ToLower(text), "went wrong") {
					t.Errorf("the chat was told something failed: %q", text)
				}
			}
			if !answered {
				t.Errorf("the written answer never arrived: %v", texts)
			}
			if len(api.callsTo("sendVoice")) != 0 {
				t.Error("a voice note was sent despite the failure")
			}
		})
	}
}

func TestSpokenRepliesCanBeTurnedOff(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga"}
	opts := defaultOptions()
	opts.RepliesEnabled = false

	voice := &fakeVoice{transcript: "three days in Lisbon"}
	b := voiceBridge(t, api, &echoingHandler{}, voice, opts)

	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 8, MIMEType: "audio/ogg"}, false)))

	// Understood, answered in writing, nothing synthesised. This is the switch
	// to reach for when speaking gets expensive.
	if voice.recordings() != 1 {
		t.Error("the recording was not understood")
	}
	if got := voice.said(); len(got) != 0 {
		t.Errorf("something was synthesised anyway: %v", got)
	}
	if len(api.callsTo("sendVoice")) != 0 {
		t.Error("a voice note was sent while spoken replies were off")
	}
}

func TestAWrittenReplyIsSpokenAfterItIsSent(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga"}
	voice := &fakeVoice{transcript: "three days in Lisbon"}

	b := voiceBridge(t, api, &echoingHandler{}, voice, defaultOptions())
	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 8, MIMEType: "audio/ogg"}, false)))

	if got := len(api.callsTo("sendVoice")); got != 1 {
		t.Fatalf("sent %d voice notes, want one", got)
	}
	if got := voice.said(); len(got) != 1 || !strings.HasPrefix(got[0], "short: ") {
		t.Errorf("spoke %v, want the shortened answer", got)
	}

	// The written answer goes first: it is the thing the person asked for, and
	// everything after it is additive.
	order := api.methodOrder()
	sendMessageAt, sendVoiceAt := -1, -1
	for i, method := range order {
		if method == "sendMessage" && sendMessageAt < 0 {
			sendMessageAt = i
		}
		if method == "sendVoice" {
			sendVoiceAt = i
		}
	}
	if sendMessageAt < 0 || sendVoiceAt < 0 || sendMessageAt > sendVoiceAt {
		t.Errorf("order was %v, want the written answer before the spoken one", order)
	}
}

// A typed message must behave exactly as it always did.
func TestATypedMessageIsUntouched(t *testing.T) {
	api := newFakeAPI(t)
	voice := &fakeVoice{transcript: "should never be reached"}
	handler := &echoingHandler{reply: messaging.OutboundMessage{Text: "here is a plan"}}

	b := voiceBridge(t, api, handler, voice, defaultOptions())
	b.handle(context.Background(), decodeUpdate(t, update(1, 4242, "three days in Lisbon")))

	if voice.recordings() != 0 {
		t.Error("a typed message was sent to the transcriber")
	}
	if len(api.callsTo("sendVoice")) != 0 {
		t.Error("a typed message was answered with a voice note")
	}
	if texts := sentTexts(api); len(texts) != 1 || texts[0] != "here is a plan" {
		t.Errorf("sent %v, want just the answer", texts)
	}
}

// Telegram puts a note sent alongside a recording in the caption, and nothing
// in the text.
func TestACaptionIsTreatedAsTypedText(t *testing.T) {
	api := newFakeAPI(t)
	voice := &fakeVoice{transcript: "should never be reached"}
	handler := &echoingHandler{reply: messaging.OutboundMessage{Text: "here is a plan"}}

	b := voiceBridge(t, api, handler, voice, defaultOptions())

	raw := voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 8, MIMEType: "audio/ogg"}, false)
	raw["message"].(map[string]any)["caption"] = "three days in Lisbon"
	b.handle(context.Background(), decodeUpdate(t, raw))

	if voice.recordings() != 0 {
		t.Error("a captioned recording was transcribed rather than read")
	}
	if texts := sentTexts(api); len(texts) != 1 {
		t.Errorf("sent %v, want just the answer", texts)
	}
}
