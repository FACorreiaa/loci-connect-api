package presenter

import (
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	cityv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/city"
)

func ToCityProto(city locitypes.CityDetail) *cityv1.CityDetail {
	return &cityv1.CityDetail{
		Id:            city.ID.String(),
		Name:          city.Name,
		Country:       city.Country,
		StateProvince: &city.StateProvince,
	}
}

func ToCityProtos(cities []locitypes.CityDetail) []*cityv1.CityDetail {
	protos := make([]*cityv1.CityDetail, len(cities))
	for i, c := range cities {
		protos[i] = ToCityProto(c)
	}
	return protos
}
