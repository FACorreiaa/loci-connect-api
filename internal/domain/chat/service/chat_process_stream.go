package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
)

// prepareChatContext handles extracting city, intent detection, fetching user profile,
// generating cache keys, and creating the initial DB session.
func (l *ServiceImpl) prepareChatContext(cc *common.ChatContext) error {
	ctx := cc.Ctx

	// 1. Extract City
	extractedCity, cleanedMessage, err := l.extractCityFromMessage(ctx, cc.Message)
	if err != nil {
		return fmt.Errorf("failed to parse message: %w", err)
	}
	if extractedCity != "" {
		cc.CityName = extractedCity
	}
	cc.Message = cleanedMessage // Update normalized message

	// 2. Detect Domain
	domainDetector := &locitypes.DomainDetector{}
	cc.Domain = domainDetector.DetectDomain(ctx, cleanedMessage)

	// 3. Fetch User Data & Preferences
	interests, searchProfile, tags, err := l.FetchUserData(ctx, cc.UserID, cc.ProfileID)
	if err != nil {
		return fmt.Errorf("failed to fetch user data: %w", err)
	}
	// FetchUserData queries three tables; two of those results used to be
	// discarded here. getUserPreferencesPrompt has always been able to render
	// interests and "Tags to Avoid" — those branches were simply unreachable
	// because the profile it was handed never carried them. The repository does
	// not join them (they live in user_profile_interests and the tag tables), so
	// attaching them is the caller's job.
	if searchProfile != nil {
		searchProfile.Interests = interests
		searchProfile.Tags = tags
	}
	cc.BasePreferences = getUserPreferencesPrompt(searchProfile)
	personalizationEnabled := preference.ExperimentVariant(cc.UserID) != "control"
	if settings, ok := l.prefVectors.(preference.SettingsReader); ok {
		enabled, settingsErr := settings.PersonalizationEnabled(ctx, cc.UserID)
		if settingsErr != nil {
			return fmt.Errorf("get personalization setting: %w", settingsErr)
		}
		personalizationEnabled = personalizationEnabled && enabled
	}
	if !personalizationEnabled {
		cc.BasePreferences = ""
	}

	// Location Fallback
	if cc.UserLocation == nil && searchProfile.UserLatitude != nil && searchProfile.UserLongitude != nil {
		cc.UserLocation = &locitypes.UserLocation{
			UserLat: *searchProfile.UserLatitude,
			UserLon: *searchProfile.UserLongitude,
		}
	}

	// 3b. Retrieve the evidence this turn may speak from.
	//
	// Runs before the prompt is built, because the whole point is that the model
	// sees real rows rather than reconstructing places from memory. Best-effort:
	// on failure cc.Packet stays nil and generation proceeds ungrounded, which is
	// the behaviour that predates this step.
	l.assembleEvidencePacket(cc)

	// 4. Resume or create session.
	// When the client sends a session_id we resume that session (appending the new
	// user turn) instead of always minting a new one — this is what makes real
	// continuation possible. We only resume a session the caller actually owns.
	if cc.RequestedSessionID != uuid.Nil {
		existing, gErr := l.llmInteractionRepo.GetSession(ctx, cc.RequestedSessionID)
		if gErr == nil && existing != nil && existing.UserID == cc.UserID {
			cc.SessionID = existing.ID
			if cc.CityName == "" {
				cc.CityName = existing.CityName
			}
			if aErr := l.llmInteractionRepo.AddMessageToSession(ctx, cc.SessionID,
				locitypes.ConversationMessage{Role: "user", Content: cc.Message, Timestamp: time.Now()}); aErr != nil {
				l.logger.WarnContext(ctx, "failed to append message to resumed session",
					slog.String("session_id", cc.SessionID.String()), slog.Any("error", aErr))
			}
		} else {
			l.logger.InfoContext(ctx, "requested session not resumable, starting a new one",
				slog.String("requested_session_id", cc.RequestedSessionID.String()), slog.Any("error", gErr))
		}
	}

	if cc.SessionID == uuid.Nil {
		cc.SessionID = uuid.New()
		session := locitypes.ChatSession{
			ID:        cc.SessionID,
			UserID:    cc.UserID,
			ProfileID: cc.ProfileID,
			CityName:  cc.CityName,
			ConversationHistory: []locitypes.ConversationMessage{
				{Role: "user", Content: cc.Message, Timestamp: time.Now()},
			},
			SessionContext: locitypes.SessionContext{
				CityName:            cc.CityName,
				ConversationSummary: fmt.Sprintf("Trip plan for %s", cc.CityName),
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ExpiresAt: time.Now().Add(24 * time.Hour),
			Status:    "active",
		}
		if err := l.llmInteractionRepo.CreateSession(ctx, session); err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}
	}

	// 5. Generate Cache Key
	cacheKeyData := map[string]any{
		"user_id":     cc.UserID.String(),
		"profile_id":  cc.ProfileID.String(),
		"city":        normalizeCacheComponent(cc.CityName),
		"message":     normalizeCacheComponent(cleanedMessage),
		"domain":      string(cc.Domain),
		"preferences": cc.BasePreferences,
		// The packet is part of the prompt, so it is part of the cache identity.
		// Without this a response generated before grounding — or against a
		// different candidate set — would be replayed and then recorded against
		// this turn's evidence, making the audit trail describe an answer that
		// was never produced from it.
		"packet_id": packetIDFor(cc),
	}
	cacheKeyBytes, err := json.Marshal(cacheKeyData)
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to marshal cache key data", slog.Any("error", err))
		// Use a fallback cache key
		cacheKeyBytes = fmt.Appendf(nil, "fallback_%s_%s", cleanedMessage, cc.CityName)
	}
	hash := md5.Sum(cacheKeyBytes)
	cc.CacheKey = hex.EncodeToString(hash[:])

	return nil
}

