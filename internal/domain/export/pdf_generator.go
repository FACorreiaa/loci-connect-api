package export

import (
	"fmt"
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"

	exportv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/export"
)

// PDFGenerator handles PDF document generation
type PDFGenerator struct{}

// NewPDFGenerator creates a new PDF generator
func NewPDFGenerator() *PDFGenerator {
	return &PDFGenerator{}
}

// getMaroto creates a new maroto instance with consistent styling
func (g *PDFGenerator) getMaroto() core.Maroto {
	cfg := config.NewBuilder().
		WithPageNumber().
		WithLeftMargin(10).
		WithTopMargin(15).
		WithRightMargin(10).
		Build()

	return maroto.New(cfg)
}

// addHeader adds a styled header to the PDF
func (g *PDFGenerator) addHeader(m core.Maroto, title, subtitle string) {
	m.AddRow(15,
		col.New(12).Add(
			text.New(title, props.Text{
				Size:  16,
				Style: fontstyle.Bold,
				Align: align.Center,
				Color: &props.Color{Red: 30, Green: 100, Blue: 180},
			}),
		),
	)

	if subtitle != "" {
		m.AddRow(8,
			col.New(12).Add(
				text.New(subtitle, props.Text{
					Size:  10,
					Align: align.Center,
					Color: &props.Color{Red: 100, Green: 100, Blue: 100},
				}),
			),
		)
	}

	// Date generated
	m.AddRow(6,
		col.New(12).Add(
			text.New(fmt.Sprintf("Generated on %s", time.Now().Format("January 2, 2006")), props.Text{
				Size:  8,
				Align: align.Center,
				Color: &props.Color{Red: 150, Green: 150, Blue: 150},
			}),
		),
	)

	m.AddRow(5) // Spacer
}

// addDivider adds a visual divider
func (g *PDFGenerator) addDivider(m core.Maroto) {
	m.AddRow(3)
}

// GeneratePOIsPDF generates a PDF for a list of POIs
func (g *PDFGenerator) GeneratePOIsPDF(pois []*exportv1.ExportPOI, title string) ([]byte, error) {
	m := g.getMaroto()

	if title == "" {
		title = "Places of Interest"
	}
	g.addHeader(m, title, fmt.Sprintf("%d places", len(pois)))

	for i, poi := range pois {
		// POI Name
		m.AddRow(10,
			col.New(12).Add(
				text.New(fmt.Sprintf("%d. %s", i+1, poi.Name), props.Text{
					Size:  12,
					Style: fontstyle.Bold,
				}),
			),
		)

		// Category and Rating row
		categoryRating := poi.Category
		if poi.Rating > 0 {
			categoryRating = fmt.Sprintf("%s • ⭐ %.1f", poi.Category, poi.Rating)
		}
		m.AddRow(6,
			col.New(12).Add(
				text.New(categoryRating, props.Text{
					Size:  9,
					Color: &props.Color{Red: 80, Green: 80, Blue: 80},
				}),
			),
		)

		// Description
		if poi.Description != "" {
			desc := poi.Description
			if len(desc) > 200 {
				desc = desc[:200] + "..."
			}
			m.AddRow(12,
				col.New(12).Add(
					text.New(desc, props.Text{
						Size: 9,
					}),
				),
			)
		}

		// Address
		if poi.Address != "" {
			m.AddRow(6,
				col.New(12).Add(
					text.New(fmt.Sprintf("📍 %s", poi.Address), props.Text{
						Size:  8,
						Color: &props.Color{Red: 60, Green: 60, Blue: 60},
					}),
				),
			)
		}

		// Contact info
		var contacts []string
		if poi.Phone != "" {
			contacts = append(contacts, fmt.Sprintf("📞 %s", poi.Phone))
		}
		if poi.Website != "" {
			contacts = append(contacts, fmt.Sprintf("🌐 %s", poi.Website))
		}
		if len(contacts) > 0 {
			m.AddRow(6,
				col.New(12).Add(
					text.New(strings.Join(contacts, " | "), props.Text{
						Size:  8,
						Color: &props.Color{Red: 60, Green: 60, Blue: 60},
					}),
				),
			)
		}

		g.addDivider(m)
	}

	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.GetBytes(), nil
}

