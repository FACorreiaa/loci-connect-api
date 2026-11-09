# Flight Search Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A "Find flights" entry on city results pages and saved trips (web and iOS). It asks for the origin, prefills a resolved destination airport, and opens plain Skyscanner and Google Flights searches. Usage is tracked, and affiliate tagging is a config switch for later.

**Architecture:**
- The server gains an embedded airport dataset in `pkg/flights` and two RPCs on `TripService` (`SearchAirports`, `ResolveFlightDestination`).
- `flights.DeepLinks` gains an IATA resolver (used for Skyscanner only) and a per-provider URL template.
- Trip flights get their links rebuilt on read.
- Web and iOS share one "Find flights" sheet each, built from the trip planner's existing form, and send three analytics events.

**Tech Stack:**
- Go: Connect RPC, `go:embed`, existing `pkg/placematch`.
- Protobuf: buf, BSR.
- Web: SolidStart, Kobalte, vitest + happy-dom, PostHog.
- iOS: SwiftUI, connect-swift, PostHog.

**Spec:** `loci-connect-server/docs/superpowers/specs/2026-11-09-flight-search-phase1-design.md`

## Global Constraints

- No prices anywhere in phase 1; no paid API calls.
- Proto change = release: merge, tag `v5.54.0` (verify the tag contains the change), `make push`, then server `go get` + `buf generate` into `gen/` + `go mod tidy`, then client `@buf/loci_loci-proto.bufbuild_es` bump, then the iOS Swift package pin bump.
- With no link template set, `flights.DeepLinks` output for a given `Query` is byte-identical to today's.
- Google Flights links use exactly the place the caller sent (name or code). A server-resolved IATA feeds Skyscanner only.
- Affiliate env var: `FLIGHTS_LINK_TEMPLATE_SKYSCANNER`, a template containing `{url}`, substituted with the URL-encoded Skyscanner URL. Unset = plain link. Google has no template.
- Analytics events, the same names on web and iOS:
  - `flight_search_opened` `{source, destination_iata?}`
  - `flight_search_built` `{source, origin_iata?, destination_iata?, has_return, passengers, cabin}`
  - `flight_link_clicked` `{source, provider, origin_iata?, destination_iata?}`
  - `source` ∈ `itinerary|hotels|restaurants|activities|trip|chat`; `provider` ∈ `skyscanner|google_flights`.
  - No free text in events.
- The origin is asked for every time and never stored.
- Outbound links keep `rel="noopener noreferrer"`, plus `sponsored` only when the server marks the link `sponsored: true` (a template was applied).
- WCAG 2.2 AA on web (`loci-client/PRODUCT.md`).

## Review Focus

1. **A city name shared by several countries** (Valencia ES/VE, Santiago CL/ES, San José CR/US). `ResolveFlightDestination` uses the country and coordinates. A name-only resolution inside `BuildFlightLinks` resolves only when every match is in one country, and otherwise leaves Skyscanner off. Tests are in Task 1 and Task 2.
2. **A multi-airport city** (London, New York, Paris, Milan): the destination comes back as the metro code (`LON`), labelled "London (all airports)". Test in Task 1.
3. **Autocomplete input that is an IATA code in lower case or with accents/spaces** (`"lis"`, `" São Paulo "`): an exact IATA match ranks first, matching is accent- and case-insensitive, and an empty or 1-character query returns nothing. Test in Task 1.
4. **A trip saved before this change**, whose stored `links` lack Skyscanner or a template: reading it returns freshly built links. Test in Task 3.
5. **A template without `{url}`** (a misconfigured env): it is ignored with a startup warning and links stay plain. Test in Task 2.

---

## File Structure

**loci-connect-proto**
- Modify `proto/loci/trip/trip.proto`:
  - add `FlightPlaceKind`, `FlightPlaceOption`, the `SearchAirports*` and `ResolveFlightDestination*` messages, and two RPCs on `TripService`;
  - add `bool sponsored = 4` to `FlightLink`.

