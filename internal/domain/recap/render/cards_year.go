package render

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
)

// Year-in-review cards. Same desk, bigger drawer: a wall map, a calendar
// strip, a postcard of the year's city, a ledger, a seal.

// statRow draws up to three big numbers with captions, divided by dashes,
// centred on y (the numerals' baseline).
func (c *canvas) statRow(y float64, ink, muted color.Color, stats []stat) {
	n := float64(len(stats))
	if n == 0 {
		return
	}
	step := float64(contentW) / n
	for i, s := range stats {
		cx := margin + step*(float64(i)+0.5)
		c.setColor(ink)
		c.fitText(thousands(s.v), fDisplay, 116, 24, step-28, cx, y, 0.5, 0)
		c.setColor(muted)
		c.label(s.k, false, 22, step-20, cx, y+56, 0.5)
		if i > 0 {
			c.dashedLine(cx-step/2, y-90, cx-step/2, y+70, 6, 8, 2, withAlpha(colInk, 0.3))
		}
	}
}

// count is "9 cities": the number and its noun.
func count(n int64, one, many string) string { return thousands(n) + " " + plural(n, one, many) }

type stat = struct {
	v int64
	k string
}

// ---------------------------------------------------------------- Year map

func drawYearMap(c *canvas, r *recapv1.Recap, m *recapv1.YearMapCard) {
	fill(c, colSand)
	chrome(c, r, false)
	acc := accent(r)

	n := int64(max(m.GetCountriesCount(), 0))
	c.setColor(colInk)
	num := c.fitLine(thousands(n), fDisplay, 260, 56, contentW/2, 0)
	c.text(num, margin, 470, 0, 0)
	nw := c.measure(num, 0)
	c.use(fItalic, 84)
	c.setColor(acc)
	c.text(plural(n, "country", "countries"), margin+nw+24, 470, 0, 0)
	c.setColor(colMuted)
	c.label(count(int64(max(m.GetCitiesCount(), 0)), "city", "cities"), false, 28, contentW, margin, 540, 0)

	// The wall map: a paper panel with a grid and a pin per city.
	const mx, my, mw, mh = margin, 600.0, float64(contentW), 640.0
	c.shadow(mx, my, mw, mh, 18)
	c.dc.DrawRoundedRectangle(mx, my, mw, mh, 18)
	c.setColor(colPaper)
	c.dc.Fill()
	c.dc.SetLineWidth(1)
	for gx := mx + 60; gx < mx+mw; gx += 60 {
		c.dc.DrawLine(gx, my+10, gx, my+mh-10)
	}
	for gy := my + 60; gy < my+mh; gy += 60 {
		c.dc.DrawLine(mx+10, gy, mx+mw-10, gy)
	}
	c.setColor(withAlpha(colInk, 0.07))
	c.dc.Stroke()
	xy := projected(m.GetCities(), mx, my, mw, mh, 110)
	for i, p := range xy {
		c.dc.DrawCircle(p[0], p[1], 16)
		c.setColor(withAlpha(colCoral, 0.25))
		c.dc.Fill()
		c.dc.DrawCircle(p[0], p[1], 8)
		c.setColor(colDeepCoral)
		c.dc.Fill()
		// Label the first few (the most-visited), on whichever side has room.
		if i < 8 && i < len(m.GetCities()) {
			c.setColor(colInk)
			if p[0] > mx+mw*0.62 {
				c.label(m.GetCities()[i].GetLabel(), false, 20, 260, p[0]-26, p[1]+7, 1)
			} else {
				c.label(m.GetCities()[i].GetLabel(), false, 20, 260, p[0]+26, p[1]+7, 0)
			}
		}
	}
	if len(xy) == 0 {
		c.setColor(colMuted)
		c.label("The map fills in as you go", false, 24, mw-80, mx+mw/2, my+mh/2, 0.5)
	}

	// Country ledger: up to five rows, with a "new" tag on first visits.
	y := my + mh + 90
	for i, co := range m.GetCountries() {
		if i == 5 {
			break
		}
		name := firstNonEmpty(co.GetCountry(), co.GetCountryCode(), "—")
		c.setColor(colInk)
		c.fitText(name, fDisplay, 52, 28, contentW-360, margin, y, 0, 0)
		if co.GetFirstTime() {
			x := margin + math.Min(c.measureWith(fDisplay, name, 52), contentW-360) + 20
			c.use(fMonoBold, 22)
			w := c.measure("NEW", 2.6) + 30
			c.dc.DrawRoundedRectangle(x, y-36, w, 42, 21)
			c.setColor(acc)
			c.dc.Fill()
			c.setColor(colPaper)
			c.text("NEW", x+w/2, y-7, 0.5, 2.6)
		}
		c.setColor(colMuted)
		c.label(count(int64(co.GetCitiesCount()), "city", "cities")+" · "+count(int64(co.GetDays()), "day", "days"),
			false, 22, 320, margin+contentW, y-6, 1)
		c.dashedLine(margin, y+24, margin+contentW, y+24, 4, 8, 2, withAlpha(colInk, 0.2))
		y += 82
	}
	if y < 1700 {
		c.setColor(colInk)
		c.wrapBlock(m.GetHeadline(), fSansBold, 44, 30, contentW, 2, margin, math.Max(y+40, 1620), 0, 1.2)
	}
}

