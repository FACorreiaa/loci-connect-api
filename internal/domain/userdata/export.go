// Package userdata assembles a complete copy of everything Loci holds about a
// person, for the self-service export.
//
// The export RPC previously returned `{exported_at, profile}` and nothing else —
// a single profile row, while the account also held trips, lists, favorites,
// itineraries, travel history, chat sessions, learned taste traits and the
// signals behind them. A GDPR-style export that omits almost everything is worse
// than none: it looks like an answer.
//
// Queries run directly against the pool rather than through eight services. The
// export is a read-only snapshot with no business logic, and threading every
// domain service into the user handler to produce one JSON blob would couple
// them all together for no gain.
package userdata

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exporter builds export bundles.
type Exporter struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

// NewExporter wires an Exporter.
func NewExporter(db *pgxpool.Pool, logger *slog.Logger) *Exporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Exporter{db: db, logger: logger.With(slog.String("component", "userdata-export"))}
}

// Bundle is everything the account holds.
//
// Sections that failed to read are named in Incomplete rather than silently
// omitted. A user comparing this against what they see in the app must be able
// to tell "you have none of these" from "we could not read them".
type Bundle struct {
	ExportedAt time.Time `json:"exported_at"`
	UserID     string    `json:"user_id"`

	Profile         any `json:"profile,omitempty"`
	Trips           any `json:"trips"`
	Lists           any `json:"lists"`
	Favorites       any `json:"favorites"`
	Itineraries     any `json:"saved_itineraries"`
	VisitedCities   any `json:"visited_cities"`
	VisitedPOIs     any `json:"visited_pois"`
	ChatSessions    any `json:"chat_sessions"`
	TasteTraits     any `json:"taste_traits"`
	TasteEvidence   any `json:"taste_evidence"`
	APIKeys         any `json:"api_keys"`
	Personalization any `json:"personalization_settings,omitempty"`

	// Incomplete names sections that could not be read.
	Incomplete []string `json:"incomplete_sections,omitempty"`
}

// section describes one part of the bundle.
type section struct {
	name   string
	query  string
	target *any
}

// Build assembles the export.
//
// A failure in one section is recorded and the rest continues: a user asking for
// their data during an incident should still receive what is readable.
func (e *Exporter) Build(ctx context.Context, userID uuid.UUID, profile any) (*Bundle, error) {
	if e == nil || e.db == nil {
		return nil, fmt.Errorf("exporter is not configured with a database")
	}

	bundle := &Bundle{
		ExportedAt: time.Now().UTC(),
		UserID:     userID.String(),
		Profile:    profile,
	}

	sections := []section{
		{"trips", `
			SELECT t.id, t.title, t.city_name, t.created_at, t.updated_at, t.is_public,
			       COALESCE(json_agg(DISTINCT jsonb_build_object(
			           'day_number', d.day_number, 'date', d.date, 'city_name', d.city_name
			       )) FILTER (WHERE d.id IS NOT NULL), '[]') AS days
			FROM trips t
			LEFT JOIN trip_days d ON d.trip_id = t.id
			WHERE t.user_id = $1
			GROUP BY t.id
			ORDER BY t.created_at DESC`, &bundle.Trips},

		{"lists", `
			SELECT id, name, description, is_public, is_itinerary, item_count, created_at
			FROM lists WHERE user_id = $1 ORDER BY created_at DESC`, &bundle.Lists},

		{"favorites", `
			SELECT item_id, item_name, content_type, city_name, notes, added_at
			FROM user_favorites WHERE user_id = $1 ORDER BY added_at DESC`, &bundle.Favorites},

		{"saved_itineraries", `
			SELECT id, title, description, tags, markdown_content, is_public, created_at
			FROM user_saved_itineraries WHERE user_id = $1 ORDER BY created_at DESC`, &bundle.Itineraries},

		{"visited_cities", `
			SELECT city_name, country, latitude, longitude, source,
			       first_visit_at, last_visit_at, visit_count
			FROM user_visited_cities WHERE user_id = $1 ORDER BY last_visit_at DESC`, &bundle.VisitedCities},

		{"visited_pois", `
			SELECT poi_id, poi_name, city_name, source, visited_at
			FROM user_visited_pois WHERE user_id = $1 ORDER BY visited_at DESC`, &bundle.VisitedPOIs},

		{"chat_sessions", `
			SELECT id, city_name, search_type, conversation_history, created_at, updated_at
			FROM chat_sessions WHERE user_id = $1 ORDER BY created_at DESC`, &bundle.ChatSessions},

		{"taste_traits", `
			SELECT trait_key, label, score, confidence, evidence_count, updated_at
			FROM user_taste_traits WHERE user_id = $1 ORDER BY confidence DESC`, &bundle.TasteTraits},

		// The evidence behind each learned trait, joined to the place it
		// concerned. This is the part that makes the profile checkable rather
		// than merely visible.
		{"taste_evidence", `
			SELECT e.trait_key, pf.event, e.weight, COALESCE(p.name, ''), e.occurred_at
			FROM taste_trait_evidence e
			JOIN preference_feedback pf ON pf.id = e.feedback_id
			LEFT JOIN points_of_interest p ON p.id::text = pf.poi_id
			WHERE e.user_id = $1
			ORDER BY e.occurred_at DESC`, &bundle.TasteEvidence},

		// Metadata only. The key hash is deliberately excluded: exporting it
		// would put a credential-equivalent value in a file users email around.
		{"api_keys", `
			SELECT id, name, key_prefix, scopes, created_at, last_used_at, expires_at, revoked_at
			FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC`, &bundle.APIKeys},

		{"personalization_settings", `
			SELECT personalization_enabled, contribute_aggregate, updated_at
			FROM personalization_settings WHERE user_id = $1`, &bundle.Personalization},
	}

	for _, sec := range sections {
		rows, err := e.collect(ctx, sec.query, userID)
		if err != nil {
			e.logger.WarnContext(ctx, "export section failed",
				slog.String("section", sec.name), slog.Any("error", err))
			bundle.Incomplete = append(bundle.Incomplete, sec.name)
			*sec.target = []any{}
			continue
		}
		*sec.target = rows
	}

	return bundle, nil
}

// collect runs a query and returns its rows as generic maps, so a schema change
// widens the export automatically instead of silently dropping a new column.
func (e *Exporter) collect(ctx context.Context, query string, userID uuid.UUID) ([]map[string]any, error) {
	rows, err := e.db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	out := make([]map[string]any, 0)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(fields))
		for i, f := range fields {
			if i < len(values) {
				row[string(f.Name)] = values[i]
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// JSON renders the bundle.
func (b *Bundle) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal export: %w", err)
	}
	return data, nil
}
