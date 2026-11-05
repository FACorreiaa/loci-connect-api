package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/memory"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/social"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/travelhistory"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
)

const (
	defaultListLimit   = 10
	maxListLimit       = 25
	searchRadiusKm     = 10
	searchResultLimit  = 10
	searchSemanticBias = 0.6
	maxDescriptionLen  = 240
)

// What the read tools read. Each is the narrowest thing that does the job, and
// every one that reaches for personal data takes the caller's id, which comes
// from the request and never from the model: no tool schema has a user field.
type (
	TripLister interface {
		ListTrips(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*trip.Trip, int, error)
	}
	// TripResolver opens a trip the caller may see: their own, or one they are
	// a member of. It is what authorises a trip id, from the model or a client.
	TripResolver interface {
		Resolve(ctx context.Context, viewer, tripID uuid.UUID) (*trip.Trip, trip.Role, error)
	}
	ListReader interface {
		GetUserLists(ctx context.Context, userID uuid.UUID, isItinerary bool) ([]*locitypes.List, error)
		GetListDetails(ctx context.Context, listID, userID uuid.UUID) (*locitypes.ListWithItems, error)
	}
	POIReader interface {
		GetFavouritePOIsByUserID(ctx context.Context, userID uuid.UUID) ([]locitypes.POIDetailedInfo, error)
		GetPOI(ctx context.Context, id uuid.UUID) (*locitypes.POIDetailedInfo, error)
		SearchPOIs(ctx context.Context, filter locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error)
		SearchPOIsFused(ctx context.Context, filter locitypes.POIFilter, query string, semanticWeight float64) ([]poi.SearchResult, error)
	}
	MemoryReader interface {
		Get(ctx context.Context, userID uuid.UUID, includeEvidence bool) (*memory.Profile, error)
	}
	FriendLister interface {
		ListFriends(ctx context.Context, userID uuid.UUID) ([]social.Friend, error)
	}
	VisitLister interface {
		ListVisitedCities(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*travelhistory.VisitedCity, int, error)
	}
	ActivityReader interface {
		GetUserActivityFeed(ctx context.Context, userID uuid.UUID, limit, offset int, filter locitypes.ActivityFeedFilter) ([]locitypes.ActivityEntry, bool, error)
	}
)

// Sources is everything the read tools draw on. A nil source drops its tools,
// so the model is never offered something that cannot answer.
type Sources struct {
	Trips    TripLister
	Members  TripResolver
	Lists    ListReader
	POIs     POIReader
	Memory   MemoryReader
	Friends  FriendLister
	Visits   VisitLister
	Activity ActivityReader

	// What the propose tools and ConfirmAction use. Nil drops the tools that need it.
	Proposals  TripProposer
	Favourites FavouriteWriter
	ListItems  ListWriter
}

// modelError is a failure worth telling the model, in words it can act on. Any
// other error stays in the logs: it is neither the model's business nor
// something to read aloud.
type modelError string

func (e modelError) Error() string { return string(e) }

// tool is one thing the model can ask for. status is what the caller sees while
// it runs, and it is written here rather than by the model.
type tool struct {
	def    anthropic.ToolDef
	status string
	run    func(ctx context.Context, userID uuid.UUID, input json.RawMessage) (any, error)
	// offers marks a tool that puts something in front of the caller, which
	// Limits.Proposals bounds. It returns an offer from run.
	offers bool
	// timeout zero gets Limits.Tool.
	timeout time.Duration
}

