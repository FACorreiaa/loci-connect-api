package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat/chatconnect"
	cityv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/city"
	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/poi"
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/recommendation"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/resumebuf"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// ChatHandler implements the ChatServiceHandler interface.
type ChatHandler struct {
	chatconnect.UnimplementedChatServiceHandler
	service   service.LlmInteractiontService
	logger    *slog.Logger
	resumeBuf *resumebuf.Buffer
}

// NewChatHandler creates a new ChatHandler.
func NewChatHandler(llmInteractionService service.LlmInteractiontService, logger *slog.Logger) *ChatHandler {
	return &ChatHandler{
		resumeBuf: resumebuf.New(),
		service:   llmInteractionService,
		logger:    logger,
	}
}

func (h *ChatHandler) StartChat(
	ctx context.Context,
	req *connect.Request[chatv1.StartChatRequest],
) (*connect.Response[chatv1.ChatResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	// Extract profileID if provided
	var profileID uuid.UUID
	if req.Msg.GetProfileId() != "" {
		if pid, err := uuid.Parse(req.Msg.GetProfileId()); err == nil {
			profileID = pid
		}
	}

	// Extract cityName from request
	cityName := req.Msg.GetCityName()

	// Extract userLocation if provided
	var userLoc *locitypes.UserLocation
	if loc := req.Msg.GetUserLocation(); loc != nil {
		userLoc = &locitypes.UserLocation{
			UserLat: loc.GetLatitude(),
			UserLon: loc.GetLongitude(),
		}
	}

	resp, err := h.service.StartChat(ctx, userID, profileID, cityName, req.Msg.GetInitialMessage(), userLoc)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.ToChatResponse(resp)), nil
}

