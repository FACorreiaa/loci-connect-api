package push

import (
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
)

// Payload is what the service worker (and, later, the iOS app) receives.
// sessionId / cityName / domain are the keys the iOS client routes on.
type Payload struct {
	SessionID string `json:"sessionId"`
	CityName  string `json:"cityName"`
	Domain    string `json:"domain"`
	Status    string `json:"status"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	URL       string `json:"url"`

	// Set only on a proactive message (a standing task posting into a
	// thread); a finished search leaves them empty and its JSON unchanged.
	MessageID   string `json:"messageId,omitempty"`
	Origin      string `json:"origin,omitempty"`
	SourceLabel string `json:"sourceLabel,omitempty"`
	// Category and ThreadID go under "aps" in the APNs body; empty means the
	// search defaults (no category, thread "search").
	Category string `json:"category,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
}

var nouns = map[string]string{
	"accommodation": "hotels",
	"dining":        "restaurants",
	"activities":    "activities",
	"nearby":        "nearby places",
}

func BuildPayload(run runs.Run) Payload {
	// Every mapped noun is plural ("hotels are ready"); the itinerary
	// fallback is the one singular.
	noun, plural := nouns[run.Domain]
	if !plural {
		noun = "itinerary"
	}
	verb := "is"
	if plural {
		verb = "are"
	}
	subject := strings.TrimSpace(run.CityName + " " + noun)
	path, routeType, _ := runs.ResultPath(run.Domain, run.SessionID, run.CityName, uuid.Nil)
	p := Payload{
		SessionID: run.SessionID.String(),
		CityName:  run.CityName,
		Domain:    routeType,
		URL:       path,
	}
	if run.Status == runs.StatusDone {
		p.Status, p.Title, p.Body = "done", "Your "+subject+" "+verb+" ready", "Tap to open it."
	} else {
		p.Status, p.Title, p.Body = "failed", "Your "+subject+" didn't finish", "Tap to try again."
	}
	return p
}
