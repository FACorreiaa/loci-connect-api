package render

import (
	"fmt"
	"math"
	"strings"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
)

// ogSummary is the Outro's summary line, else counts gathered from the
// Places, Route and Days cards, else the subtitle.
func ogSummary(r *recapv1.Recap) string {
	var places, days int64
	var km float64
	var outro, city string
	for _, card := range r.GetCards() {
		switch {
		case card.GetOutro() != nil:
			outro = firstNonEmpty(outro, card.GetOutro().GetSummaryLine())
		case card.GetPlaces() != nil:
			places = int64(card.GetPlaces().GetPlacesCount())
		case card.GetRoute() != nil:
			km = card.GetRoute().GetDistanceKm()
		case card.GetDays() != nil:
			d := card.GetDays()
			days = int64(d.GetDaysTotal())
			if days == 0 {
				days = int64(len(d.GetDays()))
			}
		case card.GetIntro() != nil:
			city = card.GetIntro().GetPrimaryCity()
		}
	}
	if outro != "" {
		return outro
	}
	var parts []string
	if places > 0 {
		parts = append(parts, fmt.Sprintf("%s %s", thousands(places), plural(places, "place", "places")))
	}
	if km > 0 {
		parts = append(parts, formatKm(km)+" km")
	}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", days, plural(days, "day", "days")))
	}
	if len(parts) > 0 {
		s := strings.Join(parts, " · ")
		if city != "" {
			s += " in " + city
		}
		return s
	}
	return strings.TrimSpace(r.GetSubtitle())
}

func drawOG(c *canvas, r *recapv1.Recap) {
	fill(c, colCream)
	acc := accent(r)
	const m = 56.0

	var intro *recapv1.IntroCard
	for _, card := range r.GetCards() {
		if in := card.GetIntro(); in != nil {
			intro = in
			break
		}
	}

	rt := findRoute(r)
	var xy [][2]float64
	const bx, by, bw, bh = 760.0, m, OGW - 760.0 - m, OGH - 2*m
	if rt != nil && len(rt.GetPoints()) >= 2 {
		xy = projected(rt.GetPoints(), bx, by, bw, bh, 60)
		if len(xy) < 2 {
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
	c.setColor(colInk)
	c.text("LOCI", m+32, 83, 0, 4)

	c.setColor(colInk)
	title := firstNonEmpty(r.GetTitle(), intro.GetPrimaryCity(), "A trip, kept")
	y := c.wrapBlock(title, fDisplay, 76, 40, leftW, 2, m, 196, 0, 1.04)
	if p := strings.TrimSpace(r.GetPeriodLabel()); p != "" {
		y += 62
		c.setColor(colMuted)
		c.label(p, false, 24, leftW, m, y, 0)
	}
	if s := ogSummary(r); s != "" {
		c.setColor(colInk)
		y = c.wrapBlock(s, fSans, 32, 24, leftW, 2, m, y+58, 0, 1.25)
	}

	if code := strings.TrimSpace(intro.GetShortCode()); code != "" {
		t := c.fitLine(strings.ToUpper(code), fDisplay, 60, 30, leftW-60, 0)
		w := c.measure(t, 0) + 52
		top := math.Max(462, y+36)
		c.dc.DrawRoundedRectangle(m, top, w, 92, 10)
		c.dc.SetLineWidth(3)
		c.setColor(acc)
		c.dc.Stroke()
		c.text(t, m+26, top+46+c.size*0.36, 0, 0)
	}
	c.setColor(colMuted)
	c.use(fMono, 22)
	c.text("LOCIAI.FYI", OGW-m, OGH-24, 1, 4)

	if xy != nil {
		c.dc.DrawRoundedRectangle(bx, by, bw, bh, 22)
		c.setColor(colNight)
		c.dc.Fill()
		c.routeLine(xy, colCoral, colPaper, 5)
		if km := rt.GetDistanceKm(); km > 0 {
			c.setColor(colPaper)
			c.label(formatKm(km)+" km", true, 26, bw-60, bx+30, by+bh-30, 0)
		}
	}
}