// GenerateHotelsPDF generates a PDF for hotels
func (g *PDFGenerator) GenerateHotelsPDF(hotels []*exportv1.ExportHotel, title, cityName string) ([]byte, error) {
	m := g.getMaroto()

	if title == "" {
		title = "Hotels"
	}
	subtitle := fmt.Sprintf("%d hotels", len(hotels))
	if cityName != "" {
		subtitle = fmt.Sprintf("%d hotels in %s", len(hotels), cityName)
	}
	g.addHeader(m, title, subtitle)

	for i, hotel := range hotels {
		// Hotel Name with stars
		stars := ""
		if hotel.StarRating > 0 {
			stars = " " + strings.Repeat("⭐", int(hotel.StarRating))
		}
		m.AddRow(10,
			col.New(12).Add(
				text.New(fmt.Sprintf("%d. %s%s", i+1, hotel.Name, stars), props.Text{
					Size:  12,
					Style: fontstyle.Bold,
				}),
			),
		)

		// Rating and Price
		info := ""
		if hotel.Rating > 0 {
			info = fmt.Sprintf("Rating: %.1f", hotel.Rating)
		}
		if hotel.PriceRange != "" {
			if info != "" {
				info += " • "
			}
			info += hotel.PriceRange
		}
		if info != "" {
			m.AddRow(6,
				col.New(12).Add(
					text.New(info, props.Text{
						Size:  9,
						Color: &props.Color{Red: 80, Green: 80, Blue: 80},
					}),
				),
			)
		}

		// Description
		if hotel.Description != "" {
			desc := hotel.Description
			if len(desc) > 200 {
				desc = desc[:200] + "..."
			}
			m.AddRow(12,
				col.New(12).Add(
					text.New(desc, props.Text{
						Size: 9,
					}),
				),
			)
		}

		// Check-in/Check-out
		if hotel.CheckInTime != "" || hotel.CheckOutTime != "" {
			times := ""
			if hotel.CheckInTime != "" {
				times = fmt.Sprintf("Check-in: %s", hotel.CheckInTime)
			}
			if hotel.CheckOutTime != "" {
				if times != "" {
					times += " | "
				}
				times += fmt.Sprintf("Check-out: %s", hotel.CheckOutTime)
			}
			m.AddRow(6,
				col.New(12).Add(
					text.New(times, props.Text{
						Size:  8,
						Color: &props.Color{Red: 60, Green: 60, Blue: 60},
					}),
				),
			)
		}

		// Amenities
		if len(hotel.Amenities) > 0 {
			amenities := strings.Join(hotel.Amenities[:min(5, len(hotel.Amenities))], ", ")
			if len(hotel.Amenities) > 5 {
				amenities += fmt.Sprintf(" +%d more", len(hotel.Amenities)-5)
			}
			m.AddRow(6,
				col.New(12).Add(
					text.New(fmt.Sprintf("Amenities: %s", amenities), props.Text{
						Size:  8,
						Color: &props.Color{Red: 60, Green: 60, Blue: 60},
					}),
				),
			)
		}

		// Address
		if hotel.Address != "" {
			m.AddRow(6,
				col.New(12).Add(
					text.New(fmt.Sprintf("📍 %s", hotel.Address), props.Text{
						Size:  8,
						Color: &props.Color{Red: 60, Green: 60, Blue: 60},
					}),
				),
			)
		}

		g.addDivider(m)
	}

	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.GetBytes(), nil
}

