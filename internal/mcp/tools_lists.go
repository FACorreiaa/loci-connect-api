package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ListSummary is the compact list representation for MCP tools.
type ListSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsPublic    bool   `json:"is_public"`
	IsItinerary bool   `json:"is_itinerary"`
	ItemCount   int    `json:"item_count,omitempty"`
}

type listUserListsInput struct {
	ItinerariesOnly bool `json:"itineraries_only,omitempty" jsonschema:"return only itinerary lists"`
}

type listUserListsOutput struct {
	Lists []ListSummary `json:"lists"`
}

type getListInput struct {
	ID string `json:"id" jsonschema:"list id from list_user_lists"`
}

type addPOIToListInput struct {
	ListID string `json:"list_id" jsonschema:"target list id from list_user_lists"`
	PoiID  string `json:"poi_id" jsonschema:"POI id from search_pois or find_nearby"`
	Notes  string `json:"notes,omitempty" jsonschema:"optional note attached to the saved item"`
}

type listFavoritesOutput struct {
	Favorites []POISummary `json:"favorites"`
	Count     int          `json:"count"`
}

type addFavoriteInput struct {
	PoiID string `json:"poi_id" jsonschema:"POI id from search_pois or find_nearby"`
}

func registerListTools(server *mcp.Server, deps Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_user_lists",
		Description: "List the user's saved place lists (and itinerary lists) in Loci.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listUserListsInput) (*mcp.CallToolResult, listUserListsOutput, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, listUserListsOutput{}, err
		}
		lists, err := deps.ListService.GetUserLists(ctx, userID, in.ItinerariesOnly)
		if err != nil {
			return nil, listUserListsOutput{}, toolError(err)
		}
		out := listUserListsOutput{}
		for _, l := range lists {
			if l == nil {
				continue
			}
			out.Lists = append(out.Lists, ListSummary{
				ID:          l.ID.String(),
				Name:        l.Name,
				Description: l.Description,
				IsPublic:    l.IsPublic,
				IsItinerary: l.IsItinerary,
			})
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_list",
		Description: "Fetch a list with all its saved items.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getListInput) (*mcp.CallToolResult, *locitypes.ListWithItems, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, nil, err
		}
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid list id %q", in.ID)
		}
		list, err := deps.ListService.GetListDetails(ctx, id, userID)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return nil, list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_poi_to_list",
		Description: "Add a point of interest to one of the user's lists.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addPOIToListInput) (*mcp.CallToolResult, map[string]string, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, nil, err
		}
		listID, err := uuid.Parse(in.ListID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid list id %q", in.ListID)
		}
		poiID, err := uuid.Parse(in.PoiID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid poi id %q", in.PoiID)
		}
		item, err := deps.ListService.AddPOIListItem(ctx, userID, listID, poiID, locitypes.AddListItemRequest{
			ItemID:      poiID,
			ContentType: locitypes.ContentTypePOI,
			Notes:       in.Notes,
		})
		if err != nil {
			return nil, nil, toolError(err)
		}
		return nil, map[string]string{"status": "added", "item_id": item.ItemID.String()}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_favorites",
		Description: "List the user's favorite points of interest.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listFavoritesOutput, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, listFavoritesOutput{}, err
		}
		pois, err := deps.POIService.GetFavouritePOIsByUserID(ctx, userID)
		if err != nil {
			return nil, listFavoritesOutput{}, toolError(err)
		}
		summarized := summarize(pois)
		return nil, listFavoritesOutput{Favorites: summarized.Results, Count: summarized.Count}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_favorite",
		Description: "Add a point of interest to the user's favorites.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addFavoriteInput) (*mcp.CallToolResult, map[string]string, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, nil, err
		}
		poiID, err := uuid.Parse(in.PoiID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid poi id %q", in.PoiID)
		}
		id, err := deps.POIService.AddPoiToFavourites(ctx, userID, poiID, false)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return nil, map[string]string{"status": "added", "favorite_id": id.String()}, nil
	})
}
