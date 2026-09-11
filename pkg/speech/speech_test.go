package speech

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// fakeService stands in for anything serving POST /v1/audio/transcriptions,
// recording the multipart form it was sent.
type fakeService struct {
	server *httptest.Server

	status int
	body   string

	fields map[string]string
	file   []byte
	name   string
	auth   string
	path   string
}

func newFakeService(t *testing.T) *fakeService {
	t.Helper()
	f := &fakeService{status: http.StatusOK, body: `{"text":"three days in Lisbon"}`, fields: map[string]string{}}

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.path = r.URL.Path
		f.auth = r.Header.Get("Authorization")

		if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil {
			reader := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				raw, _ := io.ReadAll(part)
				if part.FileName() != "" {
					f.file = raw
					f.name = part.FileName()
				} else {
					f.fields[part.FormName()] = string(raw)
				}
				_ = part.Close()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeService) client(t *testing.T, apiKey string) *Client {
	t.Helper()
	return NewClient(config.VoiceConfig{
		Provider: "openai_compatible",
		BaseURL:  f.server.URL + "/v1",
		Model:    "Systran/faster-whisper-small",
		APIKey:   apiKey,
	}, newQuietLogger())
}

func TestTranscribeSendsTheRecordingAsAnUpload(t *testing.T) {
	fake := newFakeService(t)
	client := fake.client(t, "")

	audio := []byte("OggS a recording")
	got, err := client.Transcribe(context.Background(), audio, "audio/ogg", "Lisbon, Cais do Sodré")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got != "three days in Lisbon" {
		t.Errorf("transcript = %q", got)
	}

	if fake.path != "/v1/audio/transcriptions" {
		t.Errorf("posted to %q", fake.path)
	}
	if string(fake.file) != string(audio) {
		t.Errorf("the recording did not arrive intact: %q", fake.file)
	}
	if fake.fields["model"] != "Systran/faster-whisper-small" {
		t.Errorf("model = %q", fake.fields["model"])
	}
	// The hint is the difference between "Cais do Sodré" and "Case 2 Soda".
	if fake.fields["prompt"] != "Lisbon, Cais do Sodré" {
		t.Errorf("prompt = %q", fake.fields["prompt"])
	}
	// The cluster's service needs no credential; it is guarded by
	// NetworkPolicy. An empty key must not produce an empty Bearer header,
	// which some services reject outright.
	if fake.auth != "" {
		t.Errorf("an Authorization header was sent with no key: %q", fake.auth)
	}
}

func TestTranscribeSendsAKeyWhenThereIsOne(t *testing.T) {
	fake := newFakeService(t)
	client := fake.client(t, "sk-test")

	if _, err := client.Transcribe(context.Background(), []byte("OggS"), "audio/ogg", ""); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if fake.auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", fake.auth)
	}
	if _, sent := fake.fields["prompt"]; sent {
		t.Error("an empty hint was sent as a prompt")
	}
}

// Multipart uploads to these services are routed on the filename as much as on
// the content type, and an extensionless one is refused by some of them.
func TestTheUploadIsNamedForItsFormat(t *testing.T) {
	tests := []struct {
		mimeType string
		want     string
	}{
		{"audio/ogg", "clip.ogg"},
		{"audio/ogg; codecs=opus", "clip.ogg"},
		{"audio/webm;codecs=opus", "clip.webm"},
		{"audio/mp4", "clip.m4a"},
		{"video/mp4", "clip.mp4"},
		{"audio/mpeg", "clip.mp3"},
		{"audio/wav", "clip.wav"},
		{"", "clip.wav"},
	}
	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			fake := newFakeService(t)
			if _, err := fake.client(t, "").Transcribe(context.Background(), []byte("x"), tt.mimeType, ""); err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			if fake.name != tt.want {
				t.Errorf("filename = %q, want %q", fake.name, tt.want)
			}
		})
	}
}

