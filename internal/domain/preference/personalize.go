package preference

import (
	"context"
	"crypto/sha256"
	"encoding/binary"

	"github.com/google/uuid"
)

const controlHoldoutPercent = 10

// ExperimentVariant assigns a stable account-level 10% control holdout.
func ExperimentVariant(userID uuid.UUID) string {
	if userID == uuid.Nil {
		return "anonymous"
	}
	sum := sha256.Sum256(userID[:])
	bucket := binary.BigEndian.Uint64(sum[:8]) % 100
	if bucket < controlHoldoutPercent {
		return "control"
	}
	return "personalized"
}

// PersonalizeQuery blends a query embedding with the user's stored preference
// vector when one exists. Failures / missing vectors return the query unchanged.
func PersonalizeQuery(ctx context.Context, reader VectorReader, userID uuid.UUID, query []float32) []float32 {
	if reader == nil || userID == uuid.Nil || len(query) == 0 {
		return query
	}
	if ExperimentVariant(userID) == "control" {
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