// aggregateAndParse converts raw strings to structured data using robust parsing logic.
func (l *ServiceImpl) aggregateAndParse(cc *common.ChatContext, rawResponses map[string]string) (*locitypes.AiCityResponse, error) {
	ctx := cc.Ctx
	data := &locitypes.AiCityResponse{SessionID: cc.SessionID}

	// Helper to parse part robustly
	parsePart := func(key string, target any, nestedKey string) {
		str, ok := rawResponses[key]
		if !ok {
			return
		}
		clean := extractJSONFromMarkdown(str)
		if nestedKey != "" {
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal([]byte(clean), &envelope); err != nil {
				l.logger.WarnContext(ctx, "failed to unmarshal raw", "key", key, "err", err)
				return
			}
			nested, exists := envelope[nestedKey]
			if !exists {
				return
			}
			if err := json.Unmarshal(nested, target); err != nil {
				l.logger.WarnContext(ctx, "failed to unmarshal nested", "key", key, "err", err)
			}
			return
		}
		if err := json.Unmarshal([]byte(clean), target); err != nil {
			l.logger.WarnContext(ctx, "failed to unmarshal", "key", key, "err", err)
		}
	}

	// Parse each part
	parsePart("city_data", &data.GeneralCityData, "")
	parsePart("general_pois", &data.PointsOfInterest, "points_of_interest")
	parsePart("itinerary", &data.AIItineraryResponse, "")
	parsePart("hotels", &data.Hotels, "hotels")
	parsePart("restaurants", &data.Restaurants, "restaurants")
	parsePart("activities", &data.Activities, "activities")

	// Deduplication Logic
	allPOIs := make([]locitypes.POIDetailedInfo, 0)
	seenIDs := make(map[string]bool)

	addUniquePOI := func(pois []locitypes.POIDetailedInfo) {
		for _, poi := range pois {
			key := poi.ID.String()
			if key == "00000000-0000-0000-0000-000000000000" {
				key = poi.Name
			}
			if !seenIDs[key] {
				seenIDs[key] = true
				allPOIs = append(allPOIs, poi)
			}
		}
	}

	addUniquePOI(data.AIItineraryResponse.PointsOfInterest)
	addUniquePOI(data.PointsOfInterest)
	addUniquePOI(data.AIItineraryResponse.Restaurants)
	addUniquePOI(convertHotelsToPOIs(data.Hotels))
	addUniquePOI(convertRestaurantsToPOIs(data.Restaurants))
	addUniquePOI(data.Activities)

	// Final assignment
	// We do NOT want to merge all POIs into the specific lists.
	// data.PointsOfInterest should remain as General POIs
	// data.AIItineraryResponse.PointsOfInterest should remain as Itinerary POIs
	// data.PointsOfInterest = allPOIs
	// data.AIItineraryResponse.PointsOfInterest = allPOIs

	l.logger.InfoContext(ctx, "Consolidated and deduplicated POIs",
		slog.Int("total_unique_pois", len(allPOIs)))

	// Check the answer against the evidence it was given, and strip any
	// identifier the model invented. This must happen before persistResults
	// reaches canonicalizePOIs, otherwise a fabricated UUID would be written to
	// the database as though retrieval had produced it.
	cc.Verification = l.verifyAndRecordGrounding(cc, data, rawResponses)

	return data, nil
}

