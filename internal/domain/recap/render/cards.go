package render

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
)

const contentW = StoryW - 2*margin

// ---------------------------------------------------------------- Intro

func drawIntro(c *canvas, r *recapv1.Recap, in *recapv1.IntroCard) {
	fill(c, colNight)
	rings(c, 980, 250)
	chrome(c, r, true)
	acc := accent(r)

	c.setColor(colPaper)
	c.wrapBlock(firstNonEmpty(in.GetLede(), r.GetTitle()), fItalic, 76, 44, contentW, 4, margin, 300, 0, 1.18)

	// The luggage tag, drawn flat offscreen then tilted -2°.
	const tw, th, ox, oy = 800.0, 1000.0, 30.0, 20.0
	const cx, cy, tilt = 540.0, 1190.0, -2.0
	tag := func(l *canvas) {
		poly := func(dy float64) {
			l.dc.NewSubPath()
			l.dc.MoveTo(ox, oy+110+dy)
			l.dc.LineTo(ox+110, oy+dy)
			l.dc.LineTo(ox+tw-110, oy+dy)
			l.dc.LineTo(ox+tw, oy+110+dy)
			l.dc.LineTo(ox+tw, oy+th+dy)
			l.dc.LineTo(ox, oy+th+dy)
			l.dc.ClosePath()
		}
		poly(14)
		l.setColor(color.NRGBA{A: 90})
		l.dc.Fill()
		poly(0)
		l.setColor(colPaper)
		l.dc.Fill()
		// Reinforcement ring and punched hole.
		l.dc.DrawCircle(ox+tw/2, oy+92, 44)
		l.setColor(colSand)
		l.dc.Fill()
		l.dc.DrawCircle(ox+tw/2, oy+92, 24)
		l.setColor(colNight)
		l.dc.Fill()

		left, right := ox+64, ox+tw-64
		inner := right - left
		mid := ox + tw/2
		l.setColor(colMuted)
		l.label("Loci · Baggage", true, 24, inner/2, left, oy+210, 0)
		if cc := strings.TrimSpace(in.GetCountryCode()); cc != "" {
			l.label(cc, true, 24, inner/2, right, oy+210, 1)
		}
		l.setColor(acc)
		l.fitText(shortCode(in), fDisplay, 300, 110, inner, mid, oy+500, 0.5, 0)

		l.setColor(colInk)
		l.fitText(firstNonEmpty(in.GetPrimaryCity(), "Somewhere new"), fDisplay, 72, 34, inner, mid, oy+610, 0.5, 0)
		if co := strings.TrimSpace(in.GetCountry()); co != "" {
			l.setColor(colMuted)
			l.label(co, false, 26, inner, mid, oy+662, 0.5)
		}
		l.dashedLine(left, oy+712, right, oy+712, 14, 10, 3, withAlpha(colInk, 0.45))

		colX := mid + 30
		l.dashedLine(mid, oy+745, mid, oy+890, 8, 8, 2, withAlpha(colInk, 0.3))
		l.setColor(colMuted)
		l.label("Days", false, 24, inner/2-40, left, oy+775, 0)
		l.label("Month", false, 24, inner/2-40, colX, oy+775, 0)
		days := "—"
		if n := in.GetDayCount(); n > 0 {
			days = strconv.Itoa(int(n))
		}
		l.setColor(colInk)
		l.fitText(days, fDisplay, 104, 28, inner/2-50, left, oy+880, 0, 0)
		l.fitText(firstNonEmpty(r.GetPeriodLabel(), "—"), fDisplay, 64, 28, right-colX, colX, oy+870, 0, 0)

		l.barcode(seed(in.GetShortCode(), in.GetPrimaryCity()), left, oy+920, inner, 52, colInk)
	}
	c.layer(int(tw+2*ox), int(th+2*oy+20), cx, cy, tilt, tag)

	// The string, looped through the hole, in the tag's rotated frame.
	holeX, holeY := cx, cy-(th+2*oy+20)/2+oy+92
	c.dc.Push()
	c.dc.RotateAbout(tilt*math.Pi/180, cx, cy)
	c.dc.SetLineCapRound()
	c.dc.SetLineWidth(5)
	c.setColor(withAlpha(colSand, 0.92))
	for _, d := range []float64{0, 9} {
		c.dc.NewSubPath()
		c.dc.MoveTo(holeX-4+d, holeY-27)
		c.dc.CubicTo(holeX+60+d, holeY-120, holeX+260, holeY-60+d, StoryW+60, holeY-150+d)
	}
	c.dc.Stroke()
	c.dc.Pop()
}

// ---------------------------------------------------------------- Places

var (
	stampTilts  = []float64{-4, 3, -2.5, 4.5, -3.5, 2}
	stampColors = []color.NRGBA{colDeepCoral, colInk, colTeal}
)

