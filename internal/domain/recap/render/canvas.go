package render

import (
	"image"
	"image/color"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// canvas wraps a gg context with a per-call font-face cache. opentype faces
// carry scratch buffers and are not safe for concurrent use, so they are
// never shared between calls.
type canvas struct {
	dc    *gg.Context
	w, h  float64
	faces map[faceKey]font.Face
	cur   fontID
	size  float64
}

type faceKey struct {
	id   fontID
	size float64
}

func newCanvas(w, h int) *canvas {
	return &canvas{
		dc:    gg.NewContext(w, h),
		w:     float64(w),
		h:     float64(h),
		faces: make(map[faceKey]font.Face),
	}
}

// use selects font id at size px. Sizes are rounded to half pixels so the
// shrink-to-fit loops do not mint a face per float.
func (c *canvas) use(id fontID, size float64) {
	size = math.Max(1, math.Round(size*2)/2)
	k := faceKey{id, size}
	f, ok := c.faces[k]
	if !ok {
		var err error
		f, err = opentype.NewFace(fonts[id], &opentype.FaceOptions{
			Size:    size,
			DPI:     72,
			Hinting: font.HintingNone,
		})
		if err != nil {
			// NewFace only fails on invalid options; sizes are clamped >= 1.
			panic(err)
		}
		c.faces[k] = f
	}
	c.dc.SetFontFace(f)
	c.cur, c.size = id, size
}

func (c *canvas) setColor(col color.Color) { c.dc.SetColor(col) }

// withAlpha returns col at alpha a (0..1).
func withAlpha(col color.NRGBA, a float64) color.NRGBA {
	col.A = uint8(math.Round(math.Max(0, math.Min(1, a)) * 255))
	return col
}

// measure is the advance width of s in the current face, plus tracking px
// between runes.
func (c *canvas) measure(s string, tracking float64) float64 {
	w, _ := c.dc.MeasureString(s)
	if n := utf8.RuneCountInString(s); n > 1 {
		w += tracking * float64(n-1)
	}
	return w
}

// text draws s with its baseline at y. ax anchors horizontally: 0 left, 0.5
// centre, 1 right. tracking adds px between runes (used for caps labels).
// Text ignores the context transform (a gg limitation); rotated text is drawn
// into an offscreen layer and composited with layer().
func (c *canvas) text(s string, x, y, ax, tracking float64) {
	if s == "" {
		return
	}
	w := c.measure(s, tracking)
	x -= ax * w
	if tracking == 0 {
		c.dc.DrawString(s, x, y)
		return
	}
	for _, r := range s {
		g := string(r)
		c.dc.DrawString(g, x, y)
		gw, _ := c.dc.MeasureString(g)
		x += gw + tracking
	}
}

// ellipsize trims s rune by rune until it (plus an ellipsis) fits maxW in the
// current face.
func (c *canvas) ellipsize(s string, maxW, tracking float64) string {
	if c.measure(s, tracking) <= maxW {
		return s
	}
	ell := ellipses[c.cur]
	runes := []rune(s)
	for n := len(runes) - 1; n > 0; n-- {
		t := strings.TrimRight(string(runes[:n]), " ,.;:-–—·") + ell
		if c.measure(t, tracking) <= maxW {
			return t
		}
	}
	if c.measure(ell, tracking) <= maxW {
		return ell
	}
	return ""
}

// fitLine picks the largest size in [minSize, maxSize] at which s fits maxW
// on one line, selects that face, and returns the (possibly ellipsized)
// string to draw.
func (c *canvas) fitLine(s string, id fontID, maxSize, minSize, maxW, tracking float64) string {
	s = oneLine(s)
	size := maxSize
	for {
		c.use(id, size)
		if c.measure(s, tracking) <= maxW || size <= minSize {
			break
		}
		size = math.Max(minSize, size*0.94)
	}
	return c.ellipsize(s, maxW, tracking)
}

// fitText draws s on one line with fitLine and returns the size used.
func (c *canvas) fitText(s string, id fontID, maxSize, minSize, maxW, x, y, ax, tracking float64) float64 {
	t := c.fitLine(s, id, maxSize, minSize, maxW, tracking)
	c.text(t, x, y, ax, tracking)
	return c.size
}

// wrapFit word-wraps s to maxW, shrinking from maxSize towards minSize until
// it fits in maxLines. At the floor it keeps maxLines lines and ellipsizes
// the last one; any single word still wider than maxW is ellipsized too. The
// chosen face is left selected.
func (c *canvas) wrapFit(s string, id fontID, maxSize, minSize, maxW float64, maxLines int) []string {
	s = strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " "))
	if s == "" {
		c.use(id, maxSize)
		return nil
	}
	size := maxSize
	var lines []string
	for {
		c.use(id, size)
		lines = c.dc.WordWrap(s, maxW)
		if len(lines) <= maxLines && c.allFit(lines, maxW) {
			return lines
		}
		if size <= minSize {
			break
		}
		size = math.Max(minSize, size*0.93)
	}
	if len(lines) > maxLines {
		rest := strings.Join(lines[maxLines-1:], " ")
		// The joined rest spans at least two wrapped lines, so it overflows
		// and ellipsize always trims it and marks the cut.
		lines = append(lines[:maxLines-1:maxLines-1], c.ellipsize(rest, maxW, 0))
	}
	for i, l := range lines {
		lines[i] = c.ellipsize(l, maxW, 0)
	}
	return lines
}

