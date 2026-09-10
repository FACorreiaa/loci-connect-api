# Loci cache audit — itinerary / POI generation path (read-only, 2026-09-10)

Repos: `loci-connect-server` (Go) and `loci-client` (SolidStart). Paths below are relative to those roots.
Requirement audited: identical effective inputs ⇒ served from cache; different inputs (season, profile) ⇒ different answer, never cross-served.

## 1. Inventory of caches on the request path

| # | Cache | Where | Key | TTL / layer | Scope |
|---|---|---|---|---|---|
| S | `cachestore.TieredStore` | `pkg/cachestore/store.go:40-45,63-69` | — | memory `go-cache` (default exp = `LLMTTL` 5 min, `store.go:15-17,65`); Redis mirror **only for `string` values** (`store.go:130-133`) and only when `REDIS_URL` set (off in prod example `.env.prod.example:185-186`); knobs `CACHE_LLM_TTL_SEC`/`CACHE_GEO_TTL_SEC` (`pkg/config/config.go:285-287`) | one instance shared by POI + chat services (`cmd/api/dependencies.go:313-323,332,350`) |
| C1 | Unified-chat part cache | write `chat_stream_session.go:827-828` (`Set(key, full, 0)` ⇒ 5 min); read `chat_stream_session.go:672-700` (replays 100-char chunks with 10 ms sleep); post-failure hydration `chat_process_stream.go:358-372` | `cc.CacheKey + "_" + part` (`chat_process_stream.go:318-341`); `cc.CacheKey` = md5(JSON{user_id, profile_id, city, message, domain, preferences, packet_id}) `chat_process_stream.go:136-157` | 5 min memory (+Redis if configured; values are strings) | **per user + per profile** |
| C2 | `_nearby_pois` part | `chat_process_stream.go:807-812` | `cc.CacheKey+"_nearby_pois"` | 20 min | per user; only ever read by the hydration loop |
| P1 | `GetGeneralPOIByDistance` | `internal/domain/poi/poi_nearby.go:26-71` | `poi_filtered:lat4:lon4:km:userID` (`poi_helpers.go:88-95`, rounded 4 dp / 1 km) | `DefaultGeoTTL` 20 min, **memory only** (value is a slice, not mirrored to Redis) | per user |
| P2 | `GetNearbyRestaurants/Activities/Hotels/Attractions` | `poi_nearby.go:111,174,237,300` | `restaurants_%f_%f_%f_%s_%s_%s` — **raw floats, no rounding**, userID + filters | 20 min, memory only | per user |
| P3 | `GetPOIDetailedInfosResponse` | `chat_poi_generation.go:125-233`; key fn `chat_helpers.go:24-29` | `poi:city:lat4:lon4:0:userID` | 20 min | per user |
| D1 | DB short-circuits (durable layer for POIs, not itineraries) | `poi_nearby.go:37-54` (`GetPOIsByLocationAndDistance` before LLM, log "No POIs found in database, falling back to LLM" `:56`); `chat_poi_generation.go:153-181` (`FindCityByNameAndCountry` + `FindPOIDetails` before LLM); `chat_process_stream.go:418` only for persistence | DB rows | permanent | global |
| D2 | `llm_interactions` | write `chat_process_stream.go:446-473` (one row per request, concatenated parts, `Prompt` is a summary string, `ModelUsed: l.model`); schema `pkg/db/migrations/0005_create_llm_interactions.up.sql:4-23` (no `cache_key` column; token columns exist but are not set on this path) | — | permanent | **never read back as a cache**: `GetInteractionByID/GetLatestInteractionBySessionID` (`chat_repository.go:639,701`) have no callers on the generation path |
| D3 | Session `CurrentItinerary` | server `GetSession`; client `getChatSession` `src/lib/api/llm.ts:611-614` | session_id | permanent | per session — a *restore*, not a generation cache: a new identical query mints a new session (`chat_process_stream.go:108-131`) and regenerates |
| K1 | Client `completedStreamingSession` | `src/lib/streaming/restore-session.ts:18,47-70`; `src/routes/itinerary/index.tsx:81-135` (`hasItineraryContent`), fallback `hydrateFromServer` `:167-187`; writers `useChat.ts:315`, `StreamingTransition.tsx:51-59`, `useChatSession.ts:213-245` | sessionStorage, matched on `sessionId` | tab lifetime; cleared on every send (`LoggedInDashboard.tsx:190-192`, `useChat.ts:112`) | per tab |
| K2 | solid-query | defaults `src/lib/query-client.ts:24,27` (staleTime 5 min, gcTime 10 min); `["chatSessions", profileId]` `llm.ts:619`; profile/itinerary prefs 10 min `profiles.ts:216-225` | — | — | no query results are cached client-side; identical re-typed queries always hit the server (no dedupe in `useChat.ts:122`) |

