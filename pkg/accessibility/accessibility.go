// Package accessibility looks up open accessibility data about a place.
//
// PlaceAccessibilitySource is named for its role. accessibility.cloud (the
// Sozialhelden aggregator behind Wheelmap, which carries OpenStreetMap's
// wheelchair tags among other datasets) is the implementation today.
package accessibility

import (
	"context"
	"errors"
	"time"
)

// Wheelchair is a place's overall wheelchair rating.
type Wheelchair string

const (
	WheelchairUnknown Wheelchair = ""
	WheelchairYes     Wheelchair = "yes"
	WheelchairLimited Wheelchair = "limited"
	WheelchairNo      Wheelchair = "no"
)

// PlaceFacts is what open data says about one place. Pointer fields are
// tri-state: nil means the data does not say, which is not the same as false.
type PlaceFacts struct {
	Wheelchair       Wheelchair
	EntranceLevel    *bool
	EntranceRamp     *bool
	AccessibleToilet *bool
	// SourceName names the dataset, for attribution ("OpenStreetMap", ...).
	SourceName string
	// Licence is the dataset's licence, which must be shown with the facts.
	Licence    string
	ObservedAt *time.Time
}

// Empty reports whether the facts say nothing usable.
func (f *PlaceFacts) Empty() bool {
	return f == nil || (f.Wheelchair == WheelchairUnknown && f.EntranceLevel == nil &&
		f.EntranceRamp == nil && f.AccessibleToilet == nil)
}

// PlaceAccessibilitySource finds open accessibility data for a named place at
// a coordinate. It returns (nil, nil) when no record matches: most places
// have none, and that is an answer, not an error.
type PlaceAccessibilitySource interface {
	Lookup(ctx context.Context, lat, lon float64, name string) (*PlaceFacts, error)
}

// ErrUnavailable means the source could not answer right now.
var ErrUnavailable = errors.New("accessibility: source unavailable")
