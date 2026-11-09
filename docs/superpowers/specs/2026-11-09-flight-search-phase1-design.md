# Flight search, phase 1: design

Date: 2026-11-09. Status: draft for approval.

## Why

Loci helps people choose where to go and what to do there, but getting there
is missing. Travellers reach for flights at two moments:

- planning a trip from scratch;
- already looking at a city's itinerary, hotels or activities, and needing to
  get there.

Today a flight search exists only inside the trip planner (web and iOS) and in
the AI's `search_flights` action. It builds a prefilled Skyscanner and Google
Flights search (`pkg/flights`). Nobody browsing a city ever sees it, nothing
counts its use, and the links carry no affiliate ID.

**Success, in the owner's words: "users using it."** Phase 1 has to make the
flight search findable from the places people already are, and measure whether
they use it.

## Goals

1. A **Find flights** entry on a city's results pages (itinerary, hotels,
   restaurants, activities) and on a saved trip, on **web and iOS together**.
2. It **asks where the traveller flies from, every time**, with airport/city
   autocomplete. The destination is prefilled from the page's city. Dates come
   from the trip when it has them; otherwise the traveller picks them.
   Passengers default to 1.
3. It hands over a **ready-made search on Skyscanner and Google Flights**, opened
   on the partner's site. Booking happens there.
4. **Usage is measured** on both platforms. We count when the flight search is
   opened (and from which page), when a search is built, and which partner
   the traveller clicks through to.
5. Links are built so that **adding an affiliate ID later is configuration,
   not code**.

## Non-goals (phase 1)

- Prices of any kind, cached or live.
- Booking inside Loci, payments, or any merchant-of-record flow.
- A flight results list inside Loci.
- A stored home airport or a profile field for one. The owner chose to ask
  every time.
- Affiliate sign-ups. The owner chose plain links that earn nothing until the
  business is registered.
- Price alerts or saved searches beyond what the trip planner already does.

## Anti-goals

- Showing a price that looks bookable but isn't. Phase 1 shows none.
- Stopping the traveller on a dead end. If Loci cannot resolve an airport
  code, Google Flights (name-based) still works. Never show an empty card.
- Tracking that captures personal data. Events carry route and page, never
  names, emails or free text the traveller typed beyond the airport code.

## Constraints

- **Proto changes are a release**: merge, tag, BSR push, then bump the server's
  `go.mod` and regenerate `gen/`, and bump the client's
  `@buf/loci_loci-proto.bufbuild_es` (Loci `CLAUDE.md`). iOS consumes the same
  module.
- **Reuse `BuildFlightLinks`** (`trip.proto` `BuildFlightLinksRequest`, handler
  `internal/domain/trip/plan_handler.go:111`). It already builds links for an
  ad-hoc search with no trip and no write. Phase 1 needs no second link
  builder.
- **Skyscanner needs IATA codes** (`pkg/flights/flights.go:118-135`); Google
  takes names. Today an IATA arrives only when the user types it or the LLM is
  "sure".
- **No paid APIs.** LiteAPI Flights exists on the sandbox key, but sandbox fares
  are not real, and production needs business activation plus Nuitée approval.
  It is the path for phase 3, not this one.
- Accessibility floor: WCAG 2.2 AA (`loci-client/PRODUCT.md`).

## Decided by the owner

- Both entry moments matter: from scratch, and from a city's pages.
- Phase 1 hands off to partners; booking inside Loci may come later once the
  business is registered.
- A ready-made Skyscanner search is enough. No prices in phase 1.
- Ask for the origin every time; do not store it.
- Plain links, no affiliate earnings until the business exists.
- Success = people use it.
- Web and iOS ship together.

## Calls made by the designer (overridable)

- **Airport data.** Bundle a static airport list on the server, built from
  [OurAirports](https://ourairports.com/data/) (public domain). Keep large and
  medium airports with scheduled service and an IATA code; that is roughly
  4–5k rows and fits as embedded data. It does two jobs:
  - **Autocomplete:** match on airport name, city or IATA, accent-insensitive
    (reuse `pkg/placematch.Fold`).
  - **City → airport for the destination:** pick the city's main airport, the
    largest by type with scheduled service, nearest the city centre when the
    page knows coordinates.

  For a city with several airports, use the metro code where Skyscanner has one
  (LON, NYC, PAR, MIL, ROM, …), from a small hand-kept map. Otherwise use the
  main airport.
- **Two new RPCs** in the trip service, next to `BuildFlightLinks`:
  - `SearchAirports(query, limit) → [FlightPlaceOption]` for autocomplete.
  - `ResolveFlightDestination(city, country_code?, latitude?, longitude?) →
    FlightPlaceOption?`, called **when the sheet opens**.

  `FlightPlaceOption` is `{iata, name, city, country_code, kind: AIRPORT|METRO}`.
  A metro code (LON, NYC) is a `METRO` option labelled "London (all
  airports)", so metro codes can also appear in autocomplete.