Pipeline (`chat_stream_session.go:620-658`): `prepareChatContext` → `orchestrateLLMStreams` → `aggregateAndParse` → `persistResults` → `sendCompletionEvent`.
Work that runs **before** the key exists (so a "hit" still pays for it): city extraction via `aiClient.Generate` (`chat_converters.go:30-63`, temp 0.1, uncached) and the semantic lane's query embedding (`chat_poi_generation.go:412`, `pkg/openrouter/embedding.go:47`, uncached) inside `assembleEvidencePacket` (`chat_process_stream.go:85` → `chat_grounding.go:110-175`). The `packet_id` in the key is `sha256(userID, query, cityID, ordered candidate IDs)` (`retrieval/packet.go:150-158`), so the key cannot be computed without retrieval.

## 2. Inputs: prompt vs key

Prompt builders: `chat_prompt.go` — `getCityDataPrompt(city)` `:333`, `getGeneralPOIPrompt(city)` `:354`, `getPersonalizedItineraryPrompt(city, basePreferences)` `:373`, hotels/dining/activities `(city, lat, lon, basePreferences)` `:397,430,463`; all but city_data get `groundPrompt(..., cc.Packet)` appended (`chat_grounding.go:188-196`). `basePreferences` = full rendered profile text (`chat_prompt.go:10-274`). POI prompts (`poi_prompt.go:12-40,58-95`) use lat/lon/radius only.

| Input | In prompt? | In key (C1)? | Consequence |
|---|---|---|---|
| City name (normalised) | yes, all parts | yes | ok; but no `city_id`, so "Funchal"/"funchal " fine, "Funchal, Madeira" ≠ "Funchal" ⇒ needless miss |
| User message (cleaned, normalised) | **no** — not interpolated in any generation prompt; only feeds domain detection and retrieval | yes | "Funchal in winter" vs "in summer": different keys ⇒ 2 generations, but identical itinerary prompt ⇒ answers differ only by sampling (temp 0.5, `chat_service.go:41`). Neither half of the requirement holds. |
| Season / travel dates | **no field exists** (`ChatRequest` `loci-connect-proto/proto/loci/chat/chat.proto:350-375`; `ChatContext` `common/common.go:224-264`) | no | only profile-level `PreferredSeasons/AvoidPeakSeason/SeasonSpecific` reach the prompt (`chat_prompt.go:223,258-268`) via the prefs text |
| Travel profile fields (budget, pace, interests, tags, vibes, dietary, accommodation/dining/activity/itinerary prefs) | yes (itinerary, hotels, dining, activities) — **not** city_data / general_pois | yes, as the whole rendered text | correct for itinerary; **key wider than prompt** for city_data and general_pois ⇒ every profile/user regenerates identical city text |
| `ProfileName`, profile's stored `User Location` | yes (`chat_prompt.go:15,31-34`) | yes (inside prefs text) | two profiles with identical settings but different names miss; profile location in key but request location is not (see next) |
| Request `UserLocation` lat/lon (4 dp) | yes for hotels/dining/activities (`:327,334,341`); nearby uses it for DB | **no** (not in `cacheKeyData`) — only indirectly via `packet_id` when the semantic lane ran with a location (`chat_poi_generation.go:425-433`) | ungrounded turns (packet nil ⇒ `"ungrounded"`, `chat_grounding.go:177-182`): a dining answer "near 32.65,-16.91" is replayed for the same user at another location for 5 min |
| Radius | profile `SearchRadiusKm` in prefs text; nearby parses "within X km" (`chat_process_stream.go:726-736`) | via prefs text / P1 key (1 km) | ok |
| Evidence packet (candidate rows, crowd facts, **per-user visited flags** `assemble.go:125`) | yes (rendered) | yes (`packet_id`) | correct but churns: any POI ingest, preference-vector update (`PersonalizeQuery` `chat_poi_generation.go:419`) or location change ⇒ new key ⇒ needless miss; `userID` inside packet_id forbids cross-user sharing even when candidates are identical |
| user_id / profile_id | not in prompt (only via prefs text + visited flags) | yes | zero cross-user reuse; privacy-safe by accident |
| Domain | selects prompt set | yes + part suffix | ok |
| Model id / provider | determines the answer; the BYOK/free-tier router picks per request (`aicreds/routing.go:126-170`) | **no**; and `ModelUsed` recorded as static `l.model` (`chat_process_stream.go:466`, `chat_service.go:104`) | plan upgrade/BYOK change within TTL replays the other model's answer; `llm_interactions.model_name` mis-attributes cost |
| Language / locale | no | no | none today; required once localisation exists |
| Personalisation cohort (`ExperimentVariant`, settings) | yes (prefs emptied `chat_process_stream.go:59-69`) | yes (prefs text = "") | ok |
| Temperature / prompt template version | fixed 0.5 | no | template change ⇒ stale entries until TTL (5 min, so harmless today; matters for a durable layer) |

