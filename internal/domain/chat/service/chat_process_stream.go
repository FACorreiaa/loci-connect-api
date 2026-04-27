package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
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
	_, searchProfile, _, err := l.FetchUserData(ctx, cc.UserID, cc.ProfileID)
	if err != nil {
		return fmt.Errorf("failed to fetch user data: %w", err)
	}
	cc.BasePreferences = getUserPreferencesPrompt(searchProfile)

	// Location Fallback
	if cc.UserLocation == nil && searchProfile.UserLatitude != nil && searchProfile.UserLongitude != nil {
		cc.UserLocation = &locitypes.UserLocation{
			UserLat: *searchProfile.UserLatitude,
			UserLon: *searchProfile.UserLongitude,
		}
	}

	// 4. Create Session
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

	// 5. Generate Cache Key
	cacheKeyData := map[string]interface{}{
		"user_id":     cc.UserID.String(),
		"profile_id":  cc.ProfileID.String(),
		"city":        normalizeCacheComponent(cc.CityName),
		"message":     normalizeCacheComponent(cleanedMessage),
		"domain":      string(cc.Domain),
		"preferences": cc.BasePreferences,
	}
	cacheKeyBytes, err := json.Marshal(cacheKeyData)
	if err != nil {
		l.DebugContext(ctx, "Failed to marshal cache key data", slog.Any("error", err))
		// Use a fallback cache key
		cacheKeyBytes = []byte(fmt.Sprintf("fallback_%s_%s", cleanedMessage, cc.CityName))
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
	parsePart := func(key string, target interface{}, nestedKey string) {
		if str, ok := rawResponses[key]; ok {
			clean := extractJSONFromMarkdown(str)
			var parsed interface{}
			if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
				l.logger.WarnContext(ctx, "failed to unmarshal raw", "key", key, "err", err)
				return
			}
			// Handle nested or flat structures
			if m, ok := parsed.(map[string]interface{}); ok && nestedKey != "" {
				if nested, exists := m[nestedKey]; exists {
					if jsonBytes, err := json.Marshal(nested); err == nil {
						if err := json.Unmarshal(jsonBytes, target); err != nil {
							l.logger.WarnContext(ctx, "failed to unmarshal nested", "key", key, "err", err)
						}
					}
				}
			} else {
				if jsonBytes, err := json.Marshal(parsed); err == nil {
					if err := json.Unmarshal(jsonBytes, target); err != nil {
						l.logger.WarnContext(ctx, "failed to unmarshal", "key", key, "err", err)
					}
				}
			}
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

	return data, nil
}

// orchestrateLLMStreams manages the fan-out concurrency to LLM workers.
func (l *ServiceImpl) orchestrateLLMStreams(cc *common.ChatContext) (map[string]string, error) {
	ctx := cc.Ctx
	var wg sync.WaitGroup

	// Create a detached context for LLM workers - they MUST complete even if client disconnects
	// This prevents partial JSON responses when the user navigates away
	workerCtx, cancelWorker := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancelWorker()

	l.sendEvent(workerCtx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeStart,
		Data: map[string]interface{}{
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
			if data, ok := event.Data.(map[string]interface{}); ok {
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

	// Spawn workers based on Domain
	switch cc.Domain {
	case locitypes.DomainItinerary, locitypes.DomainGeneral:
		wg.Go(func() {
			prompt := getCityDataPrompt(cc.CityName)
			partCacheKey := cc.CacheKey + "_city_data"
			responsesMutex.Lock()
			partCacheKeys["city_data"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "city_data", sendEventWithResponse, cc.Domain, partCacheKey)
		})
		wg.Go(func() {
			prompt := getGeneralPOIPrompt(cc.CityName)
			partCacheKey := cc.CacheKey + "_general_pois"
			responsesMutex.Lock()
			partCacheKeys["general_pois"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "general_pois", sendEventWithResponse, cc.Domain, partCacheKey)
		})
		wg.Go(func() {
			prompt := getPersonalizedItineraryPrompt(cc.CityName, cc.BasePreferences)
			partCacheKey := cc.CacheKey + "_itinerary"
			responsesMutex.Lock()
			partCacheKeys["itinerary"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "itinerary", sendEventWithResponse, cc.Domain, partCacheKey)
		})

		// // Added Hotels Worker
		// wg.Go(func() {
		// 	var lat, lon float64
		// 	if cc.UserLocation != nil {
		// 		lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
		// 	}
		// 	prompt := getAccommodationPrompt(cc.CityName, lat, lon, cc.BasePreferences)
		// 	partCacheKey := cc.CacheKey + "_hotels"
		// 	responsesMutex.Lock()
		// 	partCacheKeys["hotels"] = partCacheKey
		// 	responsesMutex.Unlock()
		// 	l.streamWorkerWithResponseAndCache(ctx, prompt, "hotels", sendEventWithResponse, cc.Domain, partCacheKey)
		// })

		// // Added Restaurants Worker
		// wg.Go(func() {
		// 	var lat, lon float64
		// 	if cc.UserLocation != nil {
		// 		lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
		// 	}
		// 	prompt := getDiningPrompt(cc.CityName, lat, lon, cc.BasePreferences)
		// 	partCacheKey := cc.CacheKey + "_restaurants"
		// 	responsesMutex.Lock()
		// 	partCacheKeys["restaurants"] = partCacheKey
		// 	responsesMutex.Unlock()
		// 	l.streamWorkerWithResponseAndCache(ctx, prompt, "restaurants", sendEventWithResponse, cc.Domain, partCacheKey)
		// })

		// // Added Activities Worker
		// wg.Go(func() {
		// 	var lat, lon float64
		// 	if cc.UserLocation != nil {
		// 		lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
		// 	}
		// 	prompt := getActivitiesPrompt(cc.CityName, lat, lon, cc.BasePreferences)
		// 	partCacheKey := cc.CacheKey + "_activities"
		// 	responsesMutex.Lock()
		// 	partCacheKeys["activities"] = partCacheKey
		// 	responsesMutex.Unlock()
		// 	l.streamWorkerWithResponseAndCache(ctx, prompt, "activities", sendEventWithResponse, cc.Domain, partCacheKey)
		// })
	case locitypes.DomainAccommodation:
		// Spawn city_data worker
		wg.Go(func() {
			prompt := getCityDataPrompt(cc.CityName)
			partCacheKey := cc.CacheKey + "_city_data"
			responsesMutex.Lock()
			partCacheKeys["city_data"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "city_data", sendEventWithResponse, cc.Domain, partCacheKey)
		})
		// Spawn hotels worker
		wg.Go(func() {
			var lat, lon float64
			if cc.UserLocation != nil {
				lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
			}
			prompt := getAccommodationPrompt(cc.CityName, lat, lon, cc.BasePreferences)
			partCacheKey := cc.CacheKey + "_hotels"
			responsesMutex.Lock()
			partCacheKeys["hotels"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "hotels", sendEventWithResponse, cc.Domain, partCacheKey)
		})
	case locitypes.DomainDining:
		// Spawn city_data worker
		wg.Go(func() {
			prompt := getCityDataPrompt(cc.CityName)
			partCacheKey := cc.CacheKey + "_city_data"
			responsesMutex.Lock()
			partCacheKeys["city_data"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "city_data", sendEventWithResponse, cc.Domain, partCacheKey)
		})
		// Spawn restaurants worker
		wg.Go(func() {
			var lat, lon float64
			if cc.UserLocation != nil {
				lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
			}
			prompt := getDiningPrompt(cc.CityName, lat, lon, cc.BasePreferences)
			partCacheKey := cc.CacheKey + "_restaurants"
			responsesMutex.Lock()
			partCacheKeys["restaurants"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "restaurants", sendEventWithResponse, cc.Domain, partCacheKey)
		})
	case locitypes.DomainActivities:
		// Spawn city_data worker
		wg.Go(func() {
			prompt := getCityDataPrompt(cc.CityName)
			partCacheKey := cc.CacheKey + "_city_data"
			responsesMutex.Lock()
			partCacheKeys["city_data"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "city_data", sendEventWithResponse, cc.Domain, partCacheKey)
		})
		// Spawn activities worker
		wg.Go(func() {
			var lat, lon float64
			if cc.UserLocation != nil {
				lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
			}
			prompt := getActivitiesPrompt(cc.CityName, lat, lon, cc.BasePreferences)
			partCacheKey := cc.CacheKey + "_activities"
			responsesMutex.Lock()
			partCacheKeys["activities"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(workerCtx, prompt, "activities", sendEventWithResponse, cc.Domain, partCacheKey)
		})
	case locitypes.DomainNearby:
		// Handle location-based "nearme" queries using PostGIS database queries
		wg.Go(func() {
			l.handleNearbyDomain(workerCtx, cc, sendEventWithResponse, &responsesMutex, responses, partCacheKeys)
		})
	default:
		return nil, fmt.Errorf("unhandled domain type: %s", cc.Domain)
	}

	wg.Wait()
	l.logger.InfoContext(ctx, "All streaming workers completed")

	// Hydrate missing parts from cache
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

	// Convert builders to strings for the next phase
	finalResponses := make(map[string]string)
	for k, v := range responses {
		finalResponses[k] = v.String()
	}
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
		fullResponseBuilder.WriteString(fmt.Sprintf("[%s]\n%s\n\n", partType, content))
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
	}

	// Send domain-specific event with pre-parsed data
	// Use context.Background() to bypass cancelled context - we MUST deliver this data
	switch cc.Domain {
	case locitypes.DomainAccommodation:
		// Send hotels as pre-parsed data
		l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeHotels,
			Data: map[string]interface{}{
				"general_city_data": data.GeneralCityData,
				"hotels":            data.Hotels,
				"session_id":        cc.SessionID.String(),
			},
		}, 3)
	case locitypes.DomainDining:
		// Send restaurants as pre-parsed data
		l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeRestaurants,
			Data: map[string]interface{}{
				"general_city_data": data.GeneralCityData,
				"restaurants":       data.Restaurants,
				"session_id":        cc.SessionID.String(),
			},
		}, 3)
	case locitypes.DomainActivities:
		// Send activities as pre-parsed data
		l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
			Type: "activities",
			Data: map[string]interface{}{
				"general_city_data": data.GeneralCityData,
				"activities":        data.Activities,
				"session_id":        cc.SessionID.String(),
			},
		}, 3)
	case locitypes.DomainNearby:
		// For nearby domain, the handleNearbyDomain already sends events directly
		// Don't send another event here as it would overwrite the POI data with empty data
	default:
		// Send full itinerary for DomainItinerary/DomainGeneral
		l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeItinerary,
			Data: *data,
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
func (l *ServiceImpl) sendCompletionEvent(cc *common.ChatContext) {
	var routeType string
	var baseURL string

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
		routeType = "itinerary"
		baseURL = "/itinerary"
	}

	// Use context.Background() to bypass cancelled context - we MUST deliver this event
	l.sendEvent(context.Background(), cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeComplete,
		Data: map[string]interface{}{"session_id": cc.SessionID.String()},
		Navigation: &locitypes.NavigationData{
			URL:       fmt.Sprintf("%s?sessionId=%s&cityName=%s&domain=%s", baseURL, cc.SessionID.String(), url.QueryEscape(cc.CityName), routeType),
			RouteType: routeType,
			QueryParams: map[string]string{
				"sessionId": cc.SessionID.String(),
				"cityName":  cc.CityName,
				"domain":    routeType,
			},
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
) {
	// Extract location from user location
	var lat, lon, distance float64
	if cc.UserLocation != nil {
		lat = cc.UserLocation.UserLat
		lon = cc.UserLocation.UserLon

		// Try to parse distance from message using regex
		// Format: "within X kilometers" or "within X km"
		distanceRegex := regexp.MustCompile(`within\s+(\d+(?:\.\d+)?)\s*(?:kilometers?|km)`)
		if matches := distanceRegex.FindStringSubmatch(strings.ToLower(cc.Message)); len(matches) > 1 {
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
		return
	}

	l.logger.InfoContext(ctx, "Handling nearby domain query",
		slog.Float64("lat", lat),
		slog.Float64("lon", lon),
		slog.Float64("distance_m", distance))

	// Send start event
	sendEventWithResponse(locitypes.StreamEvent{
		Type: locitypes.EventTypeStart,
		Data: map[string]interface{}{
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
		l.DebugContext(ctx, "Failed to query nearby POIs",
			slog.Any("error", err))
		// Send user-friendly error message instead of internal error details
		sendEventWithResponse(locitypes.StreamEvent{
			Type:      locitypes.EventTypeError,
			Error:     "Unable to find places near your location. Please try again or expand your search radius.",
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		})
		return
	}

	l.logger.InfoContext(ctx, "Found nearby POIs",
		slog.Int("count", len(pois)))

	// If no POIs found in database, send appropriate message
	if len(pois) == 0 {
		sendEventWithResponse(locitypes.StreamEvent{
			Type: locitypes.EventTypeProgress,
			Data: map[string]interface{}{
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
	// Marshal POIs to JSON for storage
	poisJSON, _ := json.Marshal(pois)
	responses["nearby_pois"].WriteString(string(poisJSON))
	partCacheKeys["nearby_pois"] = cc.CacheKey + "_nearby_pois"
	responsesMutex.Unlock()

	// Send the POIs as a structured event
	sendEventWithResponse(locitypes.StreamEvent{
		Type: "nearby",
		Data: map[string]interface{}{
			"general_city_data":  responseData.GeneralCityData,
			"points_of_interest": pois,
			"session_id":         cc.SessionID.String(),
			"total_count":        len(pois),
		},
		Timestamp: time.Now(),
		EventID:   uuid.New().String(),
	})

	l.logger.InfoContext(ctx, "Successfully streamed nearby POIs",
		slog.Int("poi_count", len(pois)))
}