// StreamChat handles the streaming chat RPC.
func (h *ChatHandler) StreamChat(
	ctx context.Context,
	req *connect.Request[chatv1.ChatRequest],
	stream *connect.ServerStream[chatv1.StreamEvent],
) error {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	// Extract profileID if provided
	var profileID uuid.UUID
	if req.Msg.GetProfileId() != "" {
		if pid, err := uuid.Parse(req.Msg.GetProfileId()); err == nil {
			profileID = pid
		}
	}

	// Extract cityName from request
	cityName := req.Msg.GetCityName()

	// Extract userLocation if provided
	var userLoc *locitypes.UserLocation
	if loc := req.Msg.GetUserLocation(); loc != nil {
		userLoc = &locitypes.UserLocation{
			UserLat: loc.GetLatitude(),
			UserLon: loc.GetLongitude(),
		}
	}

	eventCh := make(chan locitypes.StreamEvent, 100)

	// Propagate trace/request IDs from the RPC context, but detach client cancel
	// so LLM work can finish after disconnect. Handler timeout (CHAT_RPC_TIMEOUT_SEC,
	// default 3m) bounds preparation; workers use a separate 5m deadline.
	llmCtx, llmCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)

	// Resume/trip context from the request. session_id asks the server to
	// continue an existing session rather than mint a new one.
	var requestedSessionID uuid.UUID
	if sid := req.Msg.GetSessionId(); sid != "" {
		if parsed, perr := uuid.Parse(sid); perr == nil {
			requestedSessionID = parsed
		}
	}
	var tripID uuid.UUID
	if tid := req.Msg.GetTripId(); tid != "" {
		if parsed, perr := uuid.Parse(tid); perr == nil {
			tripID = parsed
		}
	}

	cc := common.ChatContext{
		Ctx:                llmCtx,
		UserID:             userID,
		ProfileID:          profileID,
		CityName:           cityName,
		Message:            req.Msg.GetMessage(),
		UserLocation:       userLoc,
		EventCh:            eventCh,
		RequestedSessionID: requestedSessionID,
		ResumeToken:        req.Msg.GetResumeToken(),
		TripID:             tripID,
	}

	// bufSessionID keys the resume buffer. Known up front for a resume/continue
	// (client sent session_id); for a fresh stream it's captured from the start
	// event below once the server mints the session.
	bufSessionID := ""
	if requestedSessionID != uuid.Nil {
		bufSessionID = requestedSessionID.String()
	}

	// Resume path: replay what the client missed instead of re-running the LLM.
	// The original generation goroutine keeps buffering after a disconnect, so the
	// buffer holds events produced while the client was gone. On a buffer miss
	// (evicted/unknown) we fall through to a fresh generation (session_id honored).
	if h.resumeBuf != nil && cc.ResumeToken != "" && bufSessionID != "" {
		if events, found := h.resumeBuf.Replay(bufSessionID, cc.ResumeToken); found {
			llmCancel()
			for _, ev := range events {
				resp, mErr := h.mapEventToProto(ev, userID)
				if mErr != nil {
					continue
				}
				if sErr := stream.Send(resp); sErr != nil {
					return nil
				}
			}
			h.logger.Info("resumed stream from buffer", "session_id", bufSessionID, "replayed", len(events))
			return nil
		}
	}

	// appendEvent records live events for future resume, capturing the minted
	// session id from the start event when we didn't already know it.
	appendEvent := func(ev locitypes.StreamEvent) {
		if h.resumeBuf == nil {
			return
		}
		if bufSessionID == "" && ev.Type == locitypes.EventTypeStart {
			var sd locitypes.StreamStartData
			if decodeData(ev.Data, &sd) && sd.SessionID != "" {
				bufSessionID = sd.SessionID
			}
		}
		if bufSessionID != "" {
			h.resumeBuf.Append(bufSessionID, ev)
		}
	}

	go func() {
		defer func() {
			llmCancel()
			close(eventCh)
		}()
		err := h.service.ProcessUnifiedChatMessageStream(cc)
		if err != nil {
			select {
			case eventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: err.Error()}:
			case <-llmCtx.Done():
			}
		}
	}()

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				h.logger.Info("Event channel closed, stream finished successfully")
				return nil
			}

			appendEvent(event)

			resp, err := h.mapEventToProto(event, userID)
			if err != nil {
				h.logger.Error("Failed to map event", "error", err)
				continue
			}

			if err := stream.Send(resp); err != nil {
				// Client disconnected - let goroutine finish processing
				h.logger.Info("Client disconnected during streaming, LLM processing continues in background",
					"error", err)
				// Keep buffering the rest so a reconnect can resume from the buffer.
				go func() {
					for ev := range eventCh {
						appendEvent(ev)
					}
				}()
				return nil
			}

			if event.Type == locitypes.EventTypeComplete || event.Type == locitypes.EventTypeError {
				h.logger.Info("Stream completed", "event_type", event.Type)
				return nil
			}

		case <-ctx.Done():
			// RPC context cancelled (client disconnected) but LLM processing continues
			h.logger.Info("Client disconnected, LLM processing continues in background")
			// Keep buffering so a reconnect can resume from the buffer.
			go func() {
				for ev := range eventCh {
					appendEvent(ev)
				}
			}()
			return nil
		}
	}
}

// toConnectError converts an error to a Connect error.
func (h *ChatHandler) toConnectError(err error) error {
	switch {
	case errors.Is(err, common.ErrChatNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, common.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, common.ErrInvalidInput):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, common.ErrUnauthorized):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, common.ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, common.ErrInvalidUUID):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, common.ErrItineraryNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// mapEventToProto translates an internal StreamEvent onto the typed proto
