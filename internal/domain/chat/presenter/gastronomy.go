package presenter

import (
	"google.golang.org/protobuf/proto"

	gastronomyv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gastronomy"

	"github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ToCityGastronomy maps a city's gastronomy onto the wire. Nil in, nil out.
func ToCityGastronomy(g *locitypes.CityGastronomy) *gastronomyv1.CityGastronomy {
	if g == nil {
		return nil
	}
	dishes := make([]*gastronomyv1.Dish, 0, len(g.Dishes))
	for _, d := range g.Dishes {
		places := make([]*gastronomyv1.GastronomyPlace, 0, len(d.Places))
		for _, p := range d.Places {
			place := &gastronomyv1.GastronomyPlace{
				Name:         p.Name,
				Neighborhood: p.Neighborhood,
				Address:      p.Address,
				WhyFamous:    p.WhyFamous,
				PriceRange:   p.PriceRange,
				Latitude:     p.Latitude,
				Longitude:    p.Longitude,
			}
			if p.Website != "" {
				place.Website = proto.String(p.Website)
			}
			places = append(places, place)
		}
		dishes = append(dishes, &gastronomyv1.Dish{
			Name:        d.Name,
			LocalName:   d.LocalName,
			Description: d.Description,
			Category:    ToDishCategory(d.Category),
			IsSignature: d.IsSignature,
			Places:      places,
		})
	}
	return &gastronomyv1.CityGastronomy{
		CityName:           g.CityName,
		Country:            g.Country,
		Overview:           g.Overview,
		CulinaryTraditions: g.CulinaryTraditions,
		Dishes:             dishes,
		DiningTips:         g.DiningTips,
	}
}

// ToDishCategory maps the prompt's category words onto the enum. Anything
// unrecognised is a main dish.
func ToDishCategory(c string) gastronomyv1.DishCategory {
	switch c {
	case locitypes.DishCategoryStreetFood:
		return gastronomyv1.DishCategory_DISH_CATEGORY_STREET_FOOD
	case locitypes.DishCategorySnack:
		return gastronomyv1.DishCategory_DISH_CATEGORY_SNACK
	case locitypes.DishCategoryDessert:
		return gastronomyv1.DishCategory_DISH_CATEGORY_DESSERT
	case locitypes.DishCategoryDrink:
		return gastronomyv1.DishCategory_DISH_CATEGORY_DRINK
	default:
		return gastronomyv1.DishCategory_DISH_CATEGORY_MAIN
	}
}
