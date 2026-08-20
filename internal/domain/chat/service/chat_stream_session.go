package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func (l *ServiceImpl) ContinueSessionStreamed(
	ctx context.Context, sessionID uuid.UUID,
	message string, userLocation *locitypes.UserLocation,
	eventCh chan<- locitypes.StreamEvent, // Output channel for events
) error { // Only returns error for critical setup failures
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "ContinueSessionStreamed", trace.WithAttributes(
		attribute.String("session.id", sessionID.String()),
		attribute.String("message", message),
	))
	defer span.End()

	l.logger.DebugContext(ctx, "Continuing streamed chat session", slog.String("sessionID", sessionID.String()), slog.String("message", message))

	// --- 1. Fetch Session & Basic Validation ---
	session, err := l.llmInteractionRepo.GetSession(ctx, sessionID)
	if err != nil {
		err = fmt.Errorf("failed to get session %s: %w", sessionID, err)
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error(), IsFinal: true}, 3)
		return err
	}
	if session.Status != locitypes.StatusActive {
		err = fmt.Errorf("session %s is not active (status: %s) %w", sessionID, session.Status, err)
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error(), IsFinal: true}, 3)
		return err
	}
	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: "session_validated", Data: map[string]string{"status": "active"}}, 3)

	// --- 2. Fetch City ID ---
	cityData, err := l.cityRepo.FindCityByNameAndCountry(ctx, session.SessionContext.CityName, "")
	if err != nil || cityData == nil {
		// If the city is not found, try a fuzzy match
		cityData, err = l.cityRepo.FindCityByFuzzyName(ctx, session.SessionContext.CityName)
		if err != nil || cityData == nil {
			if err == nil {
				err = fmt.Errorf("city '%s' not found for session %s", session.SessionContext.CityName, sessionID)
			} else {
				err = fmt.Errorf("failed to find city '%s' for session %s: %w", session.SessionContext.CityName, sessionID, err)
			}
			l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error(), IsFinal: true}, 3)
			return err
		}
	}
	cityID := cityData.ID
	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: map[string]any{"status": "context_loaded", "city_id": cityID.String()}}, 3)

	// --- 3. Add User Message to History ---
	userMessage := locitypes.ConversationMessage{
		ID: uuid.New(), Role: locitypes.RoleUser, Content: message, Timestamp: time.Now(), MessageType: locitypes.TypeModificationRequest,
	}
	if err := l.llmInteractionRepo.AddMessageToSession(ctx, sessionID, userMessage); err != nil {
		l.logger.WarnContext(ctx, "Failed to persist user message, continuing with in-memory history", slog.Any("error", err))
		span.RecordError(err, trace.WithAttributes(attribute.String("warning", "User message DB save failed")))
	}
	session.ConversationHistory = append(session.ConversationHistory, userMessage)

	// --- 4. Classify Intent ---
	intent, err := l.intentClassifier.Classify(ctx, message)
	if err != nil {
		err = fmt.Errorf("failed to classify intent for message '%s': %w", message, err)
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error(), IsFinal: true}, 3)
		return err
	}
	l.logger.InfoContext(ctx, "Intent classified", slog.String("intent", string(intent)))
	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: "intent_classified", Data: map[string]string{"intent": string(intent)}}, 3)

	// --- 5. Enhance with Semantic POI Recommendations ---
	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeProgress,
		Data: map[string]any{"status": "generating_semantic_context", "progress": 20},
	}, 3)

	semanticPOIs, err := l.generateSemanticPOIRecommendations(ctx, message, cityID, session.UserID, userLocation, 0.6)
	if err != nil {
		l.logger.WarnContext(ctx, "Failed to generate semantic POI recommendations for streaming session", slog.Any("error", err))
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeProgress,
			Data: map[string]any{"status": "semantic_context_failed", "progress": 22},
		}, 3)
	} else {
		l.logger.InfoContext(ctx, "Generated semantic POI recommendations for streaming session",
			slog.Int("semantic_recommendations", len(semanticPOIs)))
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type: "semantic_context_generated",
			Data: map[string]any{
				"status":                         "semantic_context_ready",
				"semantic_recommendations_count": len(semanticPOIs),
				"progress":                       25,
			},
		}, 3)
	}

	// --- 5. Handle Intent and Generate Response ---
	var finalResponseMessage string
	assistantMessageType := locitypes.TypeResponse
	itineraryModifiedByThisTurn := false

	switch intent { // Align with ContinueSession's string-based intents
	case locitypes.IntentAddPOI:
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: "Processing: Adding Point of Interest with semantic enhancement..."}, 3)
		var genErr error
		finalResponseMessage, genErr = l.handleSemanticAddPOIStreamed(ctx, message, session, semanticPOIs, userLocation, cityID, eventCh)
		if genErr != nil {
			finalResponseMessage = "I had trouble understanding your request. Could you please specify which POI you'd like to add?"
			assistantMessageType = locitypes.TypeError
		} else {
			itineraryModifiedByThisTurn = true
		}

	case locitypes.IntentRemovePOI:
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: "Processing: Removing Point of Interest with semantic understanding..."}, 3)
		finalResponseMessage = l.handleSemanticRemovePOI(ctx, message, session)
		if strings.Contains(finalResponseMessage, "I've removed") {
			itineraryModifiedByThisTurn = true
		}

	case locitypes.IntentAskQuestion:
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: "Processing: Answering your question with semantic context..."}, 3)
		// Answer the question from the evidence just retrieved, the itinerary
		// the user is holding, and what has already been said. This branch used
		// to return a fixed string without calling a model, so a direct question
		// was answered by asking the user to repeat it.
		answer, answerErr := l.answerQuestionStreamed(ctx, session, message,
			l.packetForSession(ctx, session, message, cityID, semanticPOIs), eventCh)
		if answerErr != nil {
			l.logger.WarnContext(ctx, "failed to answer question", slog.Any("error", answerErr))
			finalResponseMessage = "I could not work that one out just now. Could you rephrase it?"
			assistantMessageType = locitypes.TypeError
		} else {
			finalResponseMessage = answer
		}

	case "replace_poi":
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: "Processing: Replacing Point of Interest..."}, 3)
		if matches := replacePOIRE.FindStringSubmatch(strings.ToLower(message)); len(matches) == 3 {
			oldPOI := matches[1]
			newPOIName := matches[2]
			for i, p := range session.CurrentItinerary.AIItineraryResponse.PointsOfInterest {
				if strings.Contains(strings.ToLower(p.Name), oldPOI) {
					newPOI, err := l.generatePOIDataStream(ctx, newPOIName, session.SessionContext.CityName, userLocation, session.UserID, cityID, eventCh)
					if err != nil {
						finalResponseMessage = fmt.Sprintf("Could not replace %s with %s due to an error: %v", oldPOI, newPOIName, err)
						assistantMessageType = locitypes.TypeError
					} else {
						session.CurrentItinerary.AIItineraryResponse.PointsOfInterest[i] = newPOI
						finalResponseMessage = fmt.Sprintf("I've replaced %s with %s in your itinerary.", oldPOI, newPOIName)
						itineraryModifiedByThisTurn = true
					}
					break
				}
			}
			if finalResponseMessage == "" {
				finalResponseMessage = fmt.Sprintf("Could not find %s in your itinerary.", oldPOI)
			}
		} else {
			finalResponseMessage = "Please specify the replacement clearly (e.g., 'replace X with Y')."
			assistantMessageType = locitypes.TypeClarification
		}

	default: // modify_itinerary
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: "Processing: Updating itinerary..."}, 3)
		if matches := replacePOIRE.FindStringSubmatch(strings.ToLower(message)); len(matches) == 3 {
			oldPOI := matches[1]
			newPOIName := matches[2]
			for i, p := range session.CurrentItinerary.AIItineraryResponse.PointsOfInterest {
				if strings.Contains(strings.ToLower(p.Name), oldPOI) {
					newPOI, err := l.generatePOIData(ctx, newPOIName, session.SessionContext.CityName, userLocation, session.UserID, cityID)
					if err != nil {
						l.logger.ErrorContext(ctx, "Failed to generate POI data", slog.Any("error", err))
						span.RecordError(err)
						finalResponseMessage = fmt.Sprintf("Could not replace %s with %s due to an error.", oldPOI, newPOIName)
					} else {
						session.CurrentItinerary.AIItineraryResponse.PointsOfInterest[i] = newPOI
						finalResponseMessage = fmt.Sprintf("I’ve replaced %s with %s in your itinerary.", oldPOI, newPOIName)
					}
					break
				}
			}
			if finalResponseMessage == "" {
				finalResponseMessage = fmt.Sprintf("Could not find %s in your itinerary.", oldPOI)
			}
		} else {
			finalResponseMessage = "I’ve noted your request to modify the itinerary. Please specify the changes (e.g., 'replace X with Y')."
		}
	}

	// --- 6. Post-Modification Processing (Sorting, Saving Session) ---
	if itineraryModifiedByThisTurn && userLocation != nil && userLocation.UserLat != 0 && userLocation.UserLon != 0 && session.CurrentItinerary != nil {
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: "Sorting updated POIs by distance..."}, 3)
		// Save new POIs to DB to ensure they have valid IDs
		for i, p := range session.CurrentItinerary.AIItineraryResponse.PointsOfInterest {
			if p.ID == uuid.Nil {
				dbPoiID, saveErr := l.llmInteractionRepo.SaveSinglePOI(ctx, p, session.UserID, cityID, p.LlmInteractionID)
				if saveErr != nil {
					l.logger.WarnContext(ctx, "Failed to save new POI", slog.String("name", p.Name), slog.Any("error", saveErr))
					continue
				}
				session.CurrentItinerary.AIItineraryResponse.PointsOfInterest[i].ID = dbPoiID
			}
		}

		if (intent == locitypes.IntentAddPOI || intent == locitypes.IntentModifyItinerary) && userLocation.UserLat != 0 && userLocation.UserLon != 0 {
			sortedPOIs, err := l.llmInteractionRepo.GetPOIsBySessionSortedByDistance(ctx, sessionID, cityID, *userLocation)
			if err != nil {
				l.logger.WarnContext(ctx, "Failed to sort POIs by distance", slog.Any("error", err))
				span.RecordError(err)
			} else {
				session.CurrentItinerary.AIItineraryResponse.PointsOfInterest = sortedPOIs
				l.logger.InfoContext(ctx, "POIs sorted by distance",
					slog.Int("poi_count", len(sortedPOIs)))
				span.SetAttributes(attribute.Int("sorted_pois.count", len(sortedPOIs)))
			}
		}
	}

	// Add assistant's final response to history
	assistantMessage := locitypes.ConversationMessage{
		ID: uuid.New(), Role: locitypes.RoleAssistant, Content: finalResponseMessage, Timestamp: time.Now(), MessageType: assistantMessageType,
	}
	if err := l.llmInteractionRepo.AddMessageToSession(ctx, sessionID, assistantMessage); err != nil {
		l.logger.WarnContext(ctx, "Failed to save assistant message", slog.Any("error", err))
	}
	session.ConversationHistory = append(session.ConversationHistory, assistantMessage)

	// Update session in the database
	session.UpdatedAt = time.Now()
	session.ExpiresAt = time.Now().Add(24 * time.Hour)
	if err := l.llmInteractionRepo.UpdateSession(ctx, *session); err != nil {
		err = fmt.Errorf("failed to update session %s: %w", sessionID, err)
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error(), IsFinal: true}, 3)
		return err
	}

	// --- 7. Send Final Itinerary and Completion Event ---
	// Create a consolidated response to avoid sending duplicates to the client
	responseData := session.CurrentItinerary
	if session.CurrentItinerary != nil {
		// Make a copy to avoid modifying the session
		responseCopy := *session.CurrentItinerary
		responseData = &responseCopy

		// Merge all POIs from top-level and nested locations
		allPOIs := make([]locitypes.POIDetailedInfo, 0)
		seenIDs := make(map[string]bool)

		// Helper to add unique POIs
		addUnique := func(pois []locitypes.POIDetailedInfo) {
			for _, poi := range pois {
				key := poi.ID.String()
				if key == "00000000-0000-0000-0000-000000000000" {
					key = poi.Name // Use name for placeholder IDs
				}
				if !seenIDs[key] {
					seenIDs[key] = true
					allPOIs = append(allPOIs, poi)
				}
			}
		}

		// Collect from all sources
		addUnique(session.CurrentItinerary.PointsOfInterest)
		addUnique(session.CurrentItinerary.AIItineraryResponse.PointsOfInterest)
		addUnique(session.CurrentItinerary.AIItineraryResponse.Restaurants)

		// Clear redundant fields in the response copy and put everything in one place
		responseData.PointsOfInterest = nil
		responseData.AIItineraryResponse.Restaurants = nil
		responseData.AIItineraryResponse.PointsOfInterest = allPOIs

		l.logger.InfoContext(ctx, "Consolidated POIs for response",
			slog.Int("total_unique_pois", len(allPOIs)))
	}

	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
		Type:      locitypes.EventTypeItinerary,
		Data:      responseData,
		Message:   finalResponseMessage,
		Timestamp: time.Now(),
	}, 3)
	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
		Type:    locitypes.EventTypeComplete,
		Data:    "Turn completed.",
		IsFinal: true,
		Navigation: &locitypes.NavigationData{
			URL:       fmt.Sprintf("/itinerary?sessionId=%s&cityName=%s&domain=itinerary", sessionID.String(), url.QueryEscape(session.CityName)),
			RouteType: "itinerary",
			QueryParams: map[string]string{
				"sessionId": sessionID.String(),
				"cityName":  session.CityName,
				"domain":    "itinerary",
			},
		},
	}, 3)

	l.logger.InfoContext(ctx, "Streamed session continued", slog.String("sessionID", sessionID.String()), slog.String("intent", string(intent)))
	return nil
}

