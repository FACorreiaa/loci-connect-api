package recommendation

import (
	"testing"

	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/recommendation"
	"github.com/stretchr/testify/assert"
)

func TestLearningEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		eventType recommendationv1.RecommendationEventType
		want      string
		learns    bool
	}{
		{name: "exposure is measurement only", eventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_PRESENTED},
		{name: "favorite is positive", eventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_FAVORITED, want: "favorited", learns: true},
		{name: "dismissal is negative", eventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_DISMISSED, want: "skipped", learns: true},
		{name: "confirmed visit is positive", eventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_VISIT_CONFIRMED, want: "visited", learns: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, learns := learningEvent(tt.eventType)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.learns, learns)
		})
	}
}

func TestEventWeight(t *testing.T) {
	t.Parallel()
	assert.Equal(t, float32(-0.5), eventWeight(&recommendationv1.RecommendationEvent{
		EventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_REMOVED_FROM_TRIP,
	}))
	assert.Equal(t, float32(0.8), eventWeight(&recommendationv1.RecommendationEvent{
		EventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED,
		Rating:    int32Pointer(4),
	}))
}

func int32Pointer(value int32) *int32 { return &value }
