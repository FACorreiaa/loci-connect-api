package localcontext

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// These are the real titles a live `strike sourcecountry:FR` query returned
// while this source was written. Five articles, none of them a travel
// disruption — the concrete reason the prefilter and the classifier exist.
var realGDELTNoise = []string{
	"Cyberattaque dans l Éducation nationale : un syndicat envisage un préavis de grève",
	"Incendies , tornades , inondations : du Népal à la France , le dérèglement climatique",
	"Ukraine : au moins 16 morts dans des frappes russes , Zelensky dénonce une escalade",
	"Deux militants séropositifs en grève de la faim reçus par Aurore Bergé",
	"Ligue Europa Conférence : lAS Monaco écrase le Gornik Zabrze et valide sa qualification",
}

func TestLooksLikeTravelDisruption_RejectsTheWrongSensesOfStrike(t *testing.T) {
	reject := []string{
		"Russian air strikes hit Kyiv overnight",
		"Activists enter third week of hunger strike",
		"Striker scores twice as United win the league",
		"Drone strike kills militants",
		"Lightning strike causes power cut",
	}
	for _, title := range reject {
		if looksLikeTravelDisruption(title) {
			t.Errorf("should have rejected: %q", title)
		}
	}
}

func TestLooksLikeTravelDisruption_AcceptsRealDisruption(t *testing.T) {
	accept := []string{
		"Rail strike Thursday: most regional trains cancelled",
		"Metro workers announce walkout",
		"Airport closed after protest blocks access road",
		"Grève des transports publics à Lisbonne",
		"Ferry services suspended",
	}
	for _, title := range accept {
		if !looksLikeTravelDisruption(title) {
			t.Errorf("should have accepted: %q", title)
		}
	}
}

// The measured case, and an honest account of where the prefilter's limit is.
//
// Four of the five real articles are clear-cut wrong senses and the keyword
// filter must reject all of them. The fifth — a union *considering* a strike
// notice over a cyberattack in schools — really is about a strike, just not one
// a traveller would meet. No keyword list can make that call, which is the
// concrete argument for the classifier rather than a hand-wave at "noise".
func TestLooksLikeTravelDisruption_OnTheRealNoiseSample(t *testing.T) {
	const classifierTerritory = 0 // the education/union headline

	for i, title := range realGDELTNoise {
		got := looksLikeTravelDisruption(title)
		if i == classifierTerritory {
			if !got {
				t.Errorf("this one is expected to reach the classifier, not be "+
					"filtered by keywords: %q", title)
			}
			continue
		}
		if got {
			t.Errorf("prefilter let clear-cut noise through: %q", title)
		}
	}
}

// --- stale-while-revalidate ------------------------------------------------

const gdeltFixture = `{"articles":[
 {"title":"Rail strike Thursday: regional trains cancelled","url":"http://x/1","domain":"lemonde.fr","sourcecountry":"France","seendate":"20260827T183000Z"},
 {"title":"Striker scores twice in the league","url":"http://x/2","domain":"sport.fr","sourcecountry":"France","seendate":"20260827T183000Z"},
 {"title":"Metro walkout planned","url":"http://x/3","domain":"leparisien.fr","sourcecountry":"France","seendate":"20260827T183000Z"}
]}`

func newsSource(t *testing.T, body string, status int, c NewsClassifier) (*NewsSource, *int64) {
	t.Helper()
	url, hits := serve(t, body, status)
	return NewNewsSource(url, testClient(), c, nil), hits
}

// waitFor polls until cond holds, because the refresh is detached by design.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// The central promise: GDELT takes 23-26s, so the first call must return
// immediately with nothing rather than making a trip view wait.
func TestNews_FirstCallReturnsImmediatelyAndRefreshesInBackground(t *testing.T) {
	s, hits := newsSource(t, gdeltFixture, http.StatusOK, nil)
	req := SignalRequest{CountryCode: "FR"}

	started := time.Now()
	got, err := s.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Errorf("the first call must not block on the upstream, took %v", elapsed)
	}
	if len(got) != 0 {
		t.Errorf("expected nothing on a cold cache, got %d", len(got))
	}

	if !waitFor(t, func() bool { return *hits > 0 }) {
		t.Fatal("expected a background refresh to run")
	}

	// Once the refresh lands, a later call serves it.
	if !waitFor(t, func() bool {
		out, _ := s.Fetch(context.Background(), req)
		return len(out) > 0
	}) {
		t.Fatal("expected the refreshed alerts to be served")
	}
}