// generatePOIDataStream queries the LLM for POI details and streams updates

func (l *ServiceImpl) generatePOIDataStream(
	ctx context.Context, poiName, cityName string,
	userLocation *locitypes.UserLocation, userID, cityID uuid.UUID,
	eventCh chan<- locitypes.StreamEvent,
) (locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "generatePOIDataStream",
		trace.WithAttributes(attribute.String("p.name", poiName), attribute.String("city.name", cityName)))
	defer span.End()

	prompt := generatedContinuedConversationPrompt(poiName, cityName)
	config := &genai.GenerateContentConfig{Temperature: genai.Ptr[float32](0.2)}
	startTime := time.Now()

	var responseTextBuilder strings.Builder
	iter, err := l.aiClient.GenerateStream(ctx, prompt, config)
	if err != nil {
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type:      locitypes.EventTypeError,
			Error:     fmt.Sprintf("Failed to generate POI data for '%s': %v", poiName, err),
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		}, 3)
		return locitypes.POIDetailedInfo{}, fmt.Errorf("AI stream init failed for POI '%s': %w", poiName, err)
	}

	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
		Type:      locitypes.EventTypeProgress,
		Data:      map[string]string{"status": fmt.Sprintf("Getting details for %s...", poiName)},
		Timestamp: time.Now(),
		EventID:   uuid.New().String(),
	}, 3)

	for resp, err := range iter {
		if err != nil {
			l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
				Type:      locitypes.EventTypeError,
				Error:     fmt.Sprintf("Streaming failed for POI '%s': %v", poiName, err),
				Timestamp: time.Now(),
				EventID:   uuid.New().String(),
			}, 3)
			return locitypes.POIDetailedInfo{}, fmt.Errorf("streaming POI details for '%s' failed: %w", poiName, err)
		}
		for _, cand := range resp.Candidates {
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					if part.Text != "" {
						responseTextBuilder.WriteString(part.Text)
						l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
							Type:      "poi_detail_chunk",
							Data:      map[string]string{"poi_name": poiName, "chunk": part.Text},
							Timestamp: time.Now(),
							EventID:   uuid.New().String(),
						}, 3)
					}
				}
			}
		}
	}

	if ctx.Err() != nil {
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type:      locitypes.EventTypeError,
			Error:     ctx.Err().Error(),
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		}, 3)
		return locitypes.POIDetailedInfo{}, fmt.Errorf("context cancelled during POI detail generation: %w", ctx.Err())
	}

	fullText := responseTextBuilder.String()
	if fullText == "" {
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type:      locitypes.EventTypeError,
			Error:     fmt.Sprintf("Empty response for POI '%s'", poiName),
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		}, 3)
		return locitypes.POIDetailedInfo{Name: poiName, DescriptionPOI: "Details not found."}, fmt.Errorf("empty response for POI details '%s'", poiName)
	}

	// Save LLM interaction
	interaction := locitypes.LlmInteraction{
		UserID:       userID,
		Prompt:       prompt,
		ResponseText: fullText,
		Timestamp:    startTime,
		CityName:     cityName,
	}
	llmInteractionID, err := l.saveCityInteraction(ctx, interaction)
	if err != nil {
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type:      locitypes.EventTypeError,
			Error:     fmt.Sprintf("Failed to save LLM interaction for POI '%s': %v", poiName, err),
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		}, 3)
		return locitypes.POIDetailedInfo{}, fmt.Errorf("failed to save LLM interaction: %w", err)
	}

	// Parse response
	cleanJSON := generativeAI.CleanJSON(fullText)
	var poiData locitypes.POIDetailedInfo
	if err := json.Unmarshal([]byte(cleanJSON), &poiData); err != nil || poiData.Name == "" {
		l.logger.WarnContext(ctx, "Invalid POI data from LLM", slog.String("response", fullText), slog.Any("error", err))
		poiData = locitypes.POIDetailedInfo{
			ID:             uuid.New(),
			Name:           poiName,
			Category:       "Attraction",
			DescriptionPOI: fmt.Sprintf("Added %s based on user request, but detailed data not available.", poiName),
		}
	}
	if poiData.ID == uuid.Nil {
		poiData.ID = uuid.New()
	}
	poiData.LlmInteractionID = llmInteractionID
	poiData.City = cityName

	// Save POI to database
	dbPoiID, err := l.llmInteractionRepo.SaveSinglePOI(ctx, poiData, userID, cityID, llmInteractionID)
	if err != nil {
		l.logger.WarnContext(ctx, "Failed to save POI to database", slog.Any("error", err))
		span.RecordError(err)
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type:      locitypes.EventTypeError,
			Error:     fmt.Sprintf("Failed to save POI '%s' to database: %v", poiName, err),
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		}, 3)
		return locitypes.POIDetailedInfo{}, fmt.Errorf("failed to save POI to database: %w", err)
	}
	poiData.ID = dbPoiID

	// Calculate distance
	if userLocation != nil && userLocation.UserLat != 0 && userLocation.UserLon != 0 && poiData.Latitude != 0 && poiData.Longitude != 0 {
		distance, err := l.poiRepo.CalculateDistancePostGIS(ctx, userLocation.UserLat, userLocation.UserLon, poiData.Latitude, poiData.Longitude)
		if err != nil {
			l.logger.WarnContext(ctx, "Failed to calculate distance", slog.Any("error", err))
			span.RecordError(err)
		} else {
			poiData.Distance = distance
		}
	}

	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
		Type:      "poi_detail_complete",
		Data:      poiData,
		Timestamp: time.Now(),
		EventID:   uuid.New().String(),
	}, 3)
	return poiData, nil
}

