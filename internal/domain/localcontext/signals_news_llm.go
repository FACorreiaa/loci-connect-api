package localcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// NewsVerdict is one headline's classification.
type NewsVerdict struct {
	// Relevant is true only when the headline describes disruption a traveller
	// at this destination could actually run into.
	Relevant bool `json:"relevant"`
	// Reason is the model's one-line justification. Kept for logs and for
	// judging whether the classifier is earning its keep, not shown to users.
	Reason string `json:"reason"`
}

// NewsClassifier decides which headlines actually describe travel disruption.
//
// A narrow interface rather than an AI client, so this package keeps no
// dependency on the AI domain — the same reasoning behind CityResolver and
// POICounter above.
type NewsClassifier interface {
	Classify(ctx context.Context, country string, headlines []string) ([]NewsVerdict, error)
}

// TextFunc generates text from a prompt.
//
// A function rather than an interface so localcontext never imports genai: the
// wiring layer adapts whatever chat client it holds into this shape.
type TextFunc func(ctx context.Context, prompt string) (string, error)

// LLMNewsClassifier filters headlines with a language model.
//
// It exists because the heuristic prefilter cannot do this job. GDELT's index
// answers "strike" with military strikes, hunger strikes and football, and no
// keyword list separates "rail workers announce a strike Thursday" from
// "Russian strikes hit Kyiv" reliably enough to put in front of a user.
type LLMNewsClassifier struct {
	generate TextFunc
}

func NewLLMNewsClassifier(generate TextFunc) *LLMNewsClassifier {
	return &LLMNewsClassifier{generate: generate}
}

// Classify asks the model which headlines describe travel disruption.
//
// One call for the whole batch rather than one per headline: this already runs
// behind a slow source, and per-headline calls would multiply both latency and
// cost for a signal that is Minor severity by design.
func (c *LLMNewsClassifier) Classify(ctx context.Context, country string, headlines []string) ([]NewsVerdict, error) {
	if c == nil || c.generate == nil {
		return nil, fmt.Errorf("news classifier: no generator configured")
	}
	if len(headlines) == 0 {
		return nil, nil
	}

	var b strings.Builder
	for i, h := range headlines {
		fmt.Fprintf(&b, "%d. %s\n", i+1, h)
	}

	prompt := fmt.Sprintf(`You are filtering news headlines for a travel app.

A traveller is visiting %s. For each headline below, decide whether it describes
a disruption they could actually run into on the ground: transport strikes,
airport or station closures, cancelled flights or trains, protests blocking
travel, or major venue or site closures.

Answer false for anything else. In particular answer false for:
- military strikes, airstrikes, or fighting
- hunger strikes
- sport (a "striker", a match, a league)
- strikes or unrest in a different country from %s
- opinion, analysis, or coverage of a strike that already ended

Headlines:
%s
Reply with ONLY a JSON array, one object per headline in the same order:
[{"relevant": true, "reason": "rail strike Thursday, trains cancelled"}]
No markdown, no code fence, no commentary.`, country, country, b.String())

	raw, err := c.generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("news classifier: %w", err)
	}

	verdicts, err := parseNewsVerdicts(raw)
	if err != nil {
		return nil, err
	}

	// A model that returns the wrong number of verdicts cannot be aligned with
	// the headlines by position, and guessing the alignment would attach one
	// headline's verdict to another. Pad conservatively instead: anything the
	// model did not judge is not shown.
	if len(verdicts) < len(headlines) {
		for len(verdicts) < len(headlines) {
			verdicts = append(verdicts, NewsVerdict{Relevant: false, Reason: "not classified"})
		}
	}
	return verdicts[:len(headlines)], nil
}

// parseNewsVerdicts tolerates the code fences models add despite being asked
// not to.
func parseNewsVerdicts(raw string) ([]NewsVerdict, error) {
	s := strings.TrimSpace(raw)
	if fence := strings.Index(s, "```"); fence >= 0 {
		s = s[fence+3:]
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			// Drop a language tag such as ```json.
			if !strings.Contains(s[:nl], "[") {
				s = s[nl+1:]
			}
		}
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	}
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("news classifier: no JSON array in response %q", truncate(raw, 200))
	}

	var out []NewsVerdict
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("news classifier: %w (response %q)", err, truncate(raw, 200))
	}
	return out, nil
}
