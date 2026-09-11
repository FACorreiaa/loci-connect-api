package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWrapPCMWritesACanonicalHeader(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	got, err := wrapPCM(pcm, 24000, 1, 16)
	if err != nil {
		t.Fatalf("wrapPCM: %v", err)
	}
	if len(got) != wavHeaderBytes+len(pcm) {
		t.Fatalf("length = %d, want %d", len(got), wavHeaderBytes+len(pcm))
	}

	if string(got[0:4]) != "RIFF" || string(got[8:12]) != "WAVE" {
		t.Errorf("not a RIFF/WAVE file: %q", got[:12])
	}
	if string(got[12:16]) != "fmt " || string(got[36:40]) != "data" {
		t.Errorf("chunks are wrong: %q %q", got[12:16], got[36:40])
	}

	// A wrong rate or block alignment does not fail — it plays the reply at
	// the wrong speed, which is harder to notice than an error, so the numbers
	// are asserted exactly.
	checks := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"riff size", binary.LittleEndian.Uint32(got[4:8]), uint32(36 + len(pcm))},
		{"fmt chunk size", binary.LittleEndian.Uint32(got[16:20]), 16},
		{"sample rate", binary.LittleEndian.Uint32(got[24:28]), 24000},
		{"byte rate", binary.LittleEndian.Uint32(got[28:32]), 24000 * 2},
		{"data size", binary.LittleEndian.Uint32(got[40:44]), uint32(len(pcm))},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if n := binary.LittleEndian.Uint16(got[20:22]); n != 1 {
		t.Errorf("format = %d, want 1 (uncompressed PCM)", n)
	}
	if n := binary.LittleEndian.Uint16(got[22:24]); n != 1 {
		t.Errorf("channels = %d, want 1", n)
	}
	if n := binary.LittleEndian.Uint16(got[32:34]); n != 2 {
		t.Errorf("block align = %d, want 2", n)
	}
	if n := binary.LittleEndian.Uint16(got[34:36]); n != 16 {
		t.Errorf("bits per sample = %d, want 16", n)
	}

	if !bytes.Equal(got[44:], pcm) {
		t.Error("the samples did not survive the wrap")
	}
}

func TestWrapPCMRejectsNonsense(t *testing.T) {
	tests := []struct {
		name                 string
		pcm                  []byte
		rate, channels, bits int
	}{
		{"no samples", nil, 24000, 1, 16},
		{"no rate", []byte{0x01}, 0, 1, 16},
		{"no channels", []byte{0x01}, 24000, 0, 16},
		{"no sample width", []byte{0x01}, 24000, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := wrapPCM(tt.pcm, tt.rate, tt.channels, tt.bits); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestSampleRateOf(t *testing.T) {
	tests := []struct {
		mimeType string
		want     int
	}{
		{"audio/L16;codec=pcm;rate=24000", 24000},
		{"audio/L16; codec=pcm; rate=16000", 16000},
		{"audio/L16;RATE=48000", 48000},
		// Anything unreadable falls back to the documented rate rather than
		// to zero: a wrong rate plays at the wrong speed, and zero fails.
		{"audio/L16", defaultSampleRate},
		{"audio/L16;rate=", defaultSampleRate},
		{"audio/L16;rate=nonsense", defaultSampleRate},
		{"audio/L16;rate=0", defaultSampleRate},
		{"", defaultSampleRate},
	}
	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := sampleRateOf(tt.mimeType); got != tt.want {
				t.Errorf("sampleRateOf(%q) = %d, want %d", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestEncodeOggProducesAnOggOpusStream(t *testing.T) {
	if _, err := exec.LookPath(encoderBinary); err != nil {
		t.Skipf("%s is not installed", encoderBinary)
	}

	// Half a second of silence at 24 kHz, mono, 16-bit.
	wav, err := wrapPCM(make([]byte, 24000), 24000, 1, 16)
	if err != nil {
		t.Fatalf("wrapPCM: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := encodeOgg(ctx, wav)
	if err != nil {
		t.Fatalf("encodeOgg: %v", err)
	}
	if !bytes.HasPrefix(got, []byte("OggS")) {
		t.Errorf("output is not an Ogg stream: %q", got[:min(8, len(got))])
	}
	// Telegram plays a voice note only if it is Opus in that container.
	if !bytes.Contains(got[:min(128, len(got))], []byte("OpusHead")) {
		t.Error("output is an Ogg stream but does not carry Opus")
	}
}

func TestEncodeOggRejectsEmptyInput(t *testing.T) {
	if _, err := encodeOgg(context.Background(), nil); err == nil {
		t.Error("expected an error")
	}
}

func TestDisabledClientIsUsableAndSaysSo(t *testing.T) {
	// New returns a nil *Client when speech is not configured, and every
	// caller holds it as a pointer rather than checking first — so the methods
	// have to survive it.
	var c *Client

	if c.CanSpeak() {
		t.Error("a nil client should not claim it can speak")
	}
	if _, err := c.Transcribe(context.Background(), []byte{0x01}, "audio/ogg"); !errors.Is(err, ErrDisabled) {
		t.Errorf("Transcribe on a nil client = %v, want ErrDisabled", err)
	}
	if _, err := c.Shorten(context.Background(), "anything"); !errors.Is(err, ErrDisabled) {
		t.Errorf("Shorten on a nil client = %v, want ErrDisabled", err)
	}
	if _, err := c.Say(context.Background(), "anything", time.Second); !errors.Is(err, ErrDisabled) {
		t.Errorf("Say on a nil client = %v, want ErrDisabled", err)
	}
}

func TestTranscribeTreatsAnEmptyRecordingAsSilence(t *testing.T) {
	// No provider call is made, so the client needs no credential: an empty
	// recording is answered, not reported.
	c := &Client{}
	if _, err := c.Transcribe(context.Background(), nil, "audio/ogg"); !errors.Is(err, ErrNothingHeard) {
		t.Errorf("Transcribe of nothing = %v, want ErrNothingHeard", err)
	}
}

func TestShortenLeavesAlreadyShortRepliesAlone(t *testing.T) {
	// A short reply must not reach the provider — the client here has none,
	// so a call would panic and the test would say so.
	c := &Client{}
	short := "Start at the Jerónimos Monastery, then walk down to the water for lunch."

	got, err := c.Shorten(context.Background(), short)
	if err != nil {
		t.Fatalf("Shorten: %v", err)
	}
	if got != short {
		t.Errorf("Shorten rewrote a short reply: %q", got)
	}

	if _, err := c.Shorten(context.Background(), "   "); err == nil {
		t.Error("expected an error for blank text")
	}
	if len(strings.TrimSpace(shortenPrompt)) == 0 {
		t.Error("the shorten prompt is empty")
	}
}
