// Package render draws Loci recap cards (trips and years) as PNG images, server-side and in
// pure Go (no cgo), so it runs in the distroless image.
//
// Two shapes are produced: a 1080x1920 story image per card (Card) and a
// 1200x630 link preview for a whole recap (OG). The visual language mirrors the
// web "traveller's desk" mock: cream, ink and coral paper objects — luggage
// tags, passport stamps, boarding passes, ticket stubs, postcards.
//
// Everything is drawn from the recap itself. Nothing is fetched over the
// network (no avatars, no hero images), so rendering is deterministic and
// cannot hang on a slow CDN.
//
// Fonts are embedded and parsed once; every call builds its own canvas and
// font faces, so Card and OG are safe for concurrent use.
package render

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"sync"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// ErrUnsupportedCard is returned by Card when the card is nil or its oneof is
// empty (a card kind this renderer does not know).
var ErrUnsupportedCard = errors.New("render: unsupported card")

// Output sizes in pixels.
const (
	StoryW, StoryH = 1080, 1920
	OGW, OGH       = 1200, 630
)

// Fonts. All are static instances under the SIL Open Font License 1.1; the
// licence texts sit next to them as fonts/OFL-<Family>.txt.
//
//   - Fraunces-Black.ttf  = upstream Fraunces72pt-Black.ttf (weight 900)
//   - Fraunces-Italic.ttf = upstream Fraunces72pt-Italic.ttf (weight 400)
//     from github.com/undercasetype/Fraunces fonts/ttf. Static instances are
//     used because x/image/font/opentype cannot select a variable-font
//     instance. Upstream ships no static ExtraBold (800), so the display face
//     is Black (900), the nearest heavy cut; the 72pt optical size suits the
//     large sizes it is drawn at.
//   - DMSans-Regular.ttf, DMSans-Bold.ttf from github.com/googlefonts/dm-fonts
//     Sans/fonts/ttf (google/fonts only ships DM Sans as a variable font).
//   - SpaceMono-Regular.ttf, SpaceMono-Bold.ttf from google/fonts ofl/spacemono.
//
//go:embed fonts/*.ttf
var fontFS embed.FS

type fontID int

const (
	fDisplay  fontID = iota // Fraunces Black: numerals, names, titles
	fItalic                 // Fraunces Italic: ledes and soft captions
	fSans                   // DM Sans Regular: body copy
	fSansBold               // DM Sans Bold: headlines
	fMono                   // Space Mono Regular: labels
	fMonoBold               // Space Mono Bold: the Loci mark, stamp labels
	numFonts
)

var fontFiles = [numFonts]string{
	fDisplay:  "fonts/Fraunces-Black.ttf",
	fItalic:   "fonts/Fraunces-Italic.ttf",
	fSans:     "fonts/DMSans-Regular.ttf",
	fSansBold: "fonts/DMSans-Bold.ttf",
	fMono:     "fonts/SpaceMono-Regular.ttf",
	fMonoBold: "fonts/SpaceMono-Bold.ttf",
}

var (
	fontsOnce sync.Once
	fontsErr  error
	fonts     [numFonts]*opentype.Font
	// ellipses holds, per font, "…" when the font has the glyph and "..."
	// otherwise.
	ellipses [numFonts]string
)

func loadFonts() error {
	fontsOnce.Do(func() {
		var buf sfnt.Buffer
		for id, name := range fontFiles {
			data, err := fontFS.ReadFile(name)
			if err != nil {
				fontsErr = fmt.Errorf("render: read %s: %w", name, err)
				return
			}
			f, err := opentype.Parse(data)
			if err != nil {
				fontsErr = fmt.Errorf("render: parse %s: %w", name, err)
				return
			}
			fonts[id] = f
			ellipses[id] = "..."
			if gi, err := f.GlyphIndex(&buf, '…'); err == nil && gi != 0 {
				ellipses[id] = "…"
			}
		}
	})
	return fontsErr
}