func objectProp(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

// readTools builds the registry. Everything here only reads: writes are
// proposals, which go through ConfirmAction.
func readTools(src Sources) []tool {
	var tools []tool
	if src.Trips != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name:        "list_trips",
				Description: "List the user's own trips, newest first: id, title, cities, dates. Use this for 'my trip', 'next trip', 'where am I going'.",
				Properties:  map[string]any{"limit": objectProp("integer", "How many to return, 1-25.")},
				Required:    []string{"limit"},
			},
			status: "Checking your trips",
			run:    src.listTrips,
		})
	}
	if src.Members != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name:        "get_trip",
				Description: "Read one trip the user can open, with its days and stops, stays and flights. Takes a trip id from list_trips or the context.",
				Properties:  map[string]any{"id": objectProp("string", "The trip id.")},
				Required:    []string{"id"},
			},
			status: "Opening your trip",
			run:    src.getTrip,
		})
	}
	if src.Lists != nil {
		tools = append(tools,
			tool{
				def: anthropic.ToolDef{
					Name:        "list_user_lists",
					Description: "List the user's saved place lists: id, name, how many items.",
				},
				status: "Checking your lists",
				run:    src.listUserLists,
			},
			tool{
				def: anthropic.ToolDef{
					Name:        "get_list",
					Description: "Read one of the user's lists with its items. Takes a list id from list_user_lists or the context.",
					Properties:  map[string]any{"id": objectProp("string", "The list id.")},
					Required:    []string{"id"},
				},
				status: "Opening your list",
				run:    src.getList,
			},
		)
	}
	if src.POIs != nil {
		tools = append(tools,
			tool{
				def: anthropic.ToolDef{
					Name:        "list_favorites",
					Description: "List the places the user saved as favourites.",
				},
				status: "Checking your saved places",
				run:    src.listFavorites,
			},
			tool{
				def: anthropic.ToolDef{
					Name:        "search_pois",
					Description: "Search Loci's places near a point. Needs coordinates: from the context, from a trip, or the well-known ones of a named city. An empty query lists what is nearest.",
					Properties: map[string]any{
						"query":     objectProp("string", "What to look for, or empty for whatever is nearest."),
						"category":  objectProp("string", "A category to limit to, or empty for any."),
						"latitude":  objectProp("number", "Latitude of the search centre."),
						"longitude": objectProp("number", "Longitude of the search centre."),
					},
					Required: []string{"query", "category", "latitude", "longitude"},
				},
				status: "Looking for places",
				run:    src.searchPOIs,
			},
			tool{
				def: anthropic.ToolDef{
					Name:        "get_poi_details",
					Description: "Read one place in full. Takes a place id from another tool or the context.",
					Properties:  map[string]any{"id": objectProp("string", "The place id.")},
					Required:    []string{"id"},
				},
				status: "Reading about that place",
				run:    src.getPOI,
			},
		)
	}
	if src.Memory != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name:        "get_memory",
				Description: "What Loci has learned the user likes and avoids. Use it to tailor a suggestion.",
			},
			status: "Remembering what you like",
			run:    src.getMemory,
		})
	}
	if src.Friends != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name:        "list_friends",
				Description: "List the user's friends on Loci, by display name and username.",
			},
			status: "Checking your friends",
			run:    src.listFriends,
		})
	}
	if src.Visits != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name:        "get_travel_history",
				Description: "List the cities the user has been to.",
			},
			status: "Checking where you've been",
			run:    src.travelHistory,
		})
	}
	if src.Activity != nil {
		tools = append(tools, tool{
			def: anthropic.ToolDef{
				Name:        "list_recents",
				Description: "The user's recent activity in Loci: what they asked for, saved and planned.",
			},
			status: "Checking what you've been up to",
			run:    src.listRecents,
		})
	}
	return tools
}

func parseInput[T any](input json.RawMessage) (T, error) {
	var in T
	if err := json.Unmarshal(input, &in); err != nil {
		return in, modelError("that input could not be read")
	}
	return in, nil
}

func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, modelError(fmt.Sprintf("%q is not an id; get one from another tool", raw))
	}
	return id, nil
}

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

type tripSummary struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Cities []string `json:"cities,omitempty"`
	Start  string   `json:"start,omitempty"`
	End    string   `json:"end,omitempty"`
	Days   int      `json:"days"`
}

func summarizeTrip(t *trip.Trip) tripSummary {
	out := tripSummary{ID: t.ID.String(), Title: t.Title, Days: len(t.Days)}
	for _, c := range t.Cities {
		out.Cities = append(out.Cities, c.CityName)
	}
	if len(out.Cities) == 0 && t.CityName != "" {
		out.Cities = []string{t.CityName}
	}
	if t.StartDate != nil {
		out.Start = t.StartDate.Format(time.DateOnly)
	}
	if t.EndDate != nil {
		out.End = t.EndDate.Format(time.DateOnly)
	}
	return out
}

func (s Sources) listTrips(ctx context.Context, userID uuid.UUID, input json.RawMessage) (any, error) {
	in, err := parseInput[struct {
		Limit int `json:"limit"`
	}](input)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	trips, total, err := s.Trips.ListTrips(ctx, userID, min(limit, maxListLimit), 0)
	if err != nil {
		return nil, err
	}
	out := make([]tripSummary, 0, len(trips))
	for _, t := range trips {
		if t != nil {
			out = append(out, summarizeTrip(t))
		}
	}
	return map[string]any{"trips": out, "total": total}, nil
}

