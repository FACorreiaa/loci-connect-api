// Package ranking keeps each traveller's personal ranking of places they have
// been, one list per city, the way Beli does: a verdict picks the bucket, then
// "better or worse than Y?" answers place the place inside it by binary
// insertion. Verdicts also teach taste.
package ranking

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
)

// Verdict is the traveller's first answer about a place. Its value is the
// bucket's order: every Loved place ranks above every Fine one.
type Verdict int16

const (
	Loved    Verdict = 1
	Fine     Verdict = 2
	NotForMe Verdict = 3
)

// TasteKey is the preference_feedback metadata key a verdict's signal is stored
// under, so a new verdict replaces the old one and a deleted ranking forgets it.
const TasteKey = "ranking_poi"

var (
	ErrNotFound   = errors.New("ranking not found")
	ErrNotPending = errors.New("ranking is already placed")
	ErrNotFriends = errors.New("rankings are visible to friends only")
)

// Place is one ranked place as a list shows it.
type Place struct {
	POIID     string
	Name      string
	Category  string
	CityName  string
	Verdict   Verdict
	Rank      int // 1-based within the city, across buckets
	Score     float32
	UpdatedAt time.Time
	Pending   bool
}

// Comparison is the place to compare against next.
type Comparison struct {
	POIID string
	Name  string
}

// RateInput is a verdict on a place.
type RateInput struct {
	POIID    string
	POIName  string
	Category string
	CityName string
	Verdict  Verdict
}

// cityKey is how places are grouped into a city's list.
func cityKey(city string) string {
	return strings.ToLower(strings.TrimSpace(city))
}

// TasteSignal is what a verdict teaches taste. Not for me outweighs the visit
// signal it usually follows, so a place someone disliked pushes their taste
// away from it instead of cancelling out to nothing.
func TasteSignal(v Verdict) (event string, weight float32) {
	switch v {
	case Loved:
		return preference.EventVisited, 1.0
	case Fine:
		return preference.EventVisited, 0.2
	default:
		return preference.EventSkipped, 1.5
	}
}

// scoreRange is the 0–10 band a bucket's places spread across.
func scoreRange(v Verdict) (lo, hi float64) {
	switch v {
	case Loved:
		return 6.7, 10
	case Fine:
		return 3.4, 6.6
	default:
		return 0, 3.3
	}
}

// Score is a Beli-style 0–10 score with one decimal: the top of the bucket gets
// the top of its band, the bottom the bottom.
func Score(v Verdict, index, size int) float32 {
	lo, hi := scoreRange(v)
	span := size - 1
	if span < 1 {
		span = 1
	}
	s := hi - (hi-lo)*float64(index)/float64(span)
	return float32(math.Round(s*10) / 10)
}

// nextProbe is the index to compare against in the open range [lo, hi), or done
// when the range is empty and the place belongs at lo.
func nextProbe(lo, hi int) (mid int, done bool) {
	return (lo + hi) / 2, lo >= hi
}

// narrow applies one answer to the open range [lo, hi). idx is the compared
// place's index in the bucket, or -1 when it is no longer there, which teaches
// nothing. An answer about a place outside the range still holds, so it clamps
// rather than being thrown away.
func narrow(lo, hi, idx int, better bool) (int, int) {
	if idx < 0 {
		return lo, hi
	}
	if better {
		if idx < hi {
			hi = idx
		}
	} else if idx+1 > lo {
		lo = idx + 1
	}
	if lo > hi {
		if better {
			hi = lo
		} else {
			lo = hi
		}
	}
	return lo, hi
}
