package api

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	authpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/auth/authconnect"
	speechv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/speech"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/speech/speechv1connect"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

const mib = 1 << 20

// routerForLimitTests serves the real SetupRouter, so these tests fail if a
// service is registered without the limit. Nothing here reaches a service:
// the calls are refused while reading, or by auth just after it.
func routerForLimitTests(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(SetupRouter(&Dependencies{
		Config: &config.Config{Auth: config.AuthConfig{JWTSecret: "read-limit-test-secret"}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReadLimit_DefaultRefusesOversizedRequest(t *testing.T) {
	srv := routerForLimitTests(t)
	client := authconnect.NewAuthServiceClient(srv.Client(), srv.URL)

	_, err := client.Login(context.Background(), connect.NewRequest(&authpb.LoginRequest{
		Email: strings.Repeat("a", 2*mib),
	}))

	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), "got: %v", err)
}

// A small body that inflates past the limit must be refused like a large one.
func TestReadLimit_AppliesAfterDecompression(t *testing.T) {
	srv := routerForLimitTests(t)
	client := authconnect.NewAuthServiceClient(srv.Client(), srv.URL, connect.WithSendGzip())

	_, err := client.Login(context.Background(), connect.NewRequest(&authpb.LoginRequest{
		Email: strings.Repeat("a", 2*mib),
	}))

	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), "got: %v", err)
}

func TestReadLimit_Speech(t *testing.T) {
	srv := routerForLimitTests(t)
	// JSON, as the web client sends it: base64 makes the 10 MiB the proto
	// allows about 13.4 MiB on the wire, and that must still be read.
	client := speechv1connect.NewSpeechServiceClient(srv.Client(), srv.URL, connect.WithProtoJSON())

	transcribe := func(size int) error {
		_, err := client.Transcribe(context.Background(), connect.NewRequest(&speechv1.TranscribeRequest{
			Audio:    make([]byte, size),
			MimeType: "audio/ogg",
		}))
		return err
	}

	// The largest recording the proto allows gets past the read limit, and so
	// is refused for having no token rather than for its size.
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(transcribe(10*mib)))
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(transcribe(11*mib)))
}