type tripStop struct {
	Name  string `json:"name"`
	Notes string `json:"notes,omitempty"`
}

type tripDay struct {
	Day   int32      `json:"day"`
	Date  string     `json:"date,omitempty"`
	Stops []tripStop `json:"stops"`
}

// readableTrip resolves a trip id for the caller. A trip they cannot open reads
// the same as one that does not exist, to the model as to anybody else.
func (s Sources) readableTrip(ctx context.Context, userID, id uuid.UUID) (*trip.Trip, trip.Role, error) {
	t, role, err := s.Members.Resolve(ctx, userID, id)
	if err != nil {
		if errors.Is(err, trip.ErrNotFound) {
			return nil, trip.RoleNone, modelError("no trip with that id")
		}
		return nil, trip.RoleNone, err
	}
	if t == nil || role == trip.RoleNone {
		return nil, trip.RoleNone, modelError("no trip with that id")
	}
	return t, role, nil
}

func (s Sources) getTrip(ctx context.Context, userID uuid.UUID, input json.RawMessage) (any, error) {
	in, err := parseInput[struct {
		ID string `json:"id"`
	}](input)
	if err != nil {
		return nil, err
	}
	id, err := parseID(in.ID)
	if err != nil {
		return nil, err
	}
	t, _, err := s.readableTrip(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	days := make([]tripDay, 0, len(t.Days))
	for _, d := range t.Days {
		day := tripDay{Day: d.DayNumber, Stops: make([]tripStop, 0, len(d.Stops))}
		if d.Date != nil {
			day.Date = d.Date.Format(time.DateOnly)
		}
		for _, stop := range d.Stops {
			day.Stops = append(day.Stops, tripStop{Name: stop.Name, Notes: clip(stop.Notes, maxDescriptionLen)})
		}
		days = append(days, day)
	}
	stays := make([]string, 0, len(t.Stays))
	for _, stay := range t.Stays {
		stays = append(stays, stay.Name)
	}
	return map[string]any{"trip": summarizeTrip(t), "days": days, "stays": stays, "flights": len(t.Flights)}, nil
}

func (s Sources) listUserLists(ctx context.Context, userID uuid.UUID, _ json.RawMessage) (any, error) {
	lists, err := s.Lists.GetUserLists(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	type summary struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Items int    `json:"items"`
	}
	out := make([]summary, 0, len(lists))
	for _, l := range lists {
		if l != nil {
			out = append(out, summary{ID: l.ID.String(), Name: l.Name, Items: l.ItemCount})
		}
	}
	return map[string]any{"lists": out}, nil
}

func (s Sources) getList(ctx context.Context, userID uuid.UUID, input json.RawMessage) (any, error) {
	in, err := parseInput[struct {
		ID string `json:"id"`
	}](input)
	if err != nil {
		return nil, err
	}
	id, err := parseID(in.ID)
	if err != nil {
		return nil, err
	}
	list, err := s.Lists.GetListDetails(ctx, id, userID)
	if err != nil {
		return nil, modelError("no list with that id")
	}
	type item struct {
		ID    string `json:"id"`
		Notes string `json:"notes,omitempty"`
	}
	items := make([]item, 0, len(list.Items))
	for _, it := range list.Items {
		if it != nil {
			items = append(items, item{ID: it.ItemID.String(), Notes: clip(it.Notes, maxDescriptionLen)})
		}
	}
	return map[string]any{"name": list.List.Name, "items": items}, nil
}

type poiSummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	City        string  `json:"city,omitempty"`
	Category    string  `json:"category,omitempty"`
	Description string  `json:"description,omitempty"`
	Rating      float64 `json:"rating,omitempty"`
}

func summarizePOI(p *locitypes.POIDetailedInfo) poiSummary {
	return poiSummary{
		ID: p.ID.String(), Name: p.Name, City: p.City, Category: p.Category,
		Description: clip(p.Description, maxDescriptionLen), Rating: p.Rating,
	}
}

func (s Sources) listFavorites(ctx context.Context, userID uuid.UUID, _ json.RawMessage) (any, error) {
	pois, err := s.POIs.GetFavouritePOIsByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]poiSummary, 0, min(len(pois), maxListLimit))
	for i := range pois[:min(len(pois), maxListLimit)] {
		out = append(out, summarizePOI(&pois[i]))
	}
	return map[string]any{"favorites": out, "total": len(pois)}, nil
}