// measureWith measures s in font id at size, leaving the canvas on that face.
func (c *canvas) measureWith(id fontID, s string, size float64) float64 {
	c.use(id, size)
	return c.measure(s, 0)
}

// ---------------------------------------------------------------- Timeline

var monthInitials = [12]string{"J", "F", "M", "A", "M", "J", "J", "A", "S", "O", "N", "D"}

func drawTripsTimeline(c *canvas, r *recapv1.Recap, t *recapv1.TripsTimelineCard) {
	fill(c, colNight)
	rings(c, 980, 260)
	chrome(c, r, true)

	n := int64(max(t.GetTripsCount(), 0))
	c.setColor(colCoral)
	num := c.fitLine(thousands(n), fDisplay, 300, 56, contentW/2, 0)
	c.text(num, margin, 540, 0, 0)
	nw := c.measure(num, 0)
	c.use(fItalic, 92)
	c.setColor(colPaper)
	c.text(plural(n, "trip", "trips"), margin+nw+24, 540, 0, 0)
	c.setColor(withAlpha(colPaper, 0.7))
	c.label(count(int64(max(t.GetDaysAway(), 0)), "day away", "days away"), false, 28, contentW, margin, 610, 0)

	// Calendar strip: one column per month, the bar its days away.
	const top, base = 760.0, 1300.0
	step := float64(contentW) / 12
	most := int32(1)
	for _, m := range t.GetMonths() {
		most = max(most, m.GetDays())
	}
	for _, m := range t.GetMonths() {
		i := m.GetMonth() - 1
		if i < 0 || i > 11 {
			continue
		}
		cx := margin + step*(float64(i)+0.5)
		c.dc.DrawRoundedRectangle(cx-step*0.32, top, step*0.64, base-top, 10)
		c.setColor(withAlpha(colPaper, 0.06))
		c.dc.Fill()
		if d := m.GetDays(); d > 0 {
			h := math.Max(24, (base-top)*float64(d)/float64(most))
			c.dc.DrawRoundedRectangle(cx-step*0.32, base-h, step*0.64, h, 10)
			if m.GetMonth() == t.GetBusiestMonth() {
				c.setColor(colCoral)
			} else {
				c.setColor(withAlpha(colPaper, 0.75))
			}
			c.dc.Fill()
		}
		c.setColor(withAlpha(colPaper, 0.6))
		c.label(monthInitials[i], m.GetMonth() == t.GetBusiestMonth(), 26, step, cx, base+56, 0.5)
	}

	// The year's trips as a run of ticket labels.
	y := base + 150
	x := float64(margin)
	for _, tr := range t.GetTrips() {
		txt := strings.ToUpper(firstNonEmpty(tr.GetCityName(), "Trip"))
		c.use(fMonoBold, 24)
		w := math.Min(c.measure(txt, 2.8)+44, contentW)
		if x+w > margin+contentW {
			x = margin
			y += 70
		}
		if y > 1700 {
			break
		}
		c.dc.DrawRoundedRectangle(x, y-40, w, 54, 8)
		c.dc.SetLineWidth(2)
		c.setColor(withAlpha(colPaper, 0.45))
		c.dc.Stroke()
		c.setColor(colPaper)
		c.fitText(txt, fMonoBold, 24, 14, w-30, x+w/2, y-5, 0.5, 2.8)
		x += w + 16
	}
	if y < 1620 {
		c.setColor(colPaper)
		c.wrapBlock(t.GetHeadline(), fSansBold, 46, 30, contentW, 2, margin, math.Max(y+130, 1600), 0, 1.2)
	}
}