**loci-connect-server**
- Create `pkg/flights/airports.go`: the dataset type, Search and Resolve.
- Create `pkg/flights/airports.csv`: the embedded data.
- Create `pkg/flights/metros.go`: the metro code map.
- Create `pkg/flights/cmd/gen-airports/main.go`: builds `airports.csv` from OurAirports.
- Modify `pkg/flights/flights.go`: `DeepLinks` gains `Resolver` and `SkyscannerTemplate`; `Link` gains `Sponsored`.
- Modify:
  - `internal/domain/trip/repository.go`: a `RepositoryOption`, and rebuilding links in `loadPlan`;
  - `internal/domain/trip/plan_handler.go`: the two RPC handlers;
  - `internal/domain/trip/mappers.go`: the option mapper and `sponsored`.
- Modify `cmd/api/dependencies.go`: build one `flights.DeepLinks` and pass it to the repository and the service.
- Modify `.env.prod.example`.

**loci-client**
- Modify `src/lib/api/trip-plan.ts` (`searchAirports`, `resolveFlightDestination`) and `src/lib/api/trips.ts` (add `sponsored?: boolean` to `FlightLink`).
- Create:
  - `src/components/flights/AirportField.tsx`: the autocomplete;
  - `src/components/flights/FlightSearchForm.tsx`: extracted from `TripPlanPanel`;
  - `src/components/flights/FindFlightsDialog.tsx`: the dialog plus `FindFlightsButton`;
  - `src/lib/flights/analytics.ts`: event helpers.
- Modify:
  - `src/lib/analytics.ts`: 3 event names;
  - `src/components/trip/TripPlanPanel.tsx`: use `FlightSearchForm`;
  - `src/components/results/ResultsPage.tsx`: the entry button;
  - `src/routes/trips/[id].tsx`: the entry button;
  - `src/components/chat/TripActionCard.tsx`: click tracking.

**loci-ios** (`loci/loci/`)
- Modify:
  - `Core/Analytics/Analytics.swift`: 3 cases;
  - `Features/Trips/Services/TripPlanAPI.swift`: 2 calls;
  - `Features/Trips/UI/TripPlanSection.swift`: use the shared form;
  - `Features/Search/UI/SearchResultsView.swift`: the entry button;
  - `Features/Trips/UI/TripEditorView.swift`: the entry button;
  - `Features/Trips/UI/TripActionCardView.swift`: click tracking.
- Create:
  - `Features/Flights/UI/FlightSearchForm.swift`;
  - `Features/Flights/UI/AirportField.swift`;
  - `Features/Flights/UI/FindFlightsSheet.swift`;
  - `Features/Flights/FlightAnalytics.swift`.
- Tests in `loci/lociTests/` (next to `FlightSearchTests.swift`).

---

### Task 0: Proto: airport search, destination resolve, sponsored links

**Files:**
- Modify: `loci-connect-proto/proto/loci/trip/trip.proto` (near `FlightPlace` ~269, `FlightLink` ~279, `BuildFlightLinks*` ~769, `service TripService` ~860)

**Interfaces:**
- Produces:
  - `enum FlightPlaceKind { FLIGHT_PLACE_KIND_UNSPECIFIED = 0; FLIGHT_PLACE_KIND_AIRPORT = 1; FLIGHT_PLACE_KIND_METRO = 2; }`
  - `message FlightPlaceOption`, with fields:
    - `string iata = 1` (`^[A-Z]{3}$`)
    - `string name = 2`
    - `string city = 3`
    - `string country_code = 4`
    - `FlightPlaceKind kind = 5`
  - `message SearchAirportsRequest { string query = 1 [min_len 2, max_len 100]; int32 limit = 2 [gte 0, lte 20]; }` (0 means 8)
  - `message SearchAirportsResponse { repeated FlightPlaceOption options = 1; }`
  - `message ResolveFlightDestinationRequest`, with fields:
    - `string city = 1` (min 1, max 200)
    - `string country_code = 2` (`^([A-Za-z]{2})?$`)
    - `optional double latitude = 3`
    - `optional double longitude = 4`
  - `message ResolveFlightDestinationResponse { optional FlightPlaceOption destination = 1; }`
  - `FlightLink.sponsored = 4` (bool)
  - RPCs: `SearchAirports`, `ResolveFlightDestination` on `TripService`.