- **The destination is resolved up front and shown.** The sheet opens with the
  destination as an editable field, e.g. "Barcelona (BCN)" or "London (all
  airports)", resolved from the page's city with its country and coordinates,
  so a same-name city (Valencia ES vs VE) gets the right airport. If nothing
  resolves, the field holds the city name with no code, and only Google is
  offered. The traveller can always change it. This is why
  `flight_search_opened` can carry `destination_iata`.
- **`BuildFlightLinks` stays as it is for its inputs**, with one addition: when
  a `FlightPlace` has a name and no IATA (older clients, the trip planner's
  free-text field, the AI action), the server resolves it **for Skyscanner
  only**. Google Flights keeps using exactly what was sent, name or code, so a
  wrong resolution can never break the Google link, and Google output is
  unchanged for the same input.
- **Affiliate seam: a per-provider URL template.** Affiliate networks usually
  tag a link by wrapping it in a redirect, not by adding a query parameter.
  So the seam is one env var per provider, `FLIGHTS_LINK_TEMPLATE_SKYSCANNER`,
  holding a template with a `{url}` placeholder (URL-encoded on substitution),
  e.g. `https://tp.media/r?marker=…&u={url}`. It is read in
  `cmd/api/dependencies.go` and passed into `flights.DeepLinks`. Unset, the
  link is the plain URL, exactly as today. Google Flights has no affiliate
  programme and gets no template.
- **Saved flights pick up affiliate tags later without a migration.** Links
  are rebuilt from the flight's stored query whenever a trip is read, rather
  than served from the stored `links` column. The column keeps being written
  for older readers and is no longer the source of truth. When phase 2 sets a
  template, every saved flight's links carry it on the next read.
- **Outbound links get `rel="sponsored noopener noreferrer"`** only once an
  affiliate ID is set. Until then they keep `noopener noreferrer`.
- **Analytics** uses each platform's existing system:
  - **Web:** PostHog `capture`, `loci-client/src/lib/analytics.ts`, adding
    typed events.
  - **iOS:** `Core/Analytics/Analytics.swift`, adding cases.

  Events, the same names on both platforms:
  - `flight_search_opened` `{source: itinerary|hotels|restaurants|activities|trip|chat, destination_iata?}`
  - `flight_search_built` `{source, origin_iata?, destination_iata?, has_return, passengers, cabin}`
  - `flight_link_clicked` `{source, provider: skyscanner|google, origin_iata?, destination_iata?}`

  No server-side click redirect in phase 1. It would add a hop and a privacy
  surface for little gain while the links earn nothing.
- **The chat flight card is in scope for tracking only.** The AI's
  `search_flights` card on web (`components/chat/TripActionCard.tsx`) and iOS
  (`Features/Trips/UI/TripActionCardView.swift`) sends `flight_link_clicked`
  with `source: chat`, because usage there counts toward success. It does not
  open the new sheet in phase 1.
- **UI shape:**
  - **Web:** a sheet or dialog reusing the trip planner's flight form fields
    (`components/trip/TripPlanPanel.tsx:118-145,353-385`), extracted into a
    shared `FlightSearchForm`. The entry is a "Find flights to {city}" button
    in the results page header area (`components/results/ResultsPage.tsx`) and
    on the trip page.
  - **iOS:** the same as a sheet, reusing `TripPlanSection`'s form
    (`Features/Trips/UI/TripPlanSection.swift:132`).
  - From a trip, the form also offers **Save to trip** (the existing
    `AddFlight`). From a city page it offers links only.

## Left to the builder

- Exact visual placement and copy, within the existing design language.
- How the airport dataset is built and refreshed: a generator script plus a
  committed, embedded file is the expected shape.
- Ranking details for autocomplete (exact IATA match first, then city prefix,
  then airport-name prefix).
- Whether `SearchAirports` is rate-limited beyond the standard interceptors.

## Later phases (for context, not to build)

- **Phase 2:** affiliate IDs on (config only), plus cached "from €X" fares via
  the Travelpayouts Data API (real fares from recent searches, 2–7 days old).
- **Phase 3:** live search and booking inside Loci through LiteAPI Flights,
  once the business is registered and Nuitée approves production.

## Done when

- From a hotels page for Barcelona, a signed-in traveller can:
  - tap Find flights;
  - type "lisb" and pick Lisbon (LIS);
  - pick dates;
  - get Skyscanner and Google Flights links that open the right route and
    dates.

  This works on web and iOS.
- A destination city with no IATA in the request still gets a Skyscanner link
  when the dataset resolves it, and gets Google alone when it does not.
- The three events show up in PostHog (web) and in the iOS analytics pipeline
  with the properties above.
- With no link template set, `flights.DeepLinks` output for a given `Query` is
  byte-identical to today's (tested at the package level). Google links from
  `BuildFlightLinks` are unchanged for the same request. Skyscanner links now
  also appear when the server resolves a missing IATA.
- A saved trip's flights show freshly built links on read (tested by setting a
  template in the test and reading an old trip).
