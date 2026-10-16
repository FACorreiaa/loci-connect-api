package render

import (
	"hash/fnv"
	"image/color"
	"math"
	"strconv"
	"strings"
	"unicode"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"golang.org/x/image/font/sfnt"
)

const margin = 72.0

// fill paints the whole canvas.
func fill(c *canvas, col color.Color) {
	c.setColor(col)
	c.dc.Clear()
}

// rings draws faint concentric circles (night backgrounds).
func rings(c *canvas, cx, cy float64) {
	for r := 110.0; r < 1400; r += 74 {
		c.dc.DrawCircle(cx, cy, r)
	}
	c.dc.SetLineWidth(2)
	c.setColor(withAlpha(colPaper, 0.06))
	c.dc.Stroke()
}

// guilloche draws a faint rosette of overlapping circles (banknote paper).
func guilloche(c *canvas, cx, cy, spread, r float64) {
	const n = 36
	for i := range n {
		a := 2 * math.Pi * float64(i) / n
		c.dc.DrawCircle(cx+spread*math.Cos(a), cy+spread*math.Sin(a), r)
	}
	c.dc.SetLineWidth(1.5)
	c.setColor(withAlpha(colInk, 0.07))
	c.dc.Stroke()
}

// chrome draws the "● Loci" mark top-left, the period top-right, and the
// lociai.fyi footer so a shared image always leads back.
func chrome(c *canvas, r *recapv1.Recap, dark bool) {
	ink, muted := colInk, colMuted
	if dark {
		ink, muted = colPaper, withAlpha(colPaper, 0.62)
	}
	c.dc.DrawCircle(margin+12, 118, 12)
	c.setColor(colCoral)
	c.dc.Fill()
	c.use(fMonoBold, 32)
	c.setColor(ink)
	c.text("LOCI", margin+38, 129, 0, 5)
	if p := strings.TrimSpace(r.GetPeriodLabel()); p != "" {
		c.setColor(muted)
		c.fitText(strings.ToUpper(p), fMono, 26, 16, 560, StoryW-margin, 127, 1, 3)
	}
	c.dc.DrawLine(StoryW/2-24, 1792, StoryW/2+24, 1792)
	c.dc.SetLineWidth(3)
	c.setColor(colCoral)
	c.dc.Stroke()
	c.setColor(muted)
	c.use(fMono, 28)
	c.text("LOCIAI.FYI", StoryW/2, 1848, 0.5, 6)
}

// label draws Space Mono caps with tracking proportional to size, shrinking
// and ellipsizing to maxW.
func (c *canvas) label(s string, bold bool, size, maxW, x, y, ax float64) {
	id := fMono
	if bold {
		id = fMonoBold
	}
	c.fitText(strings.ToUpper(s), id, size, math.Max(12, size*0.6), maxW, x, y, ax, size*0.12)
}

func thousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return ""
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(1, v))
}

func formatKm(km float64) string {
	if math.IsNaN(km) || math.IsInf(km, 0) || km < 0 {
		km = 0
	}
	if km > 0 && km < 10 {
		return strconv.FormatFloat(km, 'f', 1, 64)
	}
	if km > 1e12 {
		km = 1e12
	}
	return thousands(int64(math.Round(km)))
}