func (l *ServiceImpl) handleSemanticAddPOIStreamed(ctx context.Context, message string, session *locitypes.ChatSession, semanticPOIs []locitypes.POIDetailedInfo, userLocation *locitypes.UserLocation, cityID uuid.UUID, eventCh chan<- locitypes.StreamEvent) (string, error) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "handleSemanticAddPOIStreamed")
	defer span.End()

	// Ensure the session has an initialized itinerary
	l.ensureItineraryExists(session)

	// Try semantic matching first - look for POIs semantically similar to the user's request
	if len(semanticPOIs) > 0 {
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeProgress,
			Data: map[string]any{
				"status":           "analyzing_semantic_matches",
				"semantic_options": len(semanticPOIs),
			},
		}, 3)

		for _, semanticPOI := range semanticPOIs[:minInt(3, len(semanticPOIs))] {
			alreadyExists := false
			for _, existingPOI := range session.CurrentItinerary.AIItineraryResponse.PointsOfInterest {
				if strings.EqualFold(existingPOI.Name, existingPOI.Name) {
					alreadyExists = true
					break
				}
			}

			if !alreadyExists {
				l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
					Type: "semantic_poi_added",
					Data: map[string]any{
						"poi_name":       semanticPOI.Name,
						"poi_category":   semanticPOI.Category,
						"latitude":       semanticPOI.Latitude,
						"longitude":      semanticPOI.Longitude,
						"description":    semanticPOI.DescriptionPOI,
						"semantic_match": true,
					},
				}, 3)

				// Add semantic POI to itinerary
				session.CurrentItinerary.AIItineraryResponse.PointsOfInterest = append(
					session.CurrentItinerary.AIItineraryResponse.PointsOfInterest, semanticPOI,
				)
				l.logger.InfoContext(ctx, "Added semantic POI to streaming itinerary",
					slog.String("poi_name", semanticPOI.Name))
				span.SetAttributes(attribute.String("added_poi", semanticPOI.Name))

				return fmt.Sprintf("Great! I found %s which matches what you're looking for. I've added it to your itinerary. %s",
					semanticPOI.Name, semanticPOI.DescriptionPOI), nil
			}
		}

		// If semantic POIs exist but all are already in itinerary
		l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
			Type: "semantic_alternatives_suggested",
			Data: map[string]any{
				"message": "All semantic matches already in itinerary",
				"alternatives": func() []string {
					var names []string
					for i, p := range semanticPOIs[:minInt(3, len(semanticPOIs))] {
						names = append(names, p.Name)
						if i >= 2 {
							break
						}
					}
					return names
				}(),
			},
		}, 3)

		return fmt.Sprintf("I found some great options matching your request, but they're already in your itinerary. Here are some suggestions: %s",
			strings.Join(func() []string {
				var names []string
				for i, p := range semanticPOIs[:minInt(3, len(semanticPOIs))] {
					names = append(names, p.Name)
					if i >= 2 {
						break
					}
				}
				return names
			}(), ", ")), nil
	}

	// Fallback to traditional POI name extraction and generation
	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeProgress,
		Data: map[string]any{"status": "extracting_poi_name"},
	}, 3)

	poiName := extractPOIName(message)
	if poiName == "" {
		return "I'd be happy to add a POI to your itinerary! Could you please specify which place you'd like to add?", nil
	}

	// Check if already exists
	for _, p := range session.CurrentItinerary.AIItineraryResponse.PointsOfInterest {
		if strings.EqualFold(p.Name, poiName) {
			return fmt.Sprintf("%s is already in your itinerary.", poiName), nil
		}
	}

	// Generate new POI data with streaming updates
	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeProgress,
		Data: map[string]any{
			"status":   "generating_poi_data",
			"poi_name": poiName,
		},
	}, 3)

	newPOI, err := l.generatePOIDataStream(ctx, poiName, session.SessionContext.CityName, userLocation, session.UserID, cityID, eventCh)
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to generate POI data for streaming", slog.Any("error", err))
		span.RecordError(err)
		return "", fmt.Errorf("failed to generate POI data: %w", err)
	}

	session.CurrentItinerary.AIItineraryResponse.PointsOfInterest = append(
		session.CurrentItinerary.AIItineraryResponse.PointsOfInterest, newPOI,
	)

	l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
		Type: "poi_added_successfully",
		Data: map[string]any{
			"poi_name":       newPOI.Name,
			"poi_category":   newPOI.Category,
			"semantic_match": false,
		},
	}, 3)

	return fmt.Sprintf("I've added %s to your itinerary.", poiName), nil
}

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
		hasContent := false
		for _, part := range rawResponses {
			if strings.TrimSpace(part) != "" {
				hasContent = true
				break
			}
		}
		if !hasContent {
			span.RecordError(err)
			l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}, 3)
			return err
		}
		l.logger.WarnContext(ctx, "Some streaming workers failed; continuing with partial or cache-hydrated responses",
			slog.Any("error", err))
		span.RecordError(err)
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

