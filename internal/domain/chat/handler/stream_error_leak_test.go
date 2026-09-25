package handler

import (
	"strings"
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The 2026-09-10 outage put this on people's screens, verbatim:
//
//	failed to create session: begin transaction: failed to connect to
//	`user=loci_production_app database=loci_production`: 10.43.132.104:5432
//	(loci-postgres): server error: FATAL: the database system is in
//	recovery mode (SQLSTATE 57P03)
//
// Every error event the chat service emits goes through
// streamErrorFromEvent, so that is where infrastructure detail must stop.
func TestInfrastructureErrorsNeverReachTheUser(t *testing.T) {
	leaks := []string{
		"failed to create session: begin transaction: failed to connect to `user=loci_production_app database=loci_production`: 10.43.132.104:5432 (loci-postgres): server error: FATAL: the database system is in recovery mode (SQLSTATE 57P03)",
		"query pois near 32.66,-16.92: dial tcp 10.43.132.104:5432: connect: connection refused",
		"context deadline exceeded",
		"Get \"https://openrouter.ai/api/v1/chat\": lookup openrouter.ai: no such host",
		"failed to save LLM interaction: ERROR: relation \"llm_interactions\" does not exist (SQLSTATE 42P01)",
	}
	for _, msg := range leaks {
		se := streamErrorFromEvent(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: msg})
		for _, secret := range []string{"user=", "database=", "10.43.", ":5432", "SQLSTATE", "dial tcp", "no such host", "deadline"} {
			if strings.Contains(se.UserMessage, secret) {
				t.Fatalf("user message leaked %q: %q", secret, se.UserMessage)
			}
		}
		if !se.Retryable {
			t.Fatalf("an infrastructure error must be retryable, got %+v for %q", se, msg)
		}
		if se.InternalCode != "internal" {
			t.Fatalf("internal code = %q, want internal for %q", se.InternalCode, msg)
		}
	}
}

// Sentences written for people stay as written; the scrub is not a rewrite.
func TestUserFacingSentencesPassThroughUntouched(t *testing.T) {
	msgs := []string{
		"Unable to find places near your location. Please try again or expand your search radius.",
		"Location data is required for nearby searches. Please enable location services.",
		"We're seeing high traffic right now. Please try again in a moment.",
	}
	for _, msg := range msgs {
		se := streamErrorFromEvent(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: msg})
		if se.UserMessage != msg {
			t.Fatalf("rewrote a user-facing sentence: %q -> %q", msg, se.UserMessage)
		}
	}
}

// An explicit producer code wins over the scrub's classification.
func TestExplicitCodeStillWinsOverTheScrub(t *testing.T) {
	se := streamErrorFromEvent(locitypes.StreamEvent{
		Type:      locitypes.EventTypeError,
		Error:     "dial tcp 10.0.0.1:443: i/o timeout",
		ErrorCode: locitypes.StreamErrorProviderUnavailable,
	})
	if se.InternalCode != "provider_unavailable" {
		t.Fatalf("code = %q, want provider_unavailable", se.InternalCode)
	}
	if strings.Contains(se.UserMessage, "10.0.0.1") {
		t.Fatalf("explicit code must not carry the raw address either: %q", se.UserMessage)
	}
}

// Free-tier users must never learn which model or provider served them, and
// the chain's failures used to reach them verbatim: an unclassified provider
// error falls through streamErrorFor as "<part> worker failed: %v".
func TestProviderErrorsNeverNameTheModel(t *testing.T) {
	leaks := []string{
		"itinerary worker failed: Error 404, Message: This model is unavailable for free. The paid version is available now, Status: , Details: []",
		"general_pois streaming error: Error 400, Message: nvidia/nemotron-3-ultra-550b-a55b:free is not a valid model ID",
		"Upstream error from Nvidia: Service temporarily overloaded",
		"decode openrouter stream event: invalid character 'x' looking for beginning of value",
		"Streaming failed for POI 'Belem Tower': openrouter stateful chat sessions are not supported",
	}
	for _, msg := range leaks {
		se := streamErrorFromEvent(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: msg})
		lower := strings.ToLower(se.UserMessage)
		for _, secret := range []string{"openrouter", "nvidia", "nemotron", ":free", "error 4", "message:", "upstream"} {
			if strings.Contains(lower, secret) {
				t.Fatalf("user message leaked %q: %q", secret, se.UserMessage)
			}
		}
		if se.InternalCode != "provider_unavailable" || !se.Retryable {
			t.Fatalf("want retryable provider_unavailable for %q, got %+v", msg, se)
		}
	}
}
