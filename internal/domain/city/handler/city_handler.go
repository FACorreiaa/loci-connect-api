package handler

import (
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/city/cityconnect"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
)

type CityHandler struct {
	cityconnect.UnimplementedCityServiceHandler
	service city.Service
}

func NewCityHandler(svc city.Service) *CityHandler {
	return &CityHandler{service: svc}
}
