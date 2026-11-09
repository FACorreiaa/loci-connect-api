# Partnerships and revenue

Status: draft, 2026-11-09. Audit of how Loci can earn from the places it already
recommends, and which partners to approach in what order.

> **Every commission figure below comes from affiliate networks and aggregator
> listings, not from the partners' own signed terms.** Sources disagree, sometimes
> by a factor of three. Treat the numbers as an order of magnitude and confirm each
> one in the partner portal before it goes into a forecast.

## 1. Where the money is

Loci already produces the three things travel affiliates pay for: a shortlist of
hotels, a shortlist of activities, and a day-by-day itinerary. Today none of those
lists carries a booking link. Revenue is a link per place plus a price the user can
trust enough to click.

| Stream | What the user does | Who pays Loci | Status |
|---|---|---|---|
| Pro subscription | Pays monthly/annual | User | Built; **gating switched off since 2026-09-25** (`docs/pricing.md`), so it earns nothing today |
| Hotel commission | Books a hotel from a Loci card | Rates provider or OTA | Live rates via LiteAPI planned (see `docs/adr` once written); city-level Booking.com links exist (`BOOKING_AFFILIATE_ID`, unset) |
| Activity commission | Books a tour/ticket | GetYourGuide, Viator | Nothing built |
| Restaurant reservations | Reserves a table | TheFork | City-level link exists (`RESERVE_AFFILIATE_ID`, unset) |
| Multi-day trip referral | Hands an itinerary to an operator | TourRadar, local agencies | Nothing built |
| People marketplace | Books a guide, translator, creator itinerary | Commission per booking | Nothing built |

## 2. Partner programmes

| Partner | Pays | Cookie | Join through | Gate | Confidence |
|---|---|---|---|---|---|
| **LiteAPI (Nuitée)** | The `margin` Loci sets on each rate (e.g. 10–15%) | n/a, Loci is the seller | Direct, self-serve dashboard | None for sandbox; live key from dashboard | High: documented API parameter |
| **Booking.com Affiliate Partner** | 25–40% of *Booking's* commission (≈ 4–6% of room value) | 30 days | Reportedly via CJ since 2025 | Site review | Low: sources conflict on the basis |
| **Booking.com Demand API** | Same, plus API prices | — | Account manager | Managed Affiliate status + contract | High that it is gated |
| **Stay22** | Share of OTA commission (30–50% quoted) + some CPC | Varies | Direct | API signups paused; map/links free | Medium |
| **GetYourGuide** | ~8% base (7% Awin, 5% some regions), 10–12% negotiable | 30 days | Direct or Awin | Site review | Medium |
| **Viator** | 4% (Awin default) to 8% (RevShare listing) | 30 days | Awin | Site review; no discount-code promo | Medium; paid after travel date |
| **TheFork** | Not published; restaurants pay TheFork €2–4 per cover | 2–20 days, by country | Awin / Kwanko per country | Some country programmes closed | Low |
| **TourRadar** | Up to 5% (official), ~$180 average per booking | 90 days | Direct | Site review | Medium; paid after trip ends |

### Why LiteAPI first

It is the only hotel source Loci can sign up for today and ship against:

- Booking's Demand API needs Managed Affiliate status and a contract.
- Amadeus Self-Service shut down on 2026-07-17.
- Stay22's API has paused signups, and its terms forbid storing results.
- Expedia Rapid and Hotelbeds both need partner approval.

LiteAPI also changes the economics. Loci sets the margin itself instead of taking
a share of someone else's commission. The cost is that Loci becomes the seller of
record for the booking, through LiteAPI's white-label checkout, which brings
customer-support duties (§8).

## 3. Before applying anywhere

Every affiliate review asks for the same things. Have them in place first:

1. **A live public site with real content.** `https://lociai.fyi` qualifies.
2. **A privacy policy** that names affiliate tracking and cookies.
3. **An affiliate disclosure page**, plus a label next to every paid link. The FTC
   (US) and EU consumer law (the UCPD, and the DSA for platforms) both require it.
4. **Traffic numbers**: monthly users, top countries, top destinations. Get them
   from the existing analytics before the first application.
5. **A company or sole-trader entity** with a VAT number. Networks pay a business.

## 4. Application order

| # | When | Partner | Why now |
|---|---|---|---|
| 1 | Now | LiteAPI sandbox → live | Unblocks live hotel prices; no approval |
| 2 | Now | GetYourGuide | Activities are already a Loci domain; highest activity rate |
| 3 | Now | Viator (Awin) | Second activity inventory; Awin account reused for 5 |
| 4 | After traffic numbers | Booking.com via CJ | Brand users expect; fills hotels LiteAPI misses |
| 5 | After 4 | TheFork (Awin, per country) | Restaurant links already wired server-side |
| 6 | When multi-day trips ship | TourRadar | 90-day cookie suits long-horizon trip planning |

## 5. Unit economics: one Barcelona hotel booking

€150 a night × 3 nights = **€450 room value**.

| Route | Loci earns | How |
|---|---|---|
| Booking.com affiliate | **€17–27** | €450 × ~15% Booking commission × 25–40% share |
| LiteAPI at 10% margin | **€45** | Margin on top of the net rate, before payment and support costs |
| Stay22 | **€10–20** | Share of OTA commission (wide range) |

A day of activities works out like this: two tours at €45 each is €90, and
GetYourGuide at 8% pays **€7.20**.

**Example traveller.** 3 hotel nights + 2 activities + 1 restaurant booking comes
to roughly €25–55 of commission, against a cost of a few cents of LLM calls
(measured by `loci_llm_*` metrics). That is better economics than the Pro
subscription, and the two do not compete with each other.

