package speech

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// encoderBinary is the program that turns WAV into OGG/Opus.
//
// A subprocess rather than a library because Go has no Opus encoder: pion's
// package decodes only, and every other binding is cgo, which this binary is
// not built with. opus-tools is a couple of megabytes in the runtime image and
// carries opusdec as well, which is the transcode to reach for if Gemini ever
// refuses Telegram's Ogg on the way in.
const encoderBinary = "opusenc"

// encoderBitrate is what a spoken reply is encoded at.
//
// 24 kbit/s is generous for one voice and keeps a half-minute summary well
// under a hundred kilobytes, which matters because it is uploaded on the tail
// of a request that has already taken a minute and a half.
const encoderBitrate = "24"

// maxEncodedBytes bounds what is read back from the encoder. A minute of
// speech at this bitrate is about 180 KB; this is far above that and far below
// anything worth buffering.
const maxEncodedBytes = 8 << 20

// ErrEncoderMissing reports that the Opus encoder is not installed.
//
// Named so a deployment can tell "this image was built without opus-tools"
// apart from "the encoder failed on this input". The first is a Dockerfile
// problem and affects every reply; the second is one bad recording.
var ErrEncoderMissing = errors.New("speech: the opus encoder is not installed")

// encodeOgg turns WAV audio into OGG/Opus, the only format Telegram will play
// as a voice note rather than as a file attachment.
//
// Nothing user-controlled reaches the argument list: the audio travels on
// stdin and comes back on stdout, so there is no file to name and no path to
// escape. The context carries the deadline, so a wedged encoder is cancelled
// rather than held.
func encodeOgg(ctx context.Context, wav []byte) ([]byte, error) {
	if len(wav) == 0 {
		return nil, fmt.Errorf("speech: nothing to encode")
	}

	// "--" so a future flag change cannot turn the "-" operands into options.
	cmd := exec.CommandContext(ctx, encoderBinary,
		"--quiet", "--bitrate", encoderBitrate, "--", "-", "-")
	cmd.Stdin = bytes.NewReader(wav)

	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrEncoderMissing
		}
		// The encoder's own complaint is about the audio, not about us, and it
		// is the only thing that says which.
		return nil, fmt.Errorf("speech: could not encode the reply: %s",
			strings.TrimSpace(stderr.String()))
	}

	if out.Len() == 0 {
		return nil, fmt.Errorf("speech: the encoder produced nothing")
	}
	if out.Len() > maxEncodedBytes {
		return nil, fmt.Errorf("speech: the encoder produced %d bytes, more than the %d allowed",
			out.Len(), maxEncodedBytes)
	}
	return out.Bytes(), nil
}
