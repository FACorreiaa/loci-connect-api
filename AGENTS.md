1. I have refactored the ProcessUnifiedChatMessageStream into smaller functions inside the chat_process_stream.go file. The original ProcessUnifiedChatMessageStream is commented out, so make sure that new new function, with the

So this is my old function:

// ProcessUnifiedChatMessageStream handles unified chat with optimized streaming based on Google GenAI patterns
//func (l *ServiceImpl) ProcessUnifiedChatMessageStream(cc common.ChatContext) error {
//	startTime := time.Now() // Track when processing starts
//	ctx, span := otel.Tracer("LlmInteractionService").Start(cc.Ctx, "ProcessUnifiedChatMessageStream", trace.WithAttributes(
//		attribute.String("message", cc.Message),
//	))
//	defer span.End()
//
//	// Extract city and clean message
//	extractedCity, cleanedMessage, err := l.extractCityFromMessage(ctx, cc.Message)
//	if err != nil {
//		span.RecordError(err)
//		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
//		return fmt.Errorf("failed to parse message: %w", err)
//	}
//	if extractedCity != "" {
//		cc.CityName = extractedCity
//	}
//	span.SetAttributes(attribute.String("extracted.city", cc.CityName), attribute.String("cleaned.message", cleanedMessage))
//
//	// Detect domain
//	domainDetector := &locitypes.DomainDetector{}
//	domain := domainDetector.DetectDomain(ctx, cleanedMessage)
//	span.SetAttributes(attribute.String("detected.domain", string(domain)))
//
//	// Step 3: Fetch user data
//	_, searchProfile, _, err := l.FetchUserData(ctx, cc.UserID, cc.ProfileID)
//	if err != nil {
//		span.RecordError(err)
//		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
//		return fmt.Errorf("failed to fetch user data: %w", err)
//	}
//	basePreferences := getUserPreferencesPrompt(searchProfile)
//
//	// Use default location if not provided
//	var lat, lon float64
//	if cc.UserLocation == nil && searchProfile.UserLatitude != nil && searchProfile.UserLongitude != nil {
//		cc.UserLocation = &locitypes.UserLocation{
//			UserLat: *searchProfile.UserLatitude,
//			UserLon: *searchProfile.UserLongitude,
//		}
//	}
//	if cc.UserLocation != nil {
//		lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
//	}
//
//	// Step 4: Cache Integration - Generate cache key based on session parameters
//	sessionID := uuid.New()
//
//	// Initialize session
//	session := locitypes.ChatSession{
//		ID:        sessionID,
//		UserID:    cc.UserID,
//		ProfileID: cc.ProfileID,
//		CityName:  cc.CityName,
//		ConversationHistory: []locitypes.ConversationMessage{
//			{Role: "user", Content: cc.Message, Timestamp: time.Now()},
//		},
//		SessionContext: locitypes.SessionContext{
//			CityName:            cc.CityName,
//			ConversationSummary: fmt.Sprintf("Trip plan for %s", cc.CityName),
//		},
//		CreatedAt: time.Now(),
//		UpdatedAt: time.Now(),
//		ExpiresAt: time.Now().Add(24 * time.Hour),
//		Status:    "active",
//	}
//	if err := l.llmInteractionRepo.CreateSession(ctx, session); err != nil {
//		span.RecordError(err)
//		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
//		return fmt.Errorf("failed to create session: %w", err)
//	}
//
//	// Generate cache key based on session parameters
//	cacheKeyData := map[string]interface{}{
//		"user_id":     cc.UserID.String(),
//		"profile_id":  cc.ProfileID.String(),
//		"city":        normalizeCacheComponent(cc.CityName),
//		"message":     normalizeCacheComponent(cleanedMessage),
//		"domain":      string(domain),
//		"preferences": basePreferences,
//	}
//	cacheKeyBytes, err := json.Marshal(cacheKeyData)
//	if err != nil {
//		l.logger.ErrorContext(ctx, "Failed to marshal cache key data", slog.Any("error", err))
//		// Use a fallback cache key
//		cacheKeyBytes = []byte(fmt.Sprintf("fallback_%s_%s", cleanedMessage, cc.CityName))
//	}
//	hash := md5.Sum(cacheKeyBytes)
//	cacheKey := hex.EncodeToString(hash[:])
//
//	// Step 5: Fan-in Fan-out Setup
//	var wg sync.WaitGroup
//
//	l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{
//		Type: locitypes.EventTypeStart,
//		Data: map[string]interface{}{
//			"domain":     string(domain),
//			"city":       cc.CityName,
//			"session_id": sessionID.String(),
//			"cache_key":  cacheKey,
//		},
//	}, 3)
//
//	// Step 5: Collect responses for saving interaction
//	responses := make(map[string]*strings.Builder)
//	partCacheKeys := make(map[string]string)
//	responsesMutex := sync.Mutex{}
//
//	// Modified sendEventWithResponse to capture responses
//	sendEventWithResponse := func(event locitypes.StreamEvent) {
//		if event.Type == locitypes.EventTypeChunk {
//			responsesMutex.Lock()
//			if data, ok := event.Data.(map[string]interface{}); ok {
//				if partType, exists := data["part"].(string); exists {
//					if chunk, chunkExists := data["chunk"].(string); chunkExists {
//						if responses[partType] == nil {
//							responses[partType] = &strings.Builder{}
//						}
//						responses[partType].WriteString(chunk)
//					}
//				}
//			}
//			responsesMutex.Unlock()
//		}
//		l.sendEvent(ctx, cc.EventCh, event, 3)
//	}
//
//	// Step 6: Spawn streaming workers based on domain with cache support
//	switch domain {
//	case locitypes.DomainItinerary, locitypes.DomainGeneral:
//		wg.Add(3)
//
//		// Worker 1: Stream City Data with cache
//		go func() {
//			defer wg.Done()
//			prompt := getCityDataPrompt(cc.CityName)
//			partCacheKey := cacheKey + "_city_data"
//			responsesMutex.Lock()
//			partCacheKeys["city_data"] = partCacheKey
//			responsesMutex.Unlock()
//			l.streamWorkerWithResponseAndCache(ctx, prompt, "city_data", sendEventWithResponse, domain, partCacheKey)
//		}()
//
//		// Worker 2: Stream General POIs with cache
//		go func() {
//			defer wg.Done()
//			prompt := getGeneralPOIPrompt(cc.CityName)
//			partCacheKey := cacheKey + "_general_pois"
//			responsesMutex.Lock()
//			partCacheKeys["general_pois"] = partCacheKey
//			responsesMutex.Unlock()
//			l.streamWorkerWithResponseAndCache(ctx, prompt, "general_pois", sendEventWithResponse, domain, partCacheKey)
//		}()
//
//		// Worker 3: Stream Personalized Itinerary with cache
//		go func() {
//			defer wg.Done()
//			prompt := getPersonalizedItineraryPrompt(cc.CityName, basePreferences)
//			partCacheKey := cacheKey + "_itinerary"
//			responsesMutex.Lock()
//			partCacheKeys["itinerary"] = partCacheKey
//			responsesMutex.Unlock()
//			l.streamWorkerWithResponseAndCache(ctx, prompt, "itinerary", sendEventWithResponse, domain, partCacheKey)
//		}()
//
//	case locitypes.DomainAccommodation:
//		wg.Add(1)
//		go func() {
//			defer wg.Done()
//			prompt := getAccommodationPrompt(cc.CityName, lat, lon, basePreferences)
//			partCacheKey := cacheKey + "_hotels"
//			partCacheKeys["hotels"] = partCacheKey
//			l.streamWorkerWithResponseAndCache(ctx, prompt, "hotels", sendEventWithResponse, domain, partCacheKey)
//		}()
//
//	case locitypes.DomainDining:
//		wg.Add(1)
//		go func() {
//			defer wg.Done()
//			prompt := getDiningPrompt(cc.CityName, lat, lon, basePreferences)
//			partCacheKey := cacheKey + "_restaurants"
//			partCacheKeys["restaurants"] = partCacheKey
//			l.streamWorkerWithResponseAndCache(ctx, prompt, "restaurants", sendEventWithResponse, domain, partCacheKey)
//		}()
//
//	case locitypes.DomainActivities:
//		wg.Add(1)
//		go func() {
//			defer wg.Done()
//			prompt := getActivitiesPrompt(cc.CityName, lat, lon, basePreferences)
//			partCacheKey := cacheKey + "_activities"
//			partCacheKeys["activities"] = partCacheKey
//			l.streamWorkerWithResponseAndCache(ctx, prompt, "activities", sendEventWithResponse, domain, partCacheKey)
//		}()
//
//	default:
//		sendEventWithResponse(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: fmt.Sprintf("unhandled domain: %s", domain)})
//		return fmt.Errorf("unhandled domain type: %s", domain)
//	}
//
//	// Step 7: Wait for all workers to complete synchronously
//	// IMPORTANT: This must block until workers finish so the RPC stream stays open
//	wg.Wait()
//	l.logger.InfoContext(ctx, "All streaming workers completed")
//
//	// Only process completion if context is still active
//	if ctx.Err() == nil {
//		// Build complete data from collected responses
//		responsesMutex.Lock()
//
//		// If any expected parts are missing, try to hydrate from cache
//		for part, key := range partCacheKeys {
//			if builder, ok := responses[part]; !ok || builder == nil || builder.Len() == 0 {
//				if cached, found := l.cache.Get(key); found {
//					if cachedText, ok := cached.(string); ok && cachedText != "" {
//						l.logger.InfoContext(ctx, "Hydrated missing response part from cache",
//							slog.String("part_type", part), slog.String("cache_key", key))
//						b := &strings.Builder{}
//						b.WriteString(cachedText)
//						responses[part] = b
//					}
//				}
//			}
//		}
//
//		completeData := map[string]interface{}{
//			"session_id": sessionID.String(),
//		}
//		cityDataContent := ""
//		var fullResponseBuilder strings.Builder
//
//		// Parse and add each response part as structured JSON
//		for partType, builder := range responses {
//			if builder != nil && builder.Len() > 0 {
//				content := builder.String()
//				fullResponseBuilder.WriteString(fmt.Sprintf("[%s]\n%s\n\n", partType, content))
//
//				if partType == "city_data" {
//					cityDataContent = content
//				}
//
//				// Extract JSON from markdown code blocks if present
//				content = extractJSONFromMarkdown(content)
//
//				// Try to parse as JSON
//				var parsedJSON interface{}
//				if err := json.Unmarshal([]byte(content), &parsedJSON); err == nil {
//					switch partType {
//					case "city_data":
//						completeData["general_city_data"] = parsedJSON
//					case "general_pois":
//						completeData["points_of_interest"] = parsedJSON
//					case "itinerary":
//						completeData["itinerary_response"] = parsedJSON
//					case "hotels":
//						completeData["accommodation_response"] = parsedJSON
//					case "restaurants":
//						completeData["dining_response"] = parsedJSON
//					case "activities":
//						completeData["activities_response"] = parsedJSON
//					default:
//						completeData[partType] = parsedJSON
//					}
//				} else {
//					// If parsing fails, store as string
//					l.logger.WarnContext(ctx, "Failed to parse JSON from response part",
//						slog.String("part_type", partType), slog.Any("error", err))
//					completeData[partType+"_raw"] = content
//				}
//			}
//		}
//		responsesMutex.Unlock()
//
//		// Save city data and get cityID BEFORE sending itinerary event
//		var cityID uuid.UUID
//		if cityDataContent != "" {
//			if parsedCityData, parseErr := l.parseCityDataFromResponse(ctx, cityDataContent); parseErr == nil && parsedCityData != nil {
//				if savedCityID, handleErr := l.HandleCityData(ctx, *parsedCityData); handleErr == nil {
//					cityID = savedCityID
//					l.logger.InfoContext(ctx, "Successfully saved city data", slog.String("city_id", cityID.String()))
//				}
//			}
//		}
//		// Fallback: try to get existing city from database, or create it
//		if cityID == uuid.Nil && cc.CityName != "" {
//			existingCity, err := l.cityRepo.FindCityByNameAndCountry(ctx, cc.CityName, "Unknown")
//			if err != nil || existingCity == nil {
//				// City doesn't exist, create a minimal entry to allow POI saving
//				l.logger.InfoContext(ctx, "City not found in database, creating minimal entry",
//					slog.String("city_name", cc.CityName))
//				cityDetail := locitypes.CityDetail{
//					Name:          cc.CityName,
//					Country:       "Unknown", // Use consistent default to avoid duplicates
//					StateProvince: "Unknown", // Use consistent default to avoid duplicates
//				}
//				cityID, err = l.cityRepo.SaveCity(ctx, cityDetail)
//				if err != nil {
//					l.logger.WarnContext(ctx, "Failed to create city entry",
//						slog.String("city", cc.CityName),
//						slog.Any("error", err))
//					cityID = uuid.Nil
//				} else {
//					l.logger.InfoContext(ctx, "Successfully created city entry",
//						slog.String("city", cc.CityName),
//						slog.String("city_id", cityID.String()))
//				}
//			} else {
//				cityID = existingCity.ID
//				l.logger.InfoContext(ctx, "Found existing city",
//					slog.String("city", cc.CityName),
//					slog.String("city_id", cityID.String()))
//			}
//		}
//
//		// Save interaction and get llmInteractionID BEFORE sending itinerary event
//		fullResponse := fullResponseBuilder.String()
//		if fullResponse == "" {
//			fullResponse = fmt.Sprintf("Processed %s request for %s", domain, cc.CityName)
//		}
//		interaction := locitypes.LlmInteraction{
//			ID:           uuid.New(),
//			SessionID:    sessionID,
//			UserID:       cc.UserID,
//			ProfileID:    cc.ProfileID,
//			CityName:     cc.CityName,
//			Prompt:       fmt.Sprintf("Unified Chat Stream - Domain: %s, Message: %s", domain, cleanedMessage),
//			ResponseText: fullResponse,
//			ModelUsed:    model,
//			LatencyMs:    int(time.Since(startTime).Milliseconds()),
//			Timestamp:    startTime,
//		}
//		savedInteractionID, saveErr := l.llmInteractionRepo.SaveInteraction(ctx, interaction)
//		if saveErr != nil {
//			l.logger.WarnContext(ctx, "Failed to save interaction before sending event", slog.Any("error", saveErr))
//			savedInteractionID = uuid.Nil
//		} else {
//			l.logger.InfoContext(ctx, "Successfully saved interaction", slog.String("interaction_id", savedInteractionID.String()))
//		}
//
//		// Build AiCityResponse struct with database IDs
//		itineraryData := locitypes.AiCityResponse{
//			SessionID: sessionID,
//		}
//
//		// Populate structured data if available
//		if generalCityData, ok := completeData["general_city_data"]; ok {
//			if cityData, parseOk := generalCityData.(map[string]interface{}); parseOk {
//				// Try to unmarshal into GeneralCityData struct
//				if jsonBytes, err := json.Marshal(cityData); err == nil {
//					if err := json.Unmarshal(jsonBytes, &itineraryData.GeneralCityData); err != nil {
//						l.logger.WarnContext(ctx, "failed to unmarshal general city data", slog.Any("error", err))
//					}
//				}
//			}
//		}
//		if pois, ok := completeData["points_of_interest"]; ok {
//			// Handle cases where the response is nested, e.g., {"points_of_interest": [...]}
//			if poisMap, ok := pois.(map[string]interface{}); ok {
//				if poisArr, ok := poisMap["points_of_interest"].([]interface{}); ok {
//					if jsonBytes, err := json.Marshal(poisArr); err == nil {
//						if err := json.Unmarshal(jsonBytes, &itineraryData.PointsOfInterest); err != nil {
//							l.logger.WarnContext(ctx, "failed to unmarshal nested points of interest", slog.Any("error", err))
//						}
//					}
//				}
//			} else if poisArr, parseOk := pois.([]interface{}); parseOk { // Handle flat array response
//				if jsonBytes, err := json.Marshal(poisArr); err == nil {
//					if err := json.Unmarshal(jsonBytes, &itineraryData.PointsOfInterest); err != nil {
//						l.logger.WarnContext(ctx, "failed to unmarshal points of interest", slog.Any("error", err))
//					}
//				}
//			}
//		}
//		if itinResp, ok := completeData["itinerary_response"]; ok {
//			if itinData, parseOk := itinResp.(map[string]interface{}); parseOk {
//				if jsonBytes, err := json.Marshal(itinData); err == nil {
//					if err := json.Unmarshal(jsonBytes, &itineraryData.AIItineraryResponse); err != nil {
//						l.logger.WarnContext(ctx, "failed to unmarshal itinerary response", slog.Any("error", err))
//					}
//				}
//			}
//		}
//		if hotelsResp, ok := completeData["accommodation_response"]; ok {
//			if hotelsData, parseOk := hotelsResp.(map[string]interface{}); parseOk {
//				if hotelsArr, hasHotels := hotelsData["hotels"]; hasHotels {
//					if jsonBytes, err := json.Marshal(hotelsArr); err == nil {
//						if err := json.Unmarshal(jsonBytes, &itineraryData.Hotels); err != nil {
//							l.logger.WarnContext(ctx, "failed to unmarshal hotels", slog.Any("error", err))
//						}
//					}
//				}
//			}
//		}
//		if restaurantsResp, ok := completeData["dining_response"]; ok {
//			if restaurantsData, parseOk := restaurantsResp.(map[string]interface{}); parseOk {
//				if restaurantsArr, hasRestaurants := restaurantsData["restaurants"]; hasRestaurants {
//					if jsonBytes, err := json.Marshal(restaurantsArr); err == nil {
//						if err := json.Unmarshal(jsonBytes, &itineraryData.Restaurants); err != nil {
//							l.logger.WarnContext(ctx, "failed to unmarshal restaurants", slog.Any("error", err))
//						}
//					}
//				}
//			}
//		}
//		if activitiesResp, ok := completeData["activities_response"]; ok {
//			if activitiesData, parseOk := activitiesResp.(map[string]interface{}); parseOk {
//				if activitiesArr, hasActivities := activitiesData["activities"]; hasActivities {
//					if jsonBytes, err := json.Marshal(activitiesArr); err == nil {
//						if err := json.Unmarshal(jsonBytes, &itineraryData.Activities); err != nil {
//							l.logger.WarnContext(ctx, "failed to unmarshal activities", slog.Any("error", err))
//						}
//					}
//				}
//			}
//		}
//
//		// Set cityID and llmInteractionID on POIs
//		if cityID != uuid.Nil {
//			for i := range itineraryData.PointsOfInterest {
//				itineraryData.PointsOfInterest[i].CityID = cityID
//				if savedInteractionID != uuid.Nil {
//					itineraryData.PointsOfInterest[i].LlmInteractionID = savedInteractionID
//				}
//			}
//			for i := range itineraryData.AIItineraryResponse.PointsOfInterest {
//				itineraryData.AIItineraryResponse.PointsOfInterest[i].CityID = cityID
//				if savedInteractionID != uuid.Nil {
//					itineraryData.AIItineraryResponse.PointsOfInterest[i].LlmInteractionID = savedInteractionID
//				}
//			}
//			for i := range itineraryData.AIItineraryResponse.Restaurants {
//				itineraryData.AIItineraryResponse.Restaurants[i].CityID = cityID
//				if savedInteractionID != uuid.Nil {
//					itineraryData.AIItineraryResponse.Restaurants[i].LlmInteractionID = savedInteractionID
//				}
//			}
//		}
//
//		// Consolidate POIs and surface them on both itinerary and top-level fields
//		allPOIs := make([]locitypes.POIDetailedInfo, 0)
//		seenIDs := make(map[string]bool)
//
//		addUniquePOI := func(pois []locitypes.POIDetailedInfo) {
//			for _, poi := range pois {
//				key := poi.ID.String()
//				if key == "00000000-0000-0000-0000-000000000000" {
//					key = poi.Name
//				}
//				if !seenIDs[key] {
//					seenIDs[key] = true
//					allPOIs = append(allPOIs, poi)
//				}
//			}
//		}
//
//		addUniquePOI(itineraryData.AIItineraryResponse.PointsOfInterest)
//		addUniquePOI(itineraryData.PointsOfInterest)
//		addUniquePOI(itineraryData.AIItineraryResponse.Restaurants)
//		addUniquePOI(convertHotelsToPOIs(itineraryData.Hotels))
//		addUniquePOI(itineraryData.Activities)
//
//		itineraryData.AIItineraryResponse.PointsOfInterest = allPOIs
//		itineraryData.PointsOfInterest = allPOIs
//
//		l.logger.InfoContext(ctx, "Consolidated and deduplicated POIs into AIItineraryResponse",
//			slog.Int("total_unique_pois", len(allPOIs)),
//			slog.Int("from_top_level", len(itineraryData.PointsOfInterest)),
//			slog.Int("from_nested", len(itineraryData.AIItineraryResponse.PointsOfInterest)))
//
//		// Send EventTypeItinerary with proper IDs
//		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{
//			Type: locitypes.EventTypeItinerary,
//			Data: itineraryData,
//		}, 3)
//
//		// Update session with the initial itinerary data so it persists for future ContinueChat calls
//		session.CurrentItinerary = &itineraryData
//		session.UpdatedAt = time.Now()
//		if updateErr := l.llmInteractionRepo.UpdateSession(ctx, session); updateErr != nil {
//			l.logger.WarnContext(ctx, "Failed to update session with initial itinerary", slog.Any("error", updateErr))
//		} else {
//			l.logger.InfoContext(ctx, "Successfully saved initial itinerary to session",
//				slog.Int("poi_count", len(itineraryData.AIItineraryResponse.PointsOfInterest)),
//				slog.Int("top_level_pois", len(itineraryData.PointsOfInterest)))
//		}
//
//		// Determine route type based on domain
//		var routeType string
//		var baseURL string
//		switch domain {
//		case locitypes.DomainAccommodation:
//			routeType = "hotels"
//			baseURL = "/hotels"
//		case locitypes.DomainDining:
//			routeType = "restaurants"
//			baseURL = "/restaurants"
//		case locitypes.DomainActivities:
//			routeType = "activities"
//			baseURL = "/activities"
//		default:
//			routeType = "itinerary"
//			baseURL = "/itinerary"
//		}
//
//		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{
//			Type: locitypes.EventTypeComplete,
//			Data: map[string]interface{}{"session_id": sessionID.String()},
//			Navigation: &locitypes.NavigationData{
//				URL:       fmt.Sprintf("%s?sessionId=%s&cityName=%s&domain=%s", baseURL, sessionID.String(), url.QueryEscape(cc.CityName), routeType),
//				RouteType: routeType,
//				QueryParams: map[string]string{
//					"sessionId": sessionID.String(),
//					"cityName":  cc.CityName,
//					"domain":    routeType,
//				},
//			},
//		}, 3)
//	}
//	// Note: Do NOT close eventCh here - the handler owns the channel and will close it via defer
//	l.logger.InfoContext(ctx, "Completion processing finished, event channel will be closed by handler")
//
//	span.SetStatus(codes.Ok, "Unified chat stream processed successfully")
//	return nil
//}

