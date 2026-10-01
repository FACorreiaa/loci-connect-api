package tripaction

import (
	"github.com/FACorreiaa/go-utils/pkg/util"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
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
	}
	return &chatv1.TripAction{}
}
