package render

import (
	"fmt"
	"strings"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
)

// TripCard is what a shared trip's link preview is drawn from. Everything is
// optional; a zero card still draws.
type TripCard struct {
	Title string
	City  string
	Days  int
	Stops int
	// Checked of Answered stops were found on a real map. Answered is how
	// many the map has answered for at all; the stamp is drawn only when it
	// is above zero, so a trip nobody has checked makes no claim.
	Checked  int
	Answered int
	// Route is the stops in visit order as latitude, longitude.
	Route [][2]float64
}

// colHolo is the cool signal colour (--holo on the dark ground): spatial data
// and "found on the map".
var colHolo = hexColor("#7A9CC2")

// TripOG renders the 1200x630 link preview for a shared trip: its name, the
// city and length, how many of its stops were found on a real map, and the
// route as a line.
func TripOG(t TripCard) ([]byte, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	c := newCanvas(OGW, OGH)
	drawTripOG(c, t)
	return c.encode()
}

// tripFacts is the line above the title: "LISBON · 3 DAYS · 14 STOPS".
func tripFacts(t TripCard) string {
	var parts []string
	if city := strings.TrimSpace(t.City); city != "" {
		parts = append(parts, city)
	}
	if t.Days > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", t.Days, plural(int64(t.Days), "day", "days")))
	}
	if t.Stops > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", t.Stops, plural(int64(t.Stops), "stop", "stops")))
	}
	return strings.Join(parts, " · ")
}

// tripStamp is the claim on the card, or "" when there is none to make. It
// says "checked", which means found on the map and nothing more: hours are
// shown per stop on the page, never summed here.
func tripStamp(t TripCard) string {
	if t.Answered <= 0 || t.Checked <= 0 {
		return ""
	}
	checked := min(t.Checked, t.Answered)
	return fmt.Sprintf("%d of %d stops checked", checked, t.Answered)
}

func drawTripOG(c *canvas, t TripCard) {
	fill(c, colNight)
	const m = 56.0

	pts := make([]*recapv1.RoutePoint, 0, len(t.Route))
	for _, p := range t.Route {
		if p[0] == 0 && p[1] == 0 {
			continue
		}
		pts = append(pts, &recapv1.RoutePoint{Latitude: p[0], Longitude: p[1]})
	}
	const bx, by, bw, bh = 760.0, m, OGW - 760.0 - m, OGH - 2*m
	var xy [][2]float64
	if len(pts) >= 2 {
		if xy = projected(pts, bx, by, bw, bh, 60); len(xy) < 2 {
			xy = nil
		}
	}
	leftW := float64(OGW - 2*m)
	if xy != nil {
		leftW = bx - 2*m
	}

	// Mark.
	c.dc.DrawCircle(m+10, 74, 10)
	c.setColor(colCoral)
	c.dc.Fill()
	c.use(fMonoBold, 26)
	c.setColor(colPaper)
	c.text("LOCI", m+32, 83, 0, 4)

	y := 172.0
	if facts := tripFacts(t); facts != "" {
		c.setColor(colCoral)
		c.label(facts, true, 24, leftW, m, y, 0)
	}

	c.setColor(colPaper)
	title := firstNonEmpty(t.Title, t.City, "A trip, planned")
	y = c.wrapBlock(title, fDisplay, 76, 40, leftW, 2, m, y+84, 0, 1.04)

	if stamp := tripStamp(t); stamp != "" {
		text := strings.ToUpper(stamp)
		c.use(fMonoBold, 30)
		const tracking, padX, boxH = 3.6, 26.0, 72.0
		for c.measure(text, tracking)+2*padX+42 > leftW && c.size > 18 {
			c.use(fMonoBold, c.size-2)
		}
		top := max(400.0, y+44)
		w := c.measure(text, tracking) + 2*padX + 42
		c.dc.DrawRoundedRectangle(m, top, w, boxH, 4)
		c.dc.SetLineWidth(3)
		c.setColor(colHolo)
		c.dc.Stroke()
		// The tick.
		tx, ty := m+padX, top+boxH/2
		c.dc.MoveTo(tx, ty+1)
		c.dc.LineTo(tx+8, ty+9)
		c.dc.LineTo(tx+22, ty-9)
		c.dc.SetLineWidth(4)
		c.dc.Stroke()
		c.text(text, m+padX+42, top+boxH/2+c.size*0.34, 0, tracking)

		c.setColor(withAlpha(colPaper, 0.6))
		c.label("Against the map · © OpenStreetMap contributors", false, 17, leftW, m, top+boxH+34, 0)
	}

	c.setColor(withAlpha(colPaper, 0.6))
	c.use(fMono, 22)
	c.text("LOCIAI.FYI", m, OGH-24, 0, 4)

	if xy != nil {
		c.dc.DrawRoundedRectangle(bx, by, bw, bh, 22)
		c.setColor(withAlpha(colPaper, 0.06))
		c.dc.Fill()
		c.dc.DrawRoundedRectangle(bx, by, bw, bh, 22)
		c.dc.SetLineWidth(1.5)
		c.setColor(withAlpha(colPaper, 0.16))
		c.dc.Stroke()
		c.routeLine(xy, colCoral, colPaper, 5)
	}
}
