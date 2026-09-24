package presenter

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestConversationMessageOrigin(t *testing.T) {
	// Every message stored before origins existed has none: a reply.
	legacy := ToConversationMessage(locitypes.ConversationMessage{ID: uuid.New(), Role: locitypes.RoleAssistant, Content: "hi", Timestamp: time.Now()})
	require.Equal(t, chatv1.MessageOrigin_MESSAGE_ORIGIN_REPLY, legacy.GetOrigin())
	require.Empty(t, legacy.GetSourceLabel())

	proactive := ToConversationMessage(locitypes.ConversationMessage{
		ID: uuid.New(), Role: locitypes.RoleAssistant, Content: "Dry all week.", Timestamp: time.Now(),
		Origin: locitypes.OriginProactive, SourceLabel: "Standing task",
	})
	require.Equal(t, chatv1.MessageOrigin_MESSAGE_ORIGIN_PROACTIVE, proactive.GetOrigin())
	require.Equal(t, "Standing task", proactive.GetSourceLabel())
}

// conversation_history is JSONB: old rows must still decode, and a reply
// must not grow new keys.
func TestConversationMessageOriginJSON(t *testing.T) {
	var old locitypes.ConversationMessage
	require.NoError(t, json.Unmarshal([]byte(`{"id":"`+uuid.NewString()+`","role":"assistant","content":"x","timestamp":"2026-09-23T10:00:00Z"}`), &old))
	require.Empty(t, old.Origin)

	b, err := json.Marshal(locitypes.ConversationMessage{Role: locitypes.RoleAssistant})
	require.NoError(t, err)
	require.NotContains(t, string(b), "origin")
	require.NotContains(t, string(b), "source_label")

	b, err = json.Marshal(locitypes.ConversationMessage{Origin: locitypes.OriginProactive, SourceLabel: "Standing task"})
	require.NoError(t, err)
	var back locitypes.ConversationMessage
	require.NoError(t, json.Unmarshal(b, &back))
	require.Equal(t, locitypes.OriginProactive, back.Origin)
	require.Equal(t, "Standing task", back.SourceLabel)
}
