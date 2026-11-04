package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	assistantv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// giveBackTimeout bounds returning a claimed proposal after a failed write,
// which usually happens because the request's own context has ended.
const giveBackTimeout = 5 * time.Second

// Confirm carries out a proposal the caller has said yes to. This is the only
// way anything the assistant suggested gets written. Confirming twice writes
// once, and the second answer says the same thing as the first.
func (s *Service) Confirm(ctx context.Context, userID, proposalID uuid.UUID, option *int) (*assistantv1.ConfirmActionResponse, error) {
	place, err := s.d.Store.GetPlaceAction(ctx, proposalID, userID)
	switch {
	case err == nil:
		return s.confirmPlace(ctx, place)
	case !errors.Is(err, errNoPlaceAction):
		return nil, err
	}
	if s.d.Sources.Proposals == nil {
		return nil, tripaction.ErrNotFound
	}
	return s.confirmTrip(ctx, userID, proposalID, option)
}

func (s *Service) confirmTrip(ctx context.Context, userID, proposalID uuid.UUID, option *int) (*assistantv1.ConfirmActionResponse, error) {
	t, msg, err := s.d.Sources.Proposals.ApplyCurrent(ctx, userID, proposalID, option)
	if errors.Is(err, tripaction.ErrNotPending) {
		return s.confirmedAlready(ctx, userID, proposalID, err)
	}
	if err != nil {
		return nil, err
	}
	res := &assistantv1.ConfirmActionResponse{}
	if msg != nil {
		res.Confirmation = msg.Content
	}
	// As its owner sees it, share code and all: a co-planner reads it with GetTrip.
	if t != nil && t.UserID == userID {
		res.Trip = trip.ToProto(t)
	}
	return res, nil
}

// confirmedAlready answers a second confirm of an applied card. A dismissed one
// stays an error: the answer to "do it" there is that it was called off.
func (s *Service) confirmedAlready(ctx context.Context, userID, proposalID uuid.UUID, notPending error) (*assistantv1.ConfirmActionResponse, error) {
	p, err := s.d.Sources.Proposals.Applicable(ctx, userID, proposalID)
	if err != nil {
		return nil, err
	}
	if p.Status != tripaction.StatusApplied {
		return nil, notPending
	}
	res := &assistantv1.ConfirmActionResponse{Confirmation: p.Summary}
	if s.d.Sources.Members != nil {
		if t, role, err := s.d.Sources.Members.Resolve(ctx, userID, p.TripID); err == nil && t != nil && role == trip.RoleOwner {
			res.Trip = trip.ToProto(t)
		}
	}
	return res, nil
}

func (s *Service) confirmPlace(ctx context.Context, a *PlaceAction) (*assistantv1.ConfirmActionResponse, error) {
	switch {
	case a.Status == placeStatusApplied:
		return placeConfirmation(a), nil
	case a.Status != placeStatusPending:
		return nil, tripaction.ErrNotPending
	case !s.d.Now().Before(a.ExpiresAt):
		return nil, tripaction.ErrExpired
	}
	// Claimed before the write and given back if it fails, as trip cards are, so
	// a double tap writes once and a failure can be retried.
	claimed, err := s.d.Store.MovePlaceAction(ctx, a.ID, a.UserID, placeStatusPending, placeStatusApplied)
	if err != nil {
		return nil, err
	}
	if !claimed {
		current, err := s.d.Store.GetPlaceAction(ctx, a.ID, a.UserID)
		if err != nil {
			return nil, err
		}
		if current.Status == placeStatusApplied {
			return placeConfirmation(current), nil
		}
		return nil, tripaction.ErrNotPending
	}
	if err := s.writePlace(ctx, a); err != nil {
		backCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveBackTimeout)
		defer cancel()
		if _, rerr := s.d.Store.MovePlaceAction(backCtx, a.ID, a.UserID, placeStatusApplied, placeStatusPending); rerr != nil {
			s.d.Logger.WarnContext(ctx, "assistant: could not give a failed card back", slog.Any("error", rerr))
		}
		return nil, err
	}
	return placeConfirmation(a), nil
}

func (s *Service) writePlace(ctx context.Context, a *PlaceAction) error {
	src := s.d.Sources
	switch a.Kind {
	case placeKindFavourite:
		if src.Favourites == nil {
			return errors.New("assistant: no favourites writer")
		}
		_, err := src.Favourites.AddPoiToFavourites(ctx, a.UserID, a.POIID, false)
		return err
	case placeKindList:
		if src.ListItems == nil || a.ListID == nil {
			return errors.New("assistant: no list writer")
		}
		_, err := src.ListItems.AddListItem(ctx, a.UserID, *a.ListID, locitypes.AddListItemRequest{
			ItemID: a.POIID, ContentType: locitypes.ContentTypePOI,
		})
		// Added some other way since the card went up: what they asked for is true.
		if errors.Is(err, locitypes.ErrConflict) {
			return nil
		}
		return err
	default:
		return fmt.Errorf("assistant: unknown place action %q", a.Kind)
	}
}

func placeConfirmation(a *PlaceAction) *assistantv1.ConfirmActionResponse {
	if a.Kind == placeKindList && a.ListName != nil {
		return &assistantv1.ConfirmActionResponse{Confirmation: fmt.Sprintf("Added %s to %s.", a.POIName, *a.ListName)}
	}
	return &assistantv1.ConfirmActionResponse{Confirmation: fmt.Sprintf("Saved %s to your favourites.", a.POIName)}
}

// Dismiss drops a proposal without carrying it out.
func (s *Service) Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error {
	place, err := s.d.Store.GetPlaceAction(ctx, proposalID, userID)
	switch {
	case err == nil:
		return s.dismissPlace(ctx, place)
	case !errors.Is(err, errNoPlaceAction):
		return err
	}
	if s.d.Sources.Proposals == nil {
		return tripaction.ErrNotFound
	}
	return s.d.Sources.Proposals.Dismiss(ctx, userID, proposalID)
}

func (s *Service) dismissPlace(ctx context.Context, a *PlaceAction) error {
	if a.Status == placeStatusDismissed {
		return nil
	}
	moved, err := s.d.Store.MovePlaceAction(ctx, a.ID, a.UserID, placeStatusPending, placeStatusDismissed)
	if err != nil {
		return err
	}
	if !moved {
		return tripaction.ErrNotPending
	}
	return nil
}