- [ ] **Step 1:** Add the messages, enum, field and RPCs with buf validate rules as above, with doc comments in the file's style.
- [ ] **Step 2:** Run `buf lint && buf breaking --against '.git#branch=main' && buf generate`. Expected: clean, and the `gen/` diff limited to trip files.
- [ ] **Step 3:** Commit on `feat/flight-search`, open a PR, and **stop for the owner's merge**. Then tag `v5.54.0`, push the tag over HTTPS (`git -c credential.helper='!gh auth git-credential' push https://github.com/FACorreiaa/loci-connect-proto.git v5.54.0`), and run `make push`.

---

### Task 1: Airport dataset, search and resolve (server)

**Files:**
- Create: `pkg/flights/airports.go`, `pkg/flights/metros.go`, `pkg/flights/airports.csv`, `pkg/flights/cmd/gen-airports/main.go`
- Test: `pkg/flights/airports_test.go`

**Interfaces:**
- Produces:
  - `type PlaceKind int` (`KindAirport = 1`, `KindMetro = 2`)
  - `type PlaceOption struct { IATA, Name, City, CountryCode string; Kind PlaceKind; Lat, Lon float64 }`
  - `type Airports struct` (unexported fields)
  - `func LoadAirports() (*Airports, error)`: parses the embedded CSV
  - `func (a *Airports) Search(query string, limit int) []PlaceOption`
  - `func (a *Airports) Resolve(city, countryCode string, lat, lon *float64) (PlaceOption, bool)`
  - `func (a *Airports) ResolveName(name string) (string, bool)`: an IATA only when every match is in one country

- [ ] **Step 1: Write the generator.** `go run ./pkg/flights/cmd/gen-airports` downloads `https://davidmegginson.github.io/ourairports-data/airports.csv` and writes `pkg/flights/airports.csv` with the header `iata,name,city,country,lat,lon,size`.
  - Keep rows with `type` in `large_airport`/`medium_airport`, `scheduled_service=yes` and a 3-letter `iata_code`.
  - `size` is 2 for large and 1 for medium.
  - Commit the generated file; the package must not reach the network at build or test time.
- [ ] **Step 2: Write the failing tests** in `airports_test.go`:

```go
func TestSearchRanksExactIATAFirst(t *testing.T)   // Search("lis",8)[0].IATA == "LIS"
func TestSearchFoldsAccentsAndCase(t *testing.T)   // Search(" são paulo ",8) contains "GRU"
func TestSearchShortQueryEmpty(t *testing.T)       // Search("l",8) == nil; Search("",8) == nil
func TestSearchIncludesMetro(t *testing.T)         // Search("london",8)[0] == {IATA:"LON", Kind:KindMetro, Name:"London (all airports)"}
func TestResolveUsesCountry(t *testing.T)          // Resolve("Valencia","ES",nil,nil).IATA == "VLC"; Resolve("Valencia","VE",nil,nil).IATA == "VLN"
func TestResolveMetro(t *testing.T)                // Resolve("London","GB",nil,nil) == LON metro
func TestResolvePrefersLargestThenNearest(t *testing.T) // Resolve("Barcelona","ES",&41.39,&2.17).IATA == "BCN"
func TestResolveUnknown(t *testing.T)              // Resolve("Atlantis","",nil,nil) ok == false
func TestResolveNameAmbiguous(t *testing.T)        // ResolveName("Valencia") ok == false; ResolveName("Lisbon") == "LIS"
```

- [ ] **Step 3:** Run `go test ./pkg/flights/ -run 'Search|Resolve'`. Expected: FAIL (undefined).
- [ ] **Step 4: Implement it.**
  - `go:embed airports.csv`.
  - Match tokens with `placematch.Fold`.
  - Search ranks: exact IATA, then metro name prefix, then city prefix, then airport-name prefix. Within a rank, larger `size` first.
  - Resolve: if the city is in the metro map (`metros.go`: LON, NYC, PAR, MIL, ROM, TYO, OSA, SEL, BJS, SHA, CHI, WAS, MOW, STO, BUE, SAO, RIO, YTO, YMQ, BER), return the metro, with the country checked when one is given. Otherwise filter the airports whose folded city equals the folded input (and the country when given), then take the largest `size`, then the nearest to lat/lon when given.
  - `ResolveName` is `Resolve` with no country, returning ok only if all matches share one country.
- [ ] **Step 5:** Run `go test ./pkg/flights/`. Expected: PASS.
- [ ] **Step 6:** Commit: `feat(flights): embedded airport dataset with search and resolve`.

