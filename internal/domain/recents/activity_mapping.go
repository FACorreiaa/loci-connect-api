package recents

import (
	"regexp"
	"strings"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	recentsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recents"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// promptWrapper matches the string the chat service stores as the prompt:
//
//	"Unified Chat Stream - Domain: itinerary, Message: Three days in Porto"
//
// The stored column keeps the wrapper because it is the audit record of what
// was actually sent to the model. The feed shows what the person typed, so the
// wrapper comes off here, on the way out. Anchored at both ends so a message
// that merely quotes the wrapper is left alone.
//
// The client has the same regex in prompt-wrapper.ts and keeps it as a
// defensive second pass; this is the one that matters.
var promptWrapper = regexp.MustCompile(`(?is)^Unified Chat Stream - Domain:\s*\w+,\s*Message:\s*(.+)$`)

// unwrapPrompt returns the user's own words from a stored prompt.
func unwrapPrompt(prompt string) string {
	if m := promptWrapper.FindStringSubmatch(prompt); m != nil {
		return strings.TrimSpace(m[1])
	}
	return prompt
}

// feedKinds is the set of values that name a feed kind rather than a detail.
// The RPC's entity_types filter carries both, mixed together, because the proto
// has one repeated-string field and the feed has two axes; this is what tells
// them apart. See activityFilterFromProto.
var feedKinds = map[string]locitypes.ActivityKind{
	string(locitypes.ActivityKindPrompt):         locitypes.ActivityKindPrompt,
	string(locitypes.ActivityKindSavedItinerary): locitypes.ActivityKindSavedItinerary,
	string(locitypes.ActivityKindFavourite):      locitypes.ActivityKindFavourite,
}

// interactionTypeFor maps a feed entry onto the proto's InteractionType enum.
//
// The enum predates the feed and does not line up with it cleanly, so the
// precise information lives in entity_type and this is the coarse label. A
// chat is a CHAT, a location-bound "what's near me" is a DISCOVERY, and
// everything else the user asked for is a SEARCH.
func interactionTypeFor(e locitypes.ActivityEntry) recentsv1.InteractionType {
	switch e.Kind {
	case locitypes.ActivityKindSavedItinerary:
		return recentsv1.InteractionType_INTERACTION_TYPE_SAVE_ITINERARY
	case locitypes.ActivityKindFavourite:
		return recentsv1.InteractionType_INTERACTION_TYPE_FAVORITE
	case locitypes.ActivityKindPrompt:
		switch locitypes.DomainType(e.Detail) {
		case locitypes.DomainGeneral:
			return recentsv1.InteractionType_INTERACTION_TYPE_CHAT
		case locitypes.DomainNearby:
			return recentsv1.InteractionType_INTERACTION_TYPE_DISCOVERY
		default:
			return recentsv1.InteractionType_INTERACTION_TYPE_SEARCH
		}
	}
	return recentsv1.InteractionType_INTERACTION_TYPE_UNSPECIFIED
}

// activityEntryToProto renders one feed entry as a RecentInteraction.
//
// The field the client navigates with is entity_id: a chat session id for a
// prompt or a saved itinerary, the item id for a favourite. entity_type is the
// feed kind for a save or a favourite and the routed domain for a prompt, which
// is exactly what the client needs to pick a route.
func activityEntryToProto(userID string, e locitypes.ActivityEntry) *recentsv1.RecentInteraction {
	out := &recentsv1.RecentInteraction{
		Id:              e.ID,
		UserId:          userID,
		InteractionType: interactionTypeFor(e),
		EntityId:        e.RefID,
		EntityName:      e.CityName,
		CityId:          e.CityID,
		CityName:        e.CityName,
		Metadata:        map[string]string{"kind": string(e.Kind)},
	}

	switch e.Kind {
	case locitypes.ActivityKindPrompt:
		out.EntityType = e.Detail
		out.Description = unwrapPrompt(e.Label)
	case locitypes.ActivityKindSavedItinerary:
		out.EntityType = string(locitypes.ActivityKindSavedItinerary)
		out.Description = e.Label
	case locitypes.ActivityKindFavourite:
		out.EntityType = string(locitypes.ActivityKindFavourite)
		out.Description = e.Label
		// A favourite's detail is its content type (poi, hotel, restaurant,
		// itinerary). It does not belong in entity_type, which the client
		// filters on, so it travels beside it.
		out.Metadata["content_type"] = e.Detail
	default:
		out.EntityType = e.Detail
		out.Description = e.Label
	}

	if !e.OccurredAt.IsZero() {
		out.CreatedAt = timestamppb.New(e.OccurredAt)
	}

	return out
}

// activityFilterFromProto translates the RPC's filter into the feed's.
//
// entity_types is the precise control: values naming a feed kind ("prompt",
// "saved_itinerary", "favourite") narrow the kind, anything else narrows the
// detail, and the two are combined with AND. So "itinerary searches, not saved
// itineraries" is entity_types = ["prompt", "itinerary"].
//
// interaction_types is the coarse one, kept working because the proto offers
// it: it can only ever narrow the kind, since SEARCH spans five domains.
func activityFilterFromProto(f *recentsv1.InteractionFilter, sortOrder string) locitypes.ActivityFeedFilter {
	out := locitypes.ActivityFeedFilter{Ascending: strings.EqualFold(sortOrder, "asc")}
	if f == nil {
		return out
	}

	for _, et := range f.GetEntityTypes() {
		if et == "" {
			continue
		}
		if _, ok := feedKinds[et]; ok {
			out.Kinds = append(out.Kinds, et)
			continue
		}
		out.Details = append(out.Details, et)
	}

	for _, it := range f.GetInteractionTypes() {
		switch it {
		case recentsv1.InteractionType_INTERACTION_TYPE_SAVE_ITINERARY:
			out.Kinds = append(out.Kinds, string(locitypes.ActivityKindSavedItinerary))
		case recentsv1.InteractionType_INTERACTION_TYPE_FAVORITE:
			out.Kinds = append(out.Kinds, string(locitypes.ActivityKindFavourite))
		case recentsv1.InteractionType_INTERACTION_TYPE_CHAT,
			recentsv1.InteractionType_INTERACTION_TYPE_SEARCH,
			recentsv1.InteractionType_INTERACTION_TYPE_DISCOVERY:
			out.Kinds = append(out.Kinds, string(locitypes.ActivityKindPrompt))
		}
	}
	out.Kinds = dedupe(out.Kinds)

	out.Search = f.GetSearchQuery()
	if f.GetStartDate() != nil {
		out.Since = f.GetStartDate().AsTime()
	}
	if f.GetEndDate() != nil {
		out.Until = f.GetEndDate().AsTime()
	}

	return out
}

func dedupe(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
