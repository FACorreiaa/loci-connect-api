package preference

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// DefaultUserBlend is the weight given to the stored user preference vector when
// blending with a query embedding (remainder stays on the query).
const DefaultUserBlend = 0.35

// EventMultiplier returns the signed weight multiplier for a feedback event.
// Skipped events pull the preference vector away from that POI.
func EventMultiplier(event string) float32 {
	switch event {
	case EventFavorited:
		return 1.5
	case EventSaved:
		return 1.2
	case EventVisited:
		return 1.3
	case EventReordered:
		return 0.8
	case EventExported:
		return 0.5
	case EventSkipped:
		return -1.0
	default:
		return 1.0
	}
}

// WeightedAverage computes a dimension-wise weighted mean of embedding vectors.
// Zero total absolute weight or empty input returns an error.
func WeightedAverage(vectors [][]float32, weights []float32) ([]float32, error) {
	if len(vectors) == 0 {
		return nil, fmt.Errorf("no vectors")
	}
	if len(vectors) != len(weights) {
		return nil, fmt.Errorf("vectors/weights length mismatch")
	}
	dim := len(vectors[0])
	if dim == 0 {
		return nil, fmt.Errorf("empty embedding dimension")
	}

	out := make([]float32, dim)
	var absWeight float64
	for i, v := range vectors {
		if len(v) != dim {
			return nil, fmt.Errorf("embedding dim mismatch at index %d", i)
		}
		w := float64(weights[i])
		absWeight += math.Abs(w)
		for d := 0; d < dim; d++ {
			out[d] += float32(w) * v[d]
		}
	}
	if absWeight < 1e-9 {
		return nil, fmt.Errorf("total weight is zero")
	}
	scale := float32(1.0 / absWeight)
	for d := range out {
		out[d] *= scale
	}
	return out, nil
}

// Blend mixes query and user preference embeddings.
// userWeight in [0,1]; 0 = query only. Vectors must share dimension.
func Blend(query, user []float32, userWeight float64) ([]float32, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("empty query embedding")
	}
	if len(user) == 0 {
		return append([]float32(nil), query...), nil
	}
	if len(query) != len(user) {
		return nil, fmt.Errorf("blend dim mismatch: query=%d user=%d", len(query), len(user))
	}
	if userWeight < 0 {
		userWeight = 0
	}
	if userWeight > 1 {
		userWeight = 1
	}
	qw := 1 - userWeight
	out := make([]float32, len(query))
	for i := range query {
		out[i] = float32(qw)*query[i] + float32(userWeight)*user[i]
	}
	return out, nil
}

// FormatVector encodes a float32 slice as a pgvector literal.
func FormatVector(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// ParseVector parses a pgvector text literal like "[0.1,0.2]".
func ParseVector(s string) ([]float32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty vector")
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return []float32{}, nil
	}
	parts := strings.Split(s, ",")
	out := make([]float32, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, fmt.Errorf("parse vector element %d: %w", i, err)
		}
		out[i] = float32(f)
	}
	return out, nil
}
