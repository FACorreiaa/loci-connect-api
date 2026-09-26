package locitypes

import "strings"

// CityGastronomy is a city's typical gastronomy: what people eat there and
// the well-known places to eat it. It is generated once per city and shared by
// the chat pipeline (appended to itinerary and discovery answers) and the
// standalone GastronomyService, so the JSON tags are also the shape the
// prompt asks the model for.
type CityGastronomy struct {
	CityName           string   `json:"city_name"`
	Country            string   `json:"country"`
	Overview           string   `json:"overview"`
	CulinaryTraditions []string `json:"culinary_traditions,omitempty"`
	Dishes             []Dish   `json:"dishes"`
	DiningTips         []string `json:"dining_tips,omitempty"`
}

// Dish is one typical dish or drink.
type Dish struct {
	Name        string `json:"name"`
	LocalName   string `json:"local_name,omitempty"`
	Description string `json:"description"`
	// Category is one of the DishCategory* values; anything else is treated
	// as a main dish.
	Category    string            `json:"category"`
	IsSignature bool              `json:"is_signature"`
	Places      []GastronomyPlace `json:"places"`
	// Tags are main-ingredient and diet tags (GastronomyTags), for filtering.
	Tags []string `json:"tags,omitempty"`
}

// GastronomyPlace is a light reference to a well-known place to eat a dish.
// It is deliberately not a POI: it has no id and is never written to
// points_of_interest.
type GastronomyPlace struct {
	Name         string   `json:"name"`
	Neighborhood string   `json:"neighborhood,omitempty"`
	Address      string   `json:"address,omitempty"`
	WhyFamous    string   `json:"why_famous,omitempty"`
	PriceRange   string   `json:"price_range,omitempty"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
	Website      string   `json:"website,omitempty"`
}

// Dish categories, as the prompt spells them.
const (
	DishCategoryMain       = "main"
	DishCategoryStreetFood = "street_food"
	DishCategorySnack      = "snack"
	DishCategoryDessert    = "dessert"
	DishCategoryDrink      = "drink"
)

// GastronomyTags is the tag vocabulary the prompt offers. Normalize keeps
// only these, so a filter chip always matches the same spelling.
var GastronomyTags = []string{
	"seafood", "fish", "meat", "pork", "beef", "poultry", "vegetarian", "vegan",
	"cheese", "pastry", "bread", "soup", "rice", "fruit", "spicy", "alcoholic",
}

var gastronomyTagSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(GastronomyTags))
	for _, t := range GastronomyTags {
		m[t] = struct{}{}
	}
	return m
}()

// MinGastronomyDishes is the fewest dishes an answer may have and still be
// shown or cached. Fewer means the model did not really answer.
const MinGastronomyDishes = 3

// Normalize drops dishes without a name and places without a name, and
// coordinates that are out of range or given only half. It mutates g.
func (g *CityGastronomy) Normalize() {
	if g == nil {
		return
	}
	dishes := g.Dishes[:0]
	for _, d := range g.Dishes {
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" {
			continue
		}
		places := d.Places[:0]
		for _, p := range d.Places {
			p.Name = strings.TrimSpace(p.Name)
			if p.Name == "" {
				continue
			}
			if p.Latitude == nil || p.Longitude == nil ||
				*p.Latitude < -90 || *p.Latitude > 90 ||
				*p.Longitude < -180 || *p.Longitude > 180 ||
				(*p.Latitude == 0 && *p.Longitude == 0) {
				p.Latitude, p.Longitude = nil, nil
			}
			places = append(places, p)
		}
		d.Places = places
		d.Tags = normalizeTags(d.Tags)
		dishes = append(dishes, d)
	}
	g.Dishes = dishes
}

// Usable reports whether the answer is worth showing and caching: an overview
// and at least MinGastronomyDishes dishes, each with at least one place.
func (g *CityGastronomy) Usable() bool {
	if g == nil || strings.TrimSpace(g.Overview) == "" || len(g.Dishes) < MinGastronomyDishes {
		return false
	}
	for _, d := range g.Dishes {
		if len(d.Places) == 0 {
			return false
		}
	}
	return true
}

// normalizeTags lower-cases tags, keeps the known vocabulary, and drops
// duplicates, preserving order.
func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if _, known := gastronomyTagSet[t]; !known {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
