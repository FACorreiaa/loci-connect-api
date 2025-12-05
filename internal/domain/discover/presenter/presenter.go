package presenter

import (
	chatv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat"
	commonv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	discoverv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/discover"

	chatpresenter "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ToDiscoverPageData maps domain discover page data to proto.
func ToDiscoverPageData(data *locitypes.DiscoverPageData) *discoverv1.DiscoverPageData {
	if data == nil {
		return nil
	}
	return &discoverv1.DiscoverPageData{
		Trending:          ToTrendingDiscoveries(data.Trending),
		Featured:          ToFeaturedCollections(data.Featured),
		RecentDiscoveries: ToChatSessions(data.RecentDiscoveries),
	}
}

func ToTrendingDiscoveries(items []locitypes.TrendingDiscovery) []*discoverv1.TrendingDiscovery {
	out := make([]*discoverv1.TrendingDiscovery, 0, len(items))
	for _, t := range items {
		out = append(out, &discoverv1.TrendingDiscovery{
			CityName:    t.CityName,
			SearchCount: int32(t.SearchCount),
			Emoji:       t.Emoji,
		})
	}
	return out
}

func ToFeaturedCollections(items []locitypes.FeaturedCollection) []*discoverv1.FeaturedCollection {
	out := make([]*discoverv1.FeaturedCollection, 0, len(items))
	for _, f := range items {
		out = append(out, &discoverv1.FeaturedCollection{
			Category:  f.Category,
			Title:     f.Title,
			ItemCount: int32(f.ItemCount),
			Emoji:     f.Emoji,
		})
	}
	return out
}

func ToDiscoverResults(items []locitypes.DiscoverResult) []*discoverv1.DiscoverResult {
	out := make([]*discoverv1.DiscoverResult, 0, len(items))
	for _, r := range items {
		out = append(out, &discoverv1.DiscoverResult{
			Id:           r.ID,
			Name:         r.Name,
			Latitude:     r.Latitude,
			Longitude:    r.Longitude,
			Category:     r.Category,
			Description:  r.Description,
			Address:      r.Address,
			Website:      r.Website,
			PhoneNumber:  r.PhoneNumber,
			OpeningHours: r.OpeningHours,
			PriceLevel:   r.PriceLevel,
			Rating:       r.Rating,
			Tags:         r.Tags,
			Images:       r.Images,
			CuisineType:  r.CuisineType,
			StarRating:   r.StarRating,
		})
	}
	return out
}

func ToChatSessions(items []locitypes.ChatSession) []*chatv1.ChatSession {
	out := make([]*chatv1.ChatSession, 0, len(items))
	for i := range items {
		out = append(out, chatpresenter.ToChatSession(&items[i]))
	}
	return out
}

func ToPaginationMetadata(total, page, pageSize int) *commonv1.PaginationMetadata {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	totalPages := int32(0)
	if total > 0 {
		totalPages = int32((total + pageSize - 1) / pageSize)
	}
	hasMore := int32(total) > int32(page*pageSize)

	return &commonv1.PaginationMetadata{
		TotalRecords: int32(total),
		Page:         int32(page),
		PageSize:     int32(pageSize),
		TotalPages:   totalPages,
		HasMore:      hasMore,
	}
}
