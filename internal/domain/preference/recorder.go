package preference

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
// Nothing is written for a traveller who switched personalization off.
type Recorder interface {
	Record(ctx context.Context, userID uuid.UUID, event string, opts RecordOpts)
	// Replace drops this user's signals whose metadata[key] == value, then
	// records the new one, so a revised verdict (an edited review) replaces
	// the old signal instead of stacking with it. opts.Metadata should carry
	// key=value so the next Replace finds it.
	Replace(ctx context.Context, userID uuid.UUID, key, value, event string, opts RecordOpts)
	// Forget drops this user's signals whose metadata[key] == value (a deleted
	// review or visit stops teaching taste).
	Forget(ctx context.Context, userID uuid.UUID, key, value string)
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

// insertFeedback is shared by Record and Replace. The personalization check
// lives in the statement so an opted-out traveller is never learned from,
// whichever domain records the signal. No settings row means the default (on).
const insertFeedback = `
	INSERT INTO preference_feedback (user_id, poi_id, trip_id, event, weight, metadata)
	SELECT $1, NULLIF($2, ''), $3, $4, $5, $6
	WHERE COALESCE((
		SELECT personalization_enabled FROM personalization_settings WHERE user_id = $1
	), TRUE)`

// recordTimeout bounds one best-effort write once it is detached from the
// request.
const recordTimeout = 5 * time.Second

// detached runs a signal write independently of the request: signals are
// recorded after the caller's main write commits, and a client that drops the
// connection then must not lose the signal.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
}

func feedbackArgs(opts RecordOpts) (weight float32, raw []byte) {
	weight = opts.Weight
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
	return weight, raw
}

func (r *recorder) Record(ctx context.Context, userID uuid.UUID, event string, opts RecordOpts) {
	if userID == uuid.Nil || event == "" {
		return
	}
	ctx, cancel := detached(ctx)
	defer cancel()
	weight, raw := feedbackArgs(opts)
	_, err := r.db.Exec(ctx, insertFeedback, userID, opts.POIID, opts.TripID, event, weight, raw)
	if err != nil && r.logger != nil {
		r.logger.WarnContext(ctx, "preference feedback insert failed", slog.Any("error", err))
	}
}

func (r *recorder) Replace(ctx context.Context, userID uuid.UUID, key, value, event string, opts RecordOpts) {
	if userID == uuid.Nil || event == "" || key == "" || value == "" {
		return
	}
	ctx, cancel := detached(ctx)
	defer cancel()
	weight, raw := feedbackArgs(opts)
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM preference_feedback WHERE user_id = $1 AND metadata->>$2 = $3`,
			userID, key, value); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, insertFeedback, userID, opts.POIID, opts.TripID, event, weight, raw)
		return err
	})
	if err != nil && r.logger != nil {
		r.logger.WarnContext(ctx, "preference feedback replace failed", slog.Any("error", err))
	}
}

func (r *recorder) Forget(ctx context.Context, userID uuid.UUID, key, value string) {
	if userID == uuid.Nil || key == "" || value == "" {
		return
	}
	ctx, cancel := detached(ctx)
	defer cancel()
	_, err := r.db.Exec(ctx, `
		DELETE FROM preference_feedback WHERE user_id = $1 AND metadata->>$2 = $3`,
		userID, key, value)
	if err != nil && r.logger != nil {
		r.logger.WarnContext(ctx, "preference feedback forget failed", slog.Any("error", err))
	}
}

type noop struct{}

func (noop) Record(context.Context, uuid.UUID, string, RecordOpts) {}

func (noop) Replace(context.Context, uuid.UUID, string, string, string, RecordOpts) {}

func (noop) Forget(context.Context, uuid.UUID, string, string) {}
