package locitypes

import (
	"context"
	"testing"
)

func TestDetectDomainGastronomy(t *testing.T) {
	d := &DomainDetector{}
	for msg, want := range map[string]DomainType{
		"gastronomy in Madeira":        DomainGastronomy,
		"Food in Madeira":              DomainGastronomy,
		"typical dishes of Porto":      DomainGastronomy,
		"Madeiran cuisine":             DomainGastronomy,
		"what to eat in Lisbon":        DomainGastronomy,
		"what should I eat in Naples?": DomainGastronomy,
		"restaurants in Madeira":       DomainDining,
		"where to eat in Madeira":      DomainDining,
		"food restaurants in Funchal":  DomainDining,
		"3 day food trip in Porto":     DomainItinerary,
		"what to eat on a 3 day trip":  DomainItinerary,
		"hotels in Madeira":            DomainAccommodation,
		"museums in Madeira":           DomainActivities,
		"Madeira":                      DomainGeneral,
	} {
		if got := d.DetectDomain(context.Background(), msg); got != want {
			t.Errorf("%q: domain = %s, want %s", msg, got, want)
		}
	}
}