func drawPlaces(c *canvas, r *recapv1.Recap, p *recapv1.PlacesCard) {
	fill(c, colSand)
	guilloche(c, 540, 1360, 210, 360)
	chrome(c, r, false)
	acc := accent(r)

	count := int64(p.GetPlacesCount())
	word := plural(count, "place", "places")
	if count <= 0 && p.GetStopsDone() > 0 {
		count = int64(p.GetStopsDone())
		word = "stops"
		if p.GetStopsPlanned() > 0 {
			word = fmt.Sprintf("of %d stops", p.GetStopsPlanned())
		}
	}
	if count < 0 {
		count = 0
	}
	num := c.fitLine(thousands(count), fDisplay, 340, 56, 620, 0)
	c.setColor(colInk)
	c.text(num, margin, 560, 0, 0)
	nw := c.measure(num, 0)
	c.setColor(acc)
	c.fitText(word, fItalic, 110, 40, contentW-nw-28, margin+nw+28, 560, 0, 0)

	y := 650.0
	if n := p.GetCitiesCount(); n > 1 {
		c.setColor(colMuted)
		c.label(fmt.Sprintf("across %d cities", n), false, 26, contentW, margin, 640, 0)
		y = 720
	}
	c.setColor(colInk)
	c.wrapBlock(p.GetHeadline(), fSansBold, 52, 34, contentW, 3, margin, y+20, 0, 1.2)

	groups := p.GetGroups()
	if len(groups) > 6 {
		groups = groups[:6]
	}
	for i, g := range groups {
		col, row := i%2, i/2
		cx := 300.0 + float64(col)*480
		cy := 1110.0 + float64(row)*285
		ink := withAlpha(stampColors[(i+i/3)%3], 0.9)
		sub := strings.TrimSpace(g.GetFirstDate())
		if sub == "" {
			n := int64(g.GetPlacesCount())
			sub = fmt.Sprintf("%d %s", n, plural(n, "place", "places"))
		}
		kind := i % 3
		c.layer(440, 300, cx, cy, stampTilts[i], func(l *canvas) {
			drawStamp(l, kind, g.GetLabel(), sub, ink)
		})
	}
}

// drawStamp draws a passport stamp centred in a 440x300 layer: kind 0 round
// double ring, 1 double-border rectangle, 2 oval.
func drawStamp(l *canvas, kind int, label, sub string, ink color.NRGBA) {
	const cx, cy = 220.0, 150.0
	l.setColor(ink)
	var textW float64
	switch kind {
	case 0:
		l.dc.SetLineWidth(7)
		l.dc.DrawCircle(cx, cy, 130)
		l.dc.Stroke()
		l.dc.SetLineWidth(2.5)
		l.dc.DrawCircle(cx, cy, 112)
		l.dc.Stroke()
		textW = 180
	case 1:
		l.dc.SetLineWidth(6)
		l.dc.DrawRectangle(24, 44, 392, 212)
		l.dc.Stroke()
		l.dc.SetLineWidth(2.5)
		l.dc.DrawRectangle(40, 60, 360, 180)
		l.dc.Stroke()
		textW = 310
	default:
		l.dc.SetLineWidth(6)
		l.dc.DrawEllipse(cx, cy, 205, 122)
		l.dc.Stroke()
		l.dc.SetLineWidth(2.5)
		l.dc.DrawEllipse(cx, cy, 186, 104)
		l.dc.Stroke()
		textW = 250
	}
	label = strings.ToUpper(strings.TrimSpace(label))
	lines := l.wrapFit(label, fMonoBold, 36, 18, textW, 2)
	lh := l.lineHeight(1.08)
	total := float64(len(lines)-1) * lh
	if len(lines) == 0 {
		total = -lh
	}
	top := cy - (total+44)/2 + l.size*0.35
	l.block(lines, cx, top, 0.5, 1.08)
	l.star(cx, top-l.size-14, 9)
	l.dc.Fill()
	l.label(sub, false, 22, textW, cx, top+total+44, 0.5)
}

// ---------------------------------------------------------------- Route

func basisCaption(b recapv1.RouteBasis) string {
	switch b {
	case recapv1.RouteBasis_ROUTE_BASIS_CITIES:
		return "as the crow flies between cities"
	case recapv1.RouteBasis_ROUTE_BASIS_VISITS:
		return "between the places you visited"
	case recapv1.RouteBasis_ROUTE_BASIS_LEGS:
		return "along your legs"
	default:
		return "across the trip"
	}
}

