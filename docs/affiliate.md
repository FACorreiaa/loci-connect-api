# Turning on affiliate earnings (phase 2)

Status: runbook, written 2026-11-09. Phase 1 (live hotel prices on the LiteAPI
sandbox, plain flight links) is shipped; this is what to do once the business
is registered. The business case and partner terms are in
[`docs/business/partnerships-and-revenue.md`](business/partnerships-and-revenue.md).

There are two halves:

- **Part A — switches.** Config only, no code. Every hook already exists and is
  off today.
- **Part B — "from €X" flight fares.** New code. A plan outline for the build.

> Figures and URL formats below come from partner docs and listings read on
> 2026-11-09. Partners change terms; check each one in its dashboard before you
> rely on it.

---

## Before anything: prerequisites

Every programme asks for these, and some links go live the moment you set an env
var:

1. **A registered business** (company or sole trader) with a VAT number and a
   bank account. Networks pay a business.
2. **An affiliate disclosure page** on `lociai.fyi` (for example `/affiliates`),
   linked from the footer and from the price notes ("Prices from partners · may
   change"). EU consumer law and the FTC both require it. The apps already mark
   tagged links:
   - **Web:** `rel="sponsored"`.
   - **iOS:** "· partner link".
3. **The privacy policy updated** to name affiliate cookies and click tracking.
4. **Traffic numbers ready**: monthly users, top countries, top destinations.
   PostHog has them; the new `flight_search_*` events show flight interest
   specifically.

---

## Part A — Switches (config only)

### Where config goes

| Kind | Where | How it deploys |
|---|---|---|
| Secret (API keys) | `loci-env` sealed secret, `platform/infra/secrets/loci/loci-env.yaml` | Seal with `--merge-into` (see `platform/infra/secrets/loci/README.md`), PR to the infra repo, merge |
| Not secret (IDs, templates, URLs, margins) | `loci-api-config` ConfigMap, `platform/infra/apps/loci/data/config.yaml` | PR to the infra repo, merge. No resealing |

The API reads both at startup. After either change merges, ArgoCD rolls the pod.
Always edit `~/Work/production/platform/infra`, never the archived
`loci-infra/` copy: changes committed there are silently lost.

### A1. Flight links → Skyscanner via Travelpayouts

The code reads one template and wraps every Skyscanner link in it
(`pkg/flights/flights.go`, `DeepLinks`).

| Env var | Kind | Value |
|---|---|---|
| `FLIGHTS_LINK_TEMPLATE_SKYSCANNER` | ConfigMap | An `https://` URL containing `{url}` |

`{url}` is replaced with the URL-encoded Skyscanner search. A template that is
not `https://` or has no `{url}` is **ignored**: the API logs
`ignoring FLIGHTS_LINK_TEMPLATE_SKYSCANNER` and the links stay plain.

Google Flights has no affiliate programme and is never wrapped.

**Steps**

1. **Sign up** at [travelpayouts.com](https://www.travelpayouts.com/). Create a
   project for `lociai.fyi` and note:
   - your **Partner ID** (also called the marker);
   - the **project ID** (`trs`).

   They are different numbers. Don't mix them up.
2. **Join the Skyscanner programme** inside Travelpayouts. Kiwi.com is available
   too, but phase 1 only builds Skyscanner and Google links. Wait for approval.
3. **Generate one partner link to see the exact format.** Use Travelpayouts'
   link generator ("Create link" / Deep Link Generator). Paste any Skyscanner
   search URL, for example
   `https://www.skyscanner.net/transport/flights/lis/bcn/261204/?adultsv2=1&cabinclass=economy&rtn=0`.
   The result looks like:

   ```
   https://tp.media/r?marker=<PARTNER_ID>&trs=<PROJECT_ID>&p=<PROGRAM_ID>&u=<ENCODED_SKYSCANNER_URL>&campaign_id=<CAMPAIGN_ID>
   ```
4. **Turn it into the template**: replace the encoded Skyscanner URL after `u=`
   with `{url}`, and keep every other parameter exactly as generated:

   ```
   FLIGHTS_LINK_TEMPLATE_SKYSCANNER=https://tp.media/r?marker=<PARTNER_ID>&trs=<PROJECT_ID>&p=<PROGRAM_ID>&u={url}&campaign_id=<CAMPAIGN_ID>
   ```

   Optional sub-ID per source: Travelpayouts takes a sub-ID after the marker with
   a dot (`marker=123456.loci`). Use it if you want Loci's traffic separated in
   their reports.
5. **Check before merging**: build the URL by hand for one route and open it.
   It must land on the same Skyscanner search.
6. **Add it** to `loci-api-config`, open the infra PR, and merge.

**What changes for users.** Every Skyscanner link Loci builds carries the tag,
including flights **saved before today**: trips rebuild their links on read
(`trip.WithFlightLinks`). Clients mark the link sponsored (`FlightLink.sponsored`).

**Verify after deploy**

- API boot logs: no `ignoring FLIGHTS_LINK_TEMPLATE_SKYSCANNER` line.
- On `lociai.fyi`, Find flights from a hotels page:
  - the Skyscanner link's `href` starts with `https://tp.media/r?`;
  - its `rel` includes `sponsored`;
  - it opens the right route and dates.
- On iOS, the link reads "Skyscanner · partner link".
- A saved trip's flight now shows the tagged link.
- After a day: clicks appear in the Travelpayouts dashboard, and
  `flight_link_clicked` (`provider: skyscanner`) in PostHog.

**One thing to confirm first (open from phase 1).** Multi-airport cities use
Skyscanner's own city codes (`lond`, `nyca`, `pari`, `mila`, `rome`, `tyoa`, …;
see `pkg/flights/metros.go`). These came from Skyscanner's public route pages
and were not checked against a live search. Open
`https://www.skyscanner.net/transport/flights/lis/lond/261204/`. If it doesn't
show Lisbon → London (all airports), fix the codes in `metros.go` before the
tagged links go out.

**Undo**: remove the env var. Links go back to plain on the next read.

### A2. Hotel prices → LiteAPI live, with a margin

Phase 1 runs on the **sandbox** key: prices are realistic, nothing can be
booked, and there is no "Check availability" link.

| Env var | Kind | Value |
|---|---|---|
| `LITEAPI_API_KEY` | Secret | The **live** key (dashboard → API keys). Replaces the sandbox key |
| `LITEAPI_WHITELABEL_URL` | ConfigMap | Your white-label booking site, e.g. `https://book.lociai.fyi`. **Setting this is what makes "Check availability" appear** |
| `LITEAPI_MARGIN` | ConfigMap | Markup in percent, `0`–`50`. Unset uses the account default. Out of range is ignored with a warning |
| `LITEAPI_GUEST_NATIONALITY` | ConfigMap | ISO-2 country rates are quoted for. Default `US` |

**Steps**

1. **Activate production** in the Nuitée/LiteAPI dashboard. This needs the
   business details.
2. **Set up the white-label site** in the dashboard:
   - choose a custom domain (`book.lociai.fyi` → CNAME as instructed);
   - set the branding;
   - connect payments, so Nuitée/Stripe takes the money.

   Deep links use `/hotels/{id}?checkin=&checkout=&occupancies=&currency=` and
   are built by `pkg/hotelrates/liteapi.go` (`bookingURL`).
3. **Pick a margin.** 10% matches the business plan's example. Higher earns more
   per booking but makes Loci's price worse than Booking.com's for the same room.
4. **Swap the key**: reseal `LITEAPI_API_KEY` into `loci-env` with
   `--merge-into`. The same command replaces the sandbox value:

   ```bash
   printf %s "$LIVE_KEY" | kubectl create secret generic loci-env -n horus \
     --from-file=LITEAPI_API_KEY=/dev/stdin --dry-run=client -o json \
   | kubeseal --cert ~/.kube/sealed-secrets-maat.pem --format yaml \
       --merge-into secrets/loci/loci-env.yaml
   ```

   Read the key from a file or the clipboard into `$LIVE_KEY`. Don't type it at
   an interactive prompt: shell history keeps it.
5. **Add** `LITEAPI_WHITELABEL_URL` and `LITEAPI_MARGIN` to `loci-api-config`.
6. **One infra PR** with both changes. Merge.

**Seller-of-record duties start here.** With live bookings, cancellations,
refunds and chargebacks come to Loci's support (business plan §8). Have a support
inbox and a refund policy page before step 6.

**Verify after deploy**

- "Hotels in Barcelona" shows prices that move with the dates control.
- Cards show "Check availability".
- The link opens the white-label site on the right hotel, dates and guests.
- Make one real refundable booking and cancel it, end to end.

### A3. City-level Booking.com and TheFork links

These already exist on the Weekend Compare page and the booking calls to action
(`internal/domain/localcontext/deeplinks.go`).

| Env var | Kind | Value |
|---|---|---|
| `BOOKING_AFFILIATE_ID` | ConfigMap | Your Booking.com affiliate ID (`aid`). Booking now signs partners up through CJ |
| `RESERVE_AFFILIATE_ID` | ConfigMap | TheFork partner ID (Awin or Kwanko, depending on country) |

Apply to each programme, wait for approval, set the ID, merge.

**Not built yet:**
- GetYourGuide and Viator links on activity cards.
- Per-hotel Booking.com links.

Both are new code. The business plan has them in the application order.

---

## Part B — "from €X" flight fares (new code)

Phase 1 shows no prices. Phase 2 adds a recent cached fare next to the links,
e.g. "from €89 · seen 2 days ago". It comes from the **Aviasales Data API**,
which ships with the Travelpayouts account from A1. It's free and needs no
traffic threshold: the live Flight Search API needs about 50k monthly users,
but the Data API doesn't.

### What the data is

- The cheapest fares Aviasales users **found** for a route and date, kept from
  2 to 7 days. **Not live, and not bookable at that price.** It must be labelled
  as such.
- Request: `GET https://api.travelpayouts.com/aviasales/v3/prices_for_dates`
  with:
  - `origin`, `destination` (IATA city or airport);
  - `departure_at`, `return_at` (`YYYY-MM` or `YYYY-MM-DD`);
  - `currency`, `one_way`, `direct`, `sorting=price`, `limit`.

  The token goes in `X-Access-Token` (or `token=`).
- Each result carries price, airline, transfers, departure time, and a `link`
  path on Aviasales. We use the price only; booking still goes through the
  partner links.

### Build outline (for a plan, not code yet)

1. **Proto** (one release, two halves as usual). Add
   `optional FlightFare from_fare` to `BuildFlightLinksResponse`.
   `FlightFare` holds:
   - `amount_minor`, `currency`;
   - `found_at` (Timestamp);
   - `source` (`"aviasales"`);
   - `direct` (bool).

   Unset when there is no fare. No new RPC: the form already calls
   `BuildFlightLinks`.
2. **Server**:
   - **Package:** `pkg/flightfares`, with a `FareSource` interface,
     `Cheapest(ctx, origin, dest IATA, depart, return *time.Time, currency)`.
     An Aviasales adapter on the `httpx` client, following the
     `pkg/hotelrates/liteapi.go` pattern.
   - **Caching:** cache per route, date and currency for 6h. The data is days
     old anyway, and the API is rate-limited.
   - **Missing codes:** only call it when both ends have codes (resolved or
     picked). Map metro codes to their IATA city code: Aviasales takes `LON`
     natively.
   - **Wiring:** `TRAVELPAYOUTS_TOKEN` (Secret). Unset means no fares, the same
     feature-off pattern as `LITEAPI_API_KEY`.
   - **Tests:** a stub server only, never a real token.
3. **Web and iOS**:
   - **Display:** show "from €89" beside the links with a muted "seen N days
     ago". Never next to "Book", never styled like a live price.
   - **Analytics:** add `has_fare` to `flight_search_built` so PostHog shows
     whether fares lift clicks.
4. **Copy and legal**:
   - Fare label: "Recent fare found by travellers, not a live price. Check the
     partner site."
   - Link the disclosure page.

**Decide before building**: show the fare on every search, or only when it is
under some age (for example 3 days)? Old fares erode trust faster than no fare.

---

## Checklist

- [ ] Business registered; VAT number; bank account
- [ ] `/affiliates` disclosure page live and linked; privacy policy updated
- [ ] **A1**: Travelpayouts account → Skyscanner programme approved
- [ ] **A1**: Skyscanner city codes checked on a live search
  (`/lis/lond/…`); `metros.go` fixed if not
- [ ] **A1**: `FLIGHTS_LINK_TEMPLATE_SKYSCANNER` set and verified, clicks in the
  dashboard
- [ ] **A2**: LiteAPI production activated, white-label site and payments live
- [ ] **A2**: support inbox and refund policy live
- [ ] **A2**: live key sealed; `LITEAPI_WHITELABEL_URL` and `LITEAPI_MARGIN` set;
  test booking made and cancelled
- [ ] **A3**: Booking.com (CJ) and TheFork applied, IDs set
- [ ] **B**: fare-age rule decided, then a plan written for `pkg/flightfares`

## Sources

- [Travelpayouts: how to create and use affiliate links](https://support.travelpayouts.com/hc/en-us/articles/360027634052)
- [Travelpayouts: API for partner links](https://support.travelpayouts.com/hc/en-us/articles/25289759198226-API-for-Travelpayouts-partner-links)
- [Travelpayouts: ID and SubID](https://support.travelpayouts.com/hc/en-us/articles/203955653)
- [Aviasales Data API](https://support.travelpayouts.com/hc/en-us/articles/203956163)
- [Aviasales Flight Search API (50k MAU requirement)](https://support.travelpayouts.com/hc/en-us/articles/30565016140434)
- [LiteAPI: deeplinking to the white label](https://docs.liteapi.travel/docs/deeplinking-to-whitelabel)
- [LiteAPI: revenue management and commission](https://docs.liteapi.travel/docs/revenue-management-and-commission)
