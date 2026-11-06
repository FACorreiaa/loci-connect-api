package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/accessibility"
)

// crowdAccessibility is the one read the chat service needs from retrieval.
type crowdAccessibility interface {
	VerifiedAccessibility(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]retrieval.VerifiedAccessFact, error)
}

const (
	// enrichBudget bounds how long the itinerary event may wait for
	// accessibility data. Places not looked up in time keep the model's
	// labelled estimates; nothing blocks the answer.
	enrichBudget = 1500 * time.Millisecond
	// enrichConcurrency caps parallel open-data lookups per answer.
	enrichConcurrency = 8
)

// enrichAccessibility puts evidence-backed accessibility notes on the places
// in an answer, ahead of the model's own estimates. It runs only when the
// traveller has needs, and never fails the turn.
func (l *ServiceImpl) enrichAccessibility(ctx context.Context, needs *locitypes.AccessibilityNeeds, lists ...[]locitypes.POIDetailedInfo) {
	if !needs.Active() || (l.crowdAccess == nil && l.placeAccessibility == nil) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, enrichBudget)
	defer cancel()

	// One lookup per distinct place, however many lists it appears in.
	type place struct {
		id       uuid.UUID
		name     string
		lat, lon float64
	}
	places := map[string]place{}
	var ids []uuid.UUID
	for _, list := range lists {
		for _, p := range list {
			k := poiKey(p)
			if _, seen := places[k]; seen {
				continue
			}
			places[k] = place{id: p.ID, name: p.Name, lat: p.Latitude, lon: p.Longitude}
			if p.ID != uuid.Nil {
				ids = append(ids, p.ID)
			}
		}
	}
	if len(places) == 0 {
		return
	}

	evidence := make(map[string][]locitypes.AccessibilityNote, len(places))
	var mu sync.Mutex

	if l.crowdAccess != nil && len(ids) > 0 {
		facts, err := l.crowdAccess.VerifiedAccessibility(ctx, ids)
		if err != nil {
			l.logger.WarnContext(ctx, "accessibility notes without crowd facts", slog.Any("error", err))
		}
		for k, p := range places {
			if notes := crowdNotes(facts[p.id], needs); len(notes) > 0 {
				evidence[k] = notes
			}
		}
	}

	if l.placeAccessibility != nil {
		sem := make(chan struct{}, enrichConcurrency)
		var wg sync.WaitGroup
		for k, p := range places {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				facts, err := l.placeAccessibility.Lookup(ctx, p.lat, p.lon, p.name)
				if err != nil || facts.Empty() {
					return
				}
				notes := openDataNotes(facts, needs)
				mu.Lock()
				evidence[k] = append(evidence[k], notes...)
				mu.Unlock()
			}()
		}
		wg.Wait()
	}

	for _, list := range lists {
		for i := range list {
			ev := evidence[poiKey(list[i])]
			if len(ev) == 0 {
				continue
			}
			merged := append(append([]locitypes.AccessibilityNote{}, ev...), list[i].AccessibilityNotes...)
			if len(merged) > locitypes.MaxAccessibilityNotes {
				merged = merged[:locitypes.MaxAccessibilityNotes]
			}
			list[i].AccessibilityNotes = merged
		}
	}
}

func poiKey(p locitypes.POIDetailedInfo) string {
	if p.ID != uuid.Nil {
		return p.ID.String()
	}
	return fmt.Sprintf("%s@%.4f,%.4f", strings.ToLower(p.Name), p.Latitude, p.Longitude)
}

// crowdNotes turns verified tokens into notes. "none" is a traveller-verified
// absence of every access feature, which is a barrier for anyone who needs
// step-free access.
func crowdNotes(facts []retrieval.VerifiedAccessFact, needs *locitypes.AccessibilityNeeds) []locitypes.AccessibilityNote {
	var out []locitypes.AccessibilityNote
	for _, f := range facts {
		text, severity := "", locitypes.AccessibilitySeverityInfo
		switch f.Token {
		case "step_free":
			text = "Step-free access"
		case "accessible_wc":
			text = "Accessible toilet"
		case "lift":
			text = "Lift available"
		case "wide_doors":
			text = "Wide doorways"
		case "tactile":
			text = "Tactile guidance"
		case "none":
			text = "No step-free access, lift or accessible toilet"
			severity = locitypes.AccessibilitySeverityCaution
			if needs.NeedsStepFree() {
				severity = locitypes.AccessibilitySeverityBarrier
			}
		default:
			continue
		}
		verified := f.VerifiedAt
		out = append(out, locitypes.AccessibilityNote{
			Text:       fmt.Sprintf("%s (confirmed by %d Loci travellers)", text, f.ContributorCount),
			Severity:   severity,
			Source:     locitypes.AccessibilitySourceVerifiedCrowd,
			ObservedAt: &verified,
		})
	}
	return out
}

// openDataNotes turns open-data facts into notes, each attributed to its
// dataset. Severity rises when a fact collides with a stated need.
func openDataNotes(f *accessibility.PlaceFacts, needs *locitypes.AccessibilityNeeds) []locitypes.AccessibilityNote {
	attribution := f.SourceName
	if attribution == "" {
		attribution = "open accessibility data"
	}
	note := func(text string, sev locitypes.AccessibilityNoteSeverity) locitypes.AccessibilityNote {
		return locitypes.AccessibilityNote{
			Text:          fmt.Sprintf("%s (%s)", text, attribution),
			Severity:      sev,
			Source:        locitypes.AccessibilitySourceOpenData,
			SourceLicence: f.Licence,
			ObservedAt:    f.ObservedAt,
		}
	}
	barrierIf := func(cond bool) locitypes.AccessibilityNoteSeverity {
		if cond {
			return locitypes.AccessibilitySeverityBarrier
		}
		return locitypes.AccessibilitySeverityCaution
	}

	var out []locitypes.AccessibilityNote
	switch f.Wheelchair {
	case accessibility.WheelchairYes:
		out = append(out, note("Wheelchair accessible", locitypes.AccessibilitySeverityInfo))
	case accessibility.WheelchairLimited:
		out = append(out, note("Partly wheelchair accessible — check ahead", locitypes.AccessibilitySeverityCaution))
	case accessibility.WheelchairNo:
		out = append(out, note("Not wheelchair accessible", barrierIf(needs.NeedsStepFree())))
	}
	switch {
	case f.EntranceLevel != nil && *f.EntranceLevel:
		out = append(out, note("Level entrance", locitypes.AccessibilitySeverityInfo))
	case f.EntranceRamp != nil && *f.EntranceRamp:
		out = append(out, note("Entrance has a ramp", locitypes.AccessibilitySeverityInfo))
	case f.EntranceLevel != nil:
		out = append(out, note("Entrance has steps", barrierIf(needs.NeedsStepFree())))
	}
	if f.AccessibleToilet != nil {
		if *f.AccessibleToilet {
			out = append(out, note("Accessible toilet", locitypes.AccessibilitySeverityInfo))
		} else {
			out = append(out, note("No accessible toilet", barrierIf(needs.NeedsAccessibleToilet)))
		}
	}
	return out
}
