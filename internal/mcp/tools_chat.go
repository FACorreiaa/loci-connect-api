package mcp

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

type planItineraryInput struct {
	City    string   `json:"city" jsonschema:"destination city name, e.g. Lisbon"`
	Request string   `json:"request" jsonschema:"what to plan, e.g. '3 relaxed days focused on food and viewpoints'"`
	Lat     *float64 `json:"latitude,omitempty" jsonschema:"optional user latitude for distance-aware suggestions"`
	Lon     *float64 `json:"longitude,omitempty" jsonschema:"optional user longitude"`
}

type planItineraryOutput struct {
	SessionID string `json:"session_id"`
	// Message is the assistant's itinerary text (markdown).
	Message string `json:"message"`
	// Itinerary carries structured POIs when generation produced them.
	Itinerary *locitypes.AiCityResponse `json:"itinerary,omitempty"`
}

// registerChatTools adds the LLM-in-the-loop planning tool. It is Pro-only:
// each call runs a full itinerary generation on Loci's Gemini quota and the
// result is saved to the user's account like a web chat session.
func registerChatTools(server *mcp.Server, deps Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "plan_itinerary",
		Description: "Generate and save a personalized itinerary for a city using Loci's travel AI (Pro plan required). The itinerary is stored in the user's Loci account.",
	}, guardTool(deps, "plan_itinerary", func(ctx context.Context, _ *mcp.CallToolRequest, in planItineraryInput) (*mcp.CallToolResult, planItineraryOutput, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, planItineraryOutput{}, err
		}
		if in.City == "" || in.Request == "" {
			return nil, planItineraryOutput{}, errors.New("both city and request are required")
		}

		plan, err := deps.Subscription.EffectivePlan(ctx, userID)
		if err != nil {
			return nil, planItineraryOutput{}, toolError(err)
		}
		if !subscription.IsProPlan(plan) {
			return nil, planItineraryOutput{}, errors.New("plan_itinerary requires a Loci Pro subscription — upgrade at the Loci pricing page; data tools (search_pois, find_nearby, lists, favorites) remain available on the free plan")
		}

		// Chat generation does not flow through the poi metering choke
		// point, so spend the quota unit here.
		if err := subscription.ConsumeQuotaFromContext(ctx); err != nil {
			return nil, planItineraryOutput{}, toolError(err)
		}

		var loc *locitypes.UserLocation
		if in.Lat != nil && in.Lon != nil {
			loc = &locitypes.UserLocation{UserLat: *in.Lat, UserLon: *in.Lon}
		}

		resp, err := deps.ChatService.StartChat(ctx, userID, uuid.Nil, in.City, in.Request, loc)
		if err != nil {
			return nil, planItineraryOutput{}, toolError(err)
		}
		return nil, planItineraryOutput{
			SessionID: resp.SessionID.String(),
			Message:   resp.Message,
			Itinerary: resp.UpdatedItinerary,
		}, nil
	}))
}
