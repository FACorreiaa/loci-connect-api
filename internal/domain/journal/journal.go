// Package journal turns a trip into a diary (JournalService): the plan, what
// was actually visited and how it was rated, a note per day, and photos.
//
// Photos never pass through the API. CreateUpload signs a PUT whose
// signature covers the size and content type, the client sends the bytes
// straight to the bucket, and Confirm checks the object before the photo
// shows. Rows hang off the trip only (see migration 0123 for why), and an
// AFTER DELETE trigger records every removed object for the Sweeper.
package journal

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrNotFound is a trip, day, stop or photo that does not exist or is not
	// the caller's: the two are indistinguishable on purpose.
	ErrNotFound = errors.New("not found")
	// ErrNoStorage means photo storage is not configured.
	ErrNoStorage = errors.New("photos are not set up")
	// ErrUploadMissing is a confirm before the bytes arrived.
	ErrUploadMissing = errors.New("upload not received")
	// ErrBadUpload is an object that does not match what was signed.
	ErrBadUpload = errors.New("upload does not match")
	// ErrBadPhoto is a photo request outside the limits.
	ErrBadPhoto = errors.New("photo must be a JPEG, PNG or HEIC of at most 8 MiB")
)

const (
	// MaxPhotoBytes bounds one upload; clients downscale before sending.
	MaxPhotoBytes = 8 << 20
	putTTL        = 10 * time.Minute
	getTTL        = time.Hour
	// pendingTTL is how long an unconfirmed upload is kept.
	pendingTTL    = 24 * time.Hour
	maxDiary      = 4000
	maxCaption    = 280
	statusReady   = "ready"
	statusPending = "pending"
)

// extensions are the accepted content types and the object key suffix each gets.
var extensions = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/heic": ".heic"}

// Trip is the journal's header.
type Trip struct {
	ID       uuid.UUID
	Title    string
	CityName string
}

// Day is one planned day with its stops and note.
type Day struct {
	ID       uuid.UUID
	Number   int32
	Date     *time.Time
	CityName string
	Diary    string
	Stops    []Stop
}

// Stop is a planned stop and what happened to it.
type Stop struct {
	ID          uuid.UUID
	POIID       string
	Name        string
	Notes       string
	StartMinute *int32
	VisitedAt   *time.Time
	Verdict     int16
}

// Visit is a logged visit on the trip. LocalDate is visited_at in the
// account's own time zone (UTC when unset); Longitude, when the visit has
// one, dates it where it happened instead (see visitLocalDate).
type Visit struct {
	POIID     string
	Name      string
	Source    string
	VisitedAt time.Time
	LocalDate time.Time
	Longitude *float64
	Verdict   int16
}

// Photo is a trip_photos row.
type Photo struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	TripID      uuid.UUID
	DayID       *uuid.UUID
	StopID      *uuid.UUID
	Key         string
	ContentType string
	Size        int64
	Caption     string
	Status      string
	CreatedAt   time.Time
}

// Repository is the journal's storage. Every read and write that takes a
// userID scopes to that traveller and answers ErrNotFound otherwise.
type Repository interface {
	Trip(ctx context.Context, userID, tripID uuid.UUID) (Trip, error)
	Days(ctx context.Context, userID, tripID uuid.UUID) ([]Day, error)
	Visits(ctx context.Context, userID, tripID uuid.UUID) ([]Visit, error)
	ReadyPhotos(ctx context.Context, tripID uuid.UUID) ([]Photo, error)
	// DayTrip is the trip a day belongs to, when the trip is userID's.
	DayTrip(ctx context.Context, userID, dayID uuid.UUID) (uuid.UUID, error)
	// StopDay is the day a stop sits on, when that day is on tripID.
	StopDay(ctx context.Context, tripID, stopID uuid.UUID) (uuid.UUID, error)
	// DayOnTrip reports ErrNotFound unless dayID is on tripID.
	DayOnTrip(ctx context.Context, tripID, dayID uuid.UUID) error
	// SetDiary stores a day's note; an empty body removes it.
	SetDiary(ctx context.Context, tripID, dayID uuid.UUID, body string) error
	InsertPhoto(ctx context.Context, p Photo) error
	Photo(ctx context.Context, userID, photoID uuid.UUID) (Photo, error)
	MarkReady(ctx context.Context, photoID uuid.UUID, caption string) (Photo, error)
	DeletePhoto(ctx context.Context, userID, photoID uuid.UUID) (bool, error)
	// SweepPending removes pending photos created before cutoff.
	SweepPending(ctx context.Context, cutoff time.Time) (int64, error)
	// Tombstones are keys deleted before the given time, oldest first.
	Tombstones(ctx context.Context, before time.Time, limit int) ([]string, error)
	ClearTombstones(ctx context.Context, keys []string) error
}