// orchestrateLLMStreams manages the fan-out concurrency to LLM workers.
func (l *ServiceImpl) orchestrateLLMStreams(cc *common.ChatContext) (map[string]string, error) {
	ctx := cc.Ctx

	// workerCtx survives client disconnect so in-flight work can finish and events can flush.
	// gctx (from errgroup) cancels sibling workers when any worker returns an error.
	workerCtx, cancelWorker := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancelWorker()

	l.sendEvent(workerCtx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeStart,
		Data: map[string]any{
			"domain":     string(cc.Domain),
			"city":       cc.CityName,
			"session_id": cc.SessionID.String(),
			"cache_key":  cc.CacheKey,
		},
	}, 3)

	// Thread-safe map for responses
	responses := make(map[string]*strings.Builder)
	partCacheKeys := make(map[string]string)
	var responsesMutex sync.Mutex

	// Helper to send events and capture chunks
	// Use workerCtx for sending events - this ensures we continue capturing responses
	// even if the client disconnects, while still attempting to deliver events
	sendEventWithResponse := func(event locitypes.StreamEvent) {
		if event.Type == locitypes.EventTypeChunk {
			responsesMutex.Lock()
			if data, ok := event.Data.(map[string]any); ok {
				if partType, exists := data["part"].(string); exists {
					if chunk, chunkExists := data["chunk"].(string); chunkExists {
						if responses[partType] == nil {
							responses[partType] = &strings.Builder{}
						}
						responses[partType].WriteString(chunk)
					}
				}
			}
			responsesMutex.Unlock()
		}
		// Use workerCtx instead of ctx - this prevents context cancellation from
		// blocking event delivery. The sendEvent function will still handle
		// closed channels gracefully via its recover() mechanism.
		l.sendEvent(workerCtx, cc.EventCh, event, 3)
	}

	g, gctx := errgroup.WithContext(workerCtx)

	runStreamWorker := func(partType, prompt, partCacheKey string) {
		g.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					l.logger.ErrorContext(gctx, "stream worker panicked",
						slog.String("part_type", partType),
						slog.Any("recover", r))
					err = fmt.Errorf("%s worker panic: %v", partType, r)
				}
			}()
			responsesMutex.Lock()
			partCacheKeys[partType] = partCacheKey
			responsesMutex.Unlock()
			return l.streamWorkerWithResponseAndCache(gctx, prompt, partType, sendEventWithResponse, cc.Domain, partCacheKey)
		})
	}

	// Spawn workers based on Domain.
	//
	// Prompts that name places are grounded in the evidence packet; city_data
	// describes the city itself and has nothing to cite, so it is left alone.
	switch cc.Domain {
	case locitypes.DomainItinerary, locitypes.DomainGeneral:
		runStreamWorker("city_data", getCityDataPrompt(cc.CityName), cc.CacheKey+"_city_data")
		runStreamWorker("general_pois", groundPrompt(getGeneralPOIPrompt(cc.CityName), cc.Packet), cc.CacheKey+"_general_pois")
		runStreamWorker("itinerary", groundPrompt(getPersonalizedItineraryPrompt(cc.CityName, cc.BasePreferences), cc.Packet), cc.CacheKey+"_itinerary")
	case locitypes.DomainAccommodation:
		runStreamWorker("city_data", getCityDataPrompt(cc.CityName), cc.CacheKey+"_city_data")
		var lat, lon float64
		if cc.UserLocation != nil {
			lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
		}
		runStreamWorker("hotels", groundPrompt(getAccommodationPrompt(cc.CityName, lat, lon, cc.BasePreferences), cc.Packet), cc.CacheKey+"_hotels")
	case locitypes.DomainDining:
		runStreamWorker("city_data", getCityDataPrompt(cc.CityName), cc.CacheKey+"_city_data")
		var lat, lon float64
		if cc.UserLocation != nil {
			lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
		}
		runStreamWorker("restaurants", groundPrompt(getDiningPrompt(cc.CityName, lat, lon, cc.BasePreferences), cc.Packet), cc.CacheKey+"_restaurants")
	case locitypes.DomainActivities:
		runStreamWorker("city_data", getCityDataPrompt(cc.CityName), cc.CacheKey+"_city_data")
		var lat, lon float64
		if cc.UserLocation != nil {
			lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
		}
		runStreamWorker("activities", groundPrompt(getActivitiesPrompt(cc.CityName, lat, lon, cc.BasePreferences), cc.Packet), cc.CacheKey+"_activities")
	case locitypes.DomainNearby:
		g.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					l.logger.ErrorContext(gctx, "nearby worker panicked", slog.Any("recover", r))
					err = fmt.Errorf("nearby worker panic: %v", r)
				}
			}()
			return l.handleNearbyDomain(gctx, cc, sendEventWithResponse, &responsesMutex, responses, partCacheKeys)
		})
	default:
		return nil, fmt.Errorf("unhandled domain type: %s", cc.Domain)
	}

	waitErr := g.Wait()

	// Best-effort hydration even when workers fail or siblings were canceled.
	responsesMutex.Lock()
	for part, key := range partCacheKeys {
		if builder, ok := responses[part]; !ok || builder == nil || builder.Len() == 0 {
			if cached, found := l.cache.Get(key); found {
				if cachedText, ok := cached.(string); ok && cachedText != "" {
					l.logger.InfoContext(ctx, "Hydrated missing response part from cache",
						slog.String("part_type", part), slog.String("cache_key", key))
					b := &strings.Builder{}
					b.WriteString(cachedText)
					responses[part] = b
				}
			}
		}
	}
	responsesMutex.Unlock()

	finalResponses := make(map[string]string)
	for k, v := range responses {
		finalResponses[k] = v.String()
	}

	if waitErr != nil {
		return finalResponses, waitErr
	}
	l.logger.InfoContext(ctx, "All streaming workers completed")
	return finalResponses, nil
}

