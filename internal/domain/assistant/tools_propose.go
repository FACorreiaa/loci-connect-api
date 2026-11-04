package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	assistantv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
)

const (
	// placeActionTTL matches a trip card's: after a day it has lost its context.
	placeActionTTL = 24 * time.Hour
	// proposeTripTimeout is longer than a lookup's: adding a stop searches for
	// the place, and that search allows itself eight seconds.
	proposeTripTimeout = 10 * time.Second
	maxHandoffQuery    = 500
)

// The writes a proposal can lead to. A tool only ever stores a proposal: these
// run from ConfirmAction, once the caller has said yes.
type (
	// TripProposer is the trip action service, the same one chat cards use, so
	// a card here gets the same vetting and the same rules on who may do what.
	TripProposer interface {
		ProposeActions(ctx context.Context, userID, tripID, sessionID uuid.UUID, actions []tripaction.Action) ([]tripaction.Proposal, error)
		ApplyCurrent(ctx context.Context, userID, proposalID uuid.UUID, option *int) (*trip.Trip, *locitypes.ConversationMessage, error)
		Applicable(ctx context.Context, userID, proposalID uuid.UUID) (*tripaction.Proposal, error)
		Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error
	}
	FavouriteWriter interface {
		AddPoiToFavourites(ctx context.Context, userID, poiID uuid.UUID, isLLMGenerated bool) (uuid.UUID, error)
	}
	ListWriter interface {
		AddListItem(ctx context.Context, userID, listID uuid.UUID, params locitypes.AddListItemRequest) (*locitypes.ListItem, error)
	}
)

// offer is what a propose tool returns: what to tell the model, and what to put
// in front of the caller. The event is sent once all of a turn's tools are back.
type offer struct {
	reply string
	event *assistantv1.AssistantEvent
}

// tripChangeKinds is what the assistant may put on a card. Hotel searches are
// left out: on a city with no hotels stored, the search generates them with the
// LLM and saves the result, which a tool the model calls must not be able to do.
var tripChangeKinds = map[tripaction.Kind]bool{
	tripaction.KindSetDates:       true,
	tripaction.KindRegenerateDays: true,
	tripaction.KindSearchFlights:  true,
	tripaction.KindAddStop:        true,
	tripaction.KindRemoveStop:     true,
	tripaction.KindReplaceStop:    true,
	tripaction.KindMoveStop:       true,
}

// proposeTools builds the tools that offer changes. None changes what it offers:
// the card it stores does nothing until ConfirmAction.
func proposeTools(src Sources, store Store, now func() time.Time) []tool {
	var tools []tool
	if src.Proposals != nil && src.Members != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name: "propose_trip_change",
				Description: "Show the user a card offering one change to a trip. Nothing changes unless they confirm it. " +
					"Set kind and the fields that kind uses; give the rest as an empty string or 0. " +
					"set_dates: start_date, end_date. regenerate_days: days. " +
					"search_flights: origin, destination, depart, return (empty for one way), passengers, cabin, and IATA codes when sure. " +
					"add_stop: day, place. remove_stop: day, stop_name. replace_stop: day, stop_name, place. move_stop: day, stop_name, to_day. " +
					"Day numbers and stop names come from get_trip.",
				Properties: map[string]any{
					"trip_id": objectProp("string", "The trip id."),
					"kind": map[string]any{
						"type": "string",
						"enum": []string{
							string(tripaction.KindSetDates), string(tripaction.KindRegenerateDays), string(tripaction.KindSearchFlights),
							string(tripaction.KindAddStop), string(tripaction.KindRemoveStop), string(tripaction.KindReplaceStop), string(tripaction.KindMoveStop),
						},
						"description": "Which change.",
					},
					"start_date":       objectProp("string", "YYYY-MM-DD."),
					"end_date":         objectProp("string", "YYYY-MM-DD."),
					"days":             objectProp("integer", "How many days to re-plan the trip as."),
					"day":              objectProp("integer", "The day number the stop is on, or goes on."),
					"stop_name":        objectProp("string", "A stop of that day, as get_trip lists it."),
					"place":            objectProp("string", "The place or kind of place to add, in the user's words."),
					"to_day":           objectProp("integer", "The day number to move the stop to."),
					"origin":           objectProp("string", "Where the flight leaves from."),
					"origin_iata":      objectProp("string", "The origin's IATA code, or empty."),
					"destination":      objectProp("string", "Where the flight goes."),
					"destination_iata": objectProp("string", "The destination's IATA code, or empty."),
					"depart":           objectProp("string", "YYYY-MM-DD."),
					"return":           objectProp("string", "YYYY-MM-DD, or empty for one way."),
					"passengers":       objectProp("integer", "How many are flying."),
					"cabin":            objectProp("string", "economy, premium_economy, business or first."),
				},
				Required: []string{
					"trip_id", "kind", "start_date", "end_date", "days", "day", "stop_name", "place", "to_day",
					"origin", "origin_iata", "destination", "destination_iata", "depart", "return", "passengers", "cabin",
				},
			},
			status:  "Preparing that change",
			offers:  true,
			timeout: proposeTripTimeout,
			run:     src.proposeTripChange,
		})
	}
	if src.POIs != nil && src.Favourites != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name:        "propose_save_place",
				Description: "Show the user a card offering to save a place to their favourites. Nothing is saved unless they confirm it. Takes a place id from another tool or the context.",
				Properties:  map[string]any{"poi_id": objectProp("string", "The place id.")},
				Required:    []string{"poi_id"},
			},
			status: "Preparing that",
			offers: true,
			run: func(ctx context.Context, userID uuid.UUID, input json.RawMessage) (any, error) {
				return src.proposeSavePlace(ctx, userID, input, store, now())
			},
		})
	}
	if src.POIs != nil && src.Lists != nil && src.ListItems != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name:        "propose_add_to_list",
				Description: "Show the user a card offering to add a place to one of their lists. Nothing is added unless they confirm it. Takes a place id, and a list id from list_user_lists or the context.",
				Properties: map[string]any{
					"poi_id":  objectProp("string", "The place id."),
					"list_id": objectProp("string", "The list id."),
				},
				Required: []string{"poi_id", "list_id"},
			},
			status: "Preparing that",
			offers: true,
			run: func(ctx context.Context, userID uuid.UUID, input json.RawMessage) (any, error) {
				return src.proposeAddToList(ctx, userID, input, store, now())
			},
		})
	}
	tools = append(tools, tool{
		def: anthropic.ToolDef{
			Name:        "handoff_to_planner",
			Description: "Offer to plan something in the full planner, when they ask for a new trip or itinerary. Nothing starts unless they tap the offer. Do not plan it yourself.",
			Properties: map[string]any{
				"query": objectProp("string", "What to plan, in the user's words."),
				"city":  objectProp("string", "The city, or empty if there isn't one yet."),
			},
			Required: []string{"query", "city"},
		},
		offers: true,
		run:    handoffToPlanner,
	})
	return tools
}