func drawRoute(c *canvas, r *recapv1.Recap, rt *recapv1.RouteCard) {
	fill(c, colNight)
	rings(c, 80, 1840)
	chrome(c, r, true)
	acc := accent(r)

	c.setColor(colPaper)
	c.wrapBlock(rt.GetHeadline(), fSansBold, 50, 32, contentW, 2, margin, 250, 0, 1.2)

	const tx, ty, tw, th = margin, 380.0, float64(contentW), 1340.0
	const in = tx + 40
	const iw = tw - 80
	c.shadow(tx, ty, tw, th, 28)
	c.dc.DrawRoundedRectangle(tx, ty, tw, th, 28)
	c.setColor(colPaper)
	c.dc.Fill()

	// Header band.
	c.dc.Push()
	c.dc.DrawRoundedRectangle(tx, ty, tw, th, 28)
	c.dc.Clip()
	c.dc.DrawRectangle(tx, ty, tw, 110)
	c.setColor(acc)
	c.dc.Fill()
	c.dc.ResetClip()
	c.dc.Pop()
	c.setColor(colPaper)
	c.label("Boarding pass", true, 30, iw/2, in, ty+70, 0)
	if p := strings.TrimSpace(r.GetPeriodLabel()); p != "" {
		c.label(p, false, 24, iw/2-20, in+iw, ty+68, 1)
	}

	pts := rt.GetPoints()
	from, to := "—", "—"
	if len(pts) > 0 {
		from = firstNonEmpty(pts[0].GetLabel(), "—")
		to = firstNonEmpty(pts[len(pts)-1].GetLabel(), "—")
	}
	colW := iw/2 - 60
	c.setColor(colMuted)
	c.label("From", false, 22, colW, in, ty+170, 0)
	c.label("To", false, 22, colW, in+iw, ty+170, 1)
	c.setColor(colInk)
	c.fitText(from, fDisplay, 64, 28, colW, in, ty+250, 0, 0)
	c.fitText(to, fDisplay, 64, 28, colW, in+iw, ty+250, 1, 0)
	// Arrow between the two.
	ax := tx + tw/2
	c.setColor(acc)
	c.dc.SetLineWidth(4)
	c.dc.DrawLine(ax-34, ty+228, ax+30, ty+228)
	c.dc.Stroke()
	c.dc.MoveTo(ax+38, ty+228)
	c.dc.LineTo(ax+20, ty+216)
	c.dc.LineTo(ax+20, ty+240)
	c.dc.ClosePath()
	c.dc.Fill()

	// Sketch.
	const sy, sh = ty + 300, 580.0
	c.dc.DrawRoundedRectangle(in, sy, iw, sh, 18)
	c.setColor(withAlpha(colSand, 0.55))
	c.dc.Fill()
	c.dc.SetLineWidth(1)
	for gx := in + 56; gx < in+iw; gx += 56 {
		c.dc.DrawLine(gx, sy+8, gx, sy+sh-8)
	}
	for gy := sy + 56; gy < sy+sh; gy += 56 {
		c.dc.DrawLine(in+8, gy, in+iw-8, gy)
	}
	c.setColor(withAlpha(colInk, 0.07))
	c.dc.Stroke()
	xy := projected(pts, in, sy, iw, sh, 70)
	if len(xy) >= 2 {
		c.routeLine(xy, colCoral, colInk, 6)
		c.endLabel(pts[0].GetLabel(), xy[0], in, sy, iw, sh)
		c.endLabel(pts[len(pts)-1].GetLabel(), xy[len(xy)-1], in, sy, iw, sh)
	} else {
		mid := sy + sh/2
		c.dashedLine(in+120, mid, in+iw-120, mid, 18, 14, 5, colCoral)
		c.dc.DrawCircle(in+120, mid, 14)
		c.dc.DrawCircle(in+iw-120, mid, 14)
		c.setColor(colCoral)
		c.dc.Fill()
	}

	// Perforation.
	py := sy + sh + 90
	for _, x := range []float64{tx, tx + tw} {
		c.dc.DrawCircle(x, py, 26)
	}
	c.setColor(colNight)
	c.dc.Fill()
	c.dashedLine(tx+44, py, tx+tw-44, py, 6, 12, 3, withAlpha(colInk, 0.35))

	// Distance.
	num := c.fitLine(formatKm(rt.GetDistanceKm()), fDisplay, 210, 48, iw-180, 0)
	c.setColor(acc)
	base := py + 230
	c.text(num, in, base, 0, 0)
	nw := c.measure(num, 0)
	c.use(fItalic, 92)
	c.setColor(colInk)
	c.text("km", in+nw+20, base, 0, 0)
	c.setColor(colMuted)
	c.wrapBlock(basisCaption(rt.GetBasis()), fSans, 38, 26, iw, 2, in, base+70, 0, 1.2)
}