**What we do not know yet:** the click-to-booking conversion rate. Industry
figures for travel content are often quoted at 1–5%. Measure ours before
projecting revenue (§7).

## 6. People partnerships

These partners are not affiliate networks. Each one needs outreach and a simple deal.

**YouTubers and travel creators**
- Deal: a creator publishes a Loci itinerary for a city they film ("my 3 days in Lisbon"). It has its own share URL (the share flow exists already), and their audience gets free Pro for a month.
- Pay: a fixed fee per itinerary, or a 20–30% share of the affiliate commission earned through that itinerary's links.
- Who first: mid-size channels (50k–500k subscribers) focused on one region. Their rates are reachable and the audience matches.

**Local guides**
- Deal: guides list their own tours inside a city's results, marked as "local guide".
- Pay: 10–15% commission per booked tour. That is cheaper than GetYourGuide's 20–30% take from the guide, so it is a real pitch to them.
- Needs: guide profiles, availability, a payment split. This is a product feature, not just a link.

**Translators and interpreters**
- Deal: two offers.
  - On-trip interpreting, bookable per hour from an itinerary day ("medical appointment in Tokyo", "notary in Porto").
  - Localising Loci's own content per market. This is a cost line, not revenue, but it unlocks non-English markets.
- Pay: commission per booked hour; flat fee for localisation.

**Travel agencies**
- Deal: a "Hand this trip to an agent" button exports the itinerary (the PDF export exists) to a partner agency that books the whole package.
- Pay: referral fee per closed booking, usually 5–10% of the package.
- Who first: one agency per launch market.

### Outreach template (creators and guides)

> Hi {name}, I run Loci (lociai.fyi), an AI trip planner that builds real
> day-by-day itineraries with accessible walking routes. I'd like to publish your
> {city} recommendations as a Loci itinerary under your name. Your audience gets it
> free, and you earn {deal} on every hotel and tour booked through it. Ten minutes
> to show you how it looks?

## 7. Tracking

- **Outbound clicks**
  - Use the existing events: `RECOMMENDATION_EVENT_TYPE_BOOKING_OPENED` (`recommendation.proto`) and `INTERACTION_TYPE_BOOKING_ATTEMPT` (`recents.proto`).
  - Log the provider, domain, city and a click id with each one.
- **Sub-ids:** pass the click id as the network sub-id (CJ `sid`, Awin `clickref`, GetYourGuide `cmp`). Network conversion reports then join back to Loci sessions.
- **Monthly review:** clicks, then bookings, then commission, broken down by provider and city. Drop partners that convert below a threshold.

## 8. Trust rule and risks

**Trust rule.** Ranking never depends on commission. Places are ordered by what the
user asked for; the booking link is added afterwards. Hotels with no partner price
stay in the list. This is also a selling point: hotelist.com leads with
"no pay-to-play", and Loci can make the same promise.

**Risks**
- **Seller of record (LiteAPI):** cancellations, refunds and chargebacks come to Loci's support. Budget for it before turning the margin up.
- **Price drift:** live prices change. Show "prices from partner, may change" and the fetch time, and never cache a price for longer than about 15 minutes.
- **Terms violations:** most providers forbid storing their prices and listings. Rates are fetched live and never written to the DB.
- **Disclosure:** unlabelled affiliate links are a legal risk in the EU and the US. Label every one.
- **Concentration:** one hotel source is fragile. Add Booking via CJ (step 4) as the second source.

## 9. Timeline

| Month | Milestone |
|---|---|
| Nov 2026 | Disclosure + privacy pages; LiteAPI sandbox; live hotel prices + price filter on web |
| Dec 2026 | LiteAPI live key; GetYourGuide + Viator applications; activity links on cards |
| Jan 2027 | First conversion numbers; Booking.com (CJ) application; 3 creator pilots |
| Q1 2027 | TheFork per country; first local-guide pilot city; agency hand-off button |

## Sources

Aggregator and network listings, retrieved 2026-11-09. Verify each on the official page.

- Booking.com affiliate: [track360 teardown](https://track360.io/blog/booking-com-affiliate-partner-program-operator-teardown-2026), [makeinfluence](https://www.makeinfluence.com/en/academy/booking-coms-affiliate-program-how-travel-creators-earn-commission)
- Booking.com Demand API prerequisites: [developers.booking.com](https://developers.booking.com/demand/docs/getting-started/prerequisites)
- GetYourGuide: [Awin merchant profile](https://ui.awin.com/merchant-profile/18925), [affilimate](https://affilimate.io/programs/getyourguide-affiliate-program/)
- Viator: [Awin merchant profile](https://ui.awin.com/merchant-profile/11018), [RevShare](https://www.revshare.so/programs/advertiser/viator)
- TheFork: [Awin merchant profile](https://ui.awin.com/merchant-profile/127369), [affi.io](https://affi.io/m/thefork)
- TourRadar: [affiliate programme](https://tourradar.com/affiliate-program), [ambassador programme](https://partners.tourradar.com/ambassador-program)
- Stay22: [developer docs](https://dev.stay22.com/docs), [Direct Travel API](https://api.stay22.com/)
- LiteAPI: [rates](https://docs.liteapi.travel/reference/post_hotels-rates), [commission](https://docs.liteapi.travel/docs/revenue-management-and-commission)
- Amadeus Self-Service shutdown: [PhocusWire](https://www.phocuswire.com/amadeus-shut-down-self-service-apis-portal-developers)