And my new with the aux methods:

// ProcessUnifiedChatMessageStream handles unified chat with optimized streaming based on Google GenAI patterns
func (l *ServiceImpl) ProcessUnifiedChatMessageStream(cc common.ChatContext) error {
startTime := time.Now() // Track when processing starts
ctx, span := otel.Tracer("LlmInteractionService").Start(cc.Ctx, "ProcessUnifiedChatMessageStream", trace.WithAttributes(
attribute.String("message", cc.Message),
))
defer span.End()
cc.Ctx = ctx // Update context with tracing

	if err := l.prepareChatContext(&cc); err != nil {
		span.RecordError(err)
		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
		return err
	}

	rawResponses, err := l.orchestrateLLMStreams(&cc)
	if err != nil {
		span.RecordError(err)
		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
		return err
	}

	data, err := l.aggregateAndParse(&cc, rawResponses)
	if err != nil {
		span.RecordError(err)
		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
		return err
	}

	if err := l.persistResults(&cc, data, rawResponses, startTime); err != nil {
		span.RecordError(err)
		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
		return err
	}

	l.sendCompletionEvent(&cc)

	// Note: Do NOT close eventCh here - the handler owns the channel and will close it via defer
	l.logger.InfoContext(ctx, "Completion processing finished, event channel will be closed by handler")

	span.SetStatus(codes.Ok, "Unified chat stream processed successfully")
	return nil
}

