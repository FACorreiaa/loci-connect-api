package speech

import (
	"encoding/binary"
	"fmt"
)

// wavHeaderBytes is the size of a canonical RIFF/WAVE header with a single
// fmt chunk and no extensions.
const wavHeaderBytes = 44

// wrapPCM puts a WAV header on raw PCM samples.
//
// Gemini answers with headerless PCM, and nothing downstream will take it that
// way: an encoder has to be told the sample rate, the channel count and the
// sample width, and a WAV header is the ordinary way to say all three. Written
// here rather than passed to the encoder as flags, because it is twelve lines
// that survive a change of encoder or of speech provider, and a flag surface
// is not.
func wrapPCM(pcm []byte, sampleRate, channels, bitsPerSample int) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, fmt.Errorf("speech: no samples to wrap")
	}
	if sampleRate <= 0 || channels <= 0 || bitsPerSample <= 0 {
		return nil, fmt.Errorf("speech: cannot wrap %d Hz, %d channels, %d bits",
			sampleRate, channels, bitsPerSample)
	}

	blockAlign := channels * bitsPerSample / 8
	byteRate := sampleRate * blockAlign

	out := make([]byte, wavHeaderBytes+len(pcm))
	copy(out[0:4], "RIFF")
	// Everything after this field, which is the header's remaining 36 bytes
	// plus the samples.
	binary.LittleEndian.PutUint32(out[4:8], uint32(36+len(pcm)))
	copy(out[8:12], "WAVE")

	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(out[20:22], 1)  // format 1 is uncompressed PCM
	binary.LittleEndian.PutUint16(out[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(out[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(out[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(out[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(out[34:36], uint16(bitsPerSample))

	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(pcm)))
	copy(out[44:], pcm)

	return out, nil
}
