package tripaction

import (
	"github.com/FACorreiaa/go-utils/pkg/util"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// ToProto is the card a client renders for p.
func ToProto(p *Proposal) *chatv1.ActionProposal {
	out := &chatv1.ActionProposal{
		Id: p.ID.String(), TripId: p.TripID.String(), Summary: p.Summary,
		Action: actionToProto(p.Action), ExpiresAt: timestamppb.New(p.ExpiresAt),
	}
	for _, o := range p.Options {
		po := &chatv1.ActionOption{Label: o.Label, Detail: o.Detail}
		switch {
		case o.Stay != nil:
			po.Choice = &chatv1.ActionOption_Stay{Stay: trip.StayToProto(*o.Stay)}
		case o.Flight != nil:
			po.Choice = &chatv1.ActionOption_Flight{Flight: trip.FlightToProto(*o.Flight)}
		}
		out.Options = append(out.Options, po)
	}
	out.Source = sources[p.Source]
	out.Status = statuses[p.Status]
	if p.Source == SourceEditor {
		out.BaseVersion = p.BaseVersion
	}
	if !p.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(p.CreatedAt)
	}
	return out
}

// ViewToProto is the card with who suggested it and how the trip feels.
func ViewToProto(v *View) *chatv1.ActionProposal {
	out := ToProto(&v.Proposal)
	out.Proposer = v.Proposer
	out.UpVotes, out.DownVotes, out.MyReaction = int32(v.Tally.Up), int32(v.Tally.Down), int32(v.Tally.Mine)
	return out
}

var sources = map[Source]chatv1.ProposalSource{
	SourceChat:   chatv1.ProposalSource_PROPOSAL_SOURCE_CHAT,
	SourceEditor: chatv1.ProposalSource_PROPOSAL_SOURCE_EDITOR,
}

var statuses = map[Status]chatv1.ProposalStatus{
	StatusPending:   chatv1.ProposalStatus_PROPOSAL_STATUS_PENDING,
	StatusApplied:   chatv1.ProposalStatus_PROPOSAL_STATUS_APPLIED,
	StatusDismissed: chatv1.ProposalStatus_PROPOSAL_STATUS_DISMISSED,
}

// ActionFromProto is the stop edit a member suggests from the editor. Other
// kinds come only from chat, so they are refused here.
func ActionFromProto(a *chatv1.TripAction) (Action, error) {
	stop := func(ps *tripv1.TripStop) *trip.TripStop {
		s := trip.StopFromProto(ps)
		return &s
	}
	switch k := a.GetKind().(type) {
	case *chatv1.TripAction_AddStop:
		return Action{Kind: KindAddStop, DayID: k.AddStop.GetDayId(), Stop: stop(k.AddStop.GetStop())}, nil
	case *chatv1.TripAction_RemoveStop:
		return Action{Kind: KindRemoveStop, StopID: k.RemoveStop.GetStopId()}, nil
	case *chatv1.TripAction_ReplaceStop:
		return Action{Kind: KindReplaceStop, StopID: k.ReplaceStop.GetStopId(), Stop: stop(k.ReplaceStop.GetReplacement())}, nil
	case *chatv1.TripAction_MoveStop:
		return Action{
			Kind: KindMoveStop, StopID: k.MoveStop.GetStopId(),
			ToDayID: k.MoveStop.GetToDayId(), ToIndex: int(k.MoveStop.GetToIndex()),
		}, nil
	}
	return Action{}, bad("only stop edits can be suggested from the trip")
}

func stopToProto(s *trip.TripStop) *tripv1.TripStop {
	if s == nil {
		return nil
	}
	out := trip.StopToProto(*s)
	if s.ID == uuid.Nil {
		out.Id = "" // not on the trip yet
	}
	return out
}

func actionToProto(a Action) *chatv1.TripAction {
	switch a.Kind {
	case KindSetDates:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_SetDates{SetDates: &chatv1.SetDatesAction{
			StartDate: a.StartDate, EndDate: a.EndDate,
		}}}
	case KindSearchHotels:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_SearchHotels{SearchHotels: &chatv1.SearchHotelsAction{
			CityName: a.City, MinStars: int32(a.MinStars), MaxStars: int32(a.MaxStars),
		}}}
	case KindRegenerateDays:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_RegenerateDays{RegenerateDays: &chatv1.RegenerateDaysAction{
			Days: int32(a.Days),
		}}}
	case KindSearchFlights:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_SearchFlights{SearchFlights: &chatv1.SearchFlightsAction{
			Origin:      &tripv1.FlightPlace{Name: a.Origin.Name, Iata: util.StrZeroPtr(a.Origin.IATA)},
			Destination: &tripv1.FlightPlace{Name: a.Destination.Name, Iata: util.StrZeroPtr(a.Destination.IATA)},
			DepartDate:  a.Depart, ReturnDate: util.StrZeroPtr(a.Return),
			Passengers: int32(a.Passengers), Cabin: tripv1.FlightCabin(cabins[a.Cabin]),
		}}}
	case KindAddStop:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_AddStop{AddStop: &chatv1.AddStopAction{
			DayId: a.DayID, Stop: stopToProto(a.Stop),
		}}}
	case KindRemoveStop:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_RemoveStop{RemoveStop: &chatv1.RemoveStopAction{StopId: a.StopID}}}
	case KindReplaceStop:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_ReplaceStop{ReplaceStop: &chatv1.ReplaceStopAction{
			StopId: a.StopID, Replacement: stopToProto(a.Stop),
		}}}
	case KindMoveStop:
		return &chatv1.TripAction{Kind: &chatv1.TripAction_MoveStop{MoveStop: &chatv1.MoveStopAction{
			StopId: a.StopID, ToDayId: a.ToDayID, ToIndex: int32(a.ToIndex),
		}}}
	}
	return &chatv1.TripAction{}
}
