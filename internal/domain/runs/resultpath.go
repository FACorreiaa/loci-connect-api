package runs

import (
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

// DomainHotelComparison is the run domain of a turn answered with a hotel
// comparison. It is a run's domain only: clients are told ACCOMMODATION
// (DomainToProto), as before. It exists so a run that finishes while nobody
// is watching links to the chat thread, which keeps the question and the
// verdict, and not to /hotels, which lists searched hotels and has none for
// this session.
const DomainHotelComparison = "hotel_comparison"

// ResultPath is the page that shows a run's result. The stream's complete
// event and the push notification both use it, so a tap and a redirect land
// in the same place.
func ResultPath(domain string, sessionID uuid.UUID, cityName string, tripID uuid.UUID) (path, routeType string, query map[string]string) {
	var base string
	switch domain {
	case "accommodation":
		routeType, base = "hotels", "/hotels"
	case "dining":
		routeType, base = "restaurants", "/restaurants"
	case "activities":
		routeType, base = "activities", "/activities"
	case "nearby":
		routeType, base = "nearme", "/nearme"
	case "gastronomy":
		routeType, base = "gastronomy", "/gastronomy"
	case DomainHotelComparison:
		routeType, base = "chat", "/chat"
	default:
		routeType, base = "itinerary", "/itinerary"
	}
	query = map[string]string{
		"sessionId": sessionID.String(),
		"cityName":  cityName,
		"domain":    routeType,
	}
	path = fmt.Sprintf("%s?sessionId=%s&cityName=%s&domain=%s",
		base, sessionID.String(), url.QueryEscape(cityName), routeType)
	if tripID != uuid.Nil && routeType == "itinerary" {
		query["tripId"] = tripID.String()
		path += "&tripId=" + tripID.String()
	}
	return path, routeType, query
}