// StreamEvent (event_type enum + payload oneof). It decodes event.Data through a
// JSON round-trip so both the typed payload structs and legacy map shapes map
// cleanly onto the concrete payload for each event type.
func (h *ChatHandler) mapEventToProto(event locitypes.StreamEvent, userID uuid.UUID) (*chatv1.StreamEvent, error) {
	resp := &chatv1.StreamEvent{
		Message:   event.Message,
		Timestamp: timestamppb.New(event.Timestamp),
		EventId:   event.EventID,
		IsFinal:   event.IsFinal,
		EventType: eventTypeToProto(event.Type),
	}

	if event.Navigation != nil {
		resp.Navigation = &chatv1.NavigationData{
			Url:         event.Navigation.URL,
			RouteType:   event.Navigation.RouteType,
			QueryParams: event.Navigation.QueryParams,
		}
	}

	switch event.Type {
	case locitypes.EventTypeError:
		resp.Payload = &chatv1.StreamEvent_Error{Error: streamErrorFromEvent(event)}

	case locitypes.EventTypeComplete:
		cp := &chatv1.CompletePayload{}
		var cr locitypes.AiCityResponse
		if decodeData(event.Data, &cr) && cr.SessionID != uuid.Nil {
			cp.SessionId = cr.SessionID.String()
			cp.Result = presenter.ToAiCityResponse(&cr)
		}
		resp.Payload = &chatv1.StreamEvent_Complete{Complete: cp}

	case locitypes.EventTypeStart:
		var sd locitypes.StreamStartData
		decodeData(event.Data, &sd)
		resp.Payload = &chatv1.StreamEvent_Start{Start: &chatv1.StartPayload{
			SessionId: sd.SessionID,
			Domain:    domainToProto(sd.Domain),
			CityName:  optString(sd.City),
		}}

	case locitypes.EventTypeItinerary:
		var cr locitypes.AiCityResponse
		if !decodeData(event.Data, &cr) {
			return nil, fmt.Errorf("itinerary event %q: undecodable data", event.EventID)
		}
		cityResponse := presenter.ToAiCityResponse(&cr)
		attributeCityResponse(cityResponse, event, userID)
		resp.Payload = &chatv1.StreamEvent_Itinerary{Itinerary: &chatv1.ItineraryPayload{CityResponse: cityResponse}}

	case locitypes.EventTypeCityData:
		var gcd locitypes.GeneralCityData
		decodeData(event.Data, &gcd)
		resp.Payload = &chatv1.StreamEvent_CityData{CityData: &chatv1.CityDataPayload{
			GeneralCityData: presenter.ToGeneralCityData(gcd),
		}}

	case locitypes.EventTypeHotels:
		pois, gcd, sid := decodeDomainList(event.Data)
		attributePOIs(pois, event, userID)
		resp.Payload = &chatv1.StreamEvent_Hotels{Hotels: &chatv1.HotelsPayload{
			Pois: pois, GeneralCityData: gcd, SessionId: sid,
		}}

	case locitypes.EventTypeRestaurants:
		pois, gcd, sid := decodeDomainList(event.Data)
		attributePOIs(pois, event, userID)
		resp.Payload = &chatv1.StreamEvent_Restaurants{Restaurants: &chatv1.RestaurantsPayload{
			Pois: pois, GeneralCityData: gcd, SessionId: sid,
		}}

	case "activities":
		pois, gcd, sid := decodeDomainList(event.Data)
		attributePOIs(pois, event, userID)
		resp.Payload = &chatv1.StreamEvent_Activities{Activities: &chatv1.ActivitiesPayload{
			Activities: pois, GeneralCityData: gcd, SessionId: sid,
		}}

	case "nearby", locitypes.EventTypeGeneralPOI, locitypes.EventTypePersonalizedPOI:
		pois, gcd, sid := decodeDomainList(event.Data)
		attributePOIs(pois, event, userID)
		resp.Payload = &chatv1.StreamEvent_GeneralPois{GeneralPois: &chatv1.GeneralPoisPayload{
			Pois: pois, GeneralCityData: gcd, SessionId: sid,
		}}

	case "poi_detail_complete":
		var poi locitypes.POIDetailedInfo
		if decodeData(event.Data, &poi) {
			pois := presenter.ToPOIDetailedInfoSlice([]locitypes.POIDetailedInfo{poi})
			attributePOIs(pois, event, userID)
			resp.Payload = &chatv1.StreamEvent_GeneralPois{GeneralPois: &chatv1.GeneralPoisPayload{
				Pois: pois,
			}}
		} else {
			resp.Payload = &chatv1.StreamEvent_Progress{Progress: progressPayload(event)}
		}

	case locitypes.EventTypeChunk, "poi_detail_chunk":
		var cd locitypes.StreamChunkData
		decodeData(event.Data, &cd)
		resp.Payload = &chatv1.StreamEvent_Token{Token: &chatv1.TokenPayload{Text: cd.Text}}

	default:
		// progress + developer/status events (session_validated, intent_classified,
		// domain_detected, prompt_generated, parsing_response, …) collapse to progress.
		resp.Payload = &chatv1.StreamEvent_Progress{Progress: progressPayload(event)}
	}

	return resp, nil
}