// endLabel puts a route end's label beside its dot, kept inside the box.
func (c *canvas) endLabel(s string, p [2]float64, x, y, w, h float64) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	t := c.fitLine(s, fSansBold, 30, 20, w/2, 0)
	tw := c.measure(t, 0)
	lx := p[0] + 22
	if lx+tw > x+w-16 {
		lx = p[0] - 22 - tw
	}
	lx = math.Max(x+16, lx)
	ly := p[1] - 20
	if ly-c.size < y+12 {
		ly = p[1] + 20 + c.size
	}
	c.dc.DrawRoundedRectangle(lx-8, ly-c.size+2, tw+16, c.size+10, 6)
	c.setColor(withAlpha(colPaper, 0.88))
	c.dc.Fill()
	c.setColor(colInk)
	c.text(t, lx, ly, 0, 0)
}

// ---------------------------------------------------------------- Days

func drawDays(c *canvas, r *recapv1.Recap, d *recapv1.DaysCard) {
	fill(c, colCream)
	chrome(c, r, false)
	acc := accent(r)

	stubs := d.GetDays()
	total := int(d.GetDaysTotal())
	done := int(d.GetDaysCompleted())
	if len(stubs) == 0 && total > 0 {
		n := min(total, 31)
		for i := range n {
			stubs = append(stubs, &recapv1.DayStub{DayNumber: int32(i + 1), Completed: i < done})
		}
	}
	if total <= 0 {
		total = len(stubs)
	}
	done = max(done, 0)

	num := c.fitLine(strconv.Itoa(done), fDisplay, 320, 56, 520, 0)
	c.setColor(colInk)
	c.text(num, margin, 560, 0, 0)
	nw := c.measure(num, 0)
	c.setColor(acc)
	c.fitText("/"+strconv.Itoa(total), fItalic, 150, 60, contentW-nw-16, margin+nw+12, 560, 0, 0)
	c.setColor(colMuted)
	c.label(plural(int64(total), "day", "days")+" completed", false, 26, contentW, margin, 640, 0)
	c.setColor(colInk)
	end := c.wrapBlock(d.GetHeadline(), fSansBold, 52, 34, contentW, 3, margin, 740, 0, 1.2)

	shown := stubs
	if len(shown) > 10 {
		shown = shown[:10]
	}
	const sw, sh, gap = 170.0, 300.0, 21.5
	rows := (len(shown) + 4) / 5
	blockH := float64(rows)*sh + float64(max(rows-1, 0))*40
	top := math.Max(end+110, (end+60+1680)/2-blockH/2)
	for row := 0; row*5 < len(shown); row++ {
		end := min(len(shown), row*5+5)
		rowStubs := shown[row*5 : end]
		n := float64(len(rowStubs))
		x0 := StoryW/2 - (n*sw+(n-1)*gap)/2
		y0 := top + float64(row)*(sh+40)
		for i, s := range rowStubs {
			num := int(s.GetDayNumber())
			if num <= 0 {
				num = row*5 + i + 1
			}
			drawStub(c, x0+float64(i)*(sw+gap), y0, sw, sh, num, s)
		}
	}
	if extra := len(stubs) - len(shown); extra > 0 {
		c.setColor(colMuted)
		c.label(fmt.Sprintf("+%d more %s", extra, plural(int64(extra), "day", "days")), true, 28, contentW, StoryW/2, top+blockH+80, 0.5)
	}
}

func drawStub(c *canvas, x, y, w, h float64, num int, s *recapv1.DayStub) {
	fg, sub := colInk, colMuted
	if s.GetCompleted() {
		c.dc.DrawRoundedRectangle(x, y, w, h, 14)
		c.setColor(colInk)
		c.dc.Fill()
		fg, sub = colCream, withAlpha(colCream, 0.72)
	} else {
		c.dc.DrawRoundedRectangle(x+1.5, y+1.5, w-3, h-3, 14)
		c.setColor(colPaper)
		c.dc.FillPreserve()
		c.dc.SetLineWidth(3)
		c.setColor(colInk)
		c.dc.Stroke()
	}
	// Perforated top edge and tear line, punched in the ground colour.
	for px := x + 17; px < x+w-8; px += 22 {
		c.dc.DrawCircle(px, y, 7)
	}
	for px := x + 14; px < x+w-8; px += 16 {
		c.dc.DrawCircle(px, y+84, 3.2)
	}
	c.setColor(colCream)
	c.dc.Fill()

	mid := x + w/2
	c.setColor(sub)
	c.label("Day", false, 22, w-24, mid, y+60, 0.5)
	c.setColor(fg)
	c.fitText(strconv.Itoa(num), fDisplay, 104, 40, w-24, mid, y+200, 0.5, 0)
	c.setColor(sub)
	if city := strings.TrimSpace(s.GetCityName()); city != "" {
		c.label(city, false, 20, w-24, mid, y+240, 0.5)
	}
	if t := s.GetStopsTotal(); t > 0 {
		c.label(fmt.Sprintf("%d/%d stops", s.GetStopsDone(), t), false, 18, w-24, mid, y+272, 0.5)
	}
}

// ---------------------------------------------------------------- Taste

