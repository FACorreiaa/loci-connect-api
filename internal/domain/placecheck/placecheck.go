// Package placecheck checks the places in a plan against the map.
//
// A plan's places are written by a language model. This package looks each
// one up on OpenStreetMap and keeps what it found (Verification): whether a
// place by that name exists near the city, where it really is, and its
// opening hours. Evaluate then answers, for one stop on one day, whether the
// place is open when the traveller plans to be there.
//
// Two facts are kept apart on purpose. "Checked" means the place was found on
// the map, nothing more. Whether it is open is a second answer, and when the
// map has no hours, or hours this code cannot read, that answer is "not
// known", never a guess.
package placecheck

import (
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/openinghours"
)

// SourceOSM labels a verification made against OpenStreetMap.
const SourceOSM = "osm"

const (
	// A place found on the map stays where it is; its hours change now and
	// then. A month, like the map client's own cache.
	ttlMatched = 30 * 24 * time.Hour
	// A place not found may be added to the map, or the map may gain the
	// region it is in.
	ttlUnmatched = 7 * 24 * time.Hour
)

// Verification is what the map said about one place.
type Verification struct {
	POIID string
	// Matched is false when no place by that name was found, or more than
	// one was and the name could not say which.
	Matched bool
	Source  string
	// ExternalID identifies the map entry, e.g. "way/123".
	ExternalID  string
	WikidataID  string
	MatchedName string
	Kind        string
	Lat, Lon    float64
	// HoursRaw is the map's opening_hours value as written; often empty.
	HoursRaw   string
	Confidence float64
	CheckedAt  time.Time
	ExpiresAt  time.Time
}

// Fresh reports whether the verification can still be relied on at now.
func (v Verification) Fresh(now time.Time) bool { return now.Before(v.ExpiresAt) }

// Status says whether a stop's place was found on the map.
type Status int

const (
	// StatusUnchecked means the place has not been looked up yet, or the
	// stop has no place behind it.
	StatusUnchecked Status = iota
	// StatusChecked means the place was found on the map.
	StatusChecked
	// StatusNotFound means the place was looked up and not found.
	StatusNotFound
)

// Hours says what is known about a checked place being open for a stop.
type Hours int

const (
	// HoursNotListed means the map has no opening hours for the place.
	HoursNotListed Hours = iota
	// HoursUnreadable means the map has hours in a form this code does not
	// read (seasons, dates, sunrise). Nothing is claimed.
	HoursUnreadable
	// HoursListed means the hours are readable but the stop has no date, so
	// they cannot be applied to it.
	HoursListed
	// HoursOpenThatDay means the place opens that day, and the stop has no
	// start time to check against.
	HoursOpenThatDay
	// HoursOpen means the place is open for the whole visit.
	HoursOpen
	// HoursShutThatDay means the place does not open at all that day.
	HoursShutThatDay
	// HoursOpensLater means the place is shut at the stop's start and opens
	// later that day, at Check.OpensMinute.
	HoursOpensLater
	// HoursClosesEarly means the place shuts, at Check.ClosesMinute, before
	// the visit would end.
	HoursClosesEarly
	// HoursShutByThen means the place has shut for the day by the stop's
	// start.
	HoursShutByThen
)

// NeedsAttention reports whether the hours contradict the plan: the
// traveller would arrive to a shut door or be turned out early.
func (h Hours) NeedsAttention() bool {
	switch h {
	case HoursShutThatDay, HoursOpensLater, HoursClosesEarly, HoursShutByThen:
		return true
	}
	return false
}

// Check is the answer for one stop.
type Check struct {
	Status Status
	Hours  Hours
	// OpensMinute and ClosesMinute are minutes from midnight of the stop's
	// day, for the open span the answer refers to. Zero when there is none.
	// ClosesMinute is above 1440 when the place stays open past midnight.
	OpensMinute  int
	ClosesMinute int
	// Holiday is the public holiday the stop's day falls on, if any.
	Holiday string
	// HolidayHoursUnknown is set when the day is a public holiday and the
	// map's hours say nothing about holidays: the hours shown are the
	// ordinary ones and may not hold.
	HolidayHoursUnknown bool
	// Lat and Lon are where the map puts the place. Zero unless checked.
	Lat, Lon  float64
	CheckedAt time.Time
}

// Visit is when a stop is planned. Every field is optional.
type Visit struct {
	// Date is the stop's calendar day; nil for a "Day 2" plan with no dates.
	Date *time.Time
	// StartMinute is minutes from midnight; nil when the stop has no time.
	StartMinute *int32
	// DurationMinutes is how long the traveller means to stay.
	DurationMinutes *int32
	// Holiday is the name of the public holiday on Date, or "".
	Holiday string
}

// Evaluate answers for one stop from what the map said about its place. v is
// nil when the place has never been looked up.
//
// It is a pure function of its arguments, so the answer is worked out on
// every read and can never be stale after the traveller moves a stop.
func Evaluate(v *Verification, visit Visit) Check {
	if v == nil {
		return Check{Status: StatusUnchecked}
	}
	if !v.Matched {
		return Check{Status: StatusNotFound, CheckedAt: v.CheckedAt}
	}
	c := Check{Status: StatusChecked, Lat: v.Lat, Lon: v.Lon, CheckedAt: v.CheckedAt}
	if v.HoursRaw == "" {
		return c
	}
	sched, err := openinghours.Parse(v.HoursRaw)
	if err != nil {
		c.Hours = HoursUnreadable
		return c
	}
	if visit.Date == nil {
		c.Hours = HoursListed
		return c
	}
	c.Holiday = visit.Holiday
	holiday := visit.Holiday != ""
	wd := visit.Date.Weekday()

	if visit.StartMinute == nil {
		// No time to check against: the only thing that can be said is
		// whether the place opens that day at all.
		opens, uncertain, _ := sched.OpensOn(wd, holiday)
		c.HolidayHoursUnknown = uncertain
		c.Hours = HoursShutThatDay
		if opens {
			c.Hours = HoursOpenThatDay
		}
		return c
	}

	dur := 0
	if visit.DurationMinutes != nil {
		dur = int(*visit.DurationMinutes)
	}
	r := sched.Eval(wd, int(*visit.StartMinute), dur, holiday)
	c.HolidayHoursUnknown = r.HolidayUncertain
	c.OpensMinute, c.ClosesMinute = r.Opens, r.Closes
	switch r.State {
	case openinghours.Open:
		c.Hours = HoursOpen
	case openinghours.ClosedAllDay:
		c.Hours = HoursShutThatDay
	case openinghours.OpensLater:
		c.Hours = HoursOpensLater
	case openinghours.ClosesEarly:
		c.Hours = HoursClosesEarly
	case openinghours.ClosedByThen:
		c.Hours = HoursShutByThen
	default:
		c.Hours = HoursUnreadable
	}
	return c
}

// Verdict counts a day's stops: how many have a place behind them, and how
// many of those were found on the map.
type Verdict struct {
	Checked int
	Total   int
	// NeedAttention counts checked stops whose hours contradict the plan.
	NeedAttention int
}

// Tally adds one stop's answer to the verdict. Stops with no place behind
// them (a pasted pin, a free-text note) are not passed in: there is nothing
// to check, so they count neither for nor against the day.
func (d *Verdict) Tally(c Check) {
	d.Total++
	if c.Status == StatusChecked {
		d.Checked++
		if c.Hours.NeedsAttention() {
			d.NeedAttention++
		}
	}
}