---

### Task 2: DeepLinks: resolver for Skyscanner, URL template, sponsored flag

**Files:**
- Modify: `pkg/flights/flights.go`
- Test: `pkg/flights/flights_test.go`

**Interfaces:**
- Consumes: `(*Airports).ResolveName` (Task 1)
- Produces:
  - `type IATAResolver interface { ResolveName(name string) (string, bool) }`
  - `type DeepLinks struct { Resolver IATAResolver; SkyscannerTemplate string }`. The zero value behaves exactly as today.
  - `Link.Sponsored bool` with json tag `sponsored,omitempty`
  - `func ValidTemplate(t string) bool`: contains `{url}` and starts with `https://`

- [ ] **Step 1: Write the failing tests:**

```go
func TestZeroValueUnchanged(t *testing.T)            // DeepLinks{}.Links(q) equals the golden URLs already pinned in TestDeepLinks
func TestResolverFillsSkyscannerOnly(t *testing.T)   // Origin{Name:"Lisbon"}, Destination{Name:"Barcelona"}, fake resolver → Skyscanner URL has /lis/bcn/; Google q= still says "from Lisbon" and "to Barcelona"
func TestResolverMissLeavesGoogleOnly(t *testing.T)  // resolver returns false → 1 link, provider google_flights
func TestTemplateWrapsSkyscanner(t *testing.T)       // SkyscannerTemplate "https://aff.example/r?u={url}" → URL == "https://aff.example/r?u=" + url.QueryEscape(plain); Sponsored == true; Google Sponsored == false
func TestInvalidTemplateIgnored(t *testing.T)        // ValidTemplate("https://x/r") == false; DeepLinks built through NewDeepLinks with it → plain URL, Sponsored false
```

- [ ] **Step 2:** Run `go test ./pkg/flights/ -run 'ZeroValue|Resolver|Template'`. Expected: FAIL.
- [ ] **Step 3: Implement it.**
  - `Links(q)`: `googleFlights(q)` exactly as now. For Skyscanner, copy `q`, and for each end without a valid IATA but with a `Name`, call `Resolver.ResolveName` when the resolver is non-nil. Then call `skyscanner(filled)`.
  - When `SkyscannerTemplate != ""`, replace `{url}` with `url.QueryEscape(plain)` and set `Sponsored`.
  - Add `func NewDeepLinks(r IATAResolver, skyscannerTemplate string, logger *slog.Logger) DeepLinks`, which drops an invalid template with `logger.Warn("ignoring FLIGHTS_LINK_TEMPLATE_SKYSCANNER; it needs https:// and {url}")`.
- [ ] **Step 4:** Run `go test ./pkg/flights/`. Expected: PASS, including the existing `TestDeepLinks`.
- [ ] **Step 5:** Commit: `feat(flights): resolve IATA for Skyscanner, affiliate link template`.

---

### Task 3: Trips rebuild flight links on read

**Files:**
- Modify: `internal/domain/trip/repository.go:181-195` (constructor) and `:903-950` (`loadPlan`)
- Test: `internal/domain/trip/repository_links_test.go` (unit test of the rebuild helper; no DB)

**Interfaces:**
- Consumes: `flights.Provider`
- Produces:
  - `type RepositoryOption func(*repository)`
  - `func WithFlightLinks(p flights.Provider) RepositoryOption`
  - `NewRepository(db *pgxpool.Pool, logger *slog.Logger, opts ...RepositoryOption) Repository` (existing callers unchanged)
  - `func rebuildFlightLinks(fs []TripFlight, p flights.Provider)`: sets `fs[i].Links = p.Links(FlightQuery(fs[i]))`; a nil `p` is a no-op

- [ ] **Step 1: Write the failing test:** `TestRebuildFlightLinksReplacesStored`.
  - A flight with stored `Links: [{Provider:"google_flights", URL:"old"}]` and IATA LIS→BCN, rebuilt with `flights.DeepLinks{SkyscannerTemplate:"https://aff.example/r?u={url}"}`, gets 2 links, and the Skyscanner one has `Sponsored == true`.
  - Rebuilt with a nil provider, it is left untouched.