func trendWord(t recapv1.TasteTrend) string {
	switch t {
	case recapv1.TasteTrend_TASTE_TREND_NEW:
		return "New"
	case recapv1.TasteTrend_TASTE_TREND_GROWING:
		return "Growing"
	case recapv1.TasteTrend_TASTE_TREND_STRONG:
		return "Strong"
	default:
		return ""
	}
}

func evidenceLine(n int32, k recapv1.TasteEvidenceKind) string {
	if n <= 0 {
		return ""
	}
	var one, many string
	switch k {
	case recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_SAVES:
		one, many = "save", "saves"
	case recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_VISITS:
		one, many = "visit", "visits"
	case recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_REVIEWS:
		one, many = "review", "reviews"
	default:
		one, many = "signal", "signals"
	}
	return fmt.Sprintf("from %d %s", n, plural(int64(n), one, many))
}

func drawTaste(c *canvas, r *recapv1.Recap, t *recapv1.TasteCard) {
	fill(c, colPostcard)
	chrome(c, r, false)
	acc := accent(r)

	const lw, lh, ox, oy = 1000, 1500, 30.0, 30.0
	const pw, ph = lw - 2*ox, lh - 2*oy
	c.layer(lw, lh, 540, 1000, -2, func(l *canvas) {
		l.shadow(ox, oy, pw, ph, 10)
		l.dc.DrawRoundedRectangle(ox, oy, pw, ph, 10)
		l.setColor(colPaper)
		l.dc.Fill()
		left, right := ox+60, ox+pw-60
		inner := right - left

		// Postage stamp: cream border with punched perforations, coral face.
		sx, sy, sw, sh := right-180, oy+56, 180.0, 220.0
		l.dc.DrawRectangle(sx, sy, sw, sh)
		l.setColor(colCream)
		l.dc.Fill()
		for x := sx; x <= sx+sw+0.1; x += sw / 7 {
			l.dc.DrawCircle(x, sy, 8)
			l.dc.DrawCircle(x, sy+sh, 8)
		}
		for y := sy; y <= sy+sh+0.1; y += sh / 8 {
			l.dc.DrawCircle(sx, y, 8)
			l.dc.DrawCircle(sx+sw, y, 8)
		}
		l.setColor(colPaper)
		l.dc.Fill()
		fx, fy, fw, fh := sx+18, sy+18, sw-36, sh-36
		l.dc.DrawRectangle(fx, fy, fw, fh)
		l.setColor(colCoral)
		l.dc.Fill()
		l.dc.DrawCircle(fx+fw/2, fy+56, 28)
		l.setColor(colCream)
		l.dc.Fill()
		l.dc.SetLineWidth(4)
		for i := range 3 {
			wy := fy + 104 + float64(i)*16
			l.dc.NewSubPath()
			for x := fx + 12; x <= fx+fw-12; x += 3 {
				l.dc.LineTo(x, wy+4*math.Sin((x-fx)/9))
			}
		}
		l.dc.Stroke()
		l.use(fMonoBold, 18)
		l.text("LOCI", fx+fw/2, fy+fh-12, 0.5, 2)

		// Postmark over the stamp's corner, with cancellation waves.
		mx, my := sx-20, sy+sh+10
		pm := withAlpha(colInk, 0.55)
		l.setColor(pm)
		l.dc.SetLineWidth(4)
		l.dc.DrawCircle(mx, my, 86)
		l.dc.Stroke()
		l.dc.SetLineWidth(1.5)
		l.dc.DrawCircle(mx, my, 72)
		l.dc.Stroke()
		l.dc.SetLineWidth(3)
		for i := range 3 {
			wy := my - 30 + float64(i)*30
			l.dc.NewSubPath()
			for x := mx - 250; x <= mx-96; x += 3 {
				l.dc.LineTo(x, wy+6*math.Sin(x/14))
			}
		}
		l.dc.Stroke()
		l.label("Loci", true, 20, 120, mx, my-12, 0.5)
		l.label(firstNonEmpty(r.GetPeriodLabel(), "Posted"), false, 16, 118, mx, my+22, 0.5)

		l.setColor(colMuted)
		l.label("Postcard · your taste", false, 22, inner-260, left, oy+110, 0)

		l.setColor(colInk)
		end := l.wrapBlock(firstNonEmpty(t.GetHeadline(), "What you went for"), fItalic, 76, 42, inner, 3, left, oy+470, 0, 1.15)
		y := math.Max(end+70, oy+620)
		l.dashedLine(left, y, right, y, 10, 10, 2, withAlpha(colInk, 0.3))
		y += 100

		traits := t.GetTraits()
		if len(traits) > 3 {
			traits = traits[:3]
		}
		if len(traits) == 0 {
			l.setColor(colMuted)
			l.wrapBlock("Your taste is still taking shape.", fItalic, 52, 34, inner, 2, left, y, 0, 1.2)
		}
		for _, tr := range traits {
			trend := strings.ToUpper(trendWord(tr.GetTrend()))
			labelW := inner
			if trend != "" {
				l.use(fMonoBold, 22)
				pwid := l.measure(trend, 2.6) + 32
				l.dc.DrawRoundedRectangle(right-pwid, y-34, pwid, 42, 21)
				l.dc.SetLineWidth(2.5)
				l.setColor(acc)
				l.dc.Stroke()
				l.text(trend, right-pwid/2, y-5, 0.5, 2.6)
				labelW = inner - pwid - 24
			}
			l.setColor(colInk)
			l.fitText(firstNonEmpty(tr.GetLabel(), "Something new"), fDisplay, 56, 30, labelW, left, y, 0, 0)
			l.dc.DrawRoundedRectangle(left, y+32, inner, 14, 7)
			l.setColor(colSand)
			l.dc.Fill()
			if s := clamp01(tr.GetStrength()); s > 0 {
				l.dc.DrawRoundedRectangle(left, y+32, math.Max(14, inner*s), 14, 7)
				l.setColor(acc)
				l.dc.Fill()
			}
			if meta := evidenceLine(tr.GetEvidenceCount(), tr.GetEvidenceKind()); meta != "" {
				l.setColor(colMuted)
				l.label(meta, false, 22, inner, left, y+90, 0)
			}
			y += 200
		}
	})
}