// Palette.
var (
	colCream     = hexColor("#FDF5EA")
	colInk       = hexColor("#323B42")
	colCoral     = hexColor("#FA7862")
	colDeepCoral = hexColor("#C63C24")
	colNight     = hexColor("#1F2930")
	colPaper     = hexColor("#FFFDF9")
	colSand      = hexColor("#EFE3D2")
	colPostcard  = hexColor("#E9DDCA")
	colMuted     = hexColor("#6C6458")
	colTeal      = hexColor("#2D6A6A")
)

// hexColor parses #RRGGBB. Invalid input yields opaque black; callers that
// accept user input go through parseHex instead.
func hexColor(s string) color.NRGBA {
	c, _ := parseHex(s)
	return c
}

func parseHex(s string) (color.NRGBA, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 7 || s[0] != '#' {
		return color.NRGBA{A: 255}, false
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.NRGBA{A: 255}, false
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
}

// accent is the recap's theme accent when it is a valid #RRGGBB, else deep
// coral.
func accent(r *recapv1.Recap) color.NRGBA {
	if c, ok := parseHex(r.GetTheme().GetAccentHex()); ok {
		return c
	}
	return colDeepCoral
}

// Card renders one card of r as a 1080x1920 story PNG. r may be nil (the
// recap-level fields then render empty). A nil card or one whose oneof is
// empty returns ErrUnsupportedCard.
func Card(r *recapv1.Recap, card *recapv1.RecapCard) ([]byte, error) {
	if card == nil || card.GetCard() == nil {
		return nil, ErrUnsupportedCard
	}
	if err := loadFonts(); err != nil {
		return nil, err
	}
	c := newCanvas(StoryW, StoryH)
	switch k := card.GetCard().(type) {
	case *recapv1.RecapCard_Intro:
		drawIntro(c, r, k.Intro)
	case *recapv1.RecapCard_Places:
		drawPlaces(c, r, k.Places)
	case *recapv1.RecapCard_Route:
		drawRoute(c, r, k.Route)
	case *recapv1.RecapCard_Days:
		drawDays(c, r, k.Days)
	case *recapv1.RecapCard_Taste:
		drawTaste(c, r, k.Taste)
	case *recapv1.RecapCard_Highlights:
		drawHighlights(c, r, k.Highlights)
	case *recapv1.RecapCard_Progress:
		drawProgress(c, r, k.Progress)
	case *recapv1.RecapCard_Field:
		drawField(c, r, k.Field)
	case *recapv1.RecapCard_Friends:
		drawFriends(c, r, k.Friends)
	case *recapv1.RecapCard_Outro:
		drawOutro(c, r, k.Outro)
	case *recapv1.RecapCard_YearMap:
		drawYearMap(c, r, k.YearMap)
	case *recapv1.RecapCard_TripsTimeline:
		drawTripsTimeline(c, r, k.TripsTimeline)
	case *recapv1.RecapCard_TopCity:
		drawTopCity(c, r, k.TopCity)
	case *recapv1.RecapCard_TasteShift:
		drawTasteShift(c, r, k.TasteShift)
	case *recapv1.RecapCard_Streak:
		drawStreak(c, r, k.Streak)
	case *recapv1.RecapCard_Contributions:
		drawContributions(c, r, k.Contributions)
	case *recapv1.RecapCard_Percentile:
		drawPercentile(c, r, k.Percentile)
	default:
		return nil, ErrUnsupportedCard
	}
	return c.encode()
}

// OG renders the 1200x630 link preview for the whole recap: title, period,
// the intro's short code, a summary line, and the route sketch when the recap
// has a route with at least two points. r may be nil.
func OG(r *recapv1.Recap) ([]byte, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	c := newCanvas(OGW, OGH)
	drawOG(c, r)
	return c.encode()
}

func (c *canvas) encode() ([]byte, error) {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&b, c.dc.Image()); err != nil {
		return nil, fmt.Errorf("render: encode png: %w", err)
	}
	return b.Bytes(), nil
}