package service

import (
"crypto/md5"
"encoding/hex"
"encoding/json"
"fmt"
"log/slog"
"net/url"
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
		l.logger.ErrorContext(ctx, "Failed to marshal cache key data", slog.Any("error", err))
		// Use a fallback cache key
		cacheKeyBytes = []byte(fmt.Sprintf("fallback_%s_%s", cleanedMessage, cc.CityName))
	}
	hash := md5.Sum(cacheKeyBytes)
	cc.CacheKey = hex.EncodeToString(hash[:])

	return nil
}

// orchestrateLLMStreams manages the fan-out concurrency to LLM workers.
func (l *ServiceImpl) orchestrateLLMStreams(cc *common.ChatContext) (map[string]string, error) {
ctx := cc.Ctx
var wg sync.WaitGroup

	l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{
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
		l.sendEvent(ctx, cc.EventCh, event, 3)
	}

	// Spawn workers based on Domain
	switch cc.Domain {
	case locitypes.DomainItinerary, locitypes.DomainGeneral:
		wg.Add(3)
		go func() {
			defer wg.Done()
			prompt := getCityDataPrompt(cc.CityName)
			partCacheKey := cc.CacheKey + "_city_data"
			responsesMutex.Lock()
			partCacheKeys["city_data"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(ctx, prompt, "city_data", sendEventWithResponse, cc.Domain, partCacheKey)
		}()
		go func() {
			defer wg.Done()
			prompt := getGeneralPOIPrompt(cc.CityName)
			partCacheKey := cc.CacheKey + "_general_pois"
			responsesMutex.Lock()
			partCacheKeys["general_pois"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(ctx, prompt, "general_pois", sendEventWithResponse, cc.Domain, partCacheKey)
		}()
		go func() {
			defer wg.Done()
			prompt := getPersonalizedItineraryPrompt(cc.CityName, cc.BasePreferences)
			partCacheKey := cc.CacheKey + "_itinerary"
			responsesMutex.Lock()
			partCacheKeys["itinerary"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(ctx, prompt, "itinerary", sendEventWithResponse, cc.Domain, partCacheKey)
		}()
	case locitypes.DomainAccommodation:
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lat, lon float64
			if cc.UserLocation != nil {
				lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
			}
			prompt := getAccommodationPrompt(cc.CityName, lat, lon, cc.BasePreferences)
			partCacheKey := cc.CacheKey + "_hotels"
			responsesMutex.Lock()
			partCacheKeys["hotels"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(ctx, prompt, "hotels", sendEventWithResponse, cc.Domain, partCacheKey)
		}()
	case locitypes.DomainDining:
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lat, lon float64
			if cc.UserLocation != nil {
				lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
			}
			prompt := getDiningPrompt(cc.CityName, lat, lon, cc.BasePreferences)
			partCacheKey := cc.CacheKey + "_restaurants"
			responsesMutex.Lock()
			partCacheKeys["restaurants"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(ctx, prompt, "restaurants", sendEventWithResponse, cc.Domain, partCacheKey)
		}()
	case locitypes.DomainActivities:
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lat, lon float64
			if cc.UserLocation != nil {
				lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
			}
			prompt := getActivitiesPrompt(cc.CityName, lat, lon, cc.BasePreferences)
			partCacheKey := cc.CacheKey + "_activities"
			responsesMutex.Lock()
			partCacheKeys["activities"] = partCacheKey
			responsesMutex.Unlock()
			l.streamWorkerWithResponseAndCache(ctx, prompt, "activities", sendEventWithResponse, cc.Domain, partCacheKey)
		}()
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
					responses[part] = &strings.Builder{}
					responses[part].WriteString(cachedText)
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
	//addUniquePOI(data.Restaurants)
	addUniquePOI(data.Activities)

	// Final assignment
	data.PointsOfInterest = allPOIs
	data.AIItineraryResponse.PointsOfInterest = allPOIs

	l.logger.InfoContext(ctx, "Consolidated and deduplicated POIs",
		slog.Int("total_unique_pois", len(allPOIs)))

	return data, nil
}

// persistResults handles saving City, Interactions, and Session updates.
func (l *ServiceImpl) persistResults(
cc *common.ChatContext,
data *locitypes.AiCityResponse,
rawResponses map[string]string,
startTime time.Time,
) error {
ctx := cc.Ctx

	// 1. Save City (if needed)
	var cityID uuid.UUID
	cityDataContent := rawResponses["city_data"]
	if cityDataContent != "" {
		parsedCityData, err := l.parseCityDataFromResponse(ctx, cityDataContent)
		if err == nil && parsedCityData != nil {
			savedCityID, err := l.HandleCityData(ctx, *parsedCityData)
			if err == nil {
				cityID = savedCityID
				l.logger.InfoContext(ctx, "Successfully saved city data", slog.String("city_id", cityID.String()))
			}
		}
	}
	// Fallback: try to get existing city from database, or create it
	if cityID == uuid.Nil && cc.CityName != "" {
		existingCity, err := l.cityRepo.FindCityByNameAndCountry(ctx, cc.CityName, "Unknown")
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
			cityID, err = l.cityRepo.SaveCity(ctx, cityDetail)
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
		LatencyMs:    int(time.Since(startTime).Milliseconds()),
		Timestamp:    startTime,
	}
	savedID, err := l.llmInteractionRepo.SaveInteraction(ctx, interaction)
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

	// Send Itinerary Event
	l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeItinerary,
		Data: *data,
	}, 3)

	// 4. Update Session
	session, err := l.llmInteractionRepo.GetSession(ctx, cc.SessionID)
	if err != nil {
		l.logger.WarnContext(ctx, "Failed to get session for update", slog.Any("error", err))
		return err
	}
	session.CurrentItinerary = data
	session.UpdatedAt = time.Now()
	if err := l.llmInteractionRepo.UpdateSession(ctx, *session); err != nil {
		l.logger.WarnContext(ctx, "Failed to update session with initial itinerary", slog.Any("error", err))
		return err
	}
	l.logger.InfoContext(ctx, "Successfully saved initial itinerary to session",
		slog.Int("poi_count", len(data.AIItineraryResponse.PointsOfInterest)),
		slog.Int("top_level_pois", len(data.PointsOfInterest)))

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
	default:
		routeType = "itinerary"
		baseURL = "/itinerary"
	}

	l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
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

Do the aux methods with the new function achieve the same as the original one that is commented out ?
