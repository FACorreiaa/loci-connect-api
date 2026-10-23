package tripaction

import (
	"context"
	"errors"
	"log/slog"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// Planning together: anyone on a trip sees its proposals and reacts to
// them, co-planners suggest stop edits from the editor, and only the owner
// applies. The owner's own chat cards are the same proposals, so they show
// up for the trip's members too.

// memberProposalTTL: a suggestion waits for the owner to look, which can take
// longer than a chat card's day.
const memberProposalTTL = 7 * 24 * time.Hour

// closedWindow is how far back List looks for applied and dismissed ones.
const closedWindow = 30 * 24 * time.Hour

// ErrForbidden: the caller is on the trip but their role does not allow it.
var ErrForbidden = errors.New("your role on this trip does not allow that")

// ErrTogetherOff: the service runs without planning together wired in.
var ErrTogetherOff = errors.New("planning together is not enabled")

// Members is who plans a trip together (*trip.MemberAccess).
type Members interface {
	Resolve(ctx context.Context, viewer, tripID uuid.UUID) (*trip.Trip, trip.Role, error)
	Everyone(ctx context.Context, t *trip.Trip) ([]uuid.UUID, error)
	PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error)
}

// TripNotifier tells a trip's people about a change (*push.Notifier).
type TripNotifier interface {
	TripChanged(ctx context.Context, to []uuid.UUID, tripID uuid.UUID, title, body string)
}

// Analytics records product events (*analytics.Recorder).
type Analytics interface {
	Capture(userID, event string, properties map[string]any)
}

// View is a proposal as one person on the trip sees it.
type View struct {
	Proposal
	Proposer *socialv1.PublicUser
	Tally    Tally
}

func (s *Service) together() bool { return s.d.Members != nil && s.d.Board != nil }

// onTrip resolves the caller's role on a trip; a trip they are not on is
// not found.
func (s *Service) onTrip(ctx context.Context, userID, tripID uuid.UUID) (*trip.Trip, trip.Role, error) {
	if !s.together() {
		return nil, trip.RoleNone, ErrTogetherOff
	}
	return s.d.Members.Resolve(ctx, userID, tripID)
}

