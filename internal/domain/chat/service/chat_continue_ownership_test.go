package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Continuing a conversation writes into it: new messages, a replaced
// itinerary, and LLM work billed to the session's owner. Knowing a session id
// must not be enough to do that to someone else's session.
func TestContinueChatRefusesAnotherUsersSession(t *testing.T) {
	owner := uuid.New()
	svc, sessionID := sessionWith(t, owner, &locitypes.AiCityResponse{})

	_, err := svc.ContinueChat(context.Background(), uuid.New(), sessionID, "add a museum", "")
	if !errors.Is(err, common.ErrUnauthorized) {
		t.Fatalf("another user continued the session: err = %v, want ErrUnauthorized", err)
	}
}