- [ ] **Step 2:** Run `go test ./internal/domain/trip/ -run RebuildFlightLinks`. Expected: FAIL.
- [ ] **Step 3: Implement it.** Store the provider on `repository`, and call `rebuildFlightLinks(t.Flights, r.links)` at the end of `loadPlan`. Writes stay as they are (`AddFlight` still stores links).
- [ ] **Step 4:** Run `go test ./internal/domain/trip/`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat(trip): rebuild saved flights' links on read`.

---

### Task 4: RPC handlers and wiring

**Files:**
- Modify: `go.mod`/`go.sum`/`gen/` (bump to v5.54.0 as in Global Constraints), `internal/domain/trip/plan_handler.go`, `internal/domain/trip/mappers.go`, `cmd/api/dependencies.go:~1149` and where `d.TripRepo` is built, `.env.prod.example`
- Test: `internal/domain/trip/plan_handler_test.go`

**Interfaces:**
- Consumes: `*flights.Airports` (Task 1), `flights.NewDeepLinks` (Task 2), `trip.WithFlightLinks` (Task 3)
- Produces:
  - `func (h *Handler) SearchAirports(ctx, *connect.Request[tripv1.SearchAirportsRequest]) (*connect.Response[tripv1.SearchAirportsResponse], error)`
  - `func (h *Handler) ResolveFlightDestination(ctx, *connect.Request[tripv1.ResolveFlightDestinationRequest]) (*connect.Response[tripv1.ResolveFlightDestinationResponse], error)`
  - `func (h *Handler) WithAirports(a *flights.Airports) *Handler`
  - `func placeOptionToProto(o flights.PlaceOption) *tripv1.FlightPlaceOption`
  - `flightLinkToProto` sets `Sponsored`

- [ ] **Step 1: Write the failing tests** in `plan_handler_test.go`, following its existing setup (lines 49, 79):
  - `TestSearchAirportsHandler`: "lis" → first option LIS, kind AIRPORT; unauthenticated → `CodeUnauthenticated`.
  - `TestResolveFlightDestinationHandler`: Barcelona/ES → BCN; Atlantis → `destination` unset with no error.
  - `TestBuildFlightLinksResolvesSkyscanner`: names only, Lisbon→Barcelona → 2 links, and the Google URL still contains "Lisbon".
- [ ] **Step 2:** Run `go test ./internal/domain/trip/ -run 'SearchAirports|ResolveFlight|BuildFlightLinksResolves'`. Expected: FAIL.
- [ ] **Step 3: Implement the handlers.**
  - Same auth check as `BuildFlightLinks` (`userID(ctx)`).
  - `limit` 0 means 8.
  - With nil airports, return an empty response, not an error.
- [ ] **Step 4: Wire it in `dependencies.go`.**
  - `airports, err := flights.LoadAirports()`. Log and continue with nil on error.
  - `links := flights.NewDeepLinks(airports, os.Getenv("FLIGHTS_LINK_TEMPLATE_SKYSCANNER"), d.Logger)`, used in both `trip.NewService(..., links)` and `trip.NewRepository(..., trip.WithFlightLinks(links))`.
  - `d.TripHandler.WithAirports(airports)`.
  - Document `FLIGHTS_LINK_TEMPLATE_SKYSCANNER` in `.env.prod.example` next to `BOOKING_AFFILIATE_ID`, as "unset in phase 1".

  Take care with nil: pass `airports` as an `IATAResolver` only when it is non-nil, or the interface will hold a typed nil.
- [ ] **Step 5:** Run `go test -race ./pkg/flights/... ./internal/domain/trip/... ./cmd/...` and `golangci-lint run`. Expected: PASS, 0 issues.
- [ ] **Step 6:** Commit, push, and open a PR.

---

### Task 5: Web: API, analytics, shared form, Find flights dialog

**Files:**
- Modify: `package.json` (proto bump), `src/lib/api/trip-plan.ts`, `src/lib/api/trips.ts`, `src/lib/analytics.ts`, `src/components/trip/TripPlanPanel.tsx:118-145,353-385`
- Create: `src/lib/flights/analytics.ts`, `src/components/flights/AirportField.tsx`, `src/components/flights/FlightSearchForm.tsx`, `src/components/flights/FindFlightsDialog.tsx`
- Test: `src/lib/flights/analytics.test.ts`, `src/components/flights/FindFlightsDialog.test.tsx`