// ---------------------------------------------------------------- Highlights

func reasonCaption(h recapv1.HighlightReason) string {
	switch h {
	case recapv1.HighlightReason_HIGHLIGHT_REASON_FAVORITED:
		return "You saved it"
	case recapv1.HighlightReason_HIGHLIGHT_REASON_REVIEWED:
		return "You reviewed it"
	case recapv1.HighlightReason_HIGHLIGHT_REASON_FIRST_VISIT:
		return "First visit"
	default:
		return ""
	}
}

func drawHighlights(c *canvas, r *recapv1.Recap, h *recapv1.HighlightsCard) {
	fill(c, colCream)
	chrome(c, r, false)
	acc := accent(r)

	c.setColor(colInk)
	c.wrapBlock(firstNonEmpty(h.GetHeadline(), "Places worth keeping"), fItalic, 72, 42, contentW, 2, margin, 290, 0, 1.15)

	items := h.GetHighlights()
	if len(items) > 3 {
		items = items[:3]
	}
	if len(items) == 0 {
		c.setColor(colMuted)
		c.wrapBlock("Nothing pinned yet. The next trip starts here.", fItalic, 52, 34, contentW, 2, margin, 900, 0, 1.2)
	}
	tilts := []float64{-1.6, 1.3, -0.9}
	for i, it := range items {
		cy := 690.0 + float64(i)*410
		c.layer(960, 440, 540, cy, tilts[i], func(l *canvas) {
			const x, y, w, hh = 40.0, 40.0, 880.0, 360.0
			l.shadow(x, y, w, hh, 8)
			l.dc.DrawRoundedRectangle(x, y, w, hh, 8)
			l.setColor(colPaper)
			l.dc.Fill()
			// Pin.
			l.dc.DrawCircle(480+3, y+22, 16)
			l.setColor(color.NRGBA{A: 50})
			l.dc.Fill()
			l.dc.DrawCircle(480, y+18, 16)
			l.setColor(acc)
			l.dc.Fill()
			l.dc.DrawCircle(475, y+13, 5)
			l.setColor(withAlpha(colPaper, 0.6))
			l.dc.Fill()

			left := x + 50
			textW := w - 100 - 130
			l.setColor(colSand)
			l.use(fDisplay, 96)
			l.text(fmt.Sprintf("%02d", i+1), x+w-44, y+130, 1, 0)

			l.setColor(colMuted)
			if cat := strings.TrimSpace(it.GetCategory()); cat != "" {
				l.label(cat, false, 22, textW, left, y+82, 0)
			}
			l.setColor(colInk)
			end := l.wrapBlock(firstNonEmpty(it.GetName(), "A place"), fDisplay, 60, 32, textW, 2, left, y+152, 0, 1.08)
			yy := end + 62
			if rating := min(max(int(it.GetRating()), 0), 5); rating > 0 {
				for s := range 5 {
					l.star(left+20+float64(s)*50, yy-10, 20)
					if s < rating {
						l.setColor(acc)
						l.dc.Fill()
					} else {
						l.setColor(colSand)
						l.dc.Fill()
					}
				}
				yy += 56
			}
			if reason := reasonCaption(it.GetReason()); reason != "" {
				l.setColor(acc)
				l.label(reason, true, 22, textW, left, math.Min(yy, y+hh-28), 0)
			}
		})
	}
}

