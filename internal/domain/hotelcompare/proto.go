package hotelcompare

import (
	"time"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/hotelrates"
	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

// Stays are the report's hotels as stays ready to save, in report order.
func (r *Report) Stays() []trip.TripStay {
	out := make([]trip.TripStay, len(r.Hotels))
	for i, h := range r.Hotels {
		out[i] = h.Stay
	}
	return out
}

// ToProto is the card a client renders. stayProposalID names the proposal
// that offers the same hotels, or is empty when there is none.
func ToProto(r *Report, stayProposalID string) *chatv1.HotelComparisonReport {
	out := &chatv1.HotelComparisonReport{
		Id: r.ID.String(), Verdict: r.Verdict, Caveats: r.Caveats, WebSearchUsed: r.WebSearchUsed,
		GeneratedAt: timestamppb.New(r.GeneratedAt), CityName: r.City, StayProposalId: stayProposalID,
		CheckIn: date(r.CheckIn), CheckOut: date(r.CheckOut),
	}
	if r.Recommended != nil {
		i := int32(*r.Recommended)
		out.RecommendedIndex = &i
	}
	mapped := false
	for _, h := range r.Hotels {
		ph := &chatv1.ComparedHotel{
			SourceUrl: h.URL, Source: h.Source, Identified: h.Identified, Stay: trip.StayToProto(h.Stay),
			UserNote: h.Note, Facilities: h.Facilities, Neighbourhood: h.Neighbourhood, Pros: h.Pros, Cons: h.Cons,
			LivePrice: priceToProto(h.LivePrice),
		}
		for _, s := range h.Stops {
			mapped = true
			ph.NearbyStops = append(ph.NearbyStops, &tripv1.TransitStop{
				Name: s.Name, Mode: modes[s.Mode], Lines: s.Lines, Latitude: s.Lat, Longitude: s.Lon,
				DistanceM: int32(s.DistanceM), WalkMinutes: int32(transit.EstimateWalkMinutes(s.DistanceM)),
			})
		}
		for _, hl := range h.Highlights {
			mapped = true
			w, ok := highlightWords[hl.Kind]
			if !ok {
				continue
			}
			ph.Nearby = append(ph.Nearby, &chatv1.NearbyHighlight{
				Label: w[1], Count: int32(hl.Count), NearestName: hl.NearestName,
				WalkMinutes: int32(transit.EstimateWalkMinutes(hl.NearestM)),
			})
		}
		out.Hotels = append(out.Hotels, ph)
	}
	for _, row := range r.Rows {
		out.Rows = append(out.Rows, &chatv1.ComparisonRow{Label: row.Label, Values: row.Values})
	}
	for _, s := range r.Sources {
		out.Sources = append(out.Sources, &chatv1.ReportSource{Title: s.Title, Url: s.URL, Publisher: s.Publisher})
	}
	if mapped {
		out.Attribution = transit.Attribution
	}
	return out
}

func date(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func priceToProto(r *hotelrates.Rate) *chatv1.LivePrice {
	if r == nil || r.Status != hotelrates.StatusPriced {
		return nil
	}
	return &chatv1.LivePrice{
		NightlyMinor: r.NightlyMinor, TotalMinor: r.TotalMinor, Currency: r.Currency,
		Provider: r.Provider, FetchedAt: timestamppb.New(r.FetchedAt), BookingUrl: r.BookingURL,
	}
}

var modes = map[transit.Mode]tripv1.TransitMode{
	transit.ModeMetro: tripv1.TransitMode_TRANSIT_MODE_METRO,
	transit.ModeTrain: tripv1.TransitMode_TRANSIT_MODE_TRAIN,
	transit.ModeTram:  tripv1.TransitMode_TRANSIT_MODE_TRAM,
	transit.ModeBus:   tripv1.TransitMode_TRANSIT_MODE_BUS,
	transit.ModeFerry: tripv1.TransitMode_TRANSIT_MODE_FERRY,
}

// Summary is the report in a few plain lines, for a model that has to tell
// the traveller what the card says. It holds only what the card holds.
func (r *Report) Summary() string {
	var b []byte
	add := func(s string) { b = append(append(b, s...), '\n') }
	if r.Verdict != "" {
		add("Verdict: " + r.Verdict)
	} else {
		add("No verdict could be written; the card lists the facts that could be checked.")
	}
	for i, h := range r.Hotels {
		line := "Hotel " + string(rune('1'+i)) + ": " + h.Stay.Name
		if !h.Identified {
			line += " (not matched to a known hotel)"
		}
		if h.Note != "" {
			line += "; the traveller said: " + h.Note
		}
		add(line)
	}
	for _, c := range r.Caveats {
		add("Could not check: " + c)
	}
	return string(b)
}
