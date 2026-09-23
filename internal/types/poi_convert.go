package locitypes

// HotelsToPOIs adapts hotel details into POI entries for client responses.
// Hotels and restaurants keep their own structs internally; every client
// surface (the stream, AiCityResponse, exports) shows them as places.
func HotelsToPOIs(hotels []HotelDetailedInfo) []POIDetailedInfo {
	pois := make([]POIDetailedInfo, 0, len(hotels))
	for _, h := range hotels {
		p := POIDetailedInfo{
			City:             h.City,
			Name:             h.Name,
			Latitude:         h.Latitude,
			Longitude:        h.Longitude,
			Category:         h.Category,
			Description:      h.Description,
			Rating:           h.Rating,
			Address:          h.Address,
			Tags:             h.Tags,
			Images:           h.Images,
			LlmInteractionID: h.LlmInteractionID,
		}
		if h.PhoneNumber != nil {
			p.PhoneNumber = *h.PhoneNumber
		}
		if h.Website != nil {
			p.Website = *h.Website
		}
		if h.OpeningHours != nil && *h.OpeningHours != "" {
			p.OpeningHours = map[string]string{"general": *h.OpeningHours}
		}
		if h.PriceRange != nil {
			p.PriceRange = *h.PriceRange
		}
		pois = append(pois, p)
	}
	return pois
}

// RestaurantsToPOIs adapts restaurant details into POI entries for client responses.
func RestaurantsToPOIs(restaurants []RestaurantDetailedInfo) []POIDetailedInfo {
	pois := make([]POIDetailedInfo, 0, len(restaurants))
	for _, r := range restaurants {
		poi := POIDetailedInfo{
			City:             r.City,
			Name:             r.Name,
			Latitude:         r.Latitude,
			Longitude:        r.Longitude,
			Category:         r.Category,
			Description:      r.Description,
			Rating:           r.Rating,
			Tags:             r.Tags,
			Images:           r.Images,
			LlmInteractionID: r.LlmInteractionID,
		}
		if r.Address != nil {
			poi.Address = *r.Address
		}
		if r.PhoneNumber != nil {
			poi.PhoneNumber = *r.PhoneNumber
		}
		if r.Website != nil {
			poi.Website = *r.Website
		}
		if r.OpeningHours != nil && *r.OpeningHours != "" {
			poi.OpeningHours = map[string]string{"general": *r.OpeningHours}
		}
		if r.PriceLevel != nil {
			poi.PriceLevel = *r.PriceLevel
		}
		if r.CuisineType != nil {
			poi.CuisineType = *r.CuisineType
		}
		pois = append(pois, poi)
	}
	return pois
}