// ProposeEdit stores a stop edit a co-planner (or the owner) suggests from
// the editor and tells the owner. baseVersion is the trip version they were
// looking at; it is kept for the record, not enforced.
func (s *Service) ProposeEdit(ctx context.Context, userID, tripID uuid.UUID, a Action, baseVersion int64) (*View, error) {
	if !a.Kind.IsStopEdit() {
		return nil, bad("%q cannot be suggested from the editor", a.Kind)
	}
	t, role, err := s.onTrip(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}
	if !trip.CanEdit(userID, t.UserID, role, false) {
		return nil, ErrForbidden
	}
	if a.Stop != nil {
		// The trip picks the new stop's id when the edit is applied.
		stop := *a.Stop
		stop.ID = uuid.Nil
		a.Stop = &stop
	}
	summary, err := checkStopEdit(a, t)
	if err != nil {
		return nil, err
	}
	p := Proposal{
		UserID: userID, TripID: tripID, Action: a, Summary: summary,
		ExpiresAt: s.d.Now().Add(memberProposalTTL), Source: SourceEditor, BaseVersion: baseVersion,
	}
	if err := s.d.Store.Create(ctx, &p); err != nil {
		return nil, err
	}
	s.capture(userID, "trip_proposal_created", map[string]any{
		"trip_id": tripID.String(), "source": string(SourceEditor), "kind": string(a.Kind),
	})
	if userID != t.UserID {
		s.notify(ctx, []uuid.UUID{t.UserID}, t, s.nameOf(ctx, userID)+" suggested: "+summary)
	}
	views, err := s.views(ctx, userID, []Proposal{p})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// List is a trip's open proposals for anyone on it, newest first; with
// includeClosed, also those applied or dismissed in the last 30 days.
func (s *Service) List(ctx context.Context, userID, tripID uuid.UUID, includeClosed bool) ([]View, error) {
	if _, _, err := s.onTrip(ctx, userID, tripID); err != nil {
		return nil, err
	}
	now := s.d.Now()
	var since *time.Time
	if includeClosed {
		t := now.Add(-closedWindow)
		since = &t
	}
	all, err := s.d.Board.ListForTrip(ctx, tripID, since)
	if err != nil {
		return nil, err
	}
	keep := all[:0]
	for _, p := range all {
		if p.Status == StatusPending && !now.Before(p.ExpiresAt) {
			continue
		}
		keep = append(keep, p)
	}
	return s.views(ctx, userID, keep)
}

// React sets the caller's 👍 (1) / 👎 (-1) on an open proposal; 0 takes it back.
func (s *Service) React(ctx context.Context, userID, proposalID uuid.UUID, value int) (*View, error) {
	if value < -1 || value > 1 {
		return nil, bad("reaction %d", value)
	}
	p, _, _, err := s.proposalOnTrip(ctx, userID, proposalID)
	if err != nil {
		return nil, err
	}
	if p.Status != StatusPending {
		return nil, ErrNotPending
	}
	if err := s.d.Board.React(ctx, p.ID, userID, value); err != nil {
		return nil, err
	}
	views, err := s.views(ctx, userID, []Proposal{*p})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// proposalOnTrip loads any proposal on a trip the caller is on. A proposal on
// a trip they are not on is not found.
func (s *Service) proposalOnTrip(ctx context.Context, userID, proposalID uuid.UUID) (*Proposal, *trip.Trip, trip.Role, error) {
	if !s.together() {
		return nil, nil, trip.RoleNone, ErrTogetherOff
	}
	p, err := s.d.Board.GetByID(ctx, proposalID)
	if err != nil {
		return nil, nil, trip.RoleNone, err
	}
	t, role, err := s.d.Members.Resolve(ctx, userID, p.TripID)
	if errors.Is(err, trip.ErrNotFound) {
		return nil, nil, trip.RoleNone, ErrNotFound
	}
	if err != nil {
		return nil, nil, trip.RoleNone, err
	}
	return p, t, role, nil
}

// applicable is the proposal userID may apply: without planning together,
// their own card; with it, any card on a trip they own.
func (s *Service) applicable(ctx context.Context, userID, proposalID uuid.UUID) (*Proposal, error) {
	if !s.together() {
		return s.d.Store.Get(ctx, proposalID, userID)
	}
	p, _, role, err := s.proposalOnTrip(ctx, userID, proposalID)
	if err != nil {
		return nil, err
	}
	if role != trip.RoleOwner {
		return nil, ErrForbidden
	}
	return p, nil
}

// dismissable is the proposal userID may dismiss: their own, or any on a
// trip they own.
func (s *Service) dismissable(ctx context.Context, userID, proposalID uuid.UUID) (*Proposal, error) {
	if !s.together() {
		return s.d.Store.Get(ctx, proposalID, userID)
	}
	p, err := s.d.Board.GetByID(ctx, proposalID)
	if err != nil {
		return nil, err
	}
	if p.UserID == userID {
		return p, nil
	}
	if _, err := s.applicable(ctx, userID, proposalID); err != nil {
		return nil, err
	}
	return p, nil
}

// applied records who applied p and tells everyone else on the trip. Both are
// best effort: the trip has already changed.
func (s *Service) applied(ctx context.Context, userID uuid.UUID, p *Proposal, t *trip.Trip, what string) {
	if !s.together() {
		return
	}
	if err := s.d.Board.MarkApplied(ctx, p.ID, userID); err != nil {
		s.d.Logger.WarnContext(ctx, "trip action: could not record who applied it", slog.Any("error", err))
	}
	everyone, err := s.d.Members.Everyone(ctx, t)
	if err != nil {
		s.d.Logger.WarnContext(ctx, "trip action: list trip members", slog.Any("error", err))
		return
	}
	if len(everyone) < 2 {
		return // a trip planned alone has no one to tell, and no together event
	}
	s.capture(userID, "trip_proposal_applied", map[string]any{
		"trip_id": p.TripID.String(), "source": string(p.Source), "kind": string(p.Action.Kind),
		"own": p.UserID == userID,
	})
	body := s.nameOf(ctx, userID) + " changed the trip: " + what
	if p.UserID != userID {
		body = s.nameOf(ctx, userID) + " took " + s.nameOf(ctx, p.UserID) + "'s suggestion: " + what
	}
	to := make([]uuid.UUID, 0, len(everyone))
	for _, id := range everyone {
		if id != userID {
			to = append(to, id)
		}
	}
	s.notify(ctx, to, t, body)
}

func (s *Service) views(ctx context.Context, viewer uuid.UUID, ps []Proposal) ([]View, error) {
	ids := make([]uuid.UUID, 0, len(ps))
	seen := map[uuid.UUID]bool{}
	var people []uuid.UUID
	for _, p := range ps {
		ids = append(ids, p.ID)
		if !seen[p.UserID] {
			seen[p.UserID] = true
			people = append(people, p.UserID)
		}
	}
	tallies, err := s.d.Board.Tallies(ctx, ids, viewer)
	if err != nil {
		return nil, err
	}
	cards, err := s.d.Members.PublicUsers(ctx, people)
	if err != nil {
		// A card without a name still reads; the proposal is what matters.
		s.d.Logger.WarnContext(ctx, "trip action: proposer cards", slog.Any("error", err))
	}
	out := make([]View, 0, len(ps))
	for _, p := range ps {
		out = append(out, View{Proposal: p, Proposer: cards[p.UserID], Tally: tallies[p.ID]})
	}
	return out, nil
}

func (s *Service) nameOf(ctx context.Context, id uuid.UUID) string {
	if s.d.Members != nil {
		cards, err := s.d.Members.PublicUsers(ctx, []uuid.UUID{id})
		if err == nil {
			if c := cards[id]; c != nil {
				switch {
				case c.GetDisplayName() != "":
					return c.GetDisplayName()
				case c.GetUsername() != "":
					return c.GetUsername()
				}
			}
		}
	}
	return "Someone"
}

func (s *Service) notify(ctx context.Context, to []uuid.UUID, t *trip.Trip, body string) {
	if s.d.Notify == nil || len(to) == 0 {
		return
	}
	s.d.Notify.TripChanged(ctx, to, t.ID, tripTitle(t), body)
}

func tripTitle(t *trip.Trip) string {
	switch {
	case t.Title != "":
		return t.Title
	case t.CityName != "":
		return t.CityName + " trip"
	default:
		return "Your trip"
	}
}

func (s *Service) capture(userID uuid.UUID, event string, props map[string]any) {
	if s.d.Analytics != nil {
		s.d.Analytics.Capture(userID.String(), event, props)
	}
}
