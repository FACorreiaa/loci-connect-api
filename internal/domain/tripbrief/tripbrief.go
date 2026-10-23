// Package tripbrief speaks first on trip days: a short morning brief (the
// day's weather, its stops, what is closed, any local alert) and one heads-up
// when the afternoon turns wet. It is deterministic — no model call — and
// opt-in through the "trip reminders" notification switch.
package tripbrief

import (
	"time"

	"github.com/google/uuid"
)

// Kinds of brief, one of each per traveller per local day at most.
const (
	KindMorning = "morning"
	KindWeather = "weather"
)

// Candidate is a traveller whose trip covers their local today.
type Candidate struct {
	UserID uuid.UUID
	TripID uuid.UUID
	// DayID is today's planned day, when the plan has one.
	DayID *uuid.UUID
	// City is where today is spent (the day's city, else the trip's).
	City     string
	Lat, Lon *float64
	// LocalDate is the traveller's calendar date (midnight UTC carrying it).
	LocalDate time.Time
	LocalHour int
	// DayNumber is 1 on the trip's start date.
	DayNumber int
}

// Stop is one of today's planned stops.
type Stop struct {
	// ID is the stop's, for a link that suggests swapping it.
	ID          uuid.UUID
	Name        string
	StartMinute *int
	// OpeningHours is the place's stored hours, in whatever form it has.
	OpeningHours string
}

// Brief is one notification.
type Brief struct {
	Title, Body, URL string
}
