package placeintel

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/pkg/apierr"
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

// insertSubmission records the proposal and returns the row it now lives in,
// with that row's owner.
//
// Two conflicts are expected and neither is an error. A client id seen before
// is a retry, and gets its own row back. A place already pending in the same
// city under the same name is somebody else's proposal of the same place; the
// caller treats that as a confirmation, since the owner it gets back is not the
// person asking.
func (h *Handler) insertSubmission(ctx context.Context, tx pgx.Tx, s submission, clientID string) (uuid.UUID, uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO place_submissions
			(client_submission_id, user_id, city_id, name, category, latitude, longitude, address, website)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		clientID, s.UserID, s.CityID, s.Name, s.Category, s.Lat, s.Lng, s.Address, s.Website).Scan(&id)
	if err == nil {
		return id, s.UserID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert submission: %w", err))
	}

	var owner uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, user_id FROM place_submissions
		WHERE client_submission_id = $1 AND user_id = $2`, clientID, s.UserID).Scan(&id, &owner)
	if err == nil {
		return id, owner, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read retried submission: %w", err))
	}

	// The identity expression and predicate match idx_place_submissions_identity.
	err = tx.QueryRow(ctx, `
		SELECT id, user_id FROM place_submissions
		WHERE city_id = $1 AND lower(btrim(name)) = lower(btrim($2)) AND status <> 'rejected'`,
		s.CityID, s.Name).Scan(&id, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		// The only unique column left is client_submission_id, held by another
		// account. A client that reuses ids across accounts is broken.
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("client_submission_id is already in use"))
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read matching submission: %w", err))
	}
	return id, owner, nil
}

// resolveCityError keeps the resolver's two failure kinds apart. A name we
// cannot find is the user's to fix; a geocoder outage is not, and calling it a
// bad argument would tell them their correctly-spelled city is wrong.
func resolveCityError(name string, err error) error {
	wrapped := fmt.Errorf("could not place %q in a city: %w", name, err)
	switch {
	case errors.Is(err, cityrepo.ErrGeocoderUnavailable):
		return connect.NewError(connect.CodeUnavailable, wrapped)
	case errors.Is(err, cityrepo.ErrCityUnresolvable):
		return connect.NewError(connect.CodeInvalidArgument, wrapped)
	}
	return apierr.ToConnect(wrapped)
}

// creditPlaceContributors rewards the submitter and everybody who confirmed.
//
// Deliberately the same shape as creditCorroborators: reputation used to go
// only to whoever acted last, even though their vote was worth nothing without
// the others it agreed with.
func creditPlaceContributors(ctx context.Context, tx pgx.Tx, submissionID, submitter uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT user_id FROM place_submission_confirmations WHERE submission_id = $1`, submissionID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("list confirmers: %w", err))
	}
	defer rows.Close()

	credited := []uuid.UUID{submitter}
	seen := map[uuid.UUID]struct{}{submitter: {}}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("scan confirmer: %w", err))
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		credited = append(credited, id)
	}
	if err := rows.Err(); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("iterate confirmers: %w", err))
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO contributor_profiles (user_id, submitted_claims, accepted_claims, reputation)
		SELECT unnest($1::uuid[]), 0, 1, 3
		ON CONFLICT (user_id) DO UPDATE SET
			accepted_claims = contributor_profiles.accepted_claims + 1,
			reputation = LEAST(100, contributor_profiles.reputation + 3),
			updated_at = NOW()`, credited); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("credit place contributors: %w", err))
	}
	return nil
}
