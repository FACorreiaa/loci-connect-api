package placeintel

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// placesRequiredForPromotion is how many distinct people must say a place
// exists before it becomes one. The submitter is the first, so in practice one
// other person is needed — the same shape as the claim flow, where filing a
// report is itself the first observation.
const placesRequiredForPromotion = 2

// submission is a place somebody says exists, before anybody has agreed.
type submission struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	CityID        uuid.UUID
	Name          string
	CityName      string
	Category      *string
	Address       *string
	Website       *string
	Lat           *float64
	Lng           *float64
	Status        string
	POIID         *uuid.UUID
	Confirmations int32
}

// confirmationsNeeded is how many more people must speak up, never negative.
func confirmationsNeeded(confirmations int32) int32 {
	needed := placesRequiredForPromotion - 1 - confirmations
	if needed < 0 {
		return 0
	}
	return needed
}

// existingPOIByIdentity reports whether this place is already on the guide.
//
// The expression matches migration 0068's unique index, which is what
// UpsertPOIByIdentity conflicts on, so "already exists" means exactly what it
// means everywhere else.
func (h *Handler) existingPOIByIdentity(ctx context.Context, cityID uuid.UUID, name string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := h.db.QueryRow(ctx, `
		SELECT id FROM points_of_interest
		WHERE city_id = $1 AND lower(btrim(name)) = lower(btrim($2))`, cityID, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, connect.NewError(connect.CodeInternal, fmt.Errorf("look up existing place: %w", err))
	}
	return id, true, nil
}

// insertSubmission records the proposal, or returns the existing row when this
// client id has been seen before so a retry cannot create a second one.
func (h *Handler) insertSubmission(ctx context.Context, tx pgx.Tx, s submission, clientID string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO place_submissions
			(client_submission_id, user_id, city_id, name, category, latitude, longitude, address, website)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (client_submission_id) DO NOTHING
		RETURNING id`,
		clientID, s.UserID, s.CityID, s.Name, s.Category, s.Lat, s.Lng, s.Address, s.Website).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			SELECT id FROM place_submissions WHERE client_submission_id = $1`, clientID).Scan(&id); err != nil {
			return uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read duplicate submission: %w", err))
		}
		return id, nil
	}
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert submission: %w", err))
	}
	return id, nil
}
