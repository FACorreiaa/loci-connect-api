Okay, here's a more concise version of your README, aiming to reduce repetition while retaining the essential information for Loci.

---

# **Loci** – Personalized City Discovery 🗺️✨

Loci is a smart, mobile-first web application delivering hyper-personalized city exploration recommendations based on user interests, time, location, and an evolving AI engine. It starts with an HTTP/REST API, utilizing WebSockets/SSE for real-time features.

## 🚀 Elevator Pitch & Core Features

Tired of generic city guides? loci learns your preferences (history, food, art, etc.) and combines them with your available time and location to suggest the perfect spots.

- **🧠 AI-Powered Personalization:** Recommendations adapt to explicit preferences and learned behavior.
- **🔍 Contextual Filtering:** Filter by distance, time, opening hours, interests, and soon, budget.
- **🗺 Interactive Map Integration:** Visualize recommendations and routes.
- **📌 Save & Organize:** Bookmark favorites and create lists/itineraries (enhanced in Premium).
- **📱 Mobile-First Design:** Optimized for on-the-go web browsing.

## 💰 Business Model & Monetization

Loci uses a **Freemium Model**:

- **Free Tier:** Core recommendations, basic filters, limited saves, non-intrusive ads.
- **Premium Tier (Subscription):** Enhanced/Advanced AI recommendations & filters (niche tags, cuisine, accessibility), unlimited saves, offline access, exclusive content, ad-free.

**Monetization Avenues:**

- Premium Subscriptions
- **Partnerships & Commissions:** Booking referrals (GetYourGuide, Booking.com, OpenTable), transparent featured listings, exclusive deals.
- **Future:** One-time purchases (guides), aggregated anonymized trend data.

## 🛠 Technology Stack & Design Choices

The stack prioritizes performance, personalization, SEO, and developer experience, now centered on Go/Connect for APIs, SolidStart for the web client, and fully native mobile apps.

- **API:** **Go** with **Connect** (go-connect) for RPC/HTTP, **PostgreSQL + PostGIS** for geospatial queries, `pgx` or `sqlc` for data access.
    - _Rationale:_ Go + Connect gives fast, type-safe APIs with easy gRPC/REST interoperability; PostGIS is core for location features.
- **Web Client:** **SolidStart** (SolidJS) with **Tailwind CSS** and **Mapbox GL JS/MapLibre GL JS/Leaflet** for mapping.
    - _Rationale:_ SolidStart delivers fast, SEO-friendly SSR/SPA hybrid behavior with a lean runtime.
- **Mobile Apps:** Native **Kotlin** (Android) and **Swift** (iOS) clients consuming the Connect APIs.
- **AI / Recommendation Engine:** Google Gemini via `google/generative-ai-go` SDK for prompts/function-calling; **pgvector** in PostgreSQL for semantic search and personalized rankings.
- **Infrastructure:** Docker/Docker Compose; cloud Postgres + PostGIS; CI/CD via GitHub Actions/GitLab CI.

## 🗺️ Roadmap Highlights

- **Phase 1 (MVP):** Core recommendation engine (Gemini-powered), user accounts, map view, itinerary personalisation.
- **Phase 2:** Premium tier, enhanced AI (embeddings, `pgvector`), add more gemini features like

* speech to text
* itinerary download to different formats (pdf/markdown)
* itinerary uploads
* 24/7 agent more personalised agent

reviews/ratings, booking partnerships.

- **Phase 3:** Multi-city expansion, curated content, native app exploration.

## 🚀 Elevator Pitch

Tired of generic city guides? **WanderWise** learns what you love—be it history, food, art, nightlife, or hidden gems—and combines it with your available time and location to suggest the perfect spots, activities, and restaurants.

Whether you're a tourist on a tight schedule or a local looking for something new, discover your city like never before with hyper-personalized, intelligent recommendations.

---

## 🌟 Core Features

- **🧠 AI-Powered Personalization**
  Recommendations adapt based on explicit user preferences and learned behavior over time.

- **🔍 Contextual Filtering**
  Filters results by:
    - Distance / Location
    - Available Time (e.g., “things to do in the next 2 hours”)
    - Opening Hours
    - User Interests (e.g., "art", "foodie", "outdoors", "history")
    - Budget (coming soon)

- **🗺 Interactive Map Integration**
  Visualize recommendations, your location, and potential routes.

- **📌 Save & Organize**
  Bookmark favorites, create custom lists or simple itineraries (enhanced in Premium).

- **📱 Mobile-First Design**
  Optimized for on-the-go browsing via web browser.

---

## 💰 Business Model & Monetization