// persistResults handles saving City, Interactions, and Session updates.
func (l *ServiceImpl) persistResults(
	cc *common.ChatContext,
	data *locitypes.AiCityResponse,
	rawResponses map[string]string,
	startTime time.Time,
) error {
	ctx := cc.Ctx

	// Distinguish between client context (for streaming) and storage context (for persistence)
	// Create a detached context for database operations to prevent cancellation on client disconnect
	storageCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second) // 60s for DB ops
	defer cancel()

	// 1. Save City (if needed)
	var cityID uuid.UUID
	cityDataContent := rawResponses["city_data"]
	if cityDataContent != "" {
		parsedCityData, err := l.parseCityDataFromResponse(ctx, cityDataContent)
		if err == nil && parsedCityData != nil {
			savedCityID, err := l.HandleCityData(storageCtx, *parsedCityData)
			if err == nil {
				cityID = savedCityID
				l.logger.InfoContext(ctx, "Successfully saved city data", slog.String("city_id", cityID.String()))
			}
		}
	}
	// Fallback: try to get existing city from database, or create it
	if cityID == uuid.Nil && cc.CityName != "" {
		existingCity, err := l.cityRepo.FindCityByNameAndCountry(storageCtx, cc.CityName, "Unknown")
		if err == nil && existingCity != nil {
			cityID = existingCity.ID
			l.logger.InfoContext(ctx, "Found existing city",
				slog.String("city", cc.CityName),
				slog.String("city_id", cityID.String()))
		} else {
			// City doesn't exist, create a minimal entry
			l.logger.InfoContext(ctx, "City not found in database, creating minimal entry",
				slog.String("city_name", cc.CityName))
			cityDetail := locitypes.CityDetail{
				Name:          cc.CityName,
				Country:       "Unknown",
				StateProvince: "Unknown",
			}
			cityID, err = l.cityRepo.SaveCity(storageCtx, cityDetail)
			if err != nil {
				l.logger.WarnContext(ctx, "Failed to create city entry",
					slog.String("city", cc.CityName),
					slog.Any("error", err))
				cityID = uuid.Nil
			} else {
				l.logger.InfoContext(ctx, "Successfully created city entry",
					slog.String("city", cc.CityName),
					slog.String("city_id", cityID.String()))
			}
		}
	}

	// 2. Save Interaction
	var fullResponseBuilder strings.Builder
	for partType, content := range rawResponses {
		fmt.Fprintf(&fullResponseBuilder, "[%s]\n%s\n\n", partType, content)
	}
	fullResponse := fullResponseBuilder.String()
	if fullResponse == "" {
		fullResponse = fmt.Sprintf("Processed %s request for %s", cc.Domain, cc.CityName)
	}

	interaction := locitypes.LlmInteraction{
		ID:           uuid.New(),
		SessionID:    cc.SessionID,
		UserID:       cc.UserID,
		ProfileID:    cc.ProfileID,
		CityName:     cc.CityName,
		Prompt:       fmt.Sprintf("Unified Chat Stream - Domain: %s, Message: %s", cc.Domain, cc.Message),
		ResponseText: fullResponse,
		ModelUsed:    l.model,
		LatencyMs:    int(time.Since(startTime).Milliseconds()),
		Timestamp:    startTime,
	}
	savedID, err := l.llmInteractionRepo.SaveInteraction(storageCtx, interaction)
	if err != nil {
		l.logger.WarnContext(ctx, "Failed to save interaction", slog.Any("error", err))
		return err
	}
	l.logger.InfoContext(ctx, "Successfully saved interaction", slog.String("interaction_id", savedID.String()))

	// 2b. Attach the evidence trail now that there is an interaction to hang it
	// on. Uses storageCtx so a disconnected client does not cost us the audit
	// record; a failure here is logged, never returned.
	l.recordEvidence(storageCtx, savedID.String(), cc.Packet, cc.Verification)

	// 3. Update POI IDs with the saved interaction ID and cityID
	if cityID != uuid.Nil {
		for i := range data.PointsOfInterest {
			data.PointsOfInterest[i].CityID = cityID
			data.PointsOfInterest[i].LlmInteractionID = savedID
		}
		for i := range data.AIItineraryResponse.PointsOfInterest {
			data.AIItineraryResponse.PointsOfInterest[i].CityID = cityID
			data.AIItineraryResponse.PointsOfInterest[i].LlmInteractionID = savedID
		}
		for i := range data.AIItineraryResponse.Restaurants {
			data.AIItineraryResponse.Restaurants[i].CityID = cityID
			data.AIItineraryResponse.Restaurants[i].LlmInteractionID = savedID
		}
		for i := range data.AIItineraryResponse.Bars {
			data.AIItineraryResponse.Bars[i].CityID = cityID
			data.AIItineraryResponse.Bars[i].LlmInteractionID = savedID
		}
		for i := range data.Activities {
			data.Activities[i].CityID = cityID
			data.Activities[i].LlmInteractionID = savedID
		}

		data.PointsOfInterest = l.canonicalizePOIs(storageCtx, data.PointsOfInterest, cityID)
		data.AIItineraryResponse.PointsOfInterest = l.canonicalizePOIs(storageCtx, data.AIItineraryResponse.PointsOfInterest, cityID)
		data.AIItineraryResponse.Restaurants = l.canonicalizePOIs(storageCtx, data.AIItineraryResponse.Restaurants, cityID)
		data.AIItineraryResponse.Bars = l.canonicalizePOIs(storageCtx, data.AIItineraryResponse.Bars, cityID)
		data.Activities = l.canonicalizePOIs(storageCtx, data.Activities, cityID)

		data.PointsOfInterest = l.rerankPOIs(storageCtx, cc.UserID, data.PointsOfInterest)
		data.AIItineraryResponse.PointsOfInterest = l.rerankPOIs(storageCtx, cc.UserID, data.AIItineraryResponse.PointsOfInterest)
		data.AIItineraryResponse.Restaurants = l.rerankPOIs(storageCtx, cc.UserID, data.AIItineraryResponse.Restaurants)
		data.AIItineraryResponse.Bars = l.rerankPOIs(storageCtx, cc.UserID, data.AIItineraryResponse.Bars)
		data.Activities = l.rerankPOIs(storageCtx, cc.UserID, data.Activities)
	}

	// Send domain-specific event with pre-parsed data
	// Use context.Background() to bypass cancelled context - we MUST deliver this data
	recommendationRunID := uuid.New().String()
	switch cc.Domain {
	case locitypes.DomainAccommodation:
		// Send hotels as pre-parsed data (POI-adapted for a single client shape).
		hotelPOIs := l.rerankPOIs(storageCtx, cc.UserID, l.canonicalizePOIs(storageCtx, convertHotelsToPOIs(data.Hotels), cityID))
		l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
			Type:    locitypes.EventTypeHotels,
			EventID: recommendationRunID,
			Data: locitypes.StreamDomainListData{
				GeneralCityData: data.GeneralCityData,
				POIs:            hotelPOIs,
				SessionID:       cc.SessionID.String(),
			},
		}, 3)
	case locitypes.DomainDining:
		// Send restaurants as pre-parsed data (POI-adapted).
		restaurantPOIs := l.rerankPOIs(storageCtx, cc.UserID, l.canonicalizePOIs(storageCtx, convertRestaurantsToPOIs(data.Restaurants), cityID))
		l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
			Type:    locitypes.EventTypeRestaurants,
			EventID: recommendationRunID,
			Data: locitypes.StreamDomainListData{
				GeneralCityData: data.GeneralCityData,
				POIs:            restaurantPOIs,
				SessionID:       cc.SessionID.String(),
			},
		}, 3)
	case locitypes.DomainActivities:
		// Send activities as pre-parsed data
		l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
			Type:    "activities",
			EventID: recommendationRunID,
			Data: locitypes.StreamDomainListData{
				GeneralCityData: data.GeneralCityData,
				POIs:            data.Activities,
				SessionID:       cc.SessionID.String(),
			},
		}, 3)
	case locitypes.DomainNearby:
		// For nearby domain, the handleNearbyDomain already sends events directly
		// Don't send another event here as it would overwrite the POI data with empty data
	default:
		// Send full itinerary for DomainItinerary/DomainGeneral
		l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
			Type:    locitypes.EventTypeItinerary,
			EventID: recommendationRunID,
			Data:    *data,
		}, 3)
	}
	// 4. Update Session
	session, err := l.llmInteractionRepo.GetSession(storageCtx, cc.SessionID)
	if err != nil {
		l.logger.WarnContext(ctx, "Failed to get session for update", slog.Any("error", err))
		return err
	}
	session.CurrentItinerary = data
	session.UpdatedAt = time.Now()
	if err := l.llmInteractionRepo.UpdateSession(storageCtx, *session); err != nil {
		l.logger.WarnContext(ctx, "Failed to update session with initial itinerary", slog.Any("error", err))
		return err
	}
	l.logger.InfoContext(ctx, "Successfully saved initial itinerary to session",
		slog.Int("poi_count", len(data.AIItineraryResponse.PointsOfInterest)),
		slog.Int("top_level_pois", len(data.PointsOfInterest)))

	// Auto-persist the generated itinerary as an editable TripDraft so it appears
	// in /trips. Best-effort for itinerary/general domains — never fail the stream
	// on a trip-save error. Persisted ID is stashed on cc.TripID so the completion
	// event can deep-link the client to /trips/:id.
	if l.tripRepo != nil && (cc.Domain == locitypes.DomainItinerary || cc.Domain == locitypes.DomainGeneral) {
		if tr := buildTripFromCityResponse(cc, data, recommendationRunID); tr != nil {
			saved, terr := l.tripRepo.SaveTrip(storageCtx, tr, 0)
			if terr != nil {
				l.logger.WarnContext(ctx, "auto trip persist failed", slog.Any("error", terr))
			} else if saved != nil {
				cc.TripID = saved.ID
				l.logger.InfoContext(ctx, "auto-persisted trip from itinerary",
					slog.String("trip_id", saved.ID.String()), slog.Int("days", len(saved.Days)))
			}
		}
	}

	// alternative
	// Convert rawResponses to builders for compatibility with ProcessAndSaveUnifiedResponse
	//builders := make(map[string]*strings.Builder)
	//for k, v := range rawResponses {
	//	b := &strings.Builder{}
	//	b.WriteString(v)
	//	builders[k] = b
	//}
	//
	//// Restore background processing for POI details saving
	//go func(builders map[string]*strings.Builder, userID, profileID, cityID, llmInteractionID uuid.UUID, userLocation *locitypes.UserLocation) {
	//	l.ProcessAndSaveUnifiedResponse(ctx, builders, userID, profileID, cityID, llmInteractionID, userLocation)
	//}(builders, cc.UserID, cc.ProfileID, cityID, savedID, cc.UserLocation)

	// Restore background processing for POI details saving
	go func() {
		// Use detached context with long timeout for background processing
		bgCtx, bgCancel := context.WithTimeout(context.WithoutCancel(cc.Ctx), 5*time.Minute)
		defer bgCancel()

		l.ProcessAndSaveUnifiedResponse(
			bgCtx,           // 1. Context
			rawResponses,    // 2. The map[string]*strings.Builder
			cc.UserID,       // 3. User UUID (Extract this from 'data' or 'cc'?)
			cc.ProfileID,    // 4. Profile UUID (Extract this from 'data' or 'cc'?)
			cityID,          // 5. City UUID
			savedID,         // 6. LLM Interaction UUID
			cc.UserLocation, // 7. *locitypes.UserLocation (Extract from 'data' or 'cc'?)
		)
	}()

	return nil
}

