package apikey

import "fmt"

// ClientKind is which agent a key's setup instructions were written for.
//
// Presentation only. Nothing about authentication varies by kind, and a key
// minted for one client works in any of them; this exists so the settings page
// can show what a key was made for, which is how somebody decides which one to
// revoke.
type ClientKind string

const (
	ClientClaudeCode    ClientKind = "claude_code"
	ClientClaudeDesktop ClientKind = "claude_desktop"
	ClientCursor        ClientKind = "cursor"
	ClientCodex         ClientKind = "codex"
	ClientHermes        ClientKind = "hermes"

	// ClientOther is the generic MCP configuration, and the default: keys
	// minted before this existed carry it, and so does anything unrecognised
	// at the edges of the system.
	ClientOther ClientKind = "other"
)

// ClientKinds is every kind, in the order the settings page offers them.
//
// The database CHECK constraint on api_keys.client_kind lists the same values;
// adding one here is a migration as well (see 0083).
var ClientKinds = []ClientKind{
	ClientClaudeCode, ClientClaudeDesktop, ClientCursor, ClientCodex, ClientHermes, ClientOther,
}

// Label is how the kind is written for a person.
func (k ClientKind) Label() string {
	switch k {
	case ClientClaudeCode:
		return "Claude Code"
	case ClientClaudeDesktop:
		return "Claude Desktop"
	case ClientCursor:
		return "Cursor"
	case ClientCodex:
		return "Codex"
	case ClientHermes:
		return "Hermes"
	case ClientOther:
		return "Other MCP client"
	default:
		return string(k)
	}
}

// Valid reports whether this is a kind the database will accept.
func (k ClientKind) Valid() bool {
	for _, known := range ClientKinds {
		if k == known {
			return true
		}
	}
	return false
}

// ParseClientKind converts input from the API.
//
// Empty means ClientOther, so a caller that does not know about client kinds
// still gets a working key with the generic instructions. Anything else
// unrecognised is an error rather than a silent fallback: storing it would put
// a value in the column the settings page has no name for, and quietly
// rewriting it to "other" would tell the caller their choice was accepted when
// it was discarded.
func ParseClientKind(raw string) (ClientKind, error) {
	if raw == "" {
		return ClientOther, nil
	}
	kind := ClientKind(raw)
	if !kind.Valid() {
		return "", fmt.Errorf("%q is not a client kind Loci writes setup instructions for", raw)
	}
	return kind, nil
}