// GenerateRestaurantsPDF generates a PDF for restaurants
func (g *PDFGenerator) GenerateRestaurantsPDF(restaurants []*exportv1.ExportRestaurant, title, cityName string) ([]byte, error) {
	m := g.getMaroto()

	if title == "" {
		title = "Restaurants"
	}
	subtitle := fmt.Sprintf("%d restaurants", len(restaurants))
	if cityName != "" {
		subtitle = fmt.Sprintf("%d restaurants in %s", len(restaurants), cityName)
	}
	g.addHeader(m, title, subtitle)

	for i, r := range restaurants {
		// Restaurant Name
		m.AddRow(10,
			col.New(12).Add(
				text.New(fmt.Sprintf("%d. %s", i+1, r.Name), props.Text{
					Size:  12,
					Style: fontstyle.Bold,
				}),
			),
		)

		// Cuisine and Rating
		info := r.CuisineType
		if r.Rating > 0 {
			if info != "" {
				info += " • "
			}
			info += fmt.Sprintf("⭐ %.1f", r.Rating)
		}
		if r.PriceRange != "" {
			if info != "" {
				info += " • "
			}
			info += r.PriceRange
		}
		if info != "" {
			m.AddRow(6,
				col.New(12).Add(
					text.New(info, props.Text{
						Size:  9,
						Color: &props.Color{Red: 80, Green: 80, Blue: 80},
					}),
				),
			)
		}

		// Description
		if r.Description != "" {
			desc := r.Description
			if len(desc) > 200 {
				desc = desc[:200] + "..."
			}
			m.AddRow(12,
				col.New(12).Add(
					text.New(desc, props.Text{
						Size: 9,
					}),
				),
			)
		}

		// Dietary options
		if len(r.DietaryOptions) > 0 {
			m.AddRow(6,
				col.New(12).Add(
					text.New(fmt.Sprintf("Dietary: %s", strings.Join(r.DietaryOptions, ", ")), props.Text{
						Size:  8,
						Color: &props.Color{Red: 60, Green: 60, Blue: 60},
					}),
				),
			)
		}

		// Address and hours
		if r.Address != "" {
			m.AddRow(6,
				col.New(12).Add(
					text.New(fmt.Sprintf("📍 %s", r.Address), props.Text{
						Size:  8,
						Color: &props.Color{Red: 60, Green: 60, Blue: 60},
					}),
				),
			)
		}

		g.addDivider(m)
	}

	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.GetBytes(), nil
}

// GenerateActivitiesPDF generates a PDF for activities
func (g *PDFGenerator) GenerateActivitiesPDF(activities []*exportv1.ExportActivity, title, cityName string) ([]byte, error) {
	m := g.getMaroto()

	if title == "" {
		title = "Activities"
	}
	subtitle := fmt.Sprintf("%d activities", len(activities))
	if cityName != "" {
		subtitle = fmt.Sprintf("%d activities in %s", len(activities), cityName)
	}
	g.addHeader(m, title, subtitle)

	for i, a := range activities {
		m.AddRow(10,
			col.New(12).Add(
				text.New(fmt.Sprintf("%d. %s", i+1, a.Name), props.Text{
					Size:  12,
					Style: fontstyle.Bold,
				}),
			),
		)

		// Category and details
		info := a.Category
		if a.Duration != "" {
			if info != "" {
				info += " • "
			}
			info += fmt.Sprintf("Duration: %s", a.Duration)
		}
		if a.Difficulty != "" {
			if info != "" {
				info += " • "
			}
			info += a.Difficulty
		}
		if info != "" {
			m.AddRow(6,
				col.New(12).Add(
					text.New(info, props.Text{
						Size:  9,
						Color: &props.Color{Red: 80, Green: 80, Blue: 80},
					}),
				),
			)
		}

		// Description
		if a.Description != "" {
			desc := a.Description
			if len(desc) > 200 {
				desc = desc[:200] + "..."
			}
			m.AddRow(12,
				col.New(12).Add(
					text.New(desc, props.Text{
						Size: 9,
					}),
				),
			)
		}

		// Address
		if a.Address != "" {
			m.AddRow(6,
				col.New(12).Add(
					text.New(fmt.Sprintf("📍 %s", a.Address), props.Text{
						Size:  8,
						Color: &props.Color{Red: 60, Green: 60, Blue: 60},
					}),
				),
			)
		}

		g.addDivider(m)
	}

	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.GetBytes(), nil
}

// itineraryDayLabel returns a human-readable day header, e.g. "Day 1" or "Day 1 of 2".
func itineraryDayLabel(day, totalDays int32) string {
	if totalDays > 1 {
		return fmt.Sprintf("Day %d of %d", day, totalDays)
	}
	return fmt.Sprintf("Day %d", day)
}