**Interfaces:**
- Consumes: the Task 0 RPCs; `PlanApi.buildLinks` (`trip-plan.ts:73`)
- Produces:
  - `PlanApi.searchAirports(query: string): Promise<FlightPlaceOption[]>` and `PlanApi.resolveDestination(city: string, countryCode?: string, lat?: number, lon?: number): Promise<FlightPlaceOption | undefined>`, where `FlightPlaceOption = { iata: string; name: string; city: string; countryCode: string; kind: "airport" | "metro" }`.
  - `type FlightSource = "itinerary" | "hotels" | "restaurants" | "activities" | "trip" | "chat"`
  - `trackFlightOpened(source, destIata?)`, `trackFlightBuilt(source, search: FlightSearch)`, `trackFlightClicked(source, link: FlightLink, search?: FlightSearch)`
  - `<FlightSearchForm initial={Partial<FlightSearch>} onBuilt={(links, search) => void} api={PlanApi} extraActions?={JSX} />`
  - `<FindFlightsButton source city countryCode? lat? lon? startDate? endDate? />`, which opens `<FindFlightsDialog>`

- [ ] **Step 1: Write the failing tests.**
  - `analytics.test.ts`: `trackFlightBuilt("hotels", {origin:{name:"Lisbon",iata:"LIS"}, destination:{name:"Barcelona",iata:"BCN"}, departDate:"2026-12-04", passengers:2})` captures `flight_search_built` with `{source:"hotels", origin_iata:"LIS", destination_iata:"BCN", has_return:false, passengers:2, cabin:"unspecified"}`, and no `name` keys.
  - `FindFlightsDialog.test.tsx` (happy-dom, stub `PlanApi`):
    - Opening calls `resolveDestination("Barcelona","ES")` and shows "Barcelona (BCN)".
    - Typing "lis" lists "Lisbon (LIS)".
    - Picking it, setting a date and pressing "Search flights" renders 2 links, each `target="_blank"`, `rel` containing `noopener`, and `sponsored` only when `link.sponsored`.
    - Clicking one captures `flight_link_clicked` with `provider:"skyscanner"`.
    - When resolve returns undefined, the destination field holds "Barcelona" with no code.
- [ ] **Step 2:** Run `npx vitest run src/lib/flights src/components/flights`. Expected: FAIL.
- [ ] **Step 3: Implement it.**
  - Extract the flight fields from `TripPlanPanel` into `FlightSearchForm`. `TripPlanPanel` keeps "Save to trip" via `extraActions` and its disclaimer copy.
  - `AirportField` is a combobox: debounce 200 ms, minimum 2 characters, arrow keys and Enter, `aria-autocomplete="list"`. Free text is allowed, sent as a name with no code.
  - `FindFlightsDialog` uses the existing `src/ui/dialog.tsx`; its title is "Find flights to {city}".
- [ ] **Step 4:** Run `npx vitest run` (full), `npx tsc --noEmit -p .` and `npx oxlint .`. Expected: all pass.
- [ ] **Step 5:** Commit: `feat(flights): find-flights dialog with airport autocomplete and usage events`.

---

### Task 6: Web: mount the entry points and track the chat card

**Files:**
- Modify: `src/components/results/ResultsPage.tsx` (header ~578), `src/routes/trips/[id].tsx`, `src/components/chat/TripActionCard.tsx:130-145`
- Test: `src/components/chat/TripActionCard.test.tsx` (create if missing)

**Interfaces:**
- Consumes: `FindFlightsButton`, `trackFlightClicked` (Task 5)

- [ ] **Step 1: Write the failing test.** Clicking a flight link in `TripActionCard` captures `flight_link_clicked` with `source:"chat"` and the link's provider.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement it.**
  - **ResultsPage:** render `<FindFlightsButton source={props.domain === "itinerary" ? "itinerary" : props.domain} city={resolvedCity()} countryCode={cityData()?.country_code} lat={cityData()?.center_latitude} lon={cityData()?.center_longitude} />` under the header once `resolvedCity()` is known. Check `cityData` for the country-code field name; use the one the type has.
  - **Trip page:** use `source="trip"` with the trip's first city and its dates.
  - **TripActionCard:** add an `onClick` calling `trackFlightClicked("chat", link)`.
