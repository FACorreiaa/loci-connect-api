package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
)

// fakeVoice stands in for the transcription client, recording what it was
// asked and what vocabulary it was given.
type fakeVoice struct {
	mu sync.Mutex

	transcript    string
	transcribeErr error
	heard         [][]byte
	heardTypes    []string
	hints         []string
	disabled      bool
}

func (v *fakeVoice) Transcribe(_ context.Context, audio []byte, mimeType, hint string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.heard = append(v.heard, audio)
	v.heardTypes = append(v.heardTypes, mimeType)
	v.hints = append(v.hints, hint)
	return v.transcript, v.transcribeErr
}

func (v *fakeVoice) Enabled() bool { return !v.disabled }

func (v *fakeVoice) recordings() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.heard)
}

func (v *fakeVoice) lastHint() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.hints) == 0 {
		return ""
	}
	return v.hints[len(v.hints)-1]
}

// fakePlaces is the vocabulary a speaker is expected to use.
type fakePlaces struct {
	hint  string
	asked int
}

func (p *fakePlaces) For(context.Context, uuid.UUID) string {
	p.asked++
	return p.hint
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
		transcript, err := in.Audio(ctx, testUserID)
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

// testUserID stands in for the account a chat resolved to.
var testUserID = uuid.MustParse("11111111-2222-3333-4444-555555555555")

func voiceBridge(t *testing.T, api *fakeAPI, handler Handler, voice Voice, opts VoiceOptions) bridge {
	t.Helper()
	return voiceBridgeWithPlaces(t, api, handler, voice, nil, opts)
}

func voiceBridgeWithPlaces(t *testing.T, api *fakeAPI, handler Handler, voice Voice, places Vocabulary, opts VoiceOptions) bridge {
	t.Helper()
	b := newBridge(api.client(), handler, newTestLogger())
	if voice != nil {
		b = b.withVoice(voice, places, opts)
	}
	return b
}

func defaultOptions() VoiceOptions {
	return VoiceOptions{
		MaxDuration:       45 * time.Second,
		MaxVideoDuration:  20 * time.Second,
		MaxBytes:          20 << 20,
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
		{"a voice note past the cap", &Recording{FileID: "abc", Duration: 90, MIMEType: "audio/ogg"}, false},
		{"a video message past its shorter cap", &Recording{FileID: "abc", Duration: 30}, true},
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

// The vocabulary hint is not decoration. The cluster's transcription service
// turns "Cais do Sodré" into "Case 2 Soda" without it, and an itinerary is then
// planned for somewhere that does not exist.
func TestThePlaceNamesAreSentWithTheRecording(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga"}
	voice := &fakeVoice{transcript: "three days in Lisbon"}
	places := &fakePlaces{hint: "Lisbon, Cais do Sodré, Bairro Alto"}

	b := voiceBridgeWithPlaces(t, api, &echoingHandler{}, voice, places, defaultOptions())
	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 8, MIMEType: "audio/ogg"}, false)))

	if voice.lastHint() != "Lisbon, Cais do Sodré, Bairro Alto" {
		t.Errorf("hint sent was %q", voice.lastHint())
	}
	// Looked up against the account, which only exists once the chat has been
	// resolved — so it cannot happen when the update arrives.
	if places.asked != 1 {
		t.Errorf("the vocabulary was asked for %d times, want once", places.asked)
	}
}

// A hint improves a transcript; it is not a precondition for one.
func TestARecordingIsStillUnderstoodWithNoPlaceNames(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga"}
	voice := &fakeVoice{transcript: "three days in Lisbon"}

	b := voiceBridgeWithPlaces(t, api, &echoingHandler{}, voice, &fakePlaces{hint: ""}, defaultOptions())
	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 8, MIMEType: "audio/ogg"}, false)))

	if voice.recordings() != 1 {
		t.Error("the recording was not transcribed")
	}
	if got := sentTexts(api); len(got) < 2 {
		t.Errorf("sent %v, want an echo and an answer", got)
	}
}

// A service that is down is not a reason to tell somebody to re-record: the
// clip was never going to work, and saying "I could not make that out" sends
// them back to try again for nothing.
func TestAFailureToTranscribeIsReportedAsWhatItWas(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga"}
	voice := &fakeVoice{transcribeErr: errors.New("the service is down")}

	b := voiceBridge(t, api, &echoingHandler{}, voice, defaultOptions())
	b.handle(context.Background(),
		decodeUpdate(t, voiceUpdate(1, 4242, &Recording{FileID: "abc", Duration: 8, MIMEType: "audio/ogg"}, false)))

	texts := sentTexts(api)
	if len(texts) == 0 {
		t.Fatal("nothing was said about a failed transcription")
	}
	// The handler decides the wording; what matters here is that the failure
	// reached it rather than being swallowed.
	if !strings.Contains(strings.ToLower(texts[len(texts)-1]), "make that out") {
		t.Errorf("reply was %q", texts[len(texts)-1])
	}
}

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

// The ceiling has to be bigger than the legs it contains.
//
// Getting this wrong does not fail a test or a build: it produces an update
// that is cancelled after minutes of real work, just before the answer is
// sent, which is worse than either finishing or failing fast. The budget was
// briefly wrong in exactly that direction, which is why this is pinned.
func TestTheTimeoutBudgetFitsInsideItsCeiling(t *testing.T) {
	// The longest path an update can take: fetch, download, transcribe,
	// generate, then deliver.
	longest := getFileTimeout + downloadTimeout + transcribeTimeout + answerTimeout + sendTimeout

	if longest >= updateTimeout {
		t.Errorf("the legs add up to %s but the ceiling is %s — the slowest update "+
			"would be cancelled just before its answer was sent", longest, updateTimeout)
	}

	// Transcription is the leg most likely to be raised, since it scales with
	// how long people speak. Worth knowing how much room is left.
	t.Logf("longest path %s, ceiling %s, headroom %s", longest, updateTimeout, updateTimeout-longest)
}