func (c *canvas) allFit(lines []string, maxW float64) bool {
	for _, l := range lines {
		if c.measure(l, 0) > maxW {
			return false
		}
	}
	return true
}

// lineHeight for the current face at the given leading multiplier.
func (c *canvas) lineHeight(leading float64) float64 { return c.size * leading }

// block draws lines with the first baseline at y and returns the baseline of
// the last line drawn (y when lines is empty).
func (c *canvas) block(lines []string, x, y, ax, leading float64) float64 {
	lh := c.lineHeight(leading)
	for i, l := range lines {
		c.text(l, x, y+float64(i)*lh, ax, 0)
	}
	if len(lines) == 0 {
		return y
	}
	return y + float64(len(lines)-1)*lh
}

// wrapBlock is wrapFit + block; it returns the baseline of the last line.
func (c *canvas) wrapBlock(s string, id fontID, maxSize, minSize, maxW float64, maxLines int, x, y, ax, leading float64) float64 {
	lines := c.wrapFit(s, id, maxSize, minSize, maxW, maxLines)
	return c.block(lines, x, y, ax, leading)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// layer draws fn into an offscreen w x h canvas and composites it onto c,
// centred at (cx, cy) and rotated by deg degrees. This is how paper objects
// with text get their slight tilt, since gg does not transform glyphs.
func (c *canvas) layer(w, h int, cx, cy, deg float64, fn func(l *canvas)) {
	l := newCanvas(w, h)
	fn(l)
	c.composite(l.dc.Image(), cx, cy, deg)
}

func (c *canvas) composite(im image.Image, cx, cy, deg float64) {
	c.dc.Push()
	c.dc.RotateAbout(gg.Radians(deg), cx, cy)
	c.dc.DrawImageAnchored(im, int(math.Round(cx)), int(math.Round(cy)), 0.5, 0.5)
	c.dc.Pop()
}

// shadow draws a soft drop shadow for a rounded rect, honouring the current
// transform.
func (c *canvas) shadow(x, y, w, h, radius float64) {
	for i, a := range []float64{0.05, 0.07, 0.09} {
		grow := float64(3-i) * 6
		c.dc.DrawRoundedRectangle(x-grow/2, y+10-grow/2+float64(i)*4, w+grow, h+grow, radius+grow/2)
		c.setColor(color.NRGBA{A: uint8(a * 255)})
		c.dc.Fill()
	}
}

// star traces a five-point star centred at (cx, cy).
func (c *canvas) star(cx, cy, outer float64) {
	inner := outer * 0.45
	c.dc.NewSubPath()
	for i := range 10 {
		r := outer
		if i%2 == 1 {
			r = inner
		}
		a := -math.Pi/2 + float64(i)*math.Pi/5
		c.dc.LineTo(cx+r*math.Cos(a), cy+r*math.Sin(a))
	}
	c.dc.ClosePath()
}

// scallop traces a circle with n bumps around its rim (a wax-seal edge).
func (c *canvas) scallop(cx, cy, r, bump float64, n int) {
	c.dc.NewSubPath()
	const steps = 360
	for i := 0; i <= steps; i++ {
		t := 2 * math.Pi * float64(i) / steps
		rr := r + bump*math.Cos(float64(n)*t)
		c.dc.LineTo(cx+rr*math.Cos(t), cy+rr*math.Sin(t))
	}
	c.dc.ClosePath()
}

// dashedLine strokes a dashed line from (x1,y1) to (x2,y2).
func (c *canvas) dashedLine(x1, y1, x2, y2, dash, gap, width float64, col color.Color) {
	c.dc.SetDash(dash, gap)
	c.dc.SetLineWidth(width)
	c.setColor(col)
	c.dc.DrawLine(x1, y1, x2, y2)
	c.dc.Stroke()
	c.dc.SetDash()
}
