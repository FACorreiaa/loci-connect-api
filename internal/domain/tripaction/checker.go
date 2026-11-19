package tripaction

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// checkerOfferTTL is how long an offer of the checker's stands. It is taken
// away sooner if it stops being true; this only stops one sitting forever on
// a trip nobody opens.
const checkerOfferTTL = 7 * 24 * time.Hour

// maxOpenOffers is how many of the checker's offers stand on a trip at once.
// A plan with more shut stops than this has a problem a longer list of cards
// would not fix; the next ones are offered as these are answered.
const maxOpenOffers = 3

// OfferMoves keeps the stop checker's offers on t in step with moves, which
// are all the moves that hold for the trip as it is now:
//
//   - a move with no open offer becomes one, addressed to the owner
//   - an open offer whose move no longer holds is taken away: the stop was
//     moved, removed or retimed, or the map's hours changed
//   - a move the owner already said no to is not offered again while that
//     dismissal is remembered
//
// It tells nobody. The card is there when the owner next looks at the trip's
// suggestions; a push for something nobody asked about would be noise.
// Errors are logged and never returned: this runs behind a trip read.
func (s *Service) OfferMoves(ctx context.Context, t *trip.Trip, moves []trip.StopMove) {
	if s.d.Checker == nil || s.d.Board == nil || t == nil {
		return
	}
	now := s.d.Now()
	since := now.Add(-closedWindow)
	all, err := s.d.Board.ListForTrip(ctx, t.ID, &since)
	if err != nil {
		s.d.Logger.WarnContext(ctx, "checker offers: listing failed", slog.Any("error", err))
		return
	}

	// Each move as the card it would be. A move whose stop or day is not on
	// the trip as loaded, or that cannot be worded, is no card.
	type card struct {
		action  Action
		summary string
	}
	want := make(map[string]card, len(moves))
	var order []string
	for _, m := range moves {
		a := Action{Kind: KindMoveStop, StopID: m.StopID.String(), ToDayID: m.ToDayID.String(), ToIndex: m.ToIndex}
		// The same vetting a member's suggestion passes.
		if _, err := checkStopEdit(a, t); err != nil {
			continue
		}
		summary, ok := moveSummary(t, m)
		if !ok {
			continue
		}
		if _, dup := want[a.StopID]; !dup {
			order = append(order, a.StopID)
		}
		want[a.StopID] = card{action: a, summary: summary}
	}
	// settled holds the stops needing no new offer: one stands, or the owner
	// turned this very move down.
	settled := map[string]bool{}
	open := 0
	for _, p := range all {
		if p.Source != SourceChecker {
			continue
		}
		c, wanted := want[p.Action.StopID]
		switch p.Status {
		case StatusPending:
			// It stands only while it is the card that would be written
			// now: same day, same place in the day, same hours in its text.
			if wanted && p.Action == c.action && p.Summary == c.summary && now.Before(p.ExpiresAt) {
				settled[p.Action.StopID] = true
				open++
				continue
			}
			if err := s.d.Checker.DeleteChecker(ctx, p.ID); err != nil {
				s.d.Logger.WarnContext(ctx, "checker offers: taking one back failed", slog.Any("error", err))
				// It still holds the stop's one slot; do not try to add beside it.
				settled[p.Action.StopID] = true
				open++
			}
		case StatusDismissed:
			// A no is to a day, not to a position or a wording.
			if wanted && p.Action.ToDayID == c.action.ToDayID {
				settled[p.Action.StopID] = true
			}
		case StatusApplied:
			// Applied and shut again means the trip changed since; offer anew.
		}
	}

	for _, stopID := range order {
		if open >= maxOpenOffers {
			break
		}
		if settled[stopID] {
			continue
		}
		c := want[stopID]
		p := Proposal{UserID: t.UserID, TripID: t.ID, Action: c.action, Summary: c.summary, ExpiresAt: now.Add(checkerOfferTTL)}
		created, err := s.d.Checker.CreateChecker(ctx, &p)
		if err != nil {
			s.d.Logger.WarnContext(ctx, "checker offers: storing failed", slog.Any("error", err))
			continue
		}
		if created {
			open++
			s.capture(t.UserID, "checker_move_offered", map[string]any{"trip_id": t.ID.String()})
		}
	}
}

// moveSummary words an offer: what is wrong, what the change is, and why the
// new day works. Every fact in it comes from the trip or the map's hours.
//
//	"Museu Nacional do Azulejo is shut on Mon 13 Apr. Move it to day 2
//	(Tue 14 Apr), when it is open 10:00–18:00."
func moveSummary(t *trip.Trip, m trip.StopMove) (string, bool) {
	stop, from := trip.FindStop(t, m.StopID)
	to := trip.FindDay(t, m.ToDayID)
	if stop == nil || from == nil || to == nil || from.Date == nil || to.Date == nil {
		return "", false
	}
	const day = "Mon 2 Jan"
	when := "when it is open"
	if m.Opens != 0 || m.Closes != 0 {
		when = fmt.Sprintf("when it is open %s–%s", clock(m.Opens), clock(m.Closes))
	}
	return fmt.Sprintf("%s is shut on %s. Move it to day %d (%s), %s.",
		stop.Name, from.Date.UTC().Format(day), to.DayNumber, to.Date.UTC().Format(day), when), true
}

// clock writes minutes from midnight as 24-hour time; past midnight wraps.
func clock(minutes int) string {
	minutes %= 24 * 60
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// compile-time: the service is what a trip read hands its moves to.
var _ trip.StopFixer = (*Service)(nil)