## 3. Defects, ranked by cost / risk

1. **Season is neither an input nor a key component; the query never reaches the itinerary prompt** (`chat_prompt.go:373-376`, `chat_process_stream.go:320`). Cost: "winter" vs "summer" pays twice for the same prompt; correctness: the answers are not actually seasonal. Fix belongs in the prompt first, key second.
2. **Cache lifetime is 5 minutes in process memory and nothing durable exists.** `Set(key, …, 0)` ⇒ `LLMTTL` (`chat_stream_session.go:828`, `store.go:98-103`); `llm_interactions` is write-only (D2). In practice C1 only serves retries/reconnect hydration. Every returning user, every pod, every deploy regenerates the three long streams.
3. **Two provider calls precede the key** (city extraction + query embedding). A "hit" still costs them, and the key cannot be looked up before retrieval, so a durable layer cannot be consulted early. The extractor is an LLM (temp 0.1): the same message can yield a different cleaned message ⇒ spurious misses.
4. **Key narrower than the prompt for request location** (hotels/dining/activities, ungrounded turns): same-user cross-serve of location-specific answers for 5 min. Also `general_pois`/`itinerary` prompts change with the packet's visited flags — fine today because user_id is in the key, but blocks sharing.
5. **Key wider than the prompt for city_data (and mostly general_pois):** prompt = city only, key = user+profile+message+prefs+packet. The most shareable generation is never shared.
6. **Model id absent from the key / wrong in the audit row** (routing per user, `l.model` static). Harmless while keys are per-user; a blocker before any shared layer; already wrong for PostHog cost attribution.
7. **packet_id churn** (defect table row) turns real repeats into misses; `userID` inside `computePacketID` (`packet.go:150-152`) makes identical evidence unshareable.
8. **POI caches keyed per user although prompts are location-only** (P1–P3): N users at one spot ⇒ N LLM fallbacks; P2 keys use unrounded floats ⇒ GPS jitter defeats them; slice values never reach Redis ⇒ per-pod duplicates.
9. **Cached-but-invalid outputs**: any non-empty stream is cached (`chat_stream_session.go:827`) including unparseable JSON, then replayed and re-persisted.
10. **Replay latency is artificial**: 10 ms per 100 chars (`chat_stream_session.go:679-700`) ≈ 3 s for a 30 KB itinerary.
11. Minor: `ProfileName` in prefs text ⇒ needless misses; no `city_id` in key; no client-side dedupe of an identical re-submit; `llm_interactions` token columns unpopulated (`0005…sql:15-17` vs struct at `chat_process_stream.go:457-467`).
12. Privacy today: no cache can serve another user's generation (user_id in every key). Keep it that way for any content that embeds visit history or profile text.

## 4. Recommendation (design only)

**Canonical key** (per part): `sha256("v1" | part | domain | model_id | language | city_id-or-normalised-city | normalised query | profile_snapshot_hash | season_bucket)`.
- `profile_snapshot_hash` = sha256 of canonical JSON of *only the fields that part's prompt interpolates* (per-part allowlist; exclude `ProfileName`, exclude stored user location); empty hash when personalisation is off. city_data and general_pois use an empty snapshot ⇒ global keys.
- `season_bucket` from explicit travel dates (add `travel_dates` to `ChatRequest`, or read them from the `TripDraft` when `trip_id` is set) ⇒ `YYYY-MM` or meteorological season + hemisphere; `"unspecified"` when absent. It must also be **interpolated into the itinerary/activities prompts**, otherwise keying on it changes nothing. Same for the user's query text.
- `model_id` = the router's resolved model for this request (expose it from `aicreds.Router`, record it in `llm_interactions.model_name`).
- Evidence packet: **out of the key, into the row.** Look the key up *before* retrieval; on a miss run retrieval, generate, and store `packet_id` next to the response for the audit trail. Staleness of evidence is then bounded by the layer TTL, which is the intended trade.

