package tripaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Generator is the one LLM call extraction makes. The chat service supplies
// it, on its own model chain and LLM slots.
type Generator interface {
	GenerateText(ctx context.Context, prompt string) (string, error)
}

// Snapshot is what extraction is told about the trip the message is about.
type Snapshot struct {
	Cities    []string
	StartDate string
	EndDate   string
	Days      int
	Today     time.Time
}

// maxActions caps one message's cards; a reply listing more is a model
// rambling, not a traveller asking.
const maxActions = 4

const extractPrompt = `You turn a traveller's chat message into changes to the trip they are editing.
Reply with JSON only: {"actions": [...]}. An empty list means the message asks for no change.

The trip: cities %s; dates %s; %d days planned. Today is %s; a date without a year is its next occurrence.

Action kinds. Use only these, and only for what the message asks:
- {"kind":"set_dates","start_date":"YYYY-MM-DD","end_date":"YYYY-MM-DD"}  ("5 days from 12 Nov" ends 16 Nov)
- {"kind":"search_hotels","city":"<a city of the trip>","min_stars":N,"max_stars":M}  ("4-star" is 4..4; "4 stars or more" is 4..5; no stars mentioned is 0..0)
- {"kind":"regenerate_days","days":N}  (re-plan the itinerary as N days)
- {"kind":"search_flights","origin":{"name":"...","iata":"XXX"},"destination":{"name":"...","iata":"XXX"},"depart":"YYYY-MM-DD","return":"YYYY-MM-DD","passengers":N,"cabin":"economy|premium_economy|business|first"}  (omit return for one way; omit iata when unsure)

Example message: "4-star hotels in Lisbon from 12 to 17 November, and make it 6 days"
Example reply: {"actions":[{"kind":"set_dates","start_date":"2026-11-12","end_date":"2026-11-17"},{"kind":"search_hotels","city":"Lisbon","min_stars":4,"max_stars":4},{"kind":"regenerate_days","days":6}]}

Message: %s`

// Extract asks the model which changes message wants, retrying once when the
// reply is not JSON, and keeps only actions that pass normalize. An error
// means the model could not be asked or never answered in JSON; the caller
// answers the message as usual.
func Extract(ctx context.Context, gen Generator, message string, snap Snapshot) ([]Action, error) {
	dates := "not set"
	if snap.StartDate != "" {
		dates = snap.StartDate + " to " + snap.EndDate
	}
	prompt := fmt.Sprintf(extractPrompt, strings.Join(snap.Cities, ", "), dates, snap.Days,
		snap.Today.Format(time.DateOnly), message)

	var (
		raw []Action
		err error
	)
	for attempt := 0; attempt < 2; attempt++ {
		var text string
		if text, err = gen.GenerateText(ctx, prompt); err != nil {
			return nil, fmt.Errorf("extract trip actions: %w", err)
		}
		if raw, err = parseActions(text); err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}

	out := make([]Action, 0, len(raw))
	for _, a := range raw {
		if n, verr := normalize(a, snap.Cities); verr == nil {
			out = append(out, n)
		}
		if len(out) == maxActions {
			break
		}
	}
	return out, nil
}

// parseActions reads the first JSON object in text, fenced or not.
func parseActions(text string) ([]Action, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil, errors.New("no JSON object in the reply")
	}
	var doc struct {
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &doc); err != nil {
		return nil, fmt.Errorf("parse trip actions: %w", err)
	}
	return doc.Actions, nil
}
