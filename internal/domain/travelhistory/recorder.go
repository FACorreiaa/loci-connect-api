package travelhistory

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// repoRecorder writes visits through the repository, best-effort.
type repoRecorder struct {
	repo Repository
	log  *slog.Logger
	// timeout bounds the write so a slow database cannot hold up the caller's
	// request. The visit is a side effect of their action, not the action.
	timeout time.Duration
}

// NewRecorder returns a Recorder backed by repo. A nil repo yields a no-op, so
// call sites never need a nil check and the domain stays optional.
func NewRecorder(repo Repository, log *slog.Logger) Recorder {
	if repo == nil {
		return NopRecorder{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &repoRecorder{
		repo:    repo,
		log:     log.With(slog.String("component", "travelhistory-recorder")),
		timeout: 3 * time.Second,
	}
}

// RecordVisit persists a visit and swallows every failure.
//
// Deliberate: this is called from the recommendation event path, where the
// user's actual request is "record that I confirmed this stop". Failing that
// request because a history row could not be written would trade a real feature
// for a derived one. Same policy as preference.Recorder.
func (r *repoRecorder) RecordVisit(ctx context.Context, userID uuid.UUID, in VisitInput) {
	// Detach from the caller's cancellation but keep its values, so the write
	// still completes if the client disconnects mid-response.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
	defer cancel()

	if _, err := r.repo.RecordVisit(ctx, userID, in); err != nil {
		r.log.Warn("record visit failed",
			slog.String("user_id", userID.String()),
			slog.String("city", in.CityName),
			slog.String("error", err.Error()))
	}
}
