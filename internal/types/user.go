//revive:disable-next-line:var-naming
package locitypes

import (
	"time"

	"github.com/google/uuid"
)

type UserStats struct {
	PlacesVisited  int `json:"places_visited" db:"places_visited"`
	ReviewsWritten int `json:"reviews_written" db:"reviews_written"`
	ListsCreated   int `json:"lists_created" db:"lists_created"`
	Followers      int `json:"followers" db:"followers"`
	Following      int `json:"following" db:"following"`
}

type UserProfile struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	Email           string     `json:"email" db:"email"`
	Username        *string    `json:"username,omitempty" db:"username"`
	Firstname       *string    `json:"firstname,omitempty" db:"firstname"`
	Lastname        *string    `json:"lastname,omitempty" db:"lastname"`
	PhoneNumber     *string    `json:"phone,omitempty" db:"phone"`
	Age             *int       `json:"age,omitempty" db:"age"`
	City            *string    `json:"city,omitempty" db:"city"`
	Country         *string    `json:"country,omitempty" db:"country"`
	AboutYou        *string    `json:"about_you,omitempty" db:"about_you"`
	Bio             *string    `json:"bio,omitempty"` // Maps to about_you for frontend compatibility (no db tag)
	Location        *string    `json:"location,omitempty" db:"location"`
	JoinedDate      time.Time  `json:"joinedDate"`       // Maps to created_at (no db tag)
	Avatar          *string    `json:"avatar,omitempty"` // Maps to profile_image_url (no db tag)
	Interests       []string   `json:"interests,omitempty" db:"interests"`
	Badges          []string   `json:"badges,omitempty" db:"badges"`
	Stats           *UserStats `json:"stats,omitempty"` // Populated separately (no db tag)
	PasswordHash    string     `json:"-"`               // Exclude from JSON responses
	DisplayName     *string    `json:"display_name,omitempty" db:"display_name"`
	ProfileImageURL *string    `json:"profile_image_url,omitempty" db:"profile_image_url"`
	IsActive        bool       `json:"is_active" db:"is_active"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty" db:"email_verified_at"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
	Theme           *string    `json:"theme,omitempty" db:"theme"`
	Language        *string    `json:"language,omitempty" db:"language"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
}

// UpdateProfileParams defines the fields allowed for profile updates.
// Use pointers for optional fields, allowing partial updates.
type UpdateProfileParams struct {
	Username        *string   `json:"username,omitempty"`
	PhoneNumber     *string   `json:"phone,omitempty"`
	Email           *string   `json:"email,omitempty"`
	DisplayName     *string   `json:"display_name,omitempty"`
	ProfileImageURL *string   `json:"profile_image_url,omitempty"`
	Firstname       *string   `json:"firstname,omitempty"`
	Lastname        *string   `json:"lastname,omitempty"`
	Age             *int      `json:"age,omitempty"`
	City            *string   `json:"city,omitempty"`
	Country         *string   `json:"country,omitempty"`
	AboutYou        *string   `json:"about_you,omitempty"`
	Location        *string   `json:"location,omitempty"`
	Interests       *[]string `json:"interests,omitempty"`
	Badges          *[]string `json:"badges,omitempty"`
}