// sendCompletionEvent sends the completion event with navigation data.
// When an itinerary was auto-persisted, prefer deep-linking to /trips/:id so
// the client can offer an "Edit trip" CTA without a second lookup.
func (l *ServiceImpl) sendCompletionEvent(cc *common.ChatContext) {
	var routeType string
	var baseURL string
	queryParams := map[string]string{
		"sessionId": cc.SessionID.String(),
		"cityName":  cc.CityName,
	}

	switch cc.Domain {
	case locitypes.DomainAccommodation:
		routeType = "hotels"
		baseURL = "/hotels"
	case locitypes.DomainDining:
		routeType = "restaurants"
		baseURL = "/restaurants"
	case locitypes.DomainActivities:
		routeType = "activities"
		baseURL = "/activities"
	case locitypes.DomainNearby:
		routeType = "nearme"
		baseURL = "/nearme"
	default:
		if cc.TripID != uuid.Nil {
			routeType = "itinerary"
			baseURL = "/itinerary"
			queryParams["tripId"] = cc.TripID.String()
			queryParams["domain"] = "itinerary"
		} else {
			routeType = "itinerary"
			baseURL = "/itinerary"
			queryParams["domain"] = "itinerary"
		}
	}
	if _, ok := queryParams["domain"]; !ok {
		queryParams["domain"] = routeType
	}

	navURL := fmt.Sprintf("%s?sessionId=%s&cityName=%s&domain=%s",
		baseURL, cc.SessionID.String(), url.QueryEscape(cc.CityName), queryParams["domain"])
	if tripID, ok := queryParams["tripId"]; ok {
		navURL = fmt.Sprintf("%s?sessionId=%s&cityName=%s&domain=itinerary&tripId=%s",
			baseURL, cc.SessionID.String(), url.QueryEscape(cc.CityName), tripID)
	}

	// Use context.Background() to bypass cancelled context - we MUST deliver this event
	l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeComplete,
		Data: map[string]any{"session_id": cc.SessionID.String(), "trip_id": queryParams["tripId"]},
		Navigation: &locitypes.NavigationData{
			URL:         navURL,
			RouteType:   routeType,
			QueryParams: queryParams,
		},
	}, 3)
}

