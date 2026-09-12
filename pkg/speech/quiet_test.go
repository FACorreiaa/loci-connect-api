package speech

import (
	"io"
	"log/slog"
)

// newQuietLogger keeps test output readable; these tests deliberately exercise
// paths that log at warn and error.
func newQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