// formatDuration converts minutes to a compact string, e.g. "1 h 30 min" or "45 min".
func formatDuration(minutes int32) string {
	if minutes <= 0 {
		return ""
	}
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	h := minutes / 60
	m := minutes % 60
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

// GenerateItineraryPDF generates a polished PDF for an itinerary.
func (g *PDFGenerator) GenerateItineraryPDF(itinerary *exportv1.ExportItinerary) ([]byte, error) {
	cfg := config.NewBuilder().
		WithLeftMargin(12).
		WithTopMargin(14).
		WithRightMargin(12).
		WithBottomMargin(14).
		Build()
	m := maroto.New(cfg)

	title := itinerary.Title
	if title == "" {
		title = "Travel Itinerary"
	}

	// ── Cover / header block ──────────────────────────────────────────────────
	m.AddRow(14,
		col.New(12).Add(
			text.New(title, props.Text{
				Size:  18,
				Style: fontstyle.Bold,
				Align: align.Center,
				Color: &props.Color{Red: 22, Green: 78, Blue: 140},
			}),
		),
	)

	// City + duration sub-line
	var subParts []string
	if itinerary.CityName != "" {
		subParts = append(subParts, itinerary.CityName)
	}
	if itinerary.TotalDays > 0 {
		dayWord := "day"
		if itinerary.TotalDays != 1 {
			dayWord = "days"
		}
		subParts = append(subParts, fmt.Sprintf("%d %s", itinerary.TotalDays, dayWord))
	}
	if len(subParts) > 0 {
		m.AddRow(7,
			col.New(12).Add(
				text.New(strings.Join(subParts, "  |  "), props.Text{
					Size:  10,
					Align: align.Center,
					Color: &props.Color{Red: 90, Green: 90, Blue: 90},
				}),
			),
		)
	}

	m.AddRow(5,
		col.New(12).Add(
			text.New(fmt.Sprintf("Generated on %s", time.Now().Format("January 2, 2006")), props.Text{
				Size:  8,
				Align: align.Center,
				Color: &props.Color{Red: 160, Green: 160, Blue: 160},
			}),
		),
	)

	// Trip summary / intro paragraph
	if itinerary.Description != "" {
		m.AddRow(4) // breathing space before paragraph
		m.AddRow(20,
			col.New(12).Add(
				text.New(itinerary.Description, props.Text{
					Size:  9,
					Align: align.Left,
					Color: &props.Color{Red: 55, Green: 55, Blue: 55},
				}),
			),
		)
	}

	m.AddRow(6) // gap before first day

	// ── Group items by day ────────────────────────────────────────────────────
	dayItems := make(map[int32][]*exportv1.ExportItineraryItem)
	for _, item := range itinerary.Items {
		dayItems[item.DayNumber] = append(dayItems[item.DayNumber], item)
	}

	accentBlue := &props.Color{Red: 22, Green: 78, Blue: 140}
	mutedGrey := &props.Color{Red: 110, Green: 110, Blue: 110}
	darkGrey := &props.Color{Red: 45, Green: 45, Blue: 45}

	for day := int32(1); day <= itinerary.TotalDays; day++ {
		items := dayItems[day]
		if len(items) == 0 {
			continue
		}

		// ── Day banner ────────────────────────────────────────────────────────
		m.AddRow(9,
			col.New(12).Add(
				text.New(strings.ToUpper(itineraryDayLabel(day, itinerary.TotalDays)), props.Text{
					Size:            10,
					Style:           fontstyle.Bold,
					Align:           align.Left,
					Color:           &props.Color{Red: 255, Green: 255, Blue: 255},
					VerticalPadding: 2,
					Left:            2,
				}),
			).WithStyle(&props.Cell{
				BackgroundColor: accentBlue,
			}),
		)

		m.AddRow(3) // gap after banner

		// ── Stops ─────────────────────────────────────────────────────────────
		for idx, item := range items {
			stopNum := fmt.Sprintf("%d", idx+1)

			// Row 1 — stop number | name
			m.AddRow(8,
				col.New(1).Add(
					text.New(stopNum, props.Text{
						Size:  9,
						Style: fontstyle.Bold,
						Align: align.Center,
						Color: &props.Color{Red: 255, Green: 255, Blue: 255},
					}),
				).WithStyle(&props.Cell{
					BackgroundColor: accentBlue,
				}),
				col.New(11).Add(
					text.New(item.Name, props.Text{
						Size:  11,
						Style: fontstyle.Bold,
						Color: darkGrey,
						Left:  3,
					}),
				),
			)

			// Row 2 — time slot | category tag
			var metaParts []string
			if item.TimeSlot != "" && item.TimeSlot != "Flexible" {
				metaParts = append(metaParts, item.TimeSlot)
			}
			if item.Notes != "" {
				metaParts = append(metaParts, item.Notes)
			}
			dur := formatDuration(item.DurationMinutes)
			if dur != "" {
				metaParts = append(metaParts, dur)
			}
			if len(metaParts) > 0 {
				m.AddRow(5,
					col.New(1),
					col.New(11).Add(
						text.New(strings.Join(metaParts, "  ·  "), props.Text{
							Size:  8,
							Color: mutedGrey,
							Left:  3,
						}),
					),
				)
			}

			// Row 3 — description (full text, no truncation)
			if item.Description != "" {
				descLines := estimateDescriptionRows(item.Description)
				m.AddRow(descLines,
					col.New(1),
					col.New(11).Add(
						text.New(item.Description, props.Text{
							Size:  9,
							Color: darkGrey,
							Left:  3,
						}),
					),
				)
			}

			// Row 4 — address (if present)
			if item.Address != "" {
				m.AddRow(5,
					col.New(1),
					col.New(11).Add(
						text.New("Address: "+item.Address, props.Text{
							Size:  8,
							Color: mutedGrey,
							Left:  3,
						}),
					),
				)
			}

			m.AddRow(4) // gap between stops
		}

		m.AddRow(6) // gap between days
	}

	// ── Footer branding ───────────────────────────────────────────────────────
	m.AddRow(5,
		col.New(12).Add(
			text.New("Generated by Loci  —  loci.travel", props.Text{
				Size:  7,
				Align: align.Center,
				Color: &props.Color{Red: 180, Green: 180, Blue: 180},
			}),
		),
	)

	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.GetBytes(), nil
}

// estimateDescriptionRows returns an approximate maroto row height (mm) for a
// description string rendered at 9pt in an 11-column layout (~155mm wide).
// We target ~110 characters per line at that size, then add 6mm per line.
func estimateDescriptionRows(desc string) float64 {
	const charsPerLine = 110
	const mmPerLine = 6.0
	const minRows = 8.0

	lines := (len(desc) + charsPerLine - 1) / charsPerLine
	if lines < 1 {
		lines = 1
	}
	h := float64(lines) * mmPerLine
	if h < minRows {
		h = minRows
	}
	return h
}

// GenerateListPDF generates a PDF for a list with mixed content
func (g *PDFGenerator) GenerateListPDF(listName string, pois []*exportv1.ExportPOI, hotels []*exportv1.ExportHotel, restaurants []*exportv1.ExportRestaurant) ([]byte, error) {
	m := g.getMaroto()

	title := listName
	if title == "" {
		title = "My List"
	}
	total := len(pois) + len(hotels) + len(restaurants)
	g.addHeader(m, title, fmt.Sprintf("%d items", total))

	// POIs section
	if len(pois) > 0 {
		m.AddRow(10,
			col.New(12).Add(
				text.New("📍 Places", props.Text{
					Size:  12,
					Style: fontstyle.Bold,
					Color: &props.Color{Red: 30, Green: 100, Blue: 180},
				}),
			),
		)
		for _, poi := range pois {
			m.AddRow(8,
				col.New(12).Add(
					text.New(fmt.Sprintf("• %s (%s)", poi.Name, poi.Category), props.Text{
						Size: 9,
					}),
				),
			)
		}
		m.AddRow(5)
	}

	// Hotels section
	if len(hotels) > 0 {
		m.AddRow(10,
			col.New(12).Add(
				text.New("🏨 Hotels", props.Text{
					Size:  12,
					Style: fontstyle.Bold,
					Color: &props.Color{Red: 30, Green: 100, Blue: 180},
				}),
			),
		)
		for _, h := range hotels {
			m.AddRow(8,
				col.New(12).Add(
					text.New(fmt.Sprintf("• %s", h.Name), props.Text{
						Size: 9,
					}),
				),
			)
		}
		m.AddRow(5)
	}

	// Restaurants section
	if len(restaurants) > 0 {
		m.AddRow(10,
			col.New(12).Add(
				text.New("🍽️ Restaurants", props.Text{
					Size:  12,
					Style: fontstyle.Bold,
					Color: &props.Color{Red: 30, Green: 100, Blue: 180},
				}),
			),
		)
		for _, r := range restaurants {
			m.AddRow(8,
				col.New(12).Add(
					text.New(fmt.Sprintf("• %s (%s)", r.Name, r.CuisineType), props.Text{
						Size: 9,
					}),
				),
			)
		}
	}

	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}

	return doc.GetBytes(), nil
}