// handleNearbyDomain handles location-based POI queries using the PostGIS database.
// It fetches nearby POIs (restaurants, activities, hotels, attractions) from the database
// instead of generating them via LLM, providing more accurate and real-world data.
func (l *ServiceImpl) handleNearbyDomain(
	ctx context.Context,
	cc *common.ChatContext,
	sendEventWithResponse func(locitypes.StreamEvent),
	responsesMutex *sync.Mutex,
	responses map[string]*strings.Builder,
	partCacheKeys map[string]string,
) error {
	// Extract location from user location
	var lat, lon, distance float64
	if cc.UserLocation != nil {
		lat = cc.UserLocation.UserLat
		lon = cc.UserLocation.UserLon

		// Try to parse distance from message using regex
		// Format: "within X kilometers" or "within X km"
		if matches := nearbyDistanceRE.FindStringSubmatch(strings.ToLower(cc.Message)); len(matches) > 1 {
			if parsedDist, err := strconv.ParseFloat(matches[1], 64); err == nil {
				distance = parsedDist * 1000 // Convert km to meters for PostGIS
			}
		}

		if distance <= 0 {
			distance = 50000 // Default 50km radius in meters
		}
	} else {
		// No location available - send error event
		sendEventWithResponse(locitypes.StreamEvent{
			Type:      locitypes.EventTypeError,
			Error:     "Location data is required for nearby searches. Please enable location services.",
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		})
		return nil
	}

	l.logger.InfoContext(ctx, "Handling nearby domain query",
		slog.Float64("lat", lat),
		slog.Float64("lon", lon),
		slog.Float64("distance_m", distance))

	// Send start event
	sendEventWithResponse(locitypes.StreamEvent{
		Type: locitypes.EventTypeStart,
		Data: map[string]any{
			"domain":     "nearby",
			"city":       cc.CityName,
			"session_id": cc.SessionID.String(),
			"cache_key":  cc.CacheKey,
			"lat":        lat,
			"lon":        lon,
			"distance":   distance,
		},
		Timestamp: time.Now(),
		EventID:   uuid.New().String(),
	})

	// Query POIs using POI service with full cache + DB + LLM fallback flow
	pois, err := l.poiSvc.GetGeneralPOIByDistance(ctx, cc.UserID, lat, lon, distance)
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to query nearby POIs",
			slog.Any("error", err))
		// Send user-friendly error message instead of internal error details
		sendEventWithResponse(locitypes.StreamEvent{
			Type:      locitypes.EventTypeError,
			Error:     "Unable to find places near your location. Please try again or expand your search radius.",
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		})
		return fmt.Errorf("query nearby POIs: %w", err)
	}

	l.logger.InfoContext(ctx, "Found nearby POIs",
		slog.Int("count", len(pois)))

	// If no POIs found in database, send appropriate message
	if len(pois) == 0 {
		sendEventWithResponse(locitypes.StreamEvent{
			Type: locitypes.EventTypeProgress,
			Data: map[string]any{
				"status":  "no_pois_found",
				"message": "No POIs found in the database for this location. Try expanding the search radius.",
			},
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		})
	}

	// Build response data
	responseData := &locitypes.AiCityResponse{
		SessionID: cc.SessionID,
		GeneralCityData: locitypes.GeneralCityData{
			City:            cc.CityName,
			Description:     fmt.Sprintf("Places near your location (%.2f, %.2f) within %.1f km", lat, lon, distance/1000),
			CenterLatitude:  lat,
			CenterLongitude: lon,
		},
		PointsOfInterest: pois,
	}

	// Store in responses map for persistence
	responsesMutex.Lock()
	if responses["nearby_pois"] == nil {
		responses["nearby_pois"] = &strings.Builder{}
	}
	poisJSON, err := json.Marshal(pois)
	if err != nil {
		responsesMutex.Unlock()
		return fmt.Errorf("marshal nearby POIs: %w", err)
	}
	nearbyCacheKey := cc.CacheKey + "_nearby_pois"
	responses["nearby_pois"].WriteString(string(poisJSON))
	partCacheKeys["nearby_pois"] = nearbyCacheKey
	responsesMutex.Unlock()

	l.cache.Set(nearbyCacheKey, string(poisJSON), cachestore.DefaultGeoTTL)

	// Send the POIs as a structured event
	sendEventWithResponse(locitypes.StreamEvent{
		Type: "nearby",
		Data: locitypes.StreamDomainListData{
			GeneralCityData: responseData.GeneralCityData,
			POIs:            pois,
			SessionID:       cc.SessionID.String(),
		},
		Timestamp: time.Now(),
		EventID:   uuid.New().String(),
	})

	l.logger.InfoContext(ctx, "Successfully streamed nearby POIs",
		slog.Int("poi_count", len(pois)))
	return nil
}
