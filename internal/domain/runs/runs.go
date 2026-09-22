// Package runs records each chat generation — running, done or failed — so
// a finished search can be announced to someone who stopped watching it,
// and caps how many one person can have in flight.
package runs

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

const (
	// MaxConcurrent is how many generations one person may have running.
	MaxConcurrent = 3
	// StaleAfter is well past the pipeline's own 3–5 minute timeouts, so a
	// row still running after it belongs to a pod that died.
	StaleAfter        = 10 * time.Minute
	ErrorCodeDeadline = "deadline_exceeded"
)

// ErrAtCapacity means the caller already has MaxConcurrent runs going.
var ErrAtCapacity = errors.New("runs: at capacity")

type Run struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	SessionID  uuid.UUID // uuid.Nil until the start event attaches it
	Domain     string
	CityName   string
	Status     Status
	ErrorCode  string
	StartedAt  time.Time
	FinishedAt *time.Time
}

// effective reports a stale running row as the failure it is.
func (r Run) effective(now time.Time) Run {
	if r.Status == StatusRunning && now.Sub(r.StartedAt) > StaleAfter {
		r.Status = StatusFailed
		r.ErrorCode = ErrorCodeDeadline
	}
	return r
}
