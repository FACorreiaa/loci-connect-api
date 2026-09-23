# Here brief — "where I am right now" on the home hero

Date: 2026-09-23
Repos: loci-connect-proto, loci-connect-server, loci-client, loci-ios (+ infra promote)

## Goal

The home hero ("Where next" on web, "Where to next?" on iOS) shows raw
coordinates (web) or nothing (iOS). It should tell a signed-in traveller about
the place they are standing in: its name, the weather, alerts, local news,
travel disruption and what's on.

What the user asked: weather, recent news, travel news, on web and iOS.
Decided in brainstorming: "travel news" means both disruption (strikes,
airports, trains, closures, weather warnings) and inspiration (events,
festivals, exhibitions), as separate lists.

Success: from a real position (e.g. Viana do Castelo, 41.69° N 8.83° W) the
home screen on both platforms shows the town name, today's weather and at least
one populated news list, with no fabricated content and no Lisbon fallback.

## Non-goals

- Signed-out users. The landing page is unchanged.
- Changing `GetNewsTicker` (home / next trip / recent visits). It stays as is.
- Article bodies or summaries. Headline, source, time, link only.
- Any new paid API. Every source is already in use and keyless.
- Storing a user's position.

## Contract (loci-connect-proto, minor release)

Added to `LocalContextService` in `proto/loci/localcontext/localcontext.proto`:

```proto
// GetHereBrief describes the place the caller is standing in: its name,
// weather, alerts and three short headline lists. Every part degrades to
// empty on its own; the RPC does not fail because one source did.
rpc GetHereBrief(GetHereBriefRequest) returns (HereBrief);

message GetHereBriefRequest {
  double latitude = 1;   // same validation as GetLocalContextRequest
  double longitude = 2;
}

message HerePlace {
  string locality = 1;      // "Viana do Castelo"; may be empty
  string region = 2;        // "Viana do Castelo District"; may be empty
  string country_code = 3;  // ISO 3166-1 alpha-2; empty at sea
  string country_name = 4;
}

message HereBrief {
  HerePlace place = 1;
  repeated WeatherDay weather = 2;      // today + 2 days
  bool weather_is_estimated = 3;
  repeated LocalAlert alerts = 4;
  repeated NewsTickerItem local = 5;       // "Around you"
  repeated NewsTickerItem disruption = 6;  // "Getting around"
  repeated NewsTickerItem whats_on = 7;    // "What's on"
  bool stale = 8;  // a feed is serving cache after an upstream failure
}
```

Release: merge, tag, `buf push` (`make push`) from a clean tree at the tagged
commit, and check that the tag contains `GetHereBrief` before the server
`go get`s it.

## Server (loci-connect-server, `internal/domain/localcontext`)

### Place

`BigDataCloudGeocoder` gains `Place(ctx, lat, lon) (Place, error)`, using the
same `reverse-geocode-client` call. It reads the `city`/`locality`,
`principalSubdivision`, `countryCode` and `countryName` fields. Locality prefers
`city`, falling back to `locality`.

`CountryCode` keeps its 0.1° cache key, because a country boundary is coarse.
`Place` uses its own 0.01° key under a separate cache namespace: at 0.1°
(~11 km) the town would often be the wrong one. Same TTL as the geocode cache.

### Handler `GetHereBrief`

1. Sign-in required (same `newsUserID` helper as the ticker).
2. Round lat/lon to 2 decimals (~1 km). Only the rounded value is used after
   this. Coordinates are never logged.
3. Resolve the place, then run in parallel (errgroup, each branch swallowing
   its own error to empty plus a WARN without coordinates):
   - weather: `h.weather.Forecast(rounded, 3)`
   - alerts: `h.signals` for the rounded point (empty when nil)
   - news: one aggregator call with three feeds (below), when `h.news` is set
     and a place name exists.
4. Map everything to `HereBrief`. Any branch can be empty. The RPC only errors
   on unauthenticated or invalid input.

### News queries

The subject is `"<locality>" OR "<region>"`, with an empty part omitted. With
no locality or region the three lists are empty; country-level news is already
the ticker's job. Google News search RSS, `hl=en&gl=<CC>&ceid=<CC>:en`, same as
`feedsForCountries`:

- local: `<subject>`
- disruption: `<subject> (strike OR airport OR train OR closure OR "weather warning" OR traffic)`
- whats_on: `<subject> (festival OR event OR exhibition OR concert OR "things to do")`

Items are split back into lists by `_feeds.feed_url`. They are deduped by URL
across lists (local keeps a shared item, then disruption, then what's on),
sorted newest first and capped at 5 per list. The builder lives in a pure
function `hereFeeds(place) []newsFeed` next to `feedsForCountries`.

The three lists respect the person's existing news switch
(`NewsTickerPrefs`): with news off they are empty, while weather, alerts and
place are still returned. Cached for 10 minutes per place.

Known risk: English results for small Portuguese towns may be thin. The region
in the OR, together with hiding empty lists in the UI, covers it. Language by
country is a follow-up only if measurement shows empty lists.

## Web (loci-client)

- `src/lib/api/hereBrief.ts`: `useHereBrief(lat, lon)` with the key
  `["hereBrief", lat rounded to 2dp, lon rounded to 2dp]`, `staleTime` 15 min,
  and enabled only when a real `userLocation()` exists. The Lisbon fallback is
  never passed.
- `DeskHero.tsx` kicker: `<locality> · <coords>` when the place is known,
  otherwise the coords as today.
- New `components/features/Dashboard/HereNowBand.tsx`, placed between
  `DeskHero` and `InSeasonBand` in `LoggedInDashboard`:
  - left: today's condition icon, high/low, a compact 3-day strip, an
    "estimated" badge when flagged, and alert chips coloured by severity
    (reuse `LocalWeather` pieces / `colorForSeverity`)
  - right: up to three lists — Around you / Getting around / What's on — each
    showing 3 items with source and relative time, opening in a new tab.
    Empty lists are absent.
  - the whole band is absent without location or when every part is empty.
  - built from `.loci-card`, `.kicker` and `SectionHeader` per DESIGN.md.
    Columns on ≥md, stacked on mobile.

## iOS (loci-ios)

- The `getHereBrief` call goes through the LocalContext client in
  `Features/Settings/Services/SettingsClients.swift`, which the news ticker
  toggle already uses. Move it to `Core` if a second feature makes that
  cleaner.
- `DiscoverView` hero: a place line under "Where to next?" (caption, forest
  tint) when known.
- New `Features/Discover/UI/HereBriefSection.swift` below the hero with the same
  content: a weather row, alert chips and three lists. Links open via
  `@Environment(\.openURL)`. Hidden with no permission or when empty.
- Location comes from `Core/Location/CurrentLocation.swift`, the provider Nearby
  uses. Discover reads a fix only when permission is already granted and never
  triggers the prompt itself; Nearby stays the place that asks.

## Error handling

| Failure | Result |
|---|---|
| no location permission | band/section absent |
| geocoder fails | place empty; weather and alerts still shown; news lists empty |
| weather fails | weather empty; rest shown |
| aggregator fails | lists empty; `stale` false; WARN logged |
| aggregator serving cache | lists shown; `stale` true → small "cached" note |
| everything empty | band absent |

## Testing

- Server table tests:
  - `hereFeeds` (locality only, region only, both, neither; query escaping)
  - rounding
  - list split, dedupe order and cap
  - the partial-failure matrix above, using a fake geocoder, weather and
    aggregator
  - unauthenticated → `CodeUnauthenticated`
- Web: a unit test for the pure mapping and the rounding of the query key.
  Visual check at 390px and desktop, light and dark.
- iOS: a `-designPreview` screenshot of `HereBriefSection` with fixture data.
- Prod proof after deploy:
  - an unauthenticated probe returns `unauthenticated`, not `unimplemented`
  - a signed-in call at 41.69,-8.83 returns a place name and at least one list

## Rollout

1. proto: PR → merge → tag → `buf push`; verify tag contents.
2. server: `go get proto@tag`, `make generate`, PR → merge → promote PR in
   platform/infra → verify the probe.
3. web: bump `@buf/loci_loci-proto.bufbuild_es`, PR → merge → grep the live
   bundle for `api.lociai.fyi` (double-deploy race).
4. iOS: PR on loci-ios.

No infra changes: the feed aggregator, geocoder and weather egress are already
allowed for the API pod.
