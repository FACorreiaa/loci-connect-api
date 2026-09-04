package analytics

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/posthog/posthog-go"
)

// defaultHost is PostHog's EU cloud, where this project lives. US keys must set
// POSTHOG_HOST explicitly; sending EU data to the US endpoint silently lands in
// the wrong region.
const defaultHost = "https://eu.i.posthog.com"

// shutdownTimeout bounds the flush on Close so a slow or unreachable PostHog
// cannot hold up the server's shutdown.
const shutdownTimeout = 5 * time.Second

type posthogSink struct {
	client posthog.Client
}

// NewPostHogSink builds the PostHog-backed sink from the environment, or
// returns a nil sink when POSTHOG_API_KEY is unset.
//
// A nil sink is not an error: it is how this stays off by default in
// development and in any deployment without a key. Pass the result to New and
// the resulting recorder is inert.
//
// The key is the same phc_ project key the web client uses, so browser and
// server events land in one project and can be joined on the user.
func NewPostHogSink(logger *slog.Logger) (Sink, error) {
	key := strings.TrimSpace(os.Getenv("POSTHOG_API_KEY"))
	if key == "" {
		if logger != nil {
			logger.Info("POSTHOG_API_KEY not set; server-side product analytics disabled")
		}
		return nil, nil
	}

	host := strings.TrimSpace(os.Getenv("POSTHOG_HOST"))
	if host == "" {
		host = defaultHost
	}

	client, err := posthog.NewWithConfig(key, posthog.Config{
		Endpoint: host,
		// Bound the flush so shutdown cannot block on an unreachable PostHog.
		ShutdownTimeout: shutdownTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create posthog client: %w", err)
	}
	if logger != nil {
		logger.Info("server-side product analytics enabled", "host", host)
	}
	return &posthogSink{client: client}, nil
}

func (s *posthogSink) Enqueue(e Event) error {
	props := posthog.NewProperties()
	for k, v := range e.Properties {
		props.Set(k, v)
	}
	return s.client.Enqueue(posthog.Capture{
		DistinctId: e.DistinctID,
		Event:      e.Name,
		Properties: props,
	})
}

func (s *posthogSink) Close() error { return s.client.Close() }
