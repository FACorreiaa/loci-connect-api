package retrieval

import "fmt"

// Shared retrieval bounds. Every adapter that exposes retrieval — the Connect
// handlers, the chat service, and the MCP tool set — validates against these
// constants instead of carrying its own literal, so a limit means the same
// thing on every surface.
const (
	MinLimit = 1

	// MaxSearchResults bounds a keyword/semantic search response.
	MaxSearchResults = 50
	// MaxNearbyResults bounds a radius search response.
	MaxNearbyResults = 50
	// MaxEvidence bounds how many candidates may enter a single ContextPacket.
	// This is the real token guard on the prompt path.
	MaxEvidence = 20
	// MaxFactsPerEvidence bounds the crowd-verified facts attached per POI.
	MaxFactsPerEvidence = 6
	// MaxTraitLabels bounds how many taste-trait labels are rendered.
	MaxTraitLabels = 8
	// MaxDescriptionChars truncates POI descriptions inside a packet.
	MaxDescriptionChars = 300
	// MaxQueryChars rejects pathologically long queries before they reach the
	// database or an embedding provider.
	MaxQueryChars = 512
)

// DefaultEvidence is the packet size used when a caller does not ask for one.
const DefaultEvidence = 10

// ValidateLimit clamps a caller-supplied limit into [MinLimit, maximum],
// returning an error when the value is out of range rather than silently
// coercing it. Callers that prefer coercion use ClampLimit.
func ValidateLimit(value int, name string, maximum int) (int, error) {
	if value < MinLimit || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d; got %d", name, MinLimit, maximum, value)
	}
	return value, nil
}

// ClampLimit coerces value into [MinLimit, maximum], substituting fallback for
// non-positive input. Used on paths where an out-of-range limit should degrade
// rather than fail, such as optional MCP tool arguments.
func ClampLimit(value, fallback, maximum int) int {
	if value <= 0 {
		value = fallback
	}
	if value < MinLimit {
		return MinLimit
	}
	if value > maximum {
		return maximum
	}
	return value
}

// TruncateRunes shortens s to at most maxRunes runes, appending an ellipsis
// when it had to cut. Rune-aware so multi-byte place names are never split
// mid-character.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}