- [ ] **Step 4:** Run the full `npx vitest run` and `npx tsc --noEmit -p .`. Expected: PASS.
- [ ] **Step 5:** Commit, push, and open a PR.

---

### Task 7: iOS: API, analytics, shared form, sheet, entry points

**Files:**
- Modify: the Swift package pin for `loci-connect-proto` → `v5.54.0` (Xcode package dependency / `Package.resolved`), `Core/Analytics/Analytics.swift`, `Features/Trips/Services/TripPlanAPI.swift`, `Features/Trips/UI/TripPlanSection.swift:119-160`, `Features/Search/UI/SearchResultsView.swift`, `Features/Trips/UI/TripEditorView.swift:128`, `Features/Trips/UI/TripActionCardView.swift:16-50`
- Create: `Features/Flights/FlightAnalytics.swift`, `Features/Flights/UI/AirportField.swift`, `Features/Flights/UI/FlightSearchForm.swift`, `Features/Flights/UI/FindFlightsSheet.swift`
- Test: `loci/lociTests/FlightAnalyticsTests.swift`, plus additions to `loci/lociTests/FlightSearchTests.swift`

**Interfaces:**
- Consumes: the Task 0 RPCs; `TripPlanAPI.flightLinks(_:)` (`TripPlanAPI.swift:16`); `FlightSearch` (`:31`)
- Produces:
  - Analytics cases: `flightSearchOpened = "flight_search_opened"`, `flightSearchBuilt = "flight_search_built"`, `flightLinkClicked = "flight_link_clicked"`
  - `enum FlightSource: String { case itinerary, hotels, restaurants, activities, trip, chat }`
  - `enum FlightAnalytics { static func opened(_ s: FlightSource, destinationIATA: String?); static func built(_ s: FlightSource, _ search: FlightSearch); static func clicked(_ s: FlightSource, _ link: Loci_Trip_FlightLink, _ search: FlightSearch?) }`
  - `static func properties(...) -> [String: Any]`, pure, for tests
  - `TripPlanAPI.searchAirports(_ query: String) async throws(TripRPCError) -> [Loci_Trip_FlightPlaceOption]`
  - `TripPlanAPI.resolveDestination(city:countryCode:latitude:longitude:) async throws(TripRPCError) -> Loci_Trip_FlightPlaceOption?`

- [ ] **Step 1: Write the failing tests.** `FlightAnalyticsTests`: `built(.hotels, LIS→BCN, 2 pax, no return)` gives exactly the keys and values of web Task 5 Step 1. A search with only names produces no `origin_iata` key.
- [ ] **Step 2:** Run `cd loci && xcodebuild test -project loci.xcodeproj -scheme "loci Beta" -destination 'platform=iOS Simulator,name=iPhone 17' -only-testing:lociTests/FlightAnalyticsTests -quiet CODE_SIGNING_ALLOWED=NO`. Expected: FAIL.
- [ ] **Step 3: Implement it.**
  - Extract the form from `TripPlanSection` into `FlightSearchForm`; `TripPlanSection` keeps "Save to trip".
  - `AirportField` uses a `.searchable`-style suggestions list, debounced 200 ms, minimum 2 characters.
  - `FindFlightsSheet` resolves the destination on appear, then shows the form. Links open with `openURL`, calling `FlightAnalytics.clicked` first.
  - Add a "Find flights" toolbar or button to `SearchResultsView`, with the source taken from its `domain`, and to `TripEditorView` with `.trip`.
  - Add a click call to `TripActionCardView`'s links with `.chat`.
- [ ] **Step 4:** Run the full `xcodebuild test`, `scripts/format.sh --skip-install --lint-only` and `xcodebuild build -scheme "loci Beta"`. Expected: all green, 0 lint violations.
- [ ] **Step 5:** Commit, push, and open a PR.

---

## Done when (from the spec)

- Hotels page for Barcelona → Find flights → type "lisb" → pick Lisbon (LIS) → pick dates → Skyscanner and Google links open the right route and dates, on web and iOS.
- A name-only destination gets Skyscanner when it resolves and Google alone otherwise.
- The three events arrive in PostHog from both platforms with the properties above.
- With no template, `DeepLinks{}` output is byte-identical to today's (Task 2 test).
- A saved trip shows rebuilt links on read (Task 3 test).
