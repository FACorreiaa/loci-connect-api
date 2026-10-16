package recommendation

import (
	"testing"
	"time"

	"github.com/FACorreiaa/go-utils/pkg/util"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recommendation"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestLearningEvent(t *testing.T) {
	t.Parallel()
	rated := func(r int32) *recommendationv1.RecommendationEvent {
		return &recommendationv1.RecommendationEvent{
			EventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED,
			Rating:    util.Ptr(r),
		}
	}
	ofType := func(et recommendationv1.RecommendationEventType) *recommendationv1.RecommendationEvent {
		return &recommendationv1.RecommendationEvent{EventType: et}
	}
	tests := []struct {
		name   string
		event  *recommendationv1.RecommendationEvent
		want   string
		learns bool
	}{
		{name: "exposure is measurement only", event: ofType(recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_PRESENTED)},
		{name: "favorite is positive", event: ofType(recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_FAVORITED), want: "favorited", learns: true},
		{name: "dismissal is negative", event: ofType(recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_DISMISSED), want: "skipped", learns: true},
		{name: "confirmed visit is positive", event: ofType(recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_VISIT_CONFIRMED), want: "visited", learns: true},
		{name: "high rating is a visit", event: rated(5), want: "visited", learns: true},
		{name: "low rating is skipped", event: rated(1), want: "skipped", learns: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, learns := learningEvent(tt.event)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.learns, learns)
		})
	}
}

// preference_feedback.weight is a magnitude; EventMultiplier supplies the
// sign. Storing a negative weight for a skip flips it back to positive.
func TestEventWeight(t *testing.T) {
	t.Parallel()
	for _, et := range []recommendationv1.RecommendationEventType{
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_DISMISSED,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_REMOVED_FROM_TRIP,
	} {
		w := eventWeight(&recommendationv1.RecommendationEvent{EventType: et})
		assert.Equal(t, float32(0.5), w, et.String())
		assert.Less(t, preference.EventMultiplier(preference.EventSkipped)*w, float32(0), et.String())
	}
	assert.Equal(t, float32(0.7), eventWeight(&recommendationv1.RecommendationEvent{
		EventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED,
		Rating:    util.Ptr[int32](4),
	}))
	assert.Equal(t, float32(1.0), eventWeight(&recommendationv1.RecommendationEvent{
		EventType: recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED,
		Rating:    util.Ptr[int32](1),
	}))
}

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

// Explicit verdicts refresh the taste profile at once; exposure and soft
// signals wait for the rerank pass.
func TestExplicitFeedback(t *testing.T) {
	t.Parallel()
	explicit := []recommendationv1.RecommendationEventType{
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_DISMISSED,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_REMOVED_FROM_TRIP,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_VISIT_CONFIRMED,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_RATED,
	}
	for _, et := range explicit {
		assert.True(t, explicitFeedback(et), et.String())
	}
	for _, et := range []recommendationv1.RecommendationEventType{
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_PRESENTED,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_OPENED,
		recommendationv1.RecommendationEventType_RECOMMENDATION_EVENT_TYPE_FAVORITED,
	} {
		assert.False(t, explicitFeedback(et), et.String())
	}
}
