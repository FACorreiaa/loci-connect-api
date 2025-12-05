package locitypes

import (
	"time"

	"github.com/google/uuid"
)

// DiscoverPageData contains all data needed for the discover page
type DiscoverPageData struct {
	Trending          []TrendingDiscovery  `json:"trending"`
	Featured          []FeaturedCollection `json:"featured"`
	RecentDiscoveries []ChatSession        `json:"recent_discoveries"`
	TrendingSearches  []TrendingSearch     `json:"trending_searches"` // Most searched today
}

// TrendingDiscovery represents a trending discovery/search by city
type TrendingDiscovery struct {
	CityName     string `json:"city_name" db:"city_name"`
	SearchCount  int    `json:"search_count" db:"search_count"`
	Emoji        string `json:"emoji" db:"emoji"`
	Category     string `json:"category" db:"category"`
	FirstMessage string `json:"first_message" db:"first_message"`
}

// TrendingSearch represents a trending search query
type TrendingSearch struct {
	Query        string `json:"query"`
	CityName     string `json:"city_name"`
	SearchCount  int    `json:"search_count"`
	LastSearched string `json:"last_searched"` // Human-readable time (e.g., "2 hours ago")
}

// FeaturedCollection represents a featured collection of POIs
type FeaturedCollection struct {
	ID          uuid.UUID `json:"id" db:"id"`
	Title       string    `json:"title" db:"title"`
	Description string    `json:"description" db:"description"`
	Emoji       string    `json:"emoji" db:"emoji"`
	ItemCount   int       `json:"item_count" db:"item_count"`
	Category    string    `json:"category" db:"category"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// DiscoverResult represents a single discovery result (POI)
type DiscoverResult struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Latitude     float64  `json:"latitude"`
	Longitude    float64  `json:"longitude"`
	Category     string   `json:"category"`
	Description  string   `json:"description"`
	Address      string   `json:"address"`
	Website      *string  `json:"website,omitempty"`
	PhoneNumber  *string  `json:"phone_number,omitempty"`
	OpeningHours *string  `json:"opening_hours,omitempty"`
	PriceLevel   string   `json:"price_level"`
	Rating       float64  `json:"rating"`
	Tags         []string `json:"tags,omitempty"`
	Images       []string `json:"images,omitempty"`
	CuisineType  *string  `json:"cuisine_type,omitempty"`
	StarRating   *string  `json:"star_rating,omitempty"`
}

// Response types for API endpoints

// DiscoverPageResponse is the response for GET /discover
type DiscoverPageResponse struct {
	Response
	Data *DiscoverPageData `json:"data"`
}

// TrendingDiscoveriesResponse is the response for GET /discover/trending
type TrendingDiscoveriesResponse struct {
	Response
	Data struct {
		Trending []TrendingDiscovery `json:"trending"`
	} `json:"data"`
}

// FeaturedCollectionsResponse is the response for GET /discover/featured
type FeaturedCollectionsResponse struct {
	Response
	Data struct {
		Featured []FeaturedCollection `json:"featured"`
	} `json:"data"`
}

// RecentDiscoveriesResponse is the response for GET /discover/recent
type RecentDiscoveriesResponse struct {
	Response
	Data struct {
		Sessions []ChatSession `json:"sessions"`
	} `json:"data"`
}

// CategoryResultsResponse is the response for GET /discover/category/{category}
type CategoryResultsResponse struct {
	Response
	Data struct {
		Category string           `json:"category"`
		Results  []DiscoverResult `json:"results"`
	} `json:"data"`
}