func attributeCityResponse(response *chatv1.AiCityResponse, event locitypes.StreamEvent, userID uuid.UUID) {
	if response == nil {
		return
	}
	attributePOIs(response.GetPointsOfInterest(), event, userID)
	if itinerary := response.GetItineraryResponse(); itinerary != nil {
		attributePOIs(itinerary.GetPointsOfInterest(), event, userID)
		attributePOIs(itinerary.GetRestaurants(), event, userID)
		attributePOIs(itinerary.GetBars(), event, userID)
	}
}

func attributePOIs(pois []*poiv1.POIDetailedInfo, event locitypes.StreamEvent, userID uuid.UUID) {
	if event.EventID == "" {
		return
	}
	surface, algorithm := attributionForEvent(event.Type)
	variant := preference.ExperimentVariant(userID)
	for rank, poi := range pois {
		if poi == nil || poi.GetId() == "" || poi.GetId() == uuid.Nil.String() {
			continue
		}
		poi.RecommendationTrace = &recommendationv1.RecommendationTrace{
			RunId:             event.EventID,
			ItemId:            poi.GetId(),
			Rank:              int32(rank),
			AlgorithmVersion:  algorithm,
			ExperimentVariant: variant,
			Surface:           surface,
			Channel:           recommendationv1.RecommendationChannel_RECOMMENDATION_CHANNEL_WEB,
		}
	}
}

func attributionForEvent(eventType string) (recommendationv1.RecommendationSurface, string) {
	switch eventType {
	case "nearby":
		return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_NEARBY, "nearby-hybrid-v1"
	case locitypes.EventTypeItinerary:
		return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_TRIP, "itinerary-gemini-v1"
	case "poi_detail_complete":
		return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_PLACE, "place-detail-v1"
	default:
		return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER, "discover-gemini-v1"
	}
}

// decodeData re-encodes a stream event's Data (a typed struct or a legacy map)
// into target via JSON. Best-effort: false if Data is nil or does not decode.
func decodeData(data any, target any) bool {
	if data == nil {
		return false
	}
	b, err := json.Marshal(data)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, target) == nil
}

// decodeDomainList decodes a StreamDomainListData payload into proto POIs +
// city context, tolerating the legacy map shape.
func decodeDomainList(data any) ([]*poiv1.POIDetailedInfo, *cityv1.GeneralCityData, string) {
	var d locitypes.StreamDomainListData
	decodeData(data, &d)
	return presenter.ToPOIDetailedInfoSlice(d.POIs), presenter.ToGeneralCityData(d.GeneralCityData), d.SessionID
}

func eventTypeToProto(t string) chatv1.StreamEventType {
	switch t {
	case locitypes.EventTypeStart:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_START
	case locitypes.EventTypeChunk, "poi_detail_chunk":
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_TOKEN
	case locitypes.EventTypeCityData:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_CITY_DATA
	case locitypes.EventTypeItinerary:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_ITINERARY
	case locitypes.EventTypeHotels:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_HOTELS
	case locitypes.EventTypeRestaurants:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_RESTAURANTS
	case "activities":
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_ACTIVITIES
	case "nearby", locitypes.EventTypeGeneralPOI, locitypes.EventTypePersonalizedPOI, "poi_detail_complete":
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_GENERAL_POIS
	case locitypes.EventTypeError:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR
	case locitypes.EventTypeComplete:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_COMPLETE
	default:
		return chatv1.StreamEventType_STREAM_EVENT_TYPE_PROGRESS
	}
}

