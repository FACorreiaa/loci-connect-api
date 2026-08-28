package locitypes

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// POIFilters represents filters for POI queries
type POIFilters struct {
	City       string `json:"city,omitempty"`
	Category   string `json:"category,omitempty"`
	PriceRange string `json:"price_range,omitempty"`
}

// type POIDetail struct {
// 	ID               uuid.UUID `json:"id"`
// 	LlmInteractionID uuid.UUID `json:"llm_interaction_id,omitempty"` // ID of the LLM interaction that generated this POI
// 	City             string    `json:"city"`                         // City where the POI is located
// 	CityID           uuid.UUID `json:"city_id"`
// 	//Description    string    `json:"description"`
// 	Name           string  `json:"name"`
// 	Latitude       float64 `json:"latitude"`
// 	Longitude      float64 `json:"longitude"`
// 	Category       string  `json:"category"`
// 	DescriptionPOI string  `json:"description_poi"`
// 	// Rating               float64   `json:"rating"`
// 	Address string `json:"address"`
// 	// PhoneNumber          string    `json:"phone_number"`
// 	Website      string `json:"website"`
// 	OpeningHours string `json:"opening_hours"`
// 	// Images               []string  `json:"images"`
// 	// Reviews              []string  `json:"reviews"`
// 	// PriceRange           string    `json:"price_range"`
// 	Distance float64 `json:"distance"`
// 	// DistanceUnit         string    `json:"distance_unit"`
// 	// DistanceValue        float64   `json:"distance_value"`
// 	// DistanceText         string    `json:"distance_text"`
// 	// LocationType         string    `json:"location_type"`
// 	// LocationID           string    `json:"location_id"`
// 	// LocationURL          string    `json:"location_url"`
// 	// LocationRating       float64   `json:"location_rating"`
// 	// LocationReview       int       `json:"location_review"`
// 	// LocationAddress      string    `json:"location_address"`
// 	// LocationPhone        string    `json:"location_phone"`
// 	// LocationWebsite      string    `json:"location_website"`
// 	// LocationOpeningHours string    `json:"location_opening_hours"`
// 	CuisineType string `json:"cuisine_type,omitempty"` // For restaurants
// 	StarRating  string `json:"star_rating,omitempty"`  // For hotels
// 	Err         error  `json:"-"`
// }

