package placeintel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// badgeCopy is the display copy for each badge slug the server awards. The
// wording lives here so web and iOS show the same thing without each keeping
// its own table. creditCorroborators awards "local-scout".
var badgeCopy = map[string]struct{ name, description string }{
	"local-scout": {
		name:        "Local scout",
		description: "Ten of your reports were confirmed by another scout.",
	},
}

// badgeFor returns the display form of a badge slug. A slug with no entry in
// badgeCopy still gets a readable name ("night-owl" → "Night owl") so a badge
// awarded before its copy is written never shows up blank.
func badgeFor(slug string) *placev1.Badge {
	if c, ok := badgeCopy[slug]; ok {
		return &placev1.Badge{Slug: slug, DisplayName: c.name, Description: c.description}
	}
	name := strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(slug))
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	return &placev1.Badge{Slug: slug, DisplayName: name}
}

func badgesFor(slugs []string) []*placev1.Badge {
	out := make([]*placev1.Badge, 0, len(slugs))
	for _, s := range slugs {
		out = append(out, badgeFor(s))
	}
	return out
}

const defaultMyClaimsLimit = 20

// myClaimsPage turns the request's limit and 1-based page into LIMIT/OFFSET.
func myClaimsPage(limit, page int32) (int, int) {
	l := int(limit)
	if l <= 0 {
		l = defaultMyClaimsLimit
	}
	p := int(page)
	if p < 1 {
		p = 1
	}
	return l, (p - 1) * l
}

// ListMyClaims lists the caller's own claims, newest first, with the place's
// current name and each claim's status.
func (h *Handler) ListMyClaims(ctx context.Context, req *connect.Request[placev1.ListMyClaimsRequest]) (*connect.Response[placev1.ListMyClaimsResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset := myClaimsPage(req.Msg.GetLimit(), req.Msg.GetPage())

	var total int32
	if err := h.db.QueryRow(ctx, `SELECT COUNT(*)::integer FROM place_claims WHERE user_id = $1`, uid).Scan(&total); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("count my claims: %w", err))
	}

	// place_claims.poi_id is TEXT with no foreign key; compare as text so a
	// malformed or deleted id yields an empty name instead of a cast error.
	rows, err := h.db.Query(ctx, `
		SELECT c.id::text, c.poi_id, COALESCE(p.name, ''), c.field, c.value, c.status, c.created_at
		FROM place_claims c
		LEFT JOIN points_of_interest p ON p.id::text = c.poi_id
		WHERE c.user_id = $1
		ORDER BY c.created_at DESC, c.id
		LIMIT $2 OFFSET $3`, uid, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list my claims: %w", err))
	}
	defer rows.Close()

	claims := make([]*placev1.MyPlaceClaim, 0, limit)
	for rows.Next() {
		var (
			claim         placev1.MyPlaceClaim
			field, status string
			created       time.Time
		)
		if err := rows.Scan(&claim.ClaimId, &claim.PoiId, &claim.PoiName, &field, &claim.Value, &status, &created); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan my claim: %w", err))
		}
		claim.Field = parseField(field)
		claim.Status = parseStatus(status)
		claim.CreatedAt = timestamppb.New(created)
		claims = append(claims, &claim)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate my claims: %w", err))
	}
	return connect.NewResponse(&placev1.ListMyClaimsResponse{Claims: claims, Total: total}), nil
}
