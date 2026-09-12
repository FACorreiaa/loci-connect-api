// Package vocabulary supplies the place names a recogniser should expect.
//
// It exists because of one measurement. Asked to transcribe "take me to Cais
// do Sodré, then Bairro Alto and Belém", the cluster's speech service answers
// "Case 2 Soda, then Baro Alto and Bellum" — and an itinerary is then planned,
// confidently, for somewhere that does not exist. Given the same audio and the
// same place names as a hint, it answers them correctly, accents and all.
//
// For a travel app this is not a refinement. Proper nouns are most of what
// anybody says to it.
package vocabulary

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxPool is the part of the pool this package uses.
type PgxPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

var _ PgxPool = (*pgxpool.Pool)(nil)

// maxCities is how many recent cities are considered.
//
// Somebody plans one trip at a time; the cities before that are noise
// competing for a hint window that only holds a couple of hundred tokens.
const maxCities = 3

// maxPlaces is how many place names are offered.
//
// The hint is fed to the decoder as if it were speech preceding the clip, and
// these models look back a fixed and small distance. Past that a longer hint
// is not more help — the tail is simply dropped.
const maxPlaces = 40

// Places supplies place names for a user.
type Places struct {
	pool   PgxPool
	logger *slog.Logger
}

func New(pool PgxPool, logger *slog.Logger) *Places {
	if logger == nil {
		logger = slog.Default()
	}
	return &Places{pool: pool, logger: logger}
}

// For returns place names this user is likely to say, as a comma-separated
// hint, or empty when there is nothing useful to offer.
//
// Never an error: a hint is an improvement to transcription, not a
// precondition for it, and failing to build one is no reason to refuse to
// listen. A failure is logged and the recording goes ahead unhinted.
func (p *Places) For(ctx context.Context, userID uuid.UUID) string {
	if p == nil || p.pool == nil || userID == uuid.Nil {
		return ""
	}

	cities := p.recentCities(ctx, userID)
	if len(cities) == 0 {
		return ""
	}

	names := append([]string(nil), cities...)
	names = append(names, p.placesIn(ctx, cities)...)
	return strings.Join(dedupe(names), ", ")
}

// notACity is the value a session carries when the question was about where
// somebody is standing rather than about a place by name.
//
// It is a routing marker, not somewhere anybody has been: see routeType in the
// chat service. Left in, the hint would offer the decoder the literal word
// "nearme", which is both useless and a word no speaker will say. Seven of the
// nineteen sessions in production carry it.
const notACity = "nearme"

// recentCities are the places this user has been asking about, most recent
// first.
//
// Ordered, because the hint window only holds a couple of hundred tokens and
// the trip somebody is planning now is worth more of it than one from a month
// ago. An unordered DISTINCT would have taken whichever rows the planner
// happened to return.
func (p *Places) recentCities(ctx context.Context, userID uuid.UUID) []string {
	const query = `
		SELECT city_name
		FROM chat_sessions
		WHERE user_id = $1
		  AND COALESCE(city_name, '') <> ''
		  AND LOWER(city_name) <> $2
		GROUP BY city_name
		ORDER BY MAX(updated_at) DESC
		LIMIT $3`

	return p.strings(ctx, "recent cities", query, userID, notACity, maxCities)
}

// placesIn are the named places in those cities.
//
// Not every city somebody asks about has rows here — a place discovered in
// conversation may never have become a city row — and that is fine: the city
// name on its own is still worth offering.
func (p *Places) placesIn(ctx context.Context, cities []string) []string {
	const query = `
		SELECT poi.name
		FROM points_of_interest poi
		JOIN cities c ON c.id = poi.city_id
		WHERE LOWER(c.name) = ANY($1)
		LIMIT $2`

	lowered := make([]string, 0, len(cities))
	for _, city := range cities {
		lowered = append(lowered, strings.ToLower(city))
	}
	return p.strings(ctx, "places", query, lowered, maxPlaces)
}

func (p *Places) strings(ctx context.Context, what, query string, args ...any) []string {
	result, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		p.logger.WarnContext(ctx, "could not read a transcription hint",
			slog.String("reading", what), slog.String("error", err.Error()))
		return nil
	}
	defer result.Close()

	var values []string
	for result.Next() {
		var value string
		if err := result.Scan(&value); err != nil {
			p.logger.WarnContext(ctx, "could not read a transcription hint",
				slog.String("reading", what), slog.String("error", err.Error()))
			return values
		}
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	if err := result.Err(); err != nil {
		p.logger.WarnContext(ctx, "could not read a transcription hint",
			slog.String("reading", what), slog.String("error", err.Error()))
	}
	return values
}

// dedupe removes repeats case-insensitively, keeping the first spelling.
//
// A city is usually also a place name in its own right, and repeating a word
// in the hint spends the window without adding anything.
func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := values[:0]
	for _, value := range values {
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