// ---------------------------------------------------------------- Top city

func drawTopCity(c *canvas, r *recapv1.Recap, t *recapv1.TopCityCard) {
	fill(c, colCream)
	guilloche(c, 540, 760, 160, 280)
	chrome(c, r, false)
	acc := accent(r)

	c.setColor(colMuted)
	c.label("Your city this year", false, 26, contentW, margin, 260, 0)

	// A postcard, slightly turned, with the city across it.
	c.layer(980, 760, 540, 720, -3, func(l *canvas) {
		const ox, oy, pw, ph = 30.0, 30.0, 920.0, 700.0
		l.shadow(ox, oy, pw, ph, 12)
		l.dc.DrawRoundedRectangle(ox, oy, pw, ph, 12)
		l.setColor(colPaper)
		l.dc.Fill()
		l.dc.DrawRectangle(ox+40, oy+40, pw-80, ph-200)
		l.setColor(acc)
		l.dc.Fill()
		l.dc.SetLineWidth(3)
		for i := range 5 {
			wy := oy + 120 + float64(i)*70
			l.dc.NewSubPath()
			for x := ox + 60; x <= ox+pw-60; x += 4 {
				l.dc.LineTo(x, wy+10*math.Sin((x-ox)/40+float64(i)))
			}
		}
		l.setColor(withAlpha(colPaper, 0.18))
		l.dc.Stroke()
		l.setColor(colPaper)
		lines := l.wrapFit(firstNonEmpty(t.GetCityName(), "Your city"), fDisplay, 150, 56, pw-160, 2)
		lh := l.lineHeight(1.0)
		top := oy + 40 + (ph-200)/2 - float64(len(lines)-1)*lh/2 + l.size*0.34
		l.block(lines, ox+pw/2, top, 0.5, 1.0)
		l.setColor(colInk)
		l.label("Greetings from", true, 24, pw/2, ox+40, oy+ph-90, 0)
		if co := firstNonEmpty(t.GetCountry(), t.GetCountryCode()); co != "" {
			l.setColor(colMuted)
			l.label(co, false, 24, pw/2-60, ox+pw-40, oy+ph-90, 1)
		}
	})

	c.statRow(1310, colInk, colMuted, []stat{
		{int64(max(t.GetDays(), 0)), "Days"},
		{int64(max(t.GetPlacesCount(), 0)), "Places"},
		{int64(max(t.GetTripsCount(), 0)), "Trips"},
	})
	c.setColor(colInk)
	c.wrapBlock(t.GetHeadline(), fSansBold, 48, 30, contentW, 3, margin, 1540, 0, 1.2)
}

// ---------------------------------------------------------------- Taste shift

