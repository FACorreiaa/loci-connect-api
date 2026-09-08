-- +goose Up

-- llm_suggested_pois.distance was written in metres under a kilometre label.
--
-- enrichAndFilterLLMResponse computed a correct Haversine distance in km and
-- then stored `distanceKm * 1000` into POIDetailedInfo.Distance — a field the
-- spatial repository fills in kilometres and the MCP layer publishes as
-- `distance_km`. Every row this path wrote is therefore 1000x too large, and
-- an agent asking for POIs near Funchal was told they were ~2000 km away.
--
-- The code is fixed; these rows are not, so they need correcting in place.
--
-- Only rows that are implausible as kilometres are touched. The threshold is
-- 300 km: the column records a distance from the user's own search centre,
-- which is bounded by the search radius, and no caller passes a radius
-- remotely near that. Anything above it cannot be a genuine kilometre reading,
-- and anything below it might be — so leaving those alone is the conservative
-- choice. A row that was already correct is never divided.
--
-- Rows written after this migration are correct by construction.

UPDATE llm_suggested_pois
SET distance = distance / 1000.0
WHERE distance IS NOT NULL
  AND distance > 300;

-- Deliberately NOT touched: llm_interactions.distance. Despite the shared
-- column name it holds the search RADIUS in metres, passed straight from the
-- caller (persistLLMPOIs), not a per-POI distance. It is correct as it stands.
