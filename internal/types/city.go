//revive:disable-next-line:var-naming
package locitypes

import "github.com/google/uuid"

// CityDetail matches the cities table structure.
type CityDetail struct {
	ID              uuid.UUID `json:"id" db:"id"`
	Name            string    `json:"name" db:"name"`
	Country         string    `json:"country" db:"country"`
	StateProvince   string    `json:"state_province,omitempty" db:"state_province"`
	AiSummary       string    `json:"ai_summary" db:"ai_summary"`
	CenterLatitude  *float64  `json:"center_latitude,omitempty" db:"center_latitude"`
	CenterLongitude *float64  `json:"center_longitude,omitempty" db:"center_longitude"`
}
