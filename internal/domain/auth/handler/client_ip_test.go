package handler

import (
	"net/http"
	"testing"
)

// Sessions used to record req.Peer().Addr. Behind the ingress that is the proxy
// pod, so every entry on the signed-in-devices screen read the same
// 10.42.x.x:<port> and the screen could not answer "which of these is me?".
func TestSessionClientIPUsesTheInstalledResolver(t *testing.T) {
	original := clientIPResolver
	t.Cleanup(func() { clientIPResolver = original })

	t.Run("defaults to the peer address", func(t *testing.T) {
		clientIPResolver = original
		if got := resolveClientIP(http.Header{}, "10.42.0.87:34882"); got != "10.42.0.87:34882" {
			t.Errorf("got %q, want the peer address untouched when no resolver is installed", got)
		}
	})

	t.Run("uses the resolver once installed", func(t *testing.T) {
		SetClientIPResolver(func(header http.Header, _ string) string {
			return header.Get("X-Forwarded-For")
		})

		header := http.Header{}
		header.Set("X-Forwarded-For", "203.0.113.9")

		got := resolveClientIP(header, "10.42.0.87:34882")
		if got != "203.0.113.9" {
			t.Errorf("got %q, want the forwarded client address", got)
		}
		if got == "10.42.0.87:34882" {
			t.Error("still recording the ingress pod's address")
		}
	})

	// A nil resolver must not blank the field — better the proxy address than
	// nothing at all.
	t.Run("ignores a nil resolver", func(t *testing.T) {
		clientIPResolver = original
		SetClientIPResolver(nil)
		if got := resolveClientIP(http.Header{}, "10.42.0.87:34882"); got == "" {
			t.Error("a nil resolver blanked the address")
		}
	})
}
