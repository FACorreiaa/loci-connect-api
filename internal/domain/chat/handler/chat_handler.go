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

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat/chatconnect"
	cityv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/city"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recommendation"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/resumebuf"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// ChatHandler implements the ChatServiceHandler interface.
type ChatHandler struct {
	chatconnect.UnimplementedChatServiceHandler
	service     service.LlmInteractiontService
	logger      *slog.Logger
	resumeBuf   *resumebuf.Buffer
	issuer      AttributionIssuer
	runs        runs.Store
	onRunFinish runs.FinishListener
}

// AttributionIssuer persists the exact traces emitted to an authenticated user.
type AttributionIssuer interface {
	IssueTraces(context.Context, uuid.UUID, []*recommendationv1.RecommendationTrace) error
}

// NewChatHandler creates a new ChatHandler.
func NewChatHandler(llmInteractionService service.LlmInteractiontService, logger *slog.Logger, issuer AttributionIssuer) *ChatHandler {
	return &ChatHandler{
		resumeBuf: resumebuf.New(),
		service:   llmInteractionService,
		logger:    logger,
		issuer:    issuer,
	}
}

// WithRuns records each generation and announces its end to onFinish.
func (h *ChatHandler) WithRuns(store runs.Store, onFinish runs.FinishListener) *ChatHandler {
	h.runs = store
	h.onRunFinish = onFinish
	return h
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

	// Resume path: replay what the client missed, then follow the run live until
	// its terminal event instead of re-running the LLM. The original generation
	// goroutine keeps buffering after a disconnect, so the buffer holds events
	// produced while the client was gone and keeps receiving new ones. With no
	// buffer (evicted, or another pod) a finished run points the client at its
	// stored result; otherwise we fall through to a fresh generation
	// (session_id honored).
	if cc.ResumeToken != "" && bufSessionID != "" {
		if h.resumeBuf != nil {
			if handled, rErr := h.followResume(ctx, stream, userID, requestedSessionID, bufSessionID, cc.ResumeToken); handled {
				llmCancel()
				return rErr
			}
		}
		if run, found := h.finishedRun(ctx, userID, requestedSessionID); found {
			llmCancel()
			return h.sendLoadFromSession(stream, run)
		}
	}

	var tracker *runs.Tracker
	if res, ok := runs.ReservationFrom(ctx); ok && h.runs != nil {
		res.Claim()
		tracker = runs.NewTracker(h.runs, res.RunID, h.onRunFinish, h.logger)
	}

	// record observes each live event for the run tracker, then buffers it for
	// future resume, capturing the minted session id from the start event when
	// we didn't already know it.
	record := func(ev locitypes.StreamEvent) {
		tracker.Observe(ev)
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
		// Registered after the cancel/close defer so it runs first (LIFO):
		// a panic anywhere in the pipeline becomes a terminal error event for
		// this stream instead of taking the whole process down.
		defer func() {
			if r := recover(); r != nil {
				h.logger.Error("chat stream pipeline panicked", "recover", r)
				select {
				case eventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "internal error"}:
				case <-llmCtx.Done():
				}
			}
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
				tracker.Close()
				return nil
			}

			record(event)

			resp, err := h.mapEventToProto(ctx, event, userID)
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
						record(ev)
					}
					tracker.Close()
				}()
				return nil
			}

			if event.Type == locitypes.EventTypeComplete || event.Type == locitypes.EventTypeError {
				h.logger.Info("Stream completed", "event_type", event.Type)
				// The pipeline goroutine still closes eventCh after this; keep
				// draining so any trailing events are recorded and the tracker
				// closes (a no-op if the terminal event already finished it).
				go func() {
					for ev := range eventCh {
						record(ev)
					}
					tracker.Close()
				}()
				return nil
			}

		case <-ctx.Done():
			// RPC context cancelled (client disconnected) but LLM processing continues
			h.logger.Info("Client disconnected, LLM processing continues in background")
			// Keep buffering so a reconnect can resume from the buffer.
			go func() {
				for ev := range eventCh {
					record(ev)
				}
				tracker.Close()
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

// maxResubscribes bounds how often one resume re-attaches to the buffer after
// its follower was closed without a terminal event (it fell behind, or the
// session was dropped), so a pathological run cannot spin the handler.
const maxResubscribes = 3

// followResume replays a session's buffered events after token and follows the
// run live until a terminal event reaches the client. handled is false when the
// buffer does not know the session, so the caller can try the run store.
//
// A live channel that closes before a COMPLETE or ERROR was sent means the
// follower was cut off, not that the run ended: re-subscribe from the last event
// actually sent. If the session has left the buffer, answer from the run store,
// and if that cannot say the run is over, tell the client to retry.
func (h *ChatHandler) followResume(
	ctx context.Context,
	stream *connect.ServerStream[chatv1.StreamEvent],
	userID, sessionID uuid.UUID,
	bufSessionID, token string,
) (handled bool, err error) {
	backlog, live, cancel, found := h.resumeBuf.Subscribe(bufSessionID, token)
	if !found {
		return false, nil
	}
	defer func() { cancel() }()

	lastSentID := token
	sentTerminal := false
	// send reports false once the client is gone. An event that fails to map
	// counts as not sent, so a lost terminal event still reaches the fallback.
	send := func(ev locitypes.StreamEvent) bool {
		resp, mErr := h.mapEventToProto(ctx, ev, userID)
		if mErr != nil {
			return true
		}
		if stream.Send(resp) != nil {
			return false
		}
		if ev.EventID != "" {
			lastSentID = ev.EventID
		}
		if ev.Type == locitypes.EventTypeComplete || ev.Type == locitypes.EventTypeError {
			sentTerminal = true
		}
		return true
	}

	for attempt := 0; ; attempt++ {
		for _, ev := range backlog {
			if !send(ev) {
				return true, nil
			}
		}
	follow:
		for {
			select {
			case ev, open := <-live:
				if !open {
					break follow
				}
				if !send(ev) {
					return true, nil
				}
			case <-ctx.Done():
				return true, nil
			}
		}
		cancel()
		if sentTerminal {
			h.logger.Info("resumed stream to its end", "session_id", bufSessionID, "resubscribes", attempt)
			return true, nil
		}
		if attempt >= maxResubscribes {
			h.logger.Warn("resume kept losing its follower; giving up", "session_id", bufSessionID, "last_event_id", lastSentID)
			break
		}
		h.logger.Info("resume follower closed before the run ended; resubscribing",
			"session_id", bufSessionID, "last_event_id", lastSentID)
		backlog, live, cancel, found = h.resumeBuf.Subscribe(bufSessionID, lastSentID)
		if !found {
			break
		}
	}

	if run, ok := h.finishedRun(ctx, userID, sessionID); ok {
		return true, h.sendLoadFromSession(stream, run)
	}
	return true, stream.Send(&chatv1.StreamEvent{
		Timestamp: timestamppb.Now(),
		EventId:   uuid.NewString(),
		IsFinal:   true,
		EventType: chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR,
		Payload: &chatv1.StreamEvent_Error{Error: &chatv1.StreamError{
			UserMessage:  "This search is still running. Try again in a moment.",
			InternalCode: "resume_lost",
			Retryable:    true,
		}},
	})
}

// finishedRun looks up the session's latest run and returns it when it has
// ended (done or failed). A lookup error is logged and treated as "unknown".
func (h *ChatHandler) finishedRun(ctx context.Context, userID, sessionID uuid.UUID) (runs.Run, bool) {
	if h.runs == nil || sessionID == uuid.Nil {
		return runs.Run{}, false
	}
	run, found, err := h.runs.FindBySession(ctx, userID, sessionID)
	if err != nil {
		h.logger.Warn("resume: run lookup failed", "session_id", sessionID, "error", err)
		return runs.Run{}, false
	}
	if !found || run.Status == runs.StatusRunning {
		return runs.Run{}, false
	}
	return run, true
}

// sendLoadFromSession ends a resume whose events are gone: the run finished,
// so its result is stored and GetChatSession / GetSessionPOIs can load it.
// Navigation carries the same relative path shape the live complete event does.
func (h *ChatHandler) sendLoadFromSession(stream *connect.ServerStream[chatv1.StreamEvent], run runs.Run) error {
	if run.Status == runs.StatusFailed {
		code := run.ErrorCode
		if code == "" {
			code = "internal" // StreamError.internal_code has min_len 1
		}
		return stream.Send(&chatv1.StreamEvent{
			Timestamp: timestamppb.Now(),
			EventId:   uuid.NewString(),
			IsFinal:   true,
			EventType: chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR,
			Payload: &chatv1.StreamEvent_Error{Error: &chatv1.StreamError{
				UserMessage:  "This search didn't finish. Try it again.",
				InternalCode: code,
				Retryable:    true,
			}},
		})
	}
	path, routeType, query := runs.ResultPath(run.Domain, run.SessionID, run.CityName, uuid.Nil)
	return stream.Send(&chatv1.StreamEvent{
		Timestamp:  timestamppb.Now(),
		EventId:    uuid.NewString(),
		IsFinal:    true,
		EventType:  chatv1.StreamEventType_STREAM_EVENT_TYPE_COMPLETE,
		Navigation: &chatv1.NavigationData{Url: path, RouteType: routeType, QueryParams: query},
		Payload: &chatv1.StreamEvent_Complete{Complete: &chatv1.CompletePayload{
			SessionId:       run.SessionID.String(),
			LoadFromSession: true,
		}},
	})
}

// mapEventToProto translates an internal StreamEvent onto the typed proto
// StreamEvent (event_type enum + payload oneof). It decodes event.Data through a
// JSON round-trip so both the typed payload structs and legacy map shapes map
// cleanly onto the concrete payload for each event type.
func (h *ChatHandler) mapEventToProto(ctx context.Context, event locitypes.StreamEvent, userID uuid.UUID) (*chatv1.StreamEvent, error) {
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
			// The completion event usually carries only navigation data
			// ({session_id, trip_id}); an empty Result would make the client
			// treat a zero-valued struct as the final payload and wipe the
			// data it already rendered.
			if aiCityResponseHasContent(&cr) {
				cp.Result = presenter.ToAiCityResponse(&cr)
			}
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

	if h.issuer != nil {
		traces := recommendationTraces(resp)
		if len(traces) > 0 {
			if err := h.issuer.IssueTraces(ctx, userID, traces); err != nil {
				return nil, fmt.Errorf("issue recommendation attribution: %w", err)
			}
		}
	}
	return resp, nil
}

func recommendationTraces(event *chatv1.StreamEvent) []*recommendationv1.RecommendationTrace {
	if event == nil {
		return nil
	}
	var pois []*poiv1.POIDetailedInfo
	switch {
	case event.GetGeneralPois() != nil:
		pois = event.GetGeneralPois().GetPois()
	case event.GetHotels() != nil:
		pois = event.GetHotels().GetPois()
	case event.GetRestaurants() != nil:
		pois = event.GetRestaurants().GetPois()
	case event.GetActivities() != nil:
		pois = event.GetActivities().GetActivities()
	case event.GetItinerary() != nil:
		response := event.GetItinerary().GetCityResponse()
		if response != nil {
			pois = append(pois, response.GetPointsOfInterest()...)
			if itinerary := response.GetItineraryResponse(); itinerary != nil {
				pois = append(pois, itinerary.GetPointsOfInterest()...)
				pois = append(pois, itinerary.GetRestaurants()...)
				pois = append(pois, itinerary.GetBars()...)
			}
		}
	}
	traces := make([]*recommendationv1.RecommendationTrace, 0, len(pois))
	for _, poi := range pois {
		if poi != nil && poi.GetRecommendationTrace() != nil {
			traces = append(traces, poi.GetRecommendationTrace())
		}
	}
	return traces
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
	variant := preference.ExperimentVariant(userID)
	surface, algorithm := attributionForEvent(event.Type, variant)
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

func attributionForEvent(eventType, variant string) (recommendationv1.RecommendationSurface, string) {
	switch eventType {
	case "nearby":
		return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_NEARBY, "nearby-hybrid-v1"
	case locitypes.EventTypeItinerary:
		if variant != "control" {
			return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_TRIP, "itinerary-preference-rerank-v1"
		}
		return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_TRIP, "itinerary-gemini-v1"
	case "poi_detail_complete":
		return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_PLACE, "place-detail-v1"
	default:
		if variant != "control" {
			return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER, "discover-preference-rerank-v1"
		}
		return recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER, "discover-gemini-v1"
	}
}

// decodeData re-encodes a stream event's Data (a typed struct or a legacy map)
// into target via JSON. Best-effort: false if Data is nil or does not decode.
// aiCityResponseHasContent reports whether a decoded AiCityResponse carries
// anything beyond identifiers, i.e. whether it is worth sending as a result.
func aiCityResponseHasContent(cr *locitypes.AiCityResponse) bool {
	if cr == nil {
		return false
	}
	return cr.GeneralCityData.City != "" ||
		len(cr.PointsOfInterest) > 0 ||
		len(cr.AIItineraryResponse.PointsOfInterest) > 0 ||
		len(cr.Hotels) > 0 ||
		len(cr.Restaurants) > 0 ||
		len(cr.Activities) > 0
}

func decodeData(data, target any) bool {
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
// infrastructureErrorMarkers are the fingerprints of an error that was
// written for an operator, not a person: driver and network failures wrapped
// with %w all the way up to the stream. The chat service emits err.Error()
// from many sites; rather than audit each one, everything is scrubbed here,
// the single point every error event passes through. On 2026-09-10 a Postgres
// restart put the production DB user, database name, host and port on
// people's screens as the "user message".
var infrastructureErrorMarkers = []string{
	"sqlstate", "failed to connect", "user=", "database=", "dial tcp",
	"connection refused", "connection reset", "no such host", "i/o timeout",
	"context deadline exceeded", "context canceled", "begin transaction",
	"tls:", "x509", "eof", "broken pipe", "does not exist", "relation ",
}

const infrastructureUserMessage = "Loci is restarting a service. Try again in a minute."

// looksLikeInfrastructureError reports whether a message carries the kind of
// detail a person cannot act on and an attacker can.
func looksLikeInfrastructureError(msg string) bool {
	lower := strings.ToLower(msg)
	for _, m := range infrastructureErrorMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	// host:port — an address of ours is never a user's business.
	if strings.Contains(lower, ":5432") || strings.Contains(lower, ":6379") || strings.Contains(lower, ":8080") {
		return true
	}
	return false
}

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
	if looksLikeInfrastructureError(msg) {
		se.UserMessage = infrastructureUserMessage
		se.InternalCode = "internal"
		se.Retryable = true
	}
	// Prefer the producer's classification. Text matching stays as a
	// fallback for events emitted without one, but it must not override
	// an explicit code: the prose is user-facing copy and changing a
	// sentence should never change a retry hint.
	switch event.ErrorCode {
	case locitypes.StreamErrorCapacity:
		se.InternalCode = "capacity"
		se.Retryable = true
		ra := int32(5000)
		se.RetryAfterMs = &ra
		return se
	case locitypes.StreamErrorQuotaExceeded:
		se.InternalCode = "quota_exceeded"
		se.Retryable = true
		return se
	case locitypes.StreamErrorProviderUnavailable:
		se.InternalCode = "provider_unavailable"
		se.Retryable = true
		ra := int32(30000)
		se.RetryAfterMs = &ra
		return se
	case locitypes.StreamErrorNoResults:
		se.InternalCode = "no_results"
		se.Retryable = true
		return se
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

// GetRunStatus reports the caller's runs among session_ids.
func (h *ChatHandler) GetRunStatus(
	ctx context.Context,
	req *connect.Request[chatv1.GetRunStatusRequest],
) (*connect.Response[chatv1.GetRunStatusResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	if h.runs == nil {
		return connect.NewResponse(&chatv1.GetRunStatusResponse{}), nil
	}

	ids := make([]uuid.UUID, 0, len(req.Msg.GetSessionIds()))
	for _, s := range req.Msg.GetSessionIds() {
		if id, pErr := uuid.Parse(s); pErr == nil {
			ids = append(ids, id)
		}
	}

	found, err := h.runs.Statuses(ctx, userID, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&chatv1.GetRunStatusResponse{Runs: runs.StatusesToProto(found)}), nil
}