func drawTasteShift(c *canvas, r *recapv1.Recap, t *recapv1.TasteShiftCard) {
	fill(c, colPostcard)
	chrome(c, r, false)
	acc := accent(r)

	c.setColor(colMuted)
	c.label("Your taste, January to now", false, 26, contentW, margin, 260, 0)
	c.setColor(colInk)
	end := c.wrapBlock(firstNonEmpty(t.GetTrait().GetLabel(), "Something new"), fDisplay, 120, 48, contentW, 3, margin, 420, 0, 1.02)

	// Before and after, as two bars.
	bars := []struct {
		label string
		v     float64
		col   color.NRGBA
	}{
		{"January", clamp01(t.GetStrengthStart()), withAlpha(colInk, 0.35)},
		{"Now", clamp01(t.GetStrengthEnd()), acc},
	}
	y := end + 160
	for _, b := range bars {
		c.setColor(colMuted)
		c.label(b.label, false, 24, 300, margin, y, 0)
		c.dc.DrawRoundedRectangle(margin, y+24, contentW, 56, 28)
		c.setColor(withAlpha(colPaper, 0.8))
		c.dc.Fill()
		if w := float64(contentW) * b.v; w > 0 {
			c.dc.DrawRoundedRectangle(margin, y+24, math.Max(56, w), 56, 28)
			c.setColor(b.col)
			c.dc.Fill()
		}
		y += 170
	}

	if ups := t.GetRunnersUp(); len(ups) > 0 {
		y += 40
		c.setColor(colMuted)
		c.label("Also on the rise", false, 24, contentW, margin, y, 0)
		y += 70
		for _, u := range ups {
			c.setColor(colInk)
			c.fitText(firstNonEmpty(u.GetLabel(), "—"), fDisplay, 56, 28, contentW, margin, y, 0, 0)
			y += 80
		}
	}
	c.setColor(colInk)
	c.wrapBlock(t.GetHeadline(), fSansBold, 46, 30, contentW, 3, margin, math.Max(y+40, 1540), 0, 1.2)
}

// ---------------------------------------------------------------- Streak

func drawStreak(c *canvas, r *recapv1.Recap, s *recapv1.StreakCard) {
	fill(c, colNight)
	rings(c, 100, 1800)
	chrome(c, r, true)

	n := int64(max(s.GetLongestStreakDays(), 0))
	c.setColor(colCoral)
	c.fitText(thousands(n), fDisplay, 360, 56, contentW, margin, 600, 0, 0)
	c.setColor(colPaper)
	c.use(fItalic, 88)
	c.text("days in a row", margin, 720, 0, 0)

	// The run itself: one bead per day, up to five rows of seven.
	shown := min(int(n), 35)
	const d, gap = 96.0, 36.0
	y0 := 880.0
	for i := range shown {
		cx := margin + d/2 + float64(i%7)*(d+gap)
		cy := y0 + float64(i/7)*(d+gap)
		c.dc.DrawCircle(cx, cy, d/2)
		c.setColor(colCoral)
		c.dc.Fill()
	}
	rowsUsed := (shown + 6) / 7
	y := y0 + float64(rowsUsed)*(d+gap)
	if int(n) > shown {
		c.setColor(withAlpha(colPaper, 0.7))
		c.label(fmt.Sprintf("+%d more", int(n)-shown), true, 24, contentW, margin, y-10, 0)
		y += 40
	}
	c.setColor(colPaper)
	c.wrapBlock(s.GetHeadline(), fSansBold, 46, 30, contentW, 3, margin, math.Max(y+60, 1180), 0, 1.2)

	stats := []stat{{int64(max(s.GetActiveDays(), 0)), "Active days"}}
	if m := s.GetBestMonth(); m >= 1 && m <= 12 {
		// Two columns; the month is a word, so statRow's numerals do not fit.
		c.setColor(colPaper)
		c.fitText(thousands(stats[0].v), fDisplay, 116, 24, contentW/2-28, margin+contentW/4, 1560, 0.5, 0)
		c.fitText(time.Month(m).String()[:3], fDisplay, 116, 24, contentW/2-28, margin+3*contentW/4, 1560, 0.5, 0)
		c.setColor(withAlpha(colPaper, 0.65))
		c.label("Active days", false, 22, contentW/2-20, margin+contentW/4, 1616, 0.5)
		c.label("Best month", false, 22, contentW/2-20, margin+3*contentW/4, 1616, 0.5)
		c.dashedLine(StoryW/2, 1470, StoryW/2, 1630, 6, 8, 2, withAlpha(colPaper, 0.3))
	} else {
		c.statRow(1560, colPaper, withAlpha(colPaper, 0.65), stats)
	}
}

// ---------------------------------------------------------------- Contributions

