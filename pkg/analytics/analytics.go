// Package analytics captures product events server-side.
//
// It exists because the browser cannot see everything that matters. Two of the
// launch metrics have no client at all: a trip re-opened after a day is a fact
// only the server observes, and MCP tool calls come from agents with no browser
// running. Sending those from here puts them in the same dataset, keyed by the
// same user, as the events the web client sends, so the funnel can be joined
// rather than read as three disconnected systems.
//
// This is deliberately not the metrics path. Metrics carry volume with
// low-cardinality labels and must never hold a user id; these events carry the
// user and answer "who", which is the question retention is made of.
package analytics

import (
	"log/slog"
	"strings"
)

// Event is one captured product event.
type Event struct {
	DistinctID string
	Name       string
	Properties map[string]any
}

// Sink is the transport. The PostHog client satisfies it; tests supply their
// own so no test needs a network or an API key.
type Sink interface {
	Enqueue(Event) error
	Close() error
}

// Recorder captures events, or does nothing at all.
//
// A nil *Recorder is valid and inert, so a caller holding this as an optional
// dependency never needs a nil check at the call site. Capture never returns an
// error and never blocks on the network: analytics must not be the reason a
// user's request fails or slows down.
type Recorder struct {
	sink   Sink
	logger *slog.Logger
}

// New returns a Recorder. A nil sink yields an inert recorder, which is the
// normal state wherever no PostHog key is configured.
func New(sink Sink, logger *slog.Logger) *Recorder {
	if sink == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{sink: sink, logger: logger}
}

// Capture records that userID did something.
//
// An event with no user is dropped. PostHog would otherwise create a person for
// it, and a phantom user inflates exactly the counts this exists to measure.
func (r *Recorder) Capture(userID, event string, properties map[string]any) {
	if r == nil || r.sink == nil {
		return
	}
	if strings.TrimSpace(userID) == "" {
		return
	}
	if err := r.sink.Enqueue(Event{DistinctID: userID, Name: event, Properties: properties}); err != nil {
		r.logger.Warn("analytics capture failed", "event", event, "error", err)
	}
}

// Close flushes anything queued. Safe on a nil Recorder.
func (r *Recorder) Close() error {
	if r == nil || r.sink == nil {
		return nil
	}
	return r.sink.Close()
}
