package presenter

import (
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	cityv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/city"
)

// ToCityProto converts a stored city to the wire type.
//
// Coordinates are carried deliberately. They were dropped here for a long time,
// which meant SearchCities answered every autocomplete with cities that had no
// position — so a picker could not send origin_lat/origin_lon even for cities we
// held perfectly good coordinates for, and every comparison went back through
// name resolution it could have skipped.
func ToCityProto(city locitypes.CityDetail) *cityv1.CityDetail {
	proto := &cityv1.CityDetail{
		Id:              city.ID.String(),
		Name:            city.Name,
		Country:         city.Country,
		StateProvince:   &city.StateProvince,
		AiSummary:       city.AiSummary,
		CenterLatitude:  city.CenterLatitude,
		CenterLongitude: city.CenterLongitude,
	}
	return proto
}

func ToCityProtos(cities []locitypes.CityDetail) []*cityv1.CityDetail {
	protos := make([]*cityv1.CityDetail, len(cities))
	for i, c := range cities {
		protos[i] = ToCityProto(c)
	}
	return protos
}
