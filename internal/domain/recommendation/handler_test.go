package recommendation

import (
	"testing"
	"time"

	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/recommendation"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestValidateTrace(t *testing.T) {
	t.Parallel()
	valid := &recommendationv1.RecommendationTrace{
		RunId: "run-1", ItemId: "poi-1", Rank: 0, AlgorithmVersion: "discover-v1",
		ExperimentVariant: "personalized", Surface: recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER,
		Channel: recommendationv1.RecommendationChannel_RECOMMENDATION_CHANNEL_WEB,
	}
	tests := []struct {
		name    string
		trace   *recommendationv1.RecommendationTrace
		wantErr bool
	}{
		{name: "accepts complete server trace", trace: valid},
		{name: "rejects nil trace", wantErr: true},
		{name: "rejects unspecified surface", trace: &recommendationv1.RecommendationTrace{RunId: "run", ItemId: "item", AlgorithmVersion: "v1", ExperimentVariant: "control", Channel: recommendationv1.RecommendationChannel_RECOMMENDATION_CHANNEL_WEB}, wantErr: true},
		{name: "rejects negative rank", trace: &recommendationv1.RecommendationTrace{RunId: "run", ItemId: "item", Rank: -1, AlgorithmVersion: "v1", ExperimentVariant: "control", Surface: recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER, Channel: recommendationv1.RecommendationChannel_RECOMMENDATION_CHANNEL_WEB}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateTrace(tt.trace)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestEventFingerprint_DeduplicatesLogicalRetries(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	trace := &recommendationv1.RecommendationTrace{
		RunId: "run-1", ItemId: "poi-1", AlgorithmVersion: "discover-v1", ExperimentVariant: "personalized",
		Surface: recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_DISCOVER,
		Channel: recommendationv1.RecommendationChannel_RECOMMENDATION_CHANNEL_WEB,
	}
	first := &recommendationv1.RecommendationEvent{
		ClientEventId: uuid.NewString(), EventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_FAVORITED,
		Trace: trace, OccurredAt: timestamppb.New(time.Now()), Metadata: map[string]string{"source": "card"},
	}
	second := &recommendationv1.RecommendationEvent{
		ClientEventId: uuid.NewString(), EventType: first.GetEventType(), Trace: trace,
		OccurredAt: timestamppb.New(time.Now().Add(time.Second)), Metadata: map[string]string{"source": "card"},
	}
	metadata := []byte(`{"source":"card"}`)
	assert.Equal(t, eventFingerprint(userID, first, metadata, nil), eventFingerprint(userID, second, metadata, nil))

	second.EventType = recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_DISMISSED
	assert.NotEqual(t, eventFingerprint(userID, first, metadata, nil), eventFingerprint(userID, second, metadata, nil))
}
