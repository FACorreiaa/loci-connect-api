package trip

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/recommendation"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/trip"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newShareCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)[:10]
}

func paginationMeta(page, pageSize, total int) *commonpb.PaginationMetadata {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return &commonpb.PaginationMetadata{
		TotalRecords: int32(total),
		Page:         int32(page),
		PageSize:     int32(pageSize),
		TotalPages:   int32(totalPages),
		HasMore:      page < totalPages,
	}
}

// ---- proto -> domain ----

func tripFromProto(p *tripv1.TripDraft, uid uuid.UUID) (*Trip, error) {
	if p == nil {
		return nil, fmt.Errorf("trip is required")
	}
	t := &Trip{
		UserID:      uid,
		CityName:    p.GetCityName(),
		Title:       p.GetTitle(),
		Constraints: constraintFromProto(p.GetConstraints()),
	}
	if p.GetId() != "" {
		id, err := uuid.Parse(p.GetId())
		if err != nil {
			return nil, fmt.Errorf("invalid trip id: %w", err)
		}
		t.ID = id
	}
	if p.CityId != nil && p.GetCityId() != "" {
		cid, err := uuid.Parse(p.GetCityId())
		if err != nil {
			return nil, fmt.Errorf("invalid city id: %w", err)
		}
		t.CityID = &cid
	}
	if p.SourceSessionId != nil && p.GetSourceSessionId() != "" {
		s := p.GetSourceSessionId()
		t.SourceSessionID = &s
	}
	for _, pd := range p.GetDays() {
		d := TripDay{
			DayNumber: pd.GetDayNumber(),
			CityName:  pd.GetCityName(),
			TravelDay: pd.GetTravelDay(),
		}
		if pd.Date != nil {
			dt := pd.GetDate().AsTime()
			d.Date = &dt
		}
		// A malformed city id is not worth failing the whole save over — the day
		// keeps its name and loses only the link.
		if pd.CityId != nil && pd.GetCityId() != "" {
			if cid, err := uuid.Parse(pd.GetCityId()); err == nil {
				d.CityID = &cid
			}
		}
		if pd.CityLat != nil {
			lat := pd.GetCityLat()
			d.CityLat = &lat
		}
		if pd.CityLon != nil {
			lon := pd.GetCityLon()
			d.CityLon = &lon
		}
		for _, ps := range pd.GetStops() {
			d.Stops = append(d.Stops, stopFromProto(ps))
		}
		t.Days = append(t.Days, d)
	}
	for _, pl := range p.GetLegs() {
		t.Legs = append(t.Legs, legFromProto(pl))
	}
	return t, nil
}

func constraintFromProto(p *tripv1.TripConstraint) TripConstraint {
	if p == nil {
		return TripConstraint{}
	}
	c := TripConstraint{
		Pace:      int32(p.GetPace()),
		Mobility:  p.GetMobility(),
		Interests: p.GetInterests(),
	}
	if p.BudgetLevel != nil {
		v := p.GetBudgetLevel()
		c.BudgetLevel = &v
	}
	if p.DayStartMinute != nil {
		v := p.GetDayStartMinute()
		c.DayStartMinute = &v
	}
	if p.DayEndMinute != nil {
		v := p.GetDayEndMinute()
		c.DayEndMinute = &v
	}
	return c
}

// ---- domain -> proto ----

func tripToProto(t *Trip) *tripv1.TripDraft {
	p := &tripv1.TripDraft{
		Id:          t.ID.String(),
		UserId:      t.UserID.String(),
		CityName:    t.CityName,
		Title:       t.Title,
		Constraints: constraintToProto(t.Constraints),
		Version:     t.Version,
		CreatedAt:   timestamppb.New(t.CreatedAt),
		UpdatedAt:   timestamppb.New(t.UpdatedAt),
	}
	if t.CityID != nil {
		s := t.CityID.String()
		p.CityId = &s
	}
	if t.SourceSessionID != nil {
		p.SourceSessionId = t.SourceSessionID
	}
	for _, d := range t.Days {
		pd := &tripv1.TripDay{
			Id:        d.ID.String(),
			DayNumber: d.DayNumber,
			CityName:  d.CityName,
			TravelDay: d.TravelDay,
			CityLat:   d.CityLat,
			CityLon:   d.CityLon,
		}
		if d.CityID != nil {
			cid := d.CityID.String()
			pd.CityId = &cid
		}
		if d.Date != nil {
			pd.Date = timestamppb.New(*d.Date)
		}
		for _, s := range d.Stops {
			ps := &tripv1.TripStop{
				Id:              s.ID.String(),
				PoiId:           s.POIID,
				OrderIndex:      s.OrderIndex,
				Name:            s.Name,
				Notes:           s.Notes,
				StartMinute:     s.StartMinute,
				DurationMinutes: s.DurationMinutes,
				BookingUrl:      s.BookingURL,
			}
			ps.RecommendationTrace = traceToProto(s.RecommendationTrace)
			pd.Stops = append(pd.Stops, ps)
		}
		p.Days = append(p.Days, pd)
	}
	for _, l := range t.Legs {
		p.Legs = append(p.Legs, legToProto(l))
	}
	return p
}