func domainToProto(d string) chatv1.DomainType {
	switch strings.ToLower(d) {
	case "accommodation":
		return chatv1.DomainType_DOMAIN_TYPE_ACCOMMODATION
	case "dining":
		return chatv1.DomainType_DOMAIN_TYPE_DINING
	case "activities":
		return chatv1.DomainType_DOMAIN_TYPE_ACTIVITIES
	case "itinerary":
		return chatv1.DomainType_DOMAIN_TYPE_ITINERARY
	case "transport":
		return chatv1.DomainType_DOMAIN_TYPE_TRANSPORT
	case "general", "nearby":
		return chatv1.DomainType_DOMAIN_TYPE_GENERAL
	default:
		return chatv1.DomainType_DOMAIN_TYPE_UNSPECIFIED
	}
}

// progressPayload builds a ProgressPayload, preferring a "status" string from a
// map-shaped Data when present, else falling back to the event type.
func progressPayload(event locitypes.StreamEvent) *chatv1.ProgressPayload {
	stage := event.Type
	var m map[string]any
	if decodeData(event.Data, &m) {
		if s, ok := m["status"].(string); ok && s != "" {
			stage = s
		}
	}
	if stage == "" {
		stage = "progress"
	}
	return &chatv1.ProgressPayload{Stage: stage}
}

// streamErrorFromEvent maps an error event onto a typed StreamError, classifying
// capacity/quota conditions into retry hints.
func streamErrorFromEvent(event locitypes.StreamEvent) *chatv1.StreamError {
	msg := event.Error
	if msg == "" {
		msg = event.Message
	}
	if msg == "" {
		msg = "An error occurred while processing your request."
	}
	se := &chatv1.StreamError{
		UserMessage:  msg,
		InternalCode: "stream_error",
		Retryable:    false,
	}
	switch lower := strings.ToLower(msg); {
	case strings.Contains(lower, "high traffic"), strings.Contains(lower, "capacity"):
		se.InternalCode = "capacity"
		se.Retryable = true
		ra := int32(5000)
		se.RetryAfterMs = &ra
	case strings.Contains(lower, "quota"), strings.Contains(lower, "rate limit"):
		se.InternalCode = "quota_exceeded"
		se.Retryable = true
	}
	return se
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (h *ChatHandler) ContinueChat(
	ctx context.Context,
	req *connect.Request[chatv1.ContinueChatRequest],
) (*connect.Response[chatv1.ChatResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session ID"))
	}

	resp, err := h.service.ContinueChat(ctx, userID, sessionID, req.Msg.GetMessage(), req.Msg.GetCityName())
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.ToChatResponse(resp)), nil
}

func (h *ChatHandler) GetChatSession(
	ctx context.Context,
	req *connect.Request[chatv1.GetChatSessionRequest],
) (*connect.Response[chatv1.GetChatSessionResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session ID"))
	}

	session, err := h.service.GetChatSession(ctx, userID, sessionID)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(&chatv1.GetChatSessionResponse{
		Session: presenter.ToChatSession(session),
	}), nil
}

func (h *ChatHandler) GetChatSessions(
	ctx context.Context,
	req *connect.Request[chatv1.GetChatSessionsRequest],
) (*connect.Response[chatv1.GetChatSessionsResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	page := int(req.Msg.GetPagination().GetPage())
	limit := int(req.Msg.GetPagination().GetPageSize())
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 10
	}

	result, err := h.service.GetUserChatSessions(ctx, userID, page, limit)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.ToGetChatSessionsResponse(result)), nil
}

func (h *ChatHandler) GetRecentInteractions(
	ctx context.Context,
	req *connect.Request[chatv1.GetRecentInteractionsRequest],
) (*connect.Response[chatv1.GetRecentInteractionsResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	resp, err := h.service.GetRecentInteractions(ctx, userID, req.Msg.GetPagination())
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(resp), nil
}

func (h *ChatHandler) EndSession(
	ctx context.Context,
	req *connect.Request[chatv1.GetChatSessionRequest],
) (*connect.Response[commonpb.Response], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session ID"))
	}

	if err := h.service.EndSession(ctx, userID, sessionID); err != nil {
		return nil, h.toConnectError(err)
	}

	msg := "session ended"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}