**Layers and TTLs**
- L1 memory (existing go-cache): 10–15 min; retries, reconnect hydration, duplicate submits. L2 Redis (existing string mirror) same TTL when multi-pod.
- L3 durable: `llm_generations(cache_key PK, part, model_id, prompt_hash, response, packet_id, tokens_in/out, created_at, expires_at)`. TTL by part: city_data 30 d, general_pois 7 d, itinerary 7–14 d, hotels/dining/activities 3–7 d. Order L1→L2→L3; an L3 hit repopulates L1/L2. `llm_interactions` keeps one row per request with `served_from ∈ {memory,redis,db,llm}` and `generation_id` so the trail still says what was shown; that makes the DB row the reusable artefact instead of a second copy.
- Pre-key calls: cache city extraction by `sha256(raw message)` (24 h, global, no user data) and query embeddings by `sha256(normalised query)` (7 d), or replace extraction with deterministic parsing for the common shapes. Goal: a hit costs zero provider calls.

**Never cache (or per-user ≤ 5 min only):** `DomainNearby`; any part whose prompt embeds request-time lat/lon (or round to 3 dp, put it in the key, 20 min); queries containing "now/today/tonight/open now/this weekend"; live constraints (weather, events); empty or unparseable outputs (validate JSON before `Set`). Visit-history flags must not be rendered into content that is shared across users — either render them only in the per-snapshot itinerary part or strip them from the packet used for global parts.

**Invalidation:** the snapshot hash makes profile edits self-invalidating; additionally bump a per-profile `version` on save so the hash is cheap to compute without loading every sub-preference. Bump the `"v1"` prefix on prompt-template changes. Provide an admin path to purge a city (`DELETE … WHERE city_id`) after bulk POI ingest.

**Measuring hit rate with PostHog `$ai_generation`** (nothing emits it yet: `pkg/analytics/analytics.go:21-24,68`, no chat callers): emit one event per part per request with `$ai_model`, `$ai_provider`, `$ai_input_tokens`, `$ai_output_tokens`, `$ai_latency`, `$ai_trace_id = session_id`, `$ai_span_name = part`, plus custom `loci_cache_layer ∈ {memory,redis,db,none}`, `loci_cache_key`, `domain`, `city`, `season_bucket`, `profile_snapshot_hash`, `loci_saved_output_tokens` (tokens of the reused row on a hit; 0 on a miss). Hit rate = `count(loci_cache_layer != none) / count()` by part/domain/city; savings = Σ `loci_saved_output_tokens` × model price. Mirror with a Prometheus counter `loci_llm_cache_requests_total{part,layer,result}` next to `loci_external_cache_hits_total` (`pkg/observability/external.go:80-83`). Today the only signals are the log line "Cache hit for LLM response" and `cache_used: true` on chunk events (`chat_stream_session.go:695`).

## 5. Estimated savings

Provider calls per **cold** itinerary/general request today: 1 extraction `Generate` + 1 query embedding + 3 parallel streams (`city_data`, `general_pois`, `itinerary`, `chat_process_stream.go:318-320`) = 5 calls, 3 of them long generations (up to 16k output tokens each for POI-style prompts, `poi_nearby.go:87`). Hotels/dining/activities: 2 streams + 2 pre-key calls. Nearby: 2 pre-key calls + DB, LLM only when the DB is empty. No further generation calls found in the background `ProcessAndSaveUnifiedResponse` (`chat_helpers.go:68-110`).

Today's cache hit (same user, same profile, same packet, within 5 min): skips the 3 streams but still pays the 2 pre-key calls; occurs essentially only on retry/reload.

With the recommended design: `city_data` ≈ 100 % hit after the first request per city/model per 30 d (global key); `general_pois` high hit per city+query; `itinerary` hits for returning users and for any users sharing a snapshot+season; pre-key caches make a full hit cost 0 provider calls. Per request in a warmed city: a returning user with an unchanged profile pays 0/5; a new user with a distinct profile pays 1/5 (the itinerary stream). Given that the itinerary is the longest of the three outputs, expect roughly two-thirds of generation tokens per request to disappear for new profiles and all of them for repeats.