type POIDetailedInfo struct {
	ID               uuid.UUID         `json:"id,omitempty"`
	City             string            `json:"city"`
	CityID           uuid.UUID         `json:"city_id"`
	Name             string            `json:"name"`
	DescriptionPOI   string            `json:"description_poi,omitempty"`
	Distance         float64           `json:"distance"`
	Latitude         float64           `json:"latitude,omitempty"`
	Longitude        float64           `json:"longitude,omitempty"`
	Category         string            `json:"category"`
	Description      string            `json:"description"`
	Rating           float64           `json:"rating"`
	Address          string            `json:"address"`
	PhoneNumber      string            `json:"phone_number"`
	Website          string            `json:"website"`
	OpeningHours     map[string]string `json:"opening_hours"`
	Images           []string          `json:"images,omitempty"`
	PriceRange       string            `json:"price_range"`
	PriceLevel       string            `json:"price_level"`
	Reviews          []string          `json:"reviews"`
	LlmInteractionID uuid.UUID         `json:"llm_interaction_id"`
	Tags             []string          `json:"tags,omitempty"`
	Priority         int               `json:"priority,omitempty"` // Popularity score 1-10
	CreatedAt        time.Time         `json:"created_at"`
	CuisineType      string            `json:"cuisine_type,omitempty"` // For restaurants
	StarRating       string            `json:"star_rating,omitempty"`  // For hotels
	Amenities        string            `json:"amenities"`
	Err              error             `json:"-"`
	Source           string            `json:"source,omitempty"` // Source of the POI data (e.g., "google", "yelp", etc.)

	// SimilarityScore is cosine similarity against the query embedding, in
	// [0,1], and RelevanceScore is the fused rank score from hybrid search.
	// Both are omitted when the producing query did not compute them.
	//
	// These exist because Distance cannot carry them: it already means
	// kilometres on the spatial paths and a raw similarity score on the vector
	// paths, and overloading it further is how a ranking becomes unexplainable.
	SimilarityScore float64 `json:"similarity_score,omitempty"`
	RelevanceScore  float64 `json:"relevance_score,omitempty"`

	// Grounded reports whether this place was cited from an evidence packet —
	// i.e. it came from a row retrieved before generation, not from the model's
	// memory. False means the suggestion may still be real, but Loci did not
	// verify it against its own data and must not present it as verified.
	Grounded bool `json:"grounded,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for POIDetailedInfo.
//
// Every field intercepted here is one an LLM has been observed getting wrong:
// opening_hours arrives as either a string or a map, star_rating as either a
// number or a string, and id as anything at all.
func (p *POIDetailedInfo) UnmarshalJSON(data []byte) error {
	// Define a temporary struct with the same fields as POIDetailedInfo
	// but with the loosely-typed fields as json.RawMessage.
	type Alias POIDetailedInfo
	aux := &struct {
		OpeningHours json.RawMessage `json:"opening_hours"`
		StarRating   json.RawMessage `json:"star_rating"`
		ID           json.RawMessage `json:"id"`
		CityID       json.RawMessage `json:"city_id"`
		*Alias
	}{
		Alias: (*Alias)(p),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Identifiers, leniently.
	//
	// uuid.UUID rejects anything that is not exactly 36 characters, and a model
	// invents ids freely — a 40-character one was enough to fail the whole
	// array and discard every POI in a generated itinerary, because the caller
	// logs the error and returns. One bad id must cost that POI its id, not
	// cost the user their itinerary.
	//
	// Dropping the value is safe: a generated POI is resolved against the
	// database by identity, not by the id the model supplied. See
	// UpsertPOIByIdentity, which keys on (city, normalised name) precisely
	// because a model's ids are not stable.
	p.ID = parseLenientUUID(aux.ID)
	p.CityID = parseLenientUUID(aux.CityID)

	// Handle OpeningHours field
	if len(aux.OpeningHours) > 0 {
		// Try to unmarshal as map[string]string first
		var hoursMap map[string]string
		if err := json.Unmarshal(aux.OpeningHours, &hoursMap); err == nil {
			p.OpeningHours = hoursMap
		} else {
			// If that fails, try to unmarshal as string
			var hoursString string
			if err := json.Unmarshal(aux.OpeningHours, &hoursString); err == nil {
				p.OpeningHours = map[string]string{"general": hoursString}
			}
		}
	}

	// Handle star_rating that may come as number or string
	if len(aux.StarRating) > 0 {
		var asString string
		if err := json.Unmarshal(aux.StarRating, &asString); err == nil {
			p.StarRating = asString
		} else {
			var asNumber json.Number
			if err := json.Unmarshal(aux.StarRating, &asNumber); err == nil {
				p.StarRating = asNumber.String()
			}
		}
	}

	return nil
}

// parseLenientUUID reads a UUID that may be absent, null, empty, or simply not
// a UUID at all. Anything unusable becomes uuid.Nil rather than an error.
func parseLenientUUID(raw json.RawMessage) uuid.UUID {
	if len(raw) == 0 {
		return uuid.Nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		// Not even a JSON string — a number or an object, say.
		return uuid.Nil
	}
	if s == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

type AddPoiRequest struct {
	ID       string           `json:"poi_id"`
	IsLlmPoi bool             `json:"is_llm_poi"`
	POIData  *POIDetailedInfo `json:"poi_data,omitempty"` // Optional POI data for creating new POIs
}

// TrustSignals derives transparency metadata for a POI from field completeness
// (Slice 3). uncertaintyScore is the fraction of key fields we couldn't verify;
// missing lists them; rationale is a short human-readable "why this" note.
func TrustSignals(p POIDetailedInfo) (uncertaintyScore float64, missing []string, rationale string) {
	if len(p.OpeningHours) == 0 {
		missing = append(missing, "hours")
	}
	if p.PriceLevel == "" && p.PriceRange == "" {
		missing = append(missing, "price")
	}
	if p.Rating == 0 {
		missing = append(missing, "rating")
	}
	if p.Address == "" {
		missing = append(missing, "address")
	}
	const keyFields = 4
	uncertaintyScore = float64(len(missing)) / keyFields

	switch {
	case p.Rating >= 4.0 && p.Category != "":
		rationale = "Highly rated " + p.Category + " that fits your search."
	case p.Category != "":
		rationale = "A " + p.Category + " matching your preferences."
	}
	return uncertaintyScore, missing, rationale
}
