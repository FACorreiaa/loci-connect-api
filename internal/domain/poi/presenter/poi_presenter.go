package presenter

import (
	"maps"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/poi"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ToPOIProto(poi *locitypes.POIDetailedInfo) *poiv1.POIDetailedInfo {
	if poi == nil {
		return nil
	}

	openingHours := make(map[string]string)
	maps.Copy(openingHours, poi.OpeningHours)

	website := ""
	if poi.Website != "" {
		website = poi.Website
	}

	phone := ""
	if poi.PhoneNumber != "" {
		phone = poi.PhoneNumber
	}

	return &poiv1.POIDetailedInfo{
		Id:           poi.ID.String(),
		Name:         poi.Name,
		Description:  poi.Description,
		Category:     poi.Category,
		Address:      poi.Address,
		Latitude:     &poi.Latitude,
		Longitude:    &poi.Longitude,
		Rating:       poi.Rating,
		PriceLevel:   poi.PriceLevel,
		Website:      website,
		PhoneNumber:  phone,
		OpeningHours: openingHours,
		Images:       poi.Images,
		CreatedAt:    timestamppb.New(poi.CreatedAt),
		// UpdatedAt:      timestamppb.New(poi.UpdatedAt),
	}
}

func ToPOIProtos(pois []locitypes.POIDetailedInfo) []*poiv1.POIDetailedInfo {
	protos := make([]*poiv1.POIDetailedInfo, len(pois))
	for i, p := range pois {
		protos[i] = ToPOIProto(&p)
	}
	return protos
}
