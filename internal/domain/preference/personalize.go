package preference

import (
	"context"

	"github.com/google/uuid"
)

// PersonalizeQuery blends a query embedding with the user's stored preference
// vector when one exists. Failures / missing vectors return the query unchanged.
func PersonalizeQuery(ctx context.Context, reader VectorReader, userID uuid.UUID, query []float32) []float32 {
	if reader == nil || userID == uuid.Nil || len(query) == 0 {
		return query
	}
	user, ok, err := reader.GetEmbedding(ctx, userID)
	if err != nil || !ok || len(user) == 0 {
		return query
	}
	blended, err := Blend(query, user, DefaultUserBlend)
	if err != nil {
		return query
	}
	return blended
}
