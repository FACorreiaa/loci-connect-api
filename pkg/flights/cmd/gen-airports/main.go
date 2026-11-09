// Command gen-airports rebuilds pkg/flights/airports.csv from OurAirports
// (https://ourairports.com/data/, public domain). It keeps large and medium
// airports with scheduled service and an IATA code: the ones a flight search
// can land at. Run it from the repository root:
//
//	go run ./pkg/flights/cmd/gen-airports
//
// The output is committed; nothing reads the network at build or test time.
package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"time"
)

const (
	source = "https://davidmegginson.github.io/ourairports-data/airports.csv"
	out    = "pkg/flights/airports.csv"
)

var iataRE = regexp.MustCompile(`^[A-Z]{3}$`)

func main() {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(source)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("%s: %s", source, resp.Status)
	}
	rows, err := filter(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(out)
	if err != nil {
		log.Fatal(err)
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"iata", "name", "city", "country", "lat", "lon", "size", "keywords"})
	for _, r := range rows {
		_ = w.Write(r)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d airports to %s\n", len(rows), out)
}

// filter keeps the airports a flight search can use, sorted by IATA code so
// regenerating produces a readable diff.
func filter(r io.Reader) ([][]string, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	for _, need := range []string{"type", "name", "latitude_deg", "longitude_deg", "iso_country", "municipality", "scheduled_service", "iata_code", "keywords"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("source lost column %q", need)
		}
	}
	var rows [][]string
	seen := map[string]bool{}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		size := ""
		switch rec[col["type"]] {
		case "large_airport":
			size = "2"
		case "medium_airport":
			size = "1"
		default:
			continue
		}
		iata := rec[col["iata_code"]]
		if rec[col["scheduled_service"]] != "yes" || !iataRE.MatchString(iata) || seen[iata] {
			continue
		}
		seen[iata] = true
		rows = append(rows, []string{
			iata, rec[col["name"]], rec[col["municipality"]], rec[col["iso_country"]],
			rec[col["latitude_deg"]], rec[col["longitude_deg"]], size, rec[col["keywords"]],
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return rows, nil
}