func (s Sources) searchPOIs(ctx context.Context, _ uuid.UUID, input json.RawMessage) (any, error) {
	in, err := parseInput[struct {
		Query     string  `json:"query"`
		Category  string  `json:"category"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}](input)
	if err != nil {
		return nil, err
	}
	// Zero, zero is the Gulf of Guinea, and it is what a guess looks like.
	if in.Latitude == 0 && in.Longitude == 0 {
		return nil, modelError("location unknown: ask where they mean, or use a city's coordinates")
	}
	filter := locitypes.POIFilter{
		Location: locitypes.GeoPoint{Latitude: in.Latitude, Longitude: in.Longitude},
		Radius:   searchRadiusKm,
		Category: in.Category,
	}
	var found []poiSummary
	if in.Query == "" {
		pois, err := s.POIs.SearchPOIs(ctx, filter)
		if err != nil {
			return nil, err
		}
		for i := range pois[:min(len(pois), searchResultLimit)] {
			found = append(found, summarizePOI(&pois[i]))
		}
	} else {
		results, err := s.POIs.SearchPOIsFused(ctx, filter, in.Query, searchSemanticBias)
		if err != nil {
			return nil, err
		}
		for i := range results[:min(len(results), searchResultLimit)] {
			found = append(found, summarizePOI(&results[i].POI))
		}
	}
	return map[string]any{"places": found}, nil
}

func (s Sources) getPOI(ctx context.Context, _ uuid.UUID, input json.RawMessage) (any, error) {
	in, err := parseInput[struct {
		ID string `json:"id"`
	}](input)
	if err != nil {
		return nil, err
	}
	id, err := parseID(in.ID)
	if err != nil {
		return nil, err
	}
	p, err := s.POIs.GetPOI(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, modelError("no place with that id")
	}
	return map[string]any{"place": summarizePOI(p), "address": p.Address}, nil
}

func (s Sources) getMemory(ctx context.Context, userID uuid.UUID, _ json.RawMessage) (any, error) {
	profile, err := s.Memory.Get(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	// Their own setting: with it off, what Loci learned is not used for anything.
	if profile == nil || !profile.PersonalizationEnabled {
		return map[string]any{"note": "personalisation is turned off"}, nil
	}
	type trait struct {
		Label string  `json:"label"`
		Score float64 `json:"score"`
	}
	out := make([]trait, 0, len(profile.Traits))
	for _, t := range profile.Traits[:min(len(profile.Traits), maxListLimit)] {
		out = append(out, trait{Label: t.Label, Score: t.Score})
	}
	return map[string]any{"traits": out, "note": "score runs -1 (avoids) to 1 (loves)"}, nil
}

func (s Sources) listFriends(ctx context.Context, userID uuid.UUID, _ json.RawMessage) (any, error) {
	friends, err := s.Friends.ListFriends(ctx, userID)
	if err != nil {
		return nil, err
	}
	type friend struct {
		Name     string `json:"name"`
		Username string `json:"username,omitempty"`
	}
	out := make([]friend, 0, len(friends))
	for _, f := range friends {
		if f.User != nil {
			out = append(out, friend{Name: f.User.GetDisplayName(), Username: f.User.GetUsername()})
		}
	}
	return map[string]any{"friends": out}, nil
}

func (s Sources) travelHistory(ctx context.Context, userID uuid.UUID, _ json.RawMessage) (any, error) {
	cities, total, err := s.Visits.ListVisitedCities(ctx, userID, maxListLimit, 0)
	if err != nil {
		return nil, err
	}
	type city struct {
		Name    string `json:"name"`
		Country string `json:"country,omitempty"`
	}
	out := make([]city, 0, len(cities))
	for _, c := range cities {
		if c != nil {
			out = append(out, city{Name: c.CityName, Country: c.Country})
		}
	}
	return map[string]any{"cities": out, "total": total}, nil
}

func (s Sources) listRecents(ctx context.Context, userID uuid.UUID, _ json.RawMessage) (any, error) {
	entries, _, err := s.Activity.GetUserActivityFeed(ctx, userID, defaultListLimit, 0, locitypes.ActivityFeedFilter{})
	if err != nil {
		return nil, err
	}
	type entry struct {
		Kind string `json:"kind"`
		What string `json:"what"`
		City string `json:"city,omitempty"`
		When string `json:"when"`
	}
	out := make([]entry, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		out = append(out, entry{Kind: string(e.Kind), What: clip(e.Label, maxDescriptionLen), City: e.CityName, When: e.OccurredAt.Format(time.DateOnly)})
	}
	return map[string]any{"recent": out}, nil
}
