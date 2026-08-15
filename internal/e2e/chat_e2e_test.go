package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth/authconnect"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat/chatconnect"
)

// TestChatE2E drives the full stack against a RUNNING server: register → login →
// StreamChat, and verifies typed events stream back. Opt-in (real LLM + server):
//
//	LOCI_LIVE_LLM=1 go test ./internal/e2e/ -run TestChatE2E -v
//
// Env: LOCI_E2E_BASE_URL (default http://localhost:8000).
func TestChatE2E(t *testing.T) {
	if os.Getenv("LOCI_LIVE_LLM") != "1" {
		t.Skip("set LOCI_LIVE_LLM=1 (needs a running server + billed LLM) to run E2E")
	}
	base := os.Getenv("LOCI_E2E_BASE_URL")
	if base == "" {
		base = "http://localhost:8000"
	}
	httpc := &http.Client{Timeout: 3 * time.Minute}

	authClient := authconnect.NewAuthServiceClient(httpc, base)
	chatClient := chatconnect.NewChatServiceClient(httpc, base)

	ctx := context.Background()
	stamp := time.Now().UnixNano()
	email := fmt.Sprintf("e2e%d@loci.test", stamp)
	username := fmt.Sprintf("e2e%d", stamp)
	const password = "E2eTest-Password-123!"

	// 1) Register.
	if _, err := authClient.Register(ctx, connect.NewRequest(&authv1.RegisterRequest{
		Username: username, Email: email, Password: password,
	})); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	t.Logf("registered %s", email)

	// 2) Login → token.
	loginResp, err := authClient.Login(ctx, connect.NewRequest(&authv1.LoginRequest{
		Email: email, Password: password,
	}))
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	token := loginResp.Msg.GetAccessToken()
	if token == "" {
		t.Fatal("no access token from login")
	}
	t.Logf("logged in, token len=%d", len(token))

	// 3) StreamChat (authed) → collect typed events.
	req := connect.NewRequest(&chatv1.ChatRequest{
		Message:  "Plan a relaxed one-day trip in Lisbon with a few classic sights.",
		CityName: proto("Lisbon"),
	})
	req.Header().Set("Authorization", "Bearer "+token)

	stream, err := chatClient.StreamChat(ctx, req)
	if err != nil {
		t.Fatalf("StreamChat open failed: %v", err)
	}
	defer stream.Close()

	counts := map[chatv1.StreamEventType]int{}
	var total, withItinerary, gotComplete, gotError int
	for stream.Receive() {
		ev := stream.Msg()
		total++
		counts[ev.GetEventType()]++
		switch ev.GetEventType() {
		case chatv1.StreamEventType_STREAM_EVENT_TYPE_ITINERARY:
			withItinerary++
		case chatv1.StreamEventType_STREAM_EVENT_TYPE_COMPLETE:
			gotComplete++
		case chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR:
			gotError++
			if se := ev.GetError(); se != nil {
				t.Logf("stream error event: %s (%s)", se.GetUserMessage(), se.GetInternalCode())
			}
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream receive error: %v", err)
	}

	t.Logf("received %d events; by type:", total)
	for k, v := range counts {
		t.Logf("  %s = %d", k, v)
	}

	if total == 0 {
		t.Fatal("no stream events received")
	}
	if gotError > 0 && withItinerary == 0 && gotComplete == 0 {
		t.Fatal("stream produced only errors, no itinerary/complete")
	}
	t.Logf("E2E OK: itinerary=%d complete=%d error=%d", withItinerary, gotComplete, gotError)
}

func proto(s string) *string { return &s }
