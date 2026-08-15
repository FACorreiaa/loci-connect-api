package trip

import (
	"fmt"

	exportv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/export"

	exportdomain "github.com/FACorreiaa/loci-connect-api/internal/domain/export"
)

// buildTripPDF renders a TripDraft via the shared ExportService PDF generator.
func buildTripPDF(t *Trip) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("nil trip")
	}
	items := make([]*exportv1.ExportItineraryItem, 0)
	for _, d := range t.Days {
		for _, s := range d.Stops {
			timeSlot := ""
			if s.StartMinute != nil {
				h := *s.StartMinute / 60
				m := *s.StartMinute % 60
				timeSlot = fmt.Sprintf("%02d:%02d", h, m)
			}
			dur := int32(0)
			if s.DurationMinutes != nil {
				dur = *s.DurationMinutes
			}
			items = append(items, &exportv1.ExportItineraryItem{
				DayNumber:       d.DayNumber,
				TimeSlot:        timeSlot,
				Name:            s.Name,
				Description:     s.Notes,
				DurationMinutes: dur,
				Notes:           s.Notes,
			})
		}
	}
	itinerary := &exportv1.ExportItinerary{
		Id:          t.ID.String(),
		Title:       t.Title,
		Description: "",
		CityName:    t.CityName,
		TotalDays:   int32(len(t.Days)),
		Items:       items,
	}
	return exportdomain.NewPDFGenerator().GenerateItineraryPDF(itinerary)
}
