package preference

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event names stored in preference_feedback.event.
const (
	EventSaved     = "saved"
	EventFavorited = "favorited"
	EventReordered = "reordered"
	EventExported  = "exported"
	EventSkipped   = "skipped"
	EventVisited   = "visited"
)

// Recorder writes preference learning signals. Best-effort — never fails callers.
type Recorder interface {
	Record(ctx context.Context, userID uuid.UUID, event string, opts RecordOpts)
}

type RecordOpts struct {
	POIID    string
	TripID   *uuid.UUID
	Weight   float32
	Metadata map[string]any
}

type recorder struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewRecorder(db *pgxpool.Pool, logger *slog.Logger) Recorder {
	if db == nil {
		return noop{}
	}
	return &recorder{db: db, logger: logger.With(slog.String("component", "preference-feedback"))}
}

func (r *recorder) Record(ctx context.Context, userID uuid.UUID, event string, opts RecordOpts) {
	if userID == uuid.Nil || event == "" {
		return
	}
	weight := opts.Weight
	if weight == 0 {
		weight = 1
	}
	meta := opts.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		raw = []byte("{}")
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO preference_feedback (user_id, poi_id, trip_id, event, weight, metadata)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6)`,
		userID, opts.POIID, opts.TripID, event, weight, raw)
	if err != nil && r.logger != nil {
		r.logger.WarnContext(ctx, "preference feedback insert failed", slog.Any("error", err))
	}
}

type noop struct{}

func (noop) Record(context.Context, uuid.UUID, string, RecordOpts) {}