type tripChangeInput struct {
	TripID          string `json:"trip_id"`
	Kind            string `json:"kind"`
	StartDate       string `json:"start_date"`
	EndDate         string `json:"end_date"`
	Days            int    `json:"days"`
	Day             int    `json:"day"`
	StopName        string `json:"stop_name"`
	Place           string `json:"place"`
	ToDay           int    `json:"to_day"`
	Origin          string `json:"origin"`
	OriginIATA      string `json:"origin_iata"`
	Destination     string `json:"destination"`
	DestinationIATA string `json:"destination_iata"`
	Depart          string `json:"depart"`
	Return          string `json:"return"`
	Passengers      int    `json:"passengers"`
	Cabin           string `json:"cabin"`
}

func (in tripChangeInput) action() tripaction.Action {
	return tripaction.Action{
		Kind:      tripaction.Kind(in.Kind),
		StartDate: in.StartDate, EndDate: in.EndDate,
		Days: in.Days,
		Day:  in.Day, StopName: in.StopName, PlaceQuery: in.Place, ToDay: in.ToDay,
		Origin:      tripaction.Place{Name: in.Origin, IATA: in.OriginIATA},
		Destination: tripaction.Place{Name: in.Destination, IATA: in.DestinationIATA},
		Depart:      in.Depart, Return: in.Return, Passengers: in.Passengers, Cabin: in.Cabin,
	}
}

func (s Sources) proposeTripChange(ctx context.Context, userID uuid.UUID, input json.RawMessage) (any, error) {
	in, err := parseInput[tripChangeInput](input)
	if err != nil {
		return nil, err
	}
	if !tripChangeKinds[tripaction.Kind(in.Kind)] {
		return nil, modelError("that kind of change cannot be offered here")
	}
	tripID, err := parseID(in.TripID)
	if err != nil {
		return nil, err
	}
	t, role, err := s.readableTrip(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}
	if !trip.CanEdit(userID, t.UserID, role, false) {
		return nil, modelError("they can only view this trip, so they cannot change it")
	}
	// No session: a card from here belongs to no chat, and the confirmation
	// comes back on ConfirmAction rather than being posted to a thread.
	proposals, err := s.Proposals.ProposeActions(ctx, userID, tripID, uuid.Nil, []tripaction.Action{in.action()})
	if err != nil {
		return nil, err
	}
	if len(proposals) == 0 {
		return nil, modelError("that change could not be offered: check the day numbers, stop names and dates against get_trip, and note a multi-city trip cannot be re-planned")
	}
	p := proposals[0]
	awaitsOwner := t.UserID != userID
	reply := fmt.Sprintf("A card is on screen: %q. Nothing has changed: say in one short sentence that it is waiting for them to confirm.", p.Summary)
	if awaitsOwner {
		reply = fmt.Sprintf("This went to the trip's owner as a suggestion: %q. Nothing has changed: say in one short sentence that the owner will decide.", p.Summary)
	}
	return offer{reply: reply, event: &assistantv1.AssistantEvent{Event: &assistantv1.AssistantEvent_Proposal{
		Proposal: &assistantv1.Proposal{
			Kind:        &assistantv1.Proposal_Trip{Trip: tripaction.ToProto(&p)},
			AwaitsOwner: awaitsOwner,
		},
	}}}, nil
}

