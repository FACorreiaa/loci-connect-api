package handler

import (
	"strings"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// maxProgressStageLen is the longest stage the chat header is designed for
// (Muse chat contract: verb phrase, at most 32 characters).
const maxProgressStageLen = 32

// defaultProgressStage is shown when an event carries nothing we recognise.
// It must still read after the assistant's name: "Loci is working on it".
const defaultProgressStage = "working on it"

// progressStatusStages maps the "status" codes the chat service puts in
// map-shaped progress data to header copy.
var progressStatusStages = map[string]string{
	"context_loaded":              "loading your trip",
	"generating_semantic_context": "searching places",
	"semantic_context_failed":     "searching places",
	"semantic_context_ready":      "matching places to you",
	"analyzing_semantic_matches":  "comparing places",
	"extracting_poi_name":         "reading your request",
	"generating_poi_data":         "looking up that place",
	"no_pois_found":               "widening the search",
}

// progressTypeStages maps event types that collapse to progress.
var progressTypeStages = map[string]string{
	"session_validated":                "getting started",
	"intent_classified":                "reading your request",
	locitypes.EventTypeDomainDetected:  "reading your request",
	locitypes.EventTypePromptGenerated: "planning your answer",
	locitypes.EventTypeParsingResponse: "tidying up the results",
	"semantic_context_generated":       "matching places to you",
	"semantic_poi_added":               "adding a place",
	"semantic_alternatives_suggested":  "comparing places",
	"poi_added_successfully":           "updating your itinerary",
	"poi_detail_complete":              "getting place details",
	locitypes.EventTypeUnifiedChat:     defaultProgressStage,
	locitypes.EventTypeMessage:         defaultProgressStage,
	locitypes.EventTypeProgress:        defaultProgressStage,
}

// progressDomainStages maps a detected domain to what the assistant is doing
// about it.
var progressDomainStages = map[string]string{
	string(locitypes.DomainGeneral):       "searching places",
	string(locitypes.DomainAccommodation): "checking hotels",
	string(locitypes.DomainDining):        "finding restaurants",
	string(locitypes.DomainActivities):    "finding things to do",
	string(locitypes.DomainItinerary):     "planning your day",
	string(locitypes.DomainTransport):     "checking how to get there",
	string(locitypes.DomainNearby):        "looking around you",
}

// progressTextStages maps the free-text progress messages some service paths
// send as plain-string Data. Matched by lower-cased prefix.
var progressTextStages = []struct{ prefix, stage string }{
	{"processing: adding", "adding a place"},
	{"processing: removing", "removing a place"},
	{"processing: answering", "answering your question"},
	{"processing: replacing", "swapping a place"},
	{"processing: updating itinerary", "updating your itinerary"},
	{"sorting updated pois", "sorting by distance"},
	{"getting details for", "getting place details"},
}

// progressStage returns the header copy for a progress-type stream event.
// Precedence: a recognised status code, a recognised free-text message, the
// detected domain, the event type, then defaultProgressStage. It never returns
// a raw code, so unknown or new events degrade to "working on it" rather than
// "is semantic_context_ready".
func progressStage(event locitypes.StreamEvent) string {
	if text, ok := event.Data.(string); ok {
		if stage, ok := progressTextStage(text); ok {
			return stage
		}
	}

	var m map[string]any
	if decodeData(event.Data, &m) {
		if status, ok := m["status"].(string); ok && status != "" {
			if stage, ok := progressStatusStages[status]; ok {
				return stage
			}
			if stage, ok := progressTextStage(status); ok {
				return stage
			}
		}
		if domain, ok := m["domain"].(string); ok {
			if stage, ok := progressDomainStages[domain]; ok {
				return stage
			}
		}
	}

	if stage, ok := progressTypeStages[event.Type]; ok {
		return stage
	}
	return defaultProgressStage
}

func progressTextStage(text string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, t := range progressTextStages {
		if strings.HasPrefix(lower, t.prefix) {
			return t.stage, true
		}
	}
	return "", false
}