// initials takes the first letter of up to two words.
func initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		for _, ch := range w {
			if unicode.IsLetter(ch) || unicode.IsDigit(ch) {
				out = append(out, unicode.ToUpper(ch))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// drawable returns s when font id has a glyph for every rune in it, else
// fallback. The embedded fonts are Latin-only; this keeps short tokens such
// as initials from rendering as blanks for, say, a CJK display name.
func drawable(id fontID, s, fallback string) string {
	var buf sfnt.Buffer
	for _, ch := range s {
		if gi, err := fonts[id].GlyphIndex(&buf, ch); err != nil || gi == 0 {
			return fallback
		}
	}
	return s
}

// shortCode falls back to the first three letters of the city.
func shortCode(in *recapv1.IntroCard) string {
	if s := strings.TrimSpace(in.GetShortCode()); s != "" {
		return strings.ToUpper(s)
	}
	var out []rune
	for _, ch := range in.GetPrimaryCity() {
		if unicode.IsLetter(ch) {
			out = append(out, unicode.ToUpper(ch))
		}
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 {
		return "—"
	}
	return string(out)
}

func seed(parts ...string) uint32 {
	h := fnv.New32a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum32()
}

// barcode draws a deterministic bar strip in [x, x+w] x [y, y+h].
func (c *canvas) barcode(s uint32, x, y, w, h float64, col color.Color) {
	c.setColor(col)
	cx := x
	for cx < x+w {
		s = s*1664525 + 1013904223
		bw := float64(2 + (s>>8)%7)
		gap := float64(3 + (s>>16)%6)
		if cx+bw > x+w {
			break
		}
		c.dc.DrawRectangle(cx, y, bw, h)
		cx += bw + gap
	}
	c.dc.Fill()
}

// projected maps route points into the box, equirectangular with a cos(lat)
// correction, preserving aspect and centring. Non-finite points are dropped.
func projected(pts []*recapv1.RoutePoint, x, y, w, h, pad float64) [][2]float64 {
	type ll struct{ lat, lon float64 }
	var ok []ll
	for _, p := range pts {
		lat, lon := p.GetLatitude(), p.GetLongitude()
		if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
			continue
		}
		ok = append(ok, ll{math.Max(-89, math.Min(89, lat)), lon})
	}
	if len(ok) == 0 {
		return nil
	}
	var mean float64
	for _, p := range ok {
		mean += p.lat
	}
	mean /= float64(len(ok))
	k := math.Max(0.1, math.Cos(mean*math.Pi/180))
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	xy := make([][2]float64, len(ok))
	for i, p := range ok {
		px, py := p.lon*k, -p.lat
		xy[i] = [2]float64{px, py}
		minX, maxX = math.Min(minX, px), math.Max(maxX, px)
		minY, maxY = math.Min(minY, py), math.Max(maxY, py)
	}
	iw, ih := w-2*pad, h-2*pad
	sx, sy := math.Inf(1), math.Inf(1)
	if maxX > minX {
		sx = iw / (maxX - minX)
	}
	if maxY > minY {
		sy = ih / (maxY - minY)
	}
	scale := math.Min(sx, sy)
	if math.IsInf(scale, 1) {
		scale = 0
	}
	ox := x + pad + (iw-(maxX-minX)*scale)/2
	oy := y + pad + (ih-(maxY-minY)*scale)/2
	for i := range xy {
		xy[i] = [2]float64{ox + (xy[i][0]-minX)*scale, oy + (xy[i][1]-minY)*scale}
	}
	return xy
}

// routeLine strokes the projected polyline with dots on each stop and larger
// end markers.
func (c *canvas) routeLine(xy [][2]float64, line, dot color.Color, width float64) {
	if len(xy) == 0 {
		return
	}
	c.dc.NewSubPath()
	for _, p := range xy {
		c.dc.LineTo(p[0], p[1])
	}
	c.dc.SetLineWidth(width)
	c.dc.SetLineJoinRound()
	c.dc.SetLineCapRound()
	c.setColor(line)
	c.dc.Stroke()
	r := width * 1.3
	for _, p := range xy {
		c.dc.DrawCircle(p[0], p[1], r)
	}
	c.setColor(dot)
	c.dc.Fill()
	for _, p := range [][2]float64{xy[0], xy[len(xy)-1]} {
		c.dc.DrawCircle(p[0], p[1], r*2)
		c.setColor(line)
		c.dc.Fill()
		c.dc.DrawCircle(p[0], p[1], r*0.9)
		c.setColor(dot)
		c.dc.Fill()
	}
}

// findRoute returns the first route card in r, if any.
func findRoute(r *recapv1.Recap) *recapv1.RouteCard {
	for _, card := range r.GetCards() {
		if rc := card.GetRoute(); rc != nil {
			return rc
		}
	}
	return nil
}