// knownPOI is the place an id names, by the name the database has for it. The
// model's word for a place never reaches a card.
func (s Sources) knownPOI(ctx context.Context, raw string) (*locitypes.POIDetailedInfo, error) {
	id, err := parseID(raw)
	if err != nil {
		return nil, err
	}
	p, err := s.POIs.GetPOI(ctx, id)
	if err != nil || p == nil {
		return nil, modelError("no place with that id")
	}
	return p, nil
}

func (s Sources) proposeSavePlace(ctx context.Context, userID uuid.UUID, input json.RawMessage, store Store, now time.Time) (any, error) {
	in, err := parseInput[struct {
		POIID string `json:"poi_id"`
	}](input)
	if err != nil {
		return nil, err
	}
	p, err := s.knownPOI(ctx, in.POIID)
	if err != nil {
		return nil, err
	}
	return offerPlace(ctx, store, &PlaceAction{
		UserID: userID, Kind: placeKindFavourite, POIID: p.ID, POIName: p.Name,
		Summary:   fmt.Sprintf("Save %s to your favourites", p.Name),
		ExpiresAt: now.Add(placeActionTTL),
	})
}

func (s Sources) proposeAddToList(ctx context.Context, userID uuid.UUID, input json.RawMessage, store Store, now time.Time) (any, error) {
	in, err := parseInput[struct {
		POIID  string `json:"poi_id"`
		ListID string `json:"list_id"`
	}](input)
	if err != nil {
		return nil, err
	}
	p, err := s.knownPOI(ctx, in.POIID)
	if err != nil {
		return nil, err
	}
	listID, err := parseID(in.ListID)
	if err != nil {
		return nil, err
	}
	// GetListDetails also opens other people's public lists; those can be read
	// but not added to.
	l, err := s.Lists.GetListDetails(ctx, listID, userID)
	if err != nil || l == nil || l.List.UserID != userID {
		return nil, modelError("no list of theirs with that id")
	}
	for _, item := range l.Items {
		if item != nil && item.ItemID == p.ID {
			return nil, modelError(p.Name + " is already on that list")
		}
	}
	name := l.List.Name
	return offerPlace(ctx, store, &PlaceAction{
		UserID: userID, Kind: placeKindList, POIID: p.ID, POIName: p.Name, ListID: &listID, ListName: &name,
		Summary:   fmt.Sprintf("Add %s to %s", p.Name, name),
		ExpiresAt: now.Add(placeActionTTL),
	})
}

func offerPlace(ctx context.Context, store Store, a *PlaceAction) (any, error) {
	if err := store.CreatePlaceAction(ctx, a); err != nil {
		return nil, err
	}
	kind := assistantv1.PlaceActionKind_PLACE_ACTION_KIND_SAVE_FAVORITE
	if a.Kind == placeKindList {
		kind = assistantv1.PlaceActionKind_PLACE_ACTION_KIND_ADD_TO_LIST
	}
	card := &assistantv1.PlaceAction{
		Id: a.ID.String(), Kind: kind, PoiId: a.POIID.String(), PoiName: a.POIName,
		Summary: a.Summary, ExpiresAt: timestamppb.New(a.ExpiresAt),
	}
	if a.ListID != nil {
		id := a.ListID.String()
		card.ListId, card.ListName = &id, a.ListName
	}
	return offer{
		reply: fmt.Sprintf("A card is on screen: %q. Nothing has been saved: say in one short sentence that it is waiting for them to confirm.", a.Summary),
		event: &assistantv1.AssistantEvent{Event: &assistantv1.AssistantEvent_Proposal{
			Proposal: &assistantv1.Proposal{Kind: &assistantv1.Proposal_Place{Place: card}},
		}},
	}, nil
}

func handoffToPlanner(_ context.Context, _ uuid.UUID, input json.RawMessage) (any, error) {
	in, err := parseInput[struct {
		Query string `json:"query"`
		City  string `json:"city"`
	}](input)
	if err != nil {
		return nil, err
	}
	query := clip(strings.TrimSpace(in.Query), maxHandoffQuery)
	if query == "" {
		return nil, modelError("say what to plan")
	}
	handoff := &assistantv1.Handoff{Query: query}
	if city := strings.TrimSpace(in.City); city != "" {
		handoff.City = &city
	}
	return offer{
		reply: "The offer is on screen. Say in one short sentence that they can tap it to start planning. Do not plan it here.",
		event: &assistantv1.AssistantEvent{Event: &assistantv1.AssistantEvent_Handoff{Handoff: handoff}},
	}, nil
}