// ---------------------------------------------------------------- Progress

func drawProgress(c *canvas, r *recapv1.Recap, p *recapv1.ProgressCard) {
	fill(c, colNight)
	rings(c, 1000, 300)
	chrome(c, r, true)
	acc := accent(r)

	c.setColor(colCoral)
	c.fitText(thousands(max(p.GetPoints(), 0)), fDisplay, 300, 56, contentW, margin, 540, 0, 0)
	c.setColor(colPaper)
	c.use(fItalic, 80)
	c.text("points earned", margin, 660, 0, 0)
	c.setColor(withAlpha(colPaper, 0.85))
	end := c.wrapBlock(p.GetHeadline(), fSansBold, 50, 32, contentW, 3, margin, 790, 0, 1.2)

	badges := p.GetBadges()
	shown := badges
	if len(shown) > 6 {
		shown = shown[:6]
	}
	faces := []color.NRGBA{colCoral, colSand, colTeal, acc, colSand, colCoral}
	for i, b := range shown {
		cx := 220.0 + float64(i%3)*320
		cy := 1150.0 + float64(i/3)*300
		face := faces[i%len(faces)]
		fg := colPaper
		if face == colSand {
			fg = colInk
		}
		c.scallop(cx, cy, 122, 6, 24)
		c.setColor(face)
		c.dc.Fill()
		c.dc.SetDash(6, 6)
		c.dc.SetLineWidth(2)
		c.dc.DrawCircle(cx, cy, 98)
		c.setColor(withAlpha(fg, 0.55))
		c.dc.Stroke()
		c.dc.SetDash()
		c.setColor(fg)
		lines := c.wrapFit(strings.ToUpper(firstNonEmpty(b.GetLabel(), "Badge")), fMonoBold, 28, 15, 150, 3)
		lh := c.lineHeight(1.1)
		top := cy - float64(len(lines)-1)*lh/2 + c.size*0.35
		if len(lines) <= 2 {
			c.star(cx, top-c.size-16, 11)
			c.dc.Fill()
			top += 10
		}
		c.block(lines, cx, top, 0.5, 1.1)
	}
	next := end + 140
	if len(shown) > 0 {
		next = 1150 + float64((len(shown)-1)/3)*300 + 190
	}
	if extra := len(badges) - len(shown); extra > 0 {
		c.setColor(withAlpha(colPaper, 0.7))
		c.label(fmt.Sprintf("+%d more badges", extra), false, 24, contentW, StoryW/2, next, 0.5)
		next += 60
	}
	if s := p.GetStreakDays(); s > 0 {
		sy := math.Min(next, 1660)
		txt := fmt.Sprintf("Streak · %d %s", s, plural(int64(s), "day", "days"))
		c.use(fMonoBold, 30)
		t := strings.ToUpper(txt)
		w := c.measure(t, 3.6) + 64
		c.dc.DrawRoundedRectangle(StoryW/2-w/2, sy, w, 72, 36)
		c.dc.SetLineWidth(3)
		c.setColor(colCoral)
		c.dc.Stroke()
		c.setColor(colPaper)
		c.label(txt, true, 30, contentW-64, StoryW/2, sy+47, 0.5)
	}
}

// ---------------------------------------------------------------- Field

func drawField(c *canvas, r *recapv1.Recap, f *recapv1.FieldCard) {
	fill(c, colSand)
	guilloche(c, 540, 830, 170, 300)
	chrome(c, r, false)
	acc := accent(r)

	c.setColor(colMuted)
	c.label("Field report", false, 26, contentW, margin, 250, 0)
	c.setColor(colInk)
	c.fitText(firstNonEmpty(f.GetCityName(), "Your city"), fDisplay, 130, 48, contentW, margin, 390, 0, 0)

	ink := withAlpha(acc, 0.9)
	c.layer(700, 700, 540, 830, -6, func(l *canvas) {
		const cx, cy = 350.0, 350.0
		l.setColor(ink)
		l.dc.SetLineWidth(10)
		l.dc.DrawCircle(cx, cy, 300)
		l.dc.Stroke()
		l.dc.SetLineWidth(3)
		l.dc.DrawCircle(cx, cy, 280)
		l.dc.Stroke()
		l.dc.DrawCircle(cx, cy, 204)
		l.dc.Stroke()
		for i := range 16 {
			a := 2 * math.Pi * float64(i) / 16
			l.star(cx+242*math.Cos(a), cy+242*math.Sin(a), 13)
		}
		l.dc.Fill()
		l.label("Rank", true, 28, 300, cx, cy-80, 0.5)
		lines := l.wrapFit(firstNonEmpty(f.GetRankLabel(), "Unranked"), fDisplay, 92, 36, 340, 2)
		lh := l.lineHeight(1.0)
		top := cy - float64(len(lines)-1)*lh/2 + l.size*0.36
		l.block(lines, cx, top, 0.5, 1.0)
		l.dc.SetLineWidth(3)
		l.dc.DrawLine(cx-70, cy+120, cx+70, cy+120)
		l.dc.Stroke()
		l.label("Loci field", false, 20, 260, cx, cy+160, 0.5)
	})

	stats := []struct {
		v int64
		k string
	}{
		{max(f.GetScore(), 0), "Score"},
		{int64(max(f.GetPlacesKept(), 0)), "Places kept"},
		{int64(max(f.GetDaysFinished(), 0)), "Days finished"},
	}
	for i, s := range stats {
		cx := 222.0 + float64(i)*318
		c.setColor(colInk)
		c.fitText(thousands(s.v), fDisplay, 116, 24, 290, cx, 1340, 0.5, 0)
		c.setColor(colMuted)
		c.label(s.k, false, 22, 290, cx, 1396, 0.5)
		if i > 0 {
			c.dashedLine(cx-159, 1250, cx-159, 1410, 6, 8, 2, withAlpha(colInk, 0.3))
		}
	}
	c.setColor(colInk)
	c.wrapBlock(f.GetHeadline(), fSansBold, 50, 32, contentW, 3, margin, 1540, 0, 1.2)
}

