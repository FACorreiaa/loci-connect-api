package forge

import "fmt"

func ExampleSeed_Slug() {
	seeds, _ := LoadSeeds()
	for _, s := range seeds[:4] {
		fmt.Println(s.Slug())
	}
	// Output:
	// tromso-northern-lights-4day
	// madeira-mild-winter-escape-3day
	// seville-orange-harvest-3day
	// vienna-ball-season-3day
}
