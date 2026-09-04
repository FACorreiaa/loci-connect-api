package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ItinerarySummary is the compact itinerary representation for list tools.
type ItinerarySummary struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	IsPublic    bool     `json:"is_public"`
}

type listItinerariesInput struct {
	Page     int `json:"page,omitempty" jsonschema:"1-based page number, default 1"`
	PageSize int `json:"page_size,omitempty" jsonschema:"items per page, default 10, max 50"`
}

type listItinerariesOutput struct {
	Itineraries []ItinerarySummary `json:"itineraries"`
	Total       int                `json:"total"`
	Page        int                `json:"page"`
}

type getItineraryInput struct {
	ID string `json:"id" jsonschema:"itinerary id from list_itineraries"`
}

type updateItineraryInput struct {
	ID string `json:"id" jsonschema:"itinerary id from list_itineraries"`
	// Only provided fields change; omitted fields keep their value.
	Title           *string  `json:"title,omitempty" jsonschema:"new title"`
	Description     *string  `json:"description,omitempty" jsonschema:"new description; empty string clears it"`
	Tags            []string `json:"tags,omitempty" jsonschema:"replacement tag list"`
	MarkdownContent *string  `json:"markdown_content,omitempty" jsonschema:"replacement markdown body of the itinerary"`
	IsPublic        *bool    `json:"is_public,omitempty" jsonschema:"publish or unpublish the itinerary"`
}

func summarizeItinerary(it *locitypes.UserSavedItinerary) ItinerarySummary {
	s := ItinerarySummary{
		ID:       it.ID.String(),
		Title:    it.Title,
		Tags:     it.Tags,
		IsPublic: it.IsPublic,
	}
	if it.Description != nil {
		s.Description = *it.Description
	}
	return s
}

func registerItineraryTools(server *mcp.Server, deps Deps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_itineraries",
		Description: "List the user's saved travel itineraries in Loci.",
	}, guardTool(deps, "list_itineraries", func(ctx context.Context, _ *mcp.CallToolRequest, in listItinerariesInput) (*mcp.CallToolResult, listItinerariesOutput, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, listItinerariesOutput{}, err
		}
		page := max(in.Page, 1)
		pageSize := in.PageSize
		if pageSize <= 0 {
			pageSize = 10
		}
		pageSize = min(pageSize, 50)

		resp, err := deps.POIService.GetItineraries(ctx, userID, page, pageSize)
		if err != nil {
			return nil, listItinerariesOutput{}, toolError(err)
		}
		out := listItinerariesOutput{Total: resp.TotalRecords, Page: resp.Page}
		for i := range resp.Itineraries {
			out.Itineraries = append(out.Itineraries, summarizeItinerary(&resp.Itineraries[i]))
		}
		return nil, out, nil
	}))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_itinerary",
		Description: "Fetch a saved itinerary including its full markdown content.",
	}, guardTool(deps, "get_itinerary", func(ctx context.Context, _ *mcp.CallToolRequest, in getItineraryInput) (*mcp.CallToolResult, *locitypes.UserSavedItinerary, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, nil, err
		}
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid itinerary id %q", in.ID)
		}
		it, err := deps.POIService.GetItinerary(ctx, userID, id)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return nil, it, nil
	}))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_itinerary",
		Description: "Update a saved itinerary's title, description, tags, markdown content, or visibility. Only provided fields change.",
	}, guardTool(deps, "update_itinerary", func(ctx context.Context, _ *mcp.CallToolRequest, in updateItineraryInput) (*mcp.CallToolResult, ItinerarySummary, error) {
		userID, err := callerUserID(ctx)
		if err != nil {
			return nil, ItinerarySummary{}, err
		}
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, ItinerarySummary{}, fmt.Errorf("invalid itinerary id %q", in.ID)
		}
		updated, err := deps.POIService.UpdateItinerary(ctx, userID, id, locitypes.UpdateItineraryRequest{
			Title:           in.Title,
			Description:     in.Description,
			Tags:            in.Tags,
			MarkdownContent: in.MarkdownContent,
			IsPublic:        in.IsPublic,
		})
		if err != nil {
			return nil, ItinerarySummary{}, toolError(err)
		}
		return nil, summarizeItinerary(updated), nil
	}))
}