// ---------------------------------------------------------------- Friends

func drawFriends(c *canvas, r *recapv1.Recap, f *recapv1.FriendsCard) {
	fill(c, colCream)
	chrome(c, r, false)
	acc := accent(r)

	n := int64(max(f.GetFriendsBeenCount(), 0))
	c.setColor(colInk)
	c.fitText(thousands(n), fDisplay, 340, 56, contentW, margin, 560, 0, 0)
	c.setColor(acc)
	end := c.wrapBlock(plural(n, "friend has been here", "friends have been here"), fItalic, 88, 48, contentW, 2, margin, 690, 0, 1.1)
	c.setColor(colInk)
	end = c.wrapBlock(f.GetHeadline(), fSansBold, 52, 34, contentW, 3, margin, end+110, 0, 1.2)

	friends := f.GetFriends()
	if len(friends) > 5 {
		friends = friends[:5]
	}
	fills := []color.NRGBA{acc, colInk, colTeal, colCoral, colMuted}
	const d, step = 168.0, 186.0
	cy := math.Min(math.Max(end+300, 1050), 1450)
	k := float64(len(friends))
	x0 := StoryW/2 - (k-1)*step/2
	for i, fr := range friends {
		cx := x0 + float64(i)*step
		c.dc.DrawCircle(cx, cy, d/2+6)
		c.setColor(colCream)
		c.dc.Fill()
		c.dc.DrawCircle(cx, cy, d/2)
		c.setColor(fills[i])
		c.dc.Fill()
		c.setColor(colPaper)
		c.fitText(drawable(fDisplay, initials(fr.GetDisplayName()), "?"), fDisplay, 70, 30, d-40, cx, cy+24, 0.5, 0)
		c.setColor(colMuted)
		if name := strings.Fields(fr.GetDisplayName()); len(name) > 0 {
			c.label(name[0], false, 20, step-14, cx, cy+d/2+48, 0.5)
		}
	}
	if extra := n - int64(len(friends)); extra > 0 && len(friends) > 0 {
		c.setColor(colMuted)
		c.label(fmt.Sprintf("+%s more", thousands(extra)), true, 26, contentW, StoryW/2, cy+d/2+120, 0.5)
	}
}

// ---------------------------------------------------------------- Outro

func drawOutro(c *canvas, r *recapv1.Recap, o *recapv1.OutroCard) {
	fill(c, colNight)
	rings(c, 540, 1960)
	chrome(c, r, true)

	y := 700.0
	if h := strings.TrimSpace(o.GetHeadline()); h != "" {
		c.setColor(colCoral)
		y = c.wrapBlock(h, fItalic, 64, 40, contentW, 2, margin, y, 0, 1.15) + 150
	}
	c.setColor(colPaper)
	y = c.wrapBlock(firstNonEmpty(r.GetTitle(), "Until the next trip"), fDisplay, 120, 56, contentW, 3, margin, y, 0, 1.02)
	c.setColor(withAlpha(colPaper, 0.8))
	c.wrapBlock(o.GetSummaryLine(), fSans, 46, 30, contentW, 4, margin, y+100, 0, 1.3)

	const cta = "Plan your next trip at lociai.fyi"
	t := c.fitLine(cta, fSansBold, 40, 26, contentW-96, 0)
	w := c.measure(t, 0) + 96
	c.dc.DrawRoundedRectangle(StoryW/2-w/2, 1600, w, 108, 54)
	c.setColor(colCoral)
	c.dc.Fill()
	c.setColor(colNight)
	c.text(t, StoryW/2, 1668, 0.5, 0)
}