// Telling somebody their audio was unclear when the service is out of capacity
// sends them back to re-record a clip that was never going to work.
func TestFailuresAreToldApartByWhatTheyMean(t *testing.T) {
	tests := []struct {
		status  int
		failure string
		says    string
	}{
		{401, "unavailable", "switched off"},
		{402, "unavailable", "switched off"},
		{403, "unavailable", "switched off"},
		{429, "busy", "behind on voice notes"},
		{503, "busy", "behind on voice notes"},
		{400, "failed", "could not make that out"},
		{500, "failed", "could not make that out"},
	}
	for _, tt := range tests {
		t.Run(tt.says, func(t *testing.T) {
			fake := newFakeService(t)
			fake.status = tt.status
			fake.body = `{"error":"something internal that must not be repeated"}`

			_, err := fake.client(t, "").Transcribe(context.Background(), []byte("OggS"), "audio/ogg", "")
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := FailureOf(err); got != tt.failure {
				t.Errorf("failure = %q, want %q", got, tt.failure)
			}
			if !strings.Contains(MessageFor(err), tt.says) {
				t.Errorf("message = %q, want it to mention %q", MessageFor(err), tt.says)
			}
			// The provider's body can echo the request back.
			if strings.Contains(err.Error(), "something internal") {
				t.Errorf("the provider's body reached the error: %v", err)
			}
		})
	}
}

// A recording of a pocket is something to answer, not to report.
func TestSilenceIsNotAFailure(t *testing.T) {
	fake := newFakeService(t)
	fake.body = `{"text":"   "}`

	_, err := fake.client(t, "").Transcribe(context.Background(), []byte("OggS"), "audio/ogg", "")
	if !errors.Is(err, ErrNothingHeard) {
		t.Errorf("error = %v, want ErrNothingHeard", err)
	}

	_, err = fake.client(t, "").Transcribe(context.Background(), nil, "audio/ogg", "")
	if !errors.Is(err, ErrNothingHeard) {
		t.Errorf("an empty recording = %v, want ErrNothingHeard", err)
	}
}

// A guessed base URL would point at a vendor nobody asked for. Billing
// somebody by accident is worse than the feature being off.
func TestNothingConfiguredMeansDisabledRatherThanGuessed(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.VoiceConfig
	}{
		{"no base url", config.VoiceConfig{Provider: "openai_compatible", Model: "m"}},
		{"no model", config.VoiceConfig{Provider: "openai_compatible", BaseURL: "http://whisper:8000/v1"}},
		{"nothing at all", config.VoiceConfig{}},
		{"a provider nobody implemented", config.VoiceConfig{
			Provider: "some-vendor", BaseURL: "http://whisper:8000/v1", Model: "m",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.cfg, newQuietLogger())
			if client.Enabled() {
				t.Error("transcription reports itself enabled with nothing to call")
			}
			if _, err := client.Transcribe(context.Background(), []byte("OggS"), "audio/ogg", ""); !errors.Is(err, ErrDisabled) {
				t.Errorf("Transcribe = %v, want ErrDisabled", err)
			}
			// And it says the honest thing rather than blaming the audio.
			if !strings.Contains(MessageFor(ErrDisabled), "switched off") {
				t.Error("a disabled service should not read as unclear audio")
			}
		})
	}
}

// The hint is fed to the decoder as if it were preceding speech, and these
// models look back a fixed, small distance. A long one is not more help.
func TestALongHintIsCutRatherThanSent(t *testing.T) {
	fake := newFakeService(t)
	long := strings.Repeat("Bairro Alto, ", 500)

	if _, err := fake.client(t, "").Transcribe(context.Background(), []byte("OggS"), "audio/ogg", long); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(fake.fields["prompt"]) > maxHintBytes {
		t.Errorf("prompt is %d bytes, past the %d cap", len(fake.fields["prompt"]), maxHintBytes)
	}
	if fake.fields["prompt"] == "" {
		t.Error("the hint was dropped entirely rather than cut")
	}
}

func TestATimedOutRequestReadsAsBusyRatherThanBroken(t *testing.T) {
	fake := newFakeService(t)
	client := fake.client(t, "")

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	_, err := client.Transcribe(ctx, []byte("OggS"), "audio/ogg", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	// The service is CPU-bound on a shared replica, so a slow answer means a
	// queue, and a queue is worth trying again.
	if got := FailureOf(err); got != "busy" {
		t.Errorf("failure = %q, want busy", got)
	}
}