func drawContributions(c *canvas, r *recapv1.Recap, k *recapv1.ContributionsCard) {
	fill(c, colCream)
	chrome(c, r, false)
	acc := accent(r)

	total := int64(max(k.GetReviewsCount(), 0) + max(k.GetListsCount(), 0) + max(k.GetPostsCount(), 0) + max(k.GetSubmissionsCount(), 0))
	c.setColor(colMuted)
	c.label("Given back", false, 26, contentW, margin, 260, 0)
	c.setColor(colInk)
	c.fitText(thousands(total), fDisplay, 300, 56, contentW, margin, 560, 0, 0)
	c.setColor(acc)
	c.use(fItalic, 80)
	c.text("for other travellers", margin, 670, 0, 0)

	tiles := []stat{
		{int64(max(k.GetReviewsCount(), 0)), "Reviews"},
		{int64(max(k.GetListsCount(), 0)), "Lists"},
		{int64(max(k.GetPostsCount(), 0)), "Posts"},
		{int64(max(k.GetSubmissionsCount(), 0)), "Places added"},
	}
	const tw, th, gap = 438.0, 300.0, 60.0
	for i, t := range tiles {
		x := margin + float64(i%2)*(tw+gap)
		y := 800 + float64(i/2)*(th+gap)
		c.shadow(x, y, tw, th, 16)
		c.dc.DrawRoundedRectangle(x, y, tw, th, 16)
		c.setColor(colPaper)
		c.dc.Fill()
		ink := colInk
		if t.v == 0 {
			ink = withAlpha(colInk, 0.3)
		}
		c.setColor(ink)
		c.fitText(thousands(t.v), fDisplay, 140, 32, tw-60, x+tw/2, y+180, 0.5, 0)
		c.setColor(colMuted)
		c.label(t.k, false, 24, tw-40, x+tw/2, y+246, 0.5)
	}
	c.setColor(colInk)
	c.wrapBlock(k.GetHeadline(), fSansBold, 46, 30, contentW, 3, margin, 1580, 0, 1.2)
}

// ---------------------------------------------------------------- Percentile

func metricWords(m recapv1.PercentileMetric) string {
	switch m {
	case recapv1.PercentileMetric_PERCENTILE_METRIC_CITIES:
		return "for cities"
	case recapv1.PercentileMetric_PERCENTILE_METRIC_DAYS_WALKED:
		return "for days walked"
	case recapv1.PercentileMetric_PERCENTILE_METRIC_CONTRIBUTIONS:
		return "for giving back"
	default:
		return "for places kept"
	}
}

func drawPercentile(c *canvas, r *recapv1.Recap, p *recapv1.PercentileCard) {
	fill(c, colSand)
	guilloche(c, 540, 900, 180, 320)
	chrome(c, r, false)
	acc := accent(r)

	c.setColor(colMuted)
	c.label("Of every Loci traveller this year", false, 26, contentW, margin, 260, 0)

	ink := withAlpha(acc, 0.92)
	c.layer(760, 760, 540, 900, 8, func(l *canvas) {
		const cx, cy = 380.0, 380.0
		l.scallop(cx, cy, 330, 10, 40)
		l.setColor(ink)
		l.dc.Fill()
		l.dc.SetDash(8, 8)
		l.dc.SetLineWidth(3)
		l.dc.DrawCircle(cx, cy, 290)
		l.setColor(withAlpha(colPaper, 0.6))
		l.dc.Stroke()
		l.dc.SetDash()
		l.setColor(colPaper)
		l.label("Top", true, 40, 300, cx, cy-120, 0.5)
		l.fitText(fmt.Sprintf("%d%%", max(p.GetTopPercent(), 1)), fDisplay, 230, 60, 480, cx, cy+80, 0.5, 0)
		l.label(metricWords(p.GetMetric()), false, 26, 420, cx, cy+170, 0.5)
	})

	if v := p.GetValue(); v > 0 {
		c.setColor(colInk)
		c.label(count(int64(v), "place", "places")+" this year", true, 30, contentW, StoryW/2, 1400, 0.5)
	}
	c.setColor(colInk)
	c.wrapBlock(p.GetHeadline(), fSansBold, 48, 30, contentW, 3, margin, 1540, 0, 1.2)
}
