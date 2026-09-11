package speech

import (
	"strings"
	"testing"
)

func TestNormaliseType(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		// Browsers attach codec parameters; the format is still the format.
		{"audio/webm;codecs=opus", "audio/webm"},
		{"audio/ogg; codecs=opus", "audio/ogg"},
		{"AUDIO/WAV", "audio/wav"},
		{"  audio/wav  ", "audio/wav"},
		{"audio/wav", "audio/wav"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := normaliseType(tt.raw); got != tt.want {
				t.Errorf("normaliseType(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// The MIME type is what the model is told the bytes are. A wrong one does not
// fail — it reads as a recording full of noise — so the list is an allowlist
// rather than a guess.
func TestTheAllowedTypesAreAllAudio(t *testing.T) {
	if len(allowedTypes) == 0 {
		t.Fatal("no audio formats are allowed at all")
	}
	for mimeType := range allowedTypes {
		if !strings.HasPrefix(mimeType, "audio/") {
			t.Errorf("%q is not an audio type", mimeType)
		}
		if normaliseType(mimeType) != mimeType {
			t.Errorf("%q is not in its normalised form, so it can never match", mimeType)
		}
	}

	// The two that actually arrive: Telegram sends Ogg, and the web client
	// records WAV because it is the one format every browser can produce and
	// the provider documents.
	for _, required := range []string{"audio/ogg", "audio/wav"} {
		if _, ok := allowedTypes[required]; !ok {
			t.Errorf("%q is not allowed, and it is one of the two that arrive", required)
		}
	}
}