// ensureItineraryExists initializes the session's CurrentItinerary if it's nil

func (l *ServiceImpl) streamWorkerWithResponseAndCache(ctx context.Context, prompt, partType string, sendEvent func(locitypes.StreamEvent), domain locitypes.DomainType, cacheKey string) error {
	// Step 1: Check cache first if cacheKey is provided
	if cacheKey != "" {
		if cached, found := l.cache.Get(cacheKey); found {
			if cachedText, ok := cached.(string); ok {
				l.logger.InfoContext(ctx, "Cache hit for LLM response",
					slog.String("part_type", partType),
					slog.String("cache_key", cacheKey))

				// Stream cached response in chunks to simulate real streaming
				chunkSize := 100 // characters per chunk
				for i := 0; i < len(cachedText); i += chunkSize {
					if ctx.Err() != nil {
						return ctx.Err()
					}

					end := min(i+chunkSize, len(cachedText))
					chunk := cachedText[i:end]

					sendEvent(locitypes.StreamEvent{
						Type: locitypes.EventTypeChunk,
						Data: map[string]any{
							"part":       partType,
							"chunk":      chunk,
							"domain":     string(domain),
							"cache_key":  cacheKey,
							"cache_used": true,
						},
					})

					// Small delay to simulate streaming
					time.Sleep(10 * time.Millisecond)
				}
				return nil
			}
		}

		l.logger.InfoContext(ctx, "Cache miss for LLM response",
			slog.String("part_type", partType),
			slog.String("cache_key", cacheKey))
	}

	// Step 2: Cache miss or no cache key - call LLM (SDK retries stream init)
	l.logger.InfoContext(ctx, "Calling LLM for streaming",
		slog.String("part_type", partType),
		slog.String("cache_key", cacheKey),
		slog.Int("prompt_length", len(prompt)))

	release, err := l.acquireLLMSlot(ctx)
	if err != nil {
		l.logger.ErrorContext(ctx, "LLM capacity exceeded",
			slog.String("part_type", partType),
			slog.Any("error", err))
		if ctx.Err() == nil {
			sendEvent(locitypes.StreamEvent{
				Type:  locitypes.EventTypeError,
				Error: "We are experiencing high traffic. Please try again in a minute.",
			})
		}
		return err
	}
	defer release()

	iter, err := l.aiClient.GenerateStream(ctx, prompt, &genai.GenerateContentConfig{Temperature: genai.Ptr[float32](defaultTemperature)})
	if err != nil {
		l.logger.ErrorContext(ctx, "LLM stream call failed",
			slog.String("part_type", partType),
			slog.Any("error", err))
		if ctx.Err() == nil {
			errorMsg := fmt.Sprintf("%s worker failed: %v", partType, err)
			// Check for quota/rate limit errors
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") || strings.Contains(err.Error(), "quota") {
				errorMsg = "We are experiencing high traffic (Quota Exceeded). Please try again in a minute."
			}

			sendEvent(locitypes.StreamEvent{
				Type:  locitypes.EventTypeError,
				Error: errorMsg,
			})
		}
		return fmt.Errorf("%s worker failed: %w", partType, err)
	}
	// Step 3: Stream response and collect full text for caching
	var fullResponse strings.Builder
	chunkCount := 0
	for resp, err := range iter {
		if ctx.Err() != nil {
			l.logger.WarnContext(ctx, "Context canceled during streaming",
				slog.String("part_type", partType),
				slog.Int("chunks_received", chunkCount))
			return ctx.Err()
		}
		if err != nil {
			l.logger.ErrorContext(ctx, "Streaming error from LLM",
				slog.String("part_type", partType),
				slog.Any("error", err))
			if ctx.Err() == nil {
				sendEvent(locitypes.StreamEvent{
					Type:  locitypes.EventTypeError,
					Error: fmt.Sprintf("%s streaming error: %v", partType, err),
				})
			}
			return fmt.Errorf("%s streaming error: %w", partType, err)
		}
		for _, cand := range resp.Candidates {
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					if part.Text != "" {
						chunk := part.Text
						chunkCount++
						fullResponse.WriteString(chunk)

						// Debug-only, and without a content preview: the chunk is
						// user-influenced model output and must not land in Info logs.
						if chunkCount <= 3 {
							l.logger.DebugContext(ctx, "Received chunk from LLM",
								slog.String("part_type", partType),
								slog.Int("chunk_number", chunkCount),
								slog.Int("chunk_length", len(chunk)))
						}

						sendEvent(locitypes.StreamEvent{
							Type: locitypes.EventTypeChunk,
							Data: map[string]any{
								"part":       partType,
								"chunk":      chunk,
								"domain":     string(domain),
								"cache_key":  cacheKey,
								"cache_used": false,
							},
						})
					}
				}
			}
		}
	}

	l.logger.InfoContext(ctx, "LLM streaming completed",
		slog.String("part_type", partType),
		slog.Int("total_chunks", chunkCount),
		slog.Int("total_response_length", fullResponse.Len()))

	// Step 4: Save full response to cache if cacheKey is provided
	if cacheKey != "" && fullResponse.Len() > 0 {
		l.cache.Set(cacheKey, fullResponse.String(), 0)
		l.logger.InfoContext(ctx, "Saved LLM response to cache",
			slog.String("part_type", partType),
			slog.String("cache_key", cacheKey),
			slog.Int("response_length", fullResponse.Len()))
	}
	return nil
}

// convertRestaurantsToPOIs lifts restaurant-specific details into the generic POI shape
// so they can be transported through itinerary responses that only expose POIs.
