package interceptors

import "testing"

// A confirmed "re-plan as N days" is a full city generation inside a unary
// RPC; the 30s default cuts it off mid-way.
func TestApplyTripActionGetsTheChatBudget(t *testing.T) {
	if !isChatUnaryProcedure("/loci.chat.ChatService/ApplyTripAction") {
		t.Fatal("ApplyTripAction must get the chat unary timeout")
	}
	if isChatUnaryProcedure("/loci.chat.ChatService/DismissTripAction") {
		t.Fatal("dismissing is instant; it keeps the default")
	}
}
