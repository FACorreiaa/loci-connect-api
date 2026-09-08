package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The bug this file exists for.
//
// The SDK infers a tool's output schema from the Go type the handler returns,
// then validates the marshalled result against it and returns a JSON-RPC
// TRANSPORT error on a mismatch — not a tool error, so the call simply breaks
// for the client (mcp/server.go, applySchema with forOutput=true).
//
// uuid.UUID is [16]byte. The inferrer special-cases only time.Time, slog.Level
// and big.* as strings; every other array kind becomes an array of 16 integers.
// At runtime uuid.UUID.MarshalText emits a 36-character string. So any tool
// returning a domain struct with a bare uuid.UUID field declares "array",
// emits "string", and is uncallable.
//
// get_poi_details and get_itinerary both did this. contract_test.go could not
// catch it: it compares the tool NAME tables and never inspects a schema.

// roundTripsOwnSchema is the property every tool output type must hold — what
// the handler marshals must satisfy what its schema promises.
func roundTripsOwnSchema[T any](t *testing.T, value T) {
	t.Helper()

	schema, err := jsonschema.For[T](nil)
	if err != nil {
		t.Fatalf("infer schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}

	// Marshal then unmarshal into `any`: the SDK validates the wire form, not
	// the Go value.
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if err := resolved.Validate(wire); err != nil {
		t.Errorf("what the handler emits does not satisfy its own declared output "+
			"schema, so the SDK rejects the call before the client sees it: %v\n"+
			"emitted: %s", err, raw)
	}
}

// Every output type reachable from a registered tool. A tool whose output type
// is absent from this list is a tool that can ship uncallable.
func TestEveryToolOutputSatisfiesItsOwnSchema(t *testing.T) {
	id := uuid.New()

	t.Run("poiListOutput", func(t *testing.T) {
		roundTripsOwnSchema(t, poiListOutput{
			Results: []POISummary{{ID: id.String(), Name: "x", DistanceKm: 1.8}},
		})
	})
	t.Run("POISummary", func(t *testing.T) {
		roundTripsOwnSchema(t, POISummary{ID: id.String(), Name: "x"})
	})
	t.Run("RecommendationTrace", func(t *testing.T) {
		roundTripsOwnSchema(t, RecommendationTrace{RunID: id.String(), ItemID: id.String()})
	})

	// The two that were broken. They now return hand-written wire types with
	// string ids, which is the whole fix.
	t.Run("get_poi_details", func(t *testing.T) {
		roundTripsOwnSchema(t, detailFromPOI(&locitypes.POIDetailedInfo{
			ID: id, CityID: id, LlmInteractionID: id, Name: "x",
			OpeningHours: map[string]string{"mon": "09:00-17:00"},
		}))
	})
	t.Run("get_itinerary", func(t *testing.T) {
		roundTripsOwnSchema(t, detailFromItinerary(&locitypes.UserSavedItinerary{
			ID: id, UserID: id, PrimaryCityID: &id, Title: "x",
		}))
	})
}

// Name the shape of the bug directly, so a future reader gets the reason rather
// than a validator message about integers and minItems.
func TestUUIDInfersAsAnArrayButMarshalsAsAString(t *testing.T) {
	type withUUID struct {
		ID uuid.UUID `json:"id"`
	}

	schema, err := jsonschema.For[withUUID](nil)
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	declared := schema.Properties["id"]
	if declared == nil {
		t.Fatal("no schema inferred for the uuid field")
	}

	raw, err := json.Marshal(withUUID{ID: uuid.New()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if declared.Type == "string" {
		t.Skip("the SDK now infers uuid.UUID as a string; tool outputs may use " +
			"uuid.UUID directly again and the string-id rule can be relaxed")
	}
	if declared.Type != "array" {
		t.Fatalf("uuid.UUID now infers as %q, which this package's string-id rule "+
			"does not account for; re-check the rule", declared.Type)
	}
	t.Logf("confirmed: uuid.UUID declares %q but marshals to %s — tool outputs "+
		"must use string ids", declared.Type, raw)
}
