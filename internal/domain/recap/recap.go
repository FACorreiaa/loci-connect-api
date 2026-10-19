// Package recap builds story-style recaps of what a traveller did: one per
// finished trip, and one per calendar year (the year in review).
//
// A recap is built once from what the other domains already store (visits,
// trip days, stop marks, taste evidence, points, field scores, friends) and
// kept as a frozen snapshot in the recaps table. The builder is pure: the
// repository loads a TripFacts or YearFacts, BuildTrip or BuildYear turns it
// into cards, and nothing in between touches the database.
package recap

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// BuilderVersion is stamped on every snapshot. Bump it when the cards a given
// TripFacts produces change, so image caches keyed on it move with them.
const BuilderVersion = 1

// RunnerLockKey is the advisory lock the recap runner takes; it spells
// "locirecp" in ASCII.
const RunnerLockKey int64 = 0x6c6f636972656370

var (
	// ErrNotFound is a recap or trip that does not exist or is not the
	// caller's.
	ErrNotFound = errors.New("recap: not found")
	// ErrNotReady is a recap that cannot be shared or rendered yet.
	ErrNotReady = errors.New("recap: not ready")
)

// Kind is the recap kind; values match RecapKind in the proto.
type Kind int16

const (
	KindTrip Kind = 1
	KindYear Kind = 2
)

// Status is the build state; values match RecapStatus in the proto.
type Status int16

const (
	StatusPending    Status = 1
	StatusReady      Status = 2
	StatusIneligible Status = 3
	StatusFailed     Status = 4
)

// Freeze rules. An unshared recap may be rebuilt for RefreshWindow after it
// is ready, so visits an iPhone syncs late still count; after that, or once
// it has a share code, it never changes.
const RefreshWindow = 7 * 24 * time.Hour

// Discovery rules for the runner. Only trips that ended within LookbackDays
// are picked up on their own, so switching the runner on does not push a
// recap for every old trip at once. Older trips still get one on request.
const LookbackDays = 14

// Pushes go out between these local hours.
const (
	PushFromHour = 10
	PushToHour   = 21
)

// MaxAttempts is how often the runner retries a failed build.
const MaxAttempts = 3

// Row is a stored recap.
type Row struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	Kind             Kind
	TripID           uuid.UUID
	Year             int
	PeriodStart      time.Time
	PeriodEnd        time.Time
	Status           Status
	BuilderVersion   int
	Payload          []byte
	IneligibleReason string
	Attempts         int
	ReadyAt          *time.Time
	NotifiedAt       *time.Time
	ViewedAt         *time.Time
	ShareCode        string
	SharedAt         *time.Time
}

// Refreshable reports whether the owner may still have the recap rebuilt.
func (r *Row) Refreshable(now time.Time) bool {
	if r.ShareCode != "" {
		return false
	}
	if r.Status != StatusReady || r.ReadyAt == nil {
		return true
	}
	return now.Sub(*r.ReadyAt) < RefreshWindow
}

// Candidate is a trip the runner may queue a recap for.
type Candidate struct {
	TripID   uuid.UUID
	UserID   uuid.UUID
	Timezone string
	// StartDate and EndDate are the trip's own dates, when set.
	StartDate, EndDate *time.Time
	// FirstDay and LastDay are the earliest and latest dated trip days.
	FirstDay, LastDay *time.Time
	Days, DaysDone    int
	LastCompletedAt   *time.Time
}

// Location loads tz, falling back to UTC for an empty or unknown zone.
func Location(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// LocalDate is t's calendar date in loc, as midnight UTC.
func LocalDate(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// dateOnly drops the clock from a DATE column's value.
func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Period is the trip's local date range: its own dates when set, otherwise
// its dated days. ok is false for a trip with neither.
func (c Candidate) Period(loc *time.Location) (start, end time.Time, ok bool) {
	switch {
	case c.EndDate != nil:
		end = dateOnly(*c.EndDate)
	case c.LastDay != nil:
		end = LocalDate(*c.LastDay, loc)
	default:
		return time.Time{}, time.Time{}, false
	}
	switch {
	case c.StartDate != nil:
		start = dateOnly(*c.StartDate)
	case c.FirstDay != nil:
		start = LocalDate(*c.FirstDay, loc)
	default:
		start = end
	}
	if start.After(end) {
		start = end
	}
	return start, end, true
}

// Due reports whether the trip is over and recent enough for the runner to
// queue its recap at now. A trip is over the local day after its end date,
// or as soon as every one of its days is walked.
func (c Candidate) Due(now time.Time) (start, end time.Time, due bool) {
	loc := Location(c.Timezone)
	start, end, ok := c.Period(loc)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	today := LocalDate(now, loc)
	earliest := today.AddDate(0, 0, -LookbackDays)
	if end.Before(today) && !end.Before(earliest) {
		return start, end, true
	}
	if c.Days > 0 && c.DaysDone == c.Days && c.LastCompletedAt != nil &&
		!LocalDate(*c.LastCompletedAt, loc).Before(earliest) {
		return start, end, true
	}
	return time.Time{}, time.Time{}, false
}

// Ended reports whether a trip can have its recap built on request: its end
// date has passed, or every day is walked. A trip with no dates and no walked
// days cannot.
func (c Candidate) Ended(now time.Time) (start, end time.Time, ended bool) {
	loc := Location(c.Timezone)
	start, end, ok := c.Period(loc)
	if c.Days > 0 && c.DaysDone == c.Days {
		if !ok && c.LastCompletedAt != nil {
			d := LocalDate(*c.LastCompletedAt, loc)
			return d, d, true
		}
		return start, end, ok
	}
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	return start, end, end.Before(LocalDate(now, loc))
}

// InPushWindow reports whether now is a polite hour to push in tz.
func InPushWindow(now time.Time, tz string) bool {
	h := now.In(Location(tz)).Hour()
	return h >= PushFromHour && h < PushToHour
}
