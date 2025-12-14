package locitypes

import (
	"time"

	"github.com/google/uuid"
)

// FavoriteItem represents a favorited item
type FavoriteItem struct {
	ID          uuid.UUID `json:"id" db:"id"`
	UserID      uuid.UUID `json:"user_id" db:"user_id"`
	ItemID      string    `json:"item_id" db:"item_id"`
	ItemName    string    `json:"item_name" db:"item_name"`
	ContentType string    `json:"content_type" db:"content_type"` // poi, hotel, restaurant, itinerary
	Notes       string    `json:"notes" db:"notes"`
	Description string    `json:"description" db:"description"`
	CityName    string    `json:"city_name" db:"city_name"`
	Latitude    float64   `json:"latitude" db:"latitude"`
	Longitude   float64   `json:"longitude" db:"longitude"`
	Rating      float64   `json:"rating" db:"rating"`
	Category    string    `json:"category" db:"category"`
	AddedAt     time.Time `json:"added_at" db:"added_at"`
}