### Freemium Model

- **Free Tier**:
    - Access to core recommendation engine
    - Basic preference filters
    - Limited saves/lists
    - Non-intrusive contextual ads

- **Premium Tier (Monthly/Annual Subscription)**:
    - Enhanced AI recommendations
    - Advanced filters (cuisine, accessibility, niche tags, specific hours)
    - Unlimited saves & lists
    - Offline access
    - Exclusive curated content & themed tours
    - Ad-free experience

### Partnerships & Commissions

- **Booking Referrals**
  Earn commission via integrations with platforms like GetYourGuide, Booking.com, OpenTable, etc.

- **Featured Listings (Transparent)**
  Local businesses can pay for premium visibility in relevant results.

- **Exclusive Deals**
  Offer users special discounts via business partnerships (potentially Premium-only).

### Future Monetization Options

- One-time in-app purchases (premium guides, city packs)
- Aggregated anonymized trend data (for tourism boards, researchers)

## 🧪 Getting Started

### Prerequisites

- Go 1.25+
- PostgreSQL 16+ with the `postgis` and `vector` extensions enabled
- [`buf`](https://buf.build/docs/installation) (only needed if regenerating proto stubs)

### 1. Configure environment

Copy `.env.prod.example` to `.env` (or export the variables directly). The minimum required to boot:

| Variable | Purpose |
|---|---|
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` | PostgreSQL connection |
| `JWT_SECRET` | HMAC secret for access/refresh tokens |
| `AI_PROVIDER` | AI backend: `openrouter` (default) or `gemini` |
| `OPENROUTER_API_KEY` + `OPENROUTER_MODEL` | OpenRouter chat and embedding access when `AI_PROVIDER=openrouter` |
| `GEMINI_API_KEY` + `GEMINI_MODEL` | Gemini access when `AI_PROVIDER=gemini` |
| `SERVER_HOST`, `SERVER_PORT` | HTTP listen address (default `0.0.0.0:8000`) |

Optional but commonly needed:

| Variable | Purpose |
|---|---|
| `BASE_URL`, `FRONTEND_URL`, `OAUTH_CALLBACK_URL` | Public URLs for links and OAuth redirects |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` / `APPLE_CLIENT_ID`, `APPLE_KEY_ID`, `APPLE_SECRET`, `APPLE_TEAM_ID` | OAuth providers |
| `STRIPE_TEST_API_KEY`, `STRIPE_WEBHOOK_SECRET` | Payments + Stripe webhook |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `FROM_EMAIL`, `FROM_NAME` | Mailer |
| `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, `TWILIO_VERIFY_SID` | Phone verification |
| `METRICS_ENABLED`, `METRICS_PORT` | Prometheus exporter |
| `PPROF_ENABLED`, `PPROF_PORT` | pprof endpoints |
| `ADMIN_EMAIL` | Bootstrap admin user |
| `OPENROUTER_EMBEDDING_MODEL`, `AI_EMBEDDING_DIMENSION` | Embedding model and pgvector dimension (defaults: `google/gemini-embedding-001`, `768`) |

### 2. Run migrations

Migrations are embedded in the binary and applied automatically on startup via `goose` (`pkg/db/postgres.go`). The migration files live in `pkg/db/migrations/`. To run them outside the server, install `goose` and point it at that directory.

### 3. Start the server

```bash
make run                 # plain go run
# or
make dev                 # hot reload (requires `air`)
# or
make docker-compose-up   # full stack with Postgres + monitoring
```

Health check: `curl http://localhost:8000/health`.

### 4. Regenerate proto code (optional)

```bash
make generate            # buf generate + go mod tidy + go mod vendor
```

## 🤝 Contributing

> 🛠 _Contribution guidelines and code of conduct coming soon._

## 📄 License

> 📃 _License type to be defined (MIT, Apache 2.0, or Proprietary)._

On the /discover page and on the /itinerary screen or /restaurants or any result coming from the LLM, how do I add an interactive result so that you see the text slowly being populated on the screen instead of the user waiting for the full request.
The streaming is already implemented on the endpoints.

---

func writeJSONToFile(data interface{}, filename string) error {
jsonData, err := json.MarshalIndent(data, "", " ")
if err != nil {
return fmt.Errorf("failed to marshal JSON: %w", err)
}

    err = os.WriteFile(filename, jsonData, 0644)
    if err != nil {
    	return fmt.Errorf("failed to write JSON file %s: %w", filename, err)
    }

    slog.Info("Successfully wrote JSON payload to file", "filename", filename, "size", len(jsonData))
    return nil

}