// legFromProto / legToProto convert travel between cities. The proto id is
// ignored on the way in: legs are replace-all on save, so the database assigns
// identity.
func legFromProto(pl *tripv1.TripLeg) TripLeg {
	l := TripLeg{
		AfterDay:     pl.GetAfterDay(),
		FromName:     pl.GetFromName(),
		ToName:       pl.GetToName(),
		DistanceKm:   pl.GetDistanceKm(),
		DurationMins: pl.GetDurationMins(),
		Mode:         pl.GetMode(),
		BookingURL:   pl.BookingUrl,
	}
	if l.Mode == "" {
		l.Mode = "drive"
	}
	// Coordinates are plain doubles on the wire; zero means "not supplied", and
	// 0,0 is in the Atlantic, so treating it as absent is safe.
	if lat := pl.GetFromLat(); lat != 0 {
		l.FromLat = &lat
	}
	if lon := pl.GetFromLon(); lon != 0 {
		l.FromLon = &lon
	}
	if lat := pl.GetToLat(); lat != 0 {
		l.ToLat = &lat
	}
	if lon := pl.GetToLon(); lon != 0 {
		l.ToLon = &lon
	}
	return l
}

func legToProto(l TripLeg) *tripv1.TripLeg {
	pl := &tripv1.TripLeg{
		Id:           l.ID.String(),
		AfterDay:     l.AfterDay,
		FromName:     l.FromName,
		ToName:       l.ToName,
		DistanceKm:   l.DistanceKm,
		DurationMins: l.DurationMins,
		Mode:         l.Mode,
		BookingUrl:   l.BookingURL,
	}
	if l.FromLat != nil {
		pl.FromLat = *l.FromLat
	}
	if l.FromLon != nil {
		pl.FromLon = *l.FromLon
	}
	if l.ToLat != nil {
		pl.ToLat = *l.ToLat
	}
	if l.ToLon != nil {
		pl.ToLon = *l.ToLon
	}
	return pl
}

func stopFromProto(ps *tripv1.TripStop) TripStop {
	s := TripStop{
		POIID:               ps.GetPoiId(),
		OrderIndex:          ps.GetOrderIndex(),
		Name:                ps.GetName(),
		Notes:               ps.GetNotes(),
		RecommendationTrace: traceFromProto(ps.GetRecommendationTrace()),
	}
	if ps.GetId() != "" {
		s.ID, _ = uuid.Parse(ps.GetId())
	}
	if ps.StartMinute != nil {
		v := ps.GetStartMinute()
		s.StartMinute = &v
	}
	if ps.DurationMinutes != nil {
		v := ps.GetDurationMinutes()
		s.DurationMinutes = &v
	}
	if ps.BookingUrl != nil && ps.GetBookingUrl() != "" {
		b := ps.GetBookingUrl()
		s.BookingURL = &b
	}
	return s
}

func traceFromProto(trace *recommendationv1.RecommendationTrace) *RecommendationTrace {
	if trace == nil {
		return nil
	}
	return &RecommendationTrace{
		RunID: trace.GetRunId(), ItemID: trace.GetItemId(), Rank: trace.GetRank(),
		AlgorithmVersion: trace.GetAlgorithmVersion(), ExperimentVariant: trace.GetExperimentVariant(),
		Surface: int32(trace.GetSurface()), Channel: int32(trace.GetChannel()),
	}
}

func traceToProto(trace *RecommendationTrace) *recommendationv1.RecommendationTrace {
	if trace == nil {
		return nil
	}
	return &recommendationv1.RecommendationTrace{
		RunId: trace.RunID, ItemId: trace.ItemID, Rank: trace.Rank,
		AlgorithmVersion: trace.AlgorithmVersion, ExperimentVariant: trace.ExperimentVariant,
		Surface: recommendationv1.RecommendationSurface(trace.Surface),
		Channel: recommendationv1.RecommendationChannel(trace.Channel),
	}
}

func constraintToProto(c TripConstraint) *tripv1.TripConstraint {
	return &tripv1.TripConstraint{
		BudgetLevel:    c.BudgetLevel,
		Pace:           tripv1.TripPace(c.Pace),
		Mobility:       stringPtrOrNil(c.Mobility),
		Interests:      c.Interests,
		DayStartMinute: c.DayStartMinute,
		DayEndMinute:   c.DayEndMinute,
	}
}

func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// buildICS renders a trip as a minimal but valid iCalendar. Stops with a day
// date and a start_minute become timed VEVENTs; others are skipped (an .ics with
// no usable times still opens cleanly, just empty).
func buildICS(t *Trip) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//Loci//Trip//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")

	for _, d := range t.Days {
		if d.Date == nil {
			continue
		}
		for _, s := range d.Stops {
			if s.StartMinute == nil {
				continue
			}
			start := dayAt(*d.Date, int(*s.StartMinute))
			dur := 60
			if s.DurationMinutes != nil && *s.DurationMinutes > 0 {
				dur = int(*s.DurationMinutes)
			}
			end := start.Add(time.Duration(dur) * time.Minute)
			b.WriteString("BEGIN:VEVENT\r\n")
			fmt.Fprintf(&b, "UID:%s@loci\r\n", s.ID.String())
			fmt.Fprintf(&b, "DTSTART:%s\r\n", start.UTC().Format("20060102T150405Z"))
			fmt.Fprintf(&b, "DTEND:%s\r\n", end.UTC().Format("20060102T150405Z"))
			fmt.Fprintf(&b, "SUMMARY:%s\r\n", icsEscape(s.Name))
			if s.Notes != "" {
				fmt.Fprintf(&b, "DESCRIPTION:%s\r\n", icsEscape(s.Notes))
			}
			b.WriteString("END:VEVENT\r\n")
		}
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func dayAt(date time.Time, minutes int) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, minutes/60, minutes%60, 0, 0, date.Location())
}

func icsEscape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n")
	return r.Replace(s)
}