func TestNews_ProducesLabelledMinorAlerts(t *testing.T) {
	s, _ := newsSource(t, gdeltFixture, http.StatusOK, nil)
	req := SignalRequest{CountryCode: "FR"}
	_, _ = s.Fetch(context.Background(), req)

	var got []Alert
	if !waitFor(t, func() bool {
		got, _ = s.Fetch(context.Background(), req)
		return len(got) > 0
	}) {
		t.Fatal("no alerts after refresh")
	}

	for _, a := range got {
		if a.Kind != AlertStrike {
			t.Errorf("kind: got %q", a.Kind)
		}
		if a.Source != SourceNews {
			t.Errorf("source: got %q", a.Source)
		}
		// A headline is the weakest evidence here and must never outweigh a
		// measured hazard.
		if a.Severity != SeverityMinor {
			t.Errorf("news must always be minor, got %v", a.Severity)
		}
		if !strings.Contains(a.Detail, "unverified") {
			t.Errorf("detail must mark it unverified, got %q", a.Detail)
		}
		if a.Located() {
			t.Error("a news alert has no coordinates")
		}
		if strings.Contains(a.Title, "Striker") {
			t.Error("the prefilter should have dropped the football headline")
		}
	}
}

// A slow upstream must not spawn a refresh per request.
func TestNews_DoesNotStampedeConcurrentRefreshes(t *testing.T) {
	s, hits := newsSource(t, gdeltFixture, http.StatusOK, nil)
	req := SignalRequest{CountryCode: "FR"}

	for range 5 {
		if _, err := s.Fetch(context.Background(), req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	waitFor(t, func() bool { return *hits > 0 })
	time.Sleep(150 * time.Millisecond)

	if *hits > 1 {
		t.Errorf("expected a single in-flight refresh, got %d upstream calls", *hits)
	}
}

// The RPC's context dies when the response is written; the detached refresh
// must survive that or it would be cancelled every single time.
func TestNews_RefreshSurvivesRequestCancellation(t *testing.T) {
	s, hits := newsSource(t, gdeltFixture, http.StatusOK, nil)

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := s.Fetch(ctx, SignalRequest{CountryCode: "FR"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cancel() // the RPC ends immediately, as it does in production

	if !waitFor(t, func() bool { return *hits > 0 }) {
		t.Fatal("the detached refresh must outlive the request context")
	}
}

// An upstream failure must never surface: this source's whole contract is that
// it returns what it has and nothing else.
func TestNews_UpstreamFailureIsSilent(t *testing.T) {
	s, _ := newsSource(t, `{}`, http.StatusInternalServerError, nil)

	got, err := s.Fetch(context.Background(), SignalRequest{CountryCode: "FR"})
	if err != nil {
		t.Errorf("Fetch must not surface upstream failures, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d alerts", len(got))
	}
}

func TestNews_NoCountryIsQuiet(t *testing.T) {
	s, hits := newsSource(t, gdeltFixture, http.StatusOK, nil)

	got, err := s.Fetch(context.Background(), SignalRequest{CountryCode: ""})
	if err != nil || len(got) != 0 {
		t.Errorf("got %d alerts, err %v", len(got), err)
	}
	time.Sleep(100 * time.Millisecond)
	if *hits != 0 {
		t.Errorf("no country should mean no upstream call, got %d", *hits)
	}
}

// --- classifier ------------------------------------------------------------

type fakeClassifier struct {
	verdicts []NewsVerdict
	err      error
	seen     []string
}

func (f *fakeClassifier) Classify(_ context.Context, _ string, headlines []string) ([]NewsVerdict, error) {
	f.seen = headlines
	return f.verdicts, f.err
}

func TestNews_ClassifierRejectionsAreDropped(t *testing.T) {
	// The fixture yields two headlines past the prefilter; reject the first.
	c := &fakeClassifier{verdicts: []NewsVerdict{{Relevant: false}, {Relevant: true}}}
	s, _ := newsSource(t, gdeltFixture, http.StatusOK, c)
	req := SignalRequest{CountryCode: "FR"}
	_, _ = s.Fetch(context.Background(), req)

	var got []Alert
	if !waitFor(t, func() bool {
		got, _ = s.Fetch(context.Background(), req)
		return len(got) > 0
	}) {
		t.Fatal("no alerts after refresh")
	}
	if len(got) != 1 {
		t.Fatalf("expected the rejected headline dropped, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "Metro") {
		t.Errorf("kept the wrong headline: %q", got[0].Title)
	}
}

// The alerts are Minor and clearly sourced either way, so a classifier outage
// degrades to the heuristic result rather than losing the source.
func TestNews_ClassifierFailureFallsBackToHeuristics(t *testing.T) {
	c := &fakeClassifier{err: errors.New("model unavailable")}
	s, _ := newsSource(t, gdeltFixture, http.StatusOK, c)
	req := SignalRequest{CountryCode: "FR"}
	_, _ = s.Fetch(context.Background(), req)

	if !waitFor(t, func() bool {
		got, _ := s.Fetch(context.Background(), req)
		return len(got) == 2
	}) {
		t.Error("expected both prefiltered headlines to survive a classifier failure")
	}
}

// Aligning verdicts to headlines by position only works if the counts match;
// guessing would attach one headline's verdict to another.
func TestLLMClassifier_ShortResponseIsPaddedAsIrrelevant(t *testing.T) {
	c := NewLLMNewsClassifier(func(context.Context, string) (string, error) {
		return `[{"relevant": true, "reason": "rail strike"}]`, nil
	})
	got, err := c.Classify(context.Background(), "FR", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 verdicts, got %d", len(got))
	}
	if !got[0].Relevant {
		t.Error("the judged headline should stay relevant")
	}
	if got[1].Relevant || got[2].Relevant {
		t.Error("unjudged headlines must default to not shown")
	}
}

func TestLLMClassifier_ParsesFencedJSON(t *testing.T) {
	for _, raw := range []string{
		"[{\"relevant\":true,\"reason\":\"x\"}]",
		"```json\n[{\"relevant\":true,\"reason\":\"x\"}]\n```",
		"Here you go:\n```\n[{\"relevant\":true,\"reason\":\"x\"}]\n```\nHope that helps",
	} {
		got, err := parseNewsVerdicts(raw)
		if err != nil {
			t.Errorf("failed on %q: %v", raw, err)
			continue
		}
		if len(got) != 1 || !got[0].Relevant {
			t.Errorf("got %+v for %q", got, raw)
		}
	}
}

func TestLLMClassifier_UnparseableResponseIsAnError(t *testing.T) {
	c := NewLLMNewsClassifier(func(context.Context, string) (string, error) {
		return "I'm sorry, I can't help with that.", nil
	})
	if _, err := c.Classify(context.Background(), "FR", []string{"a"}); err == nil {
		t.Fatal("expected an error so the source falls back to heuristics")
	}
}

func TestNews_ImplementsSignalSource(t *testing.T) {
	var _ SignalSource = NewNewsSource("", testClient(), nil, nil)
	var _ NewsClassifier = NewLLMNewsClassifier(nil)
}

func TestParseGDELTTime(t *testing.T) {
	if got := parseGDELTTime("20260827T183000Z"); got.IsZero() || got.Year() != 2026 || got.Hour() != 18 {
		t.Errorf("got %v", got)
	}
	if got := parseGDELTTime("rubbish"); !got.IsZero() {
		t.Errorf("garbage should yield the zero time, got %v", got)
	}
}
