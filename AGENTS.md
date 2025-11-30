Im migrating an old REST API to gRPC using Connect-Go.
I have already migrated the authentication on the folder Auth to RPC using Connect-Go.
Now I want to migrate a new the chat feature. But before we do that there is one issue:
- Chat issue has a lot of dependencies on the old REST API services.
- After importing everything I need from the old REST API services, I still have a lot of code that uses the types.
- I added the types to a type folder to make it easy
- All necessary proto schemas are on folder gen.

We first need:

1. read the chat proto gen inside the gen folder
2. understand how the chat feature is working with the old REST API (chat_handler.go.old)
3. migrate the chat feature to Connect-Go using the same pattern as the Auth folder (folder structure, service organisation)
4. make sure everything is working by running the tests on the chat folder
5. organise the chat folder and delete the import cycle
6. based on chat_handler.go.old, create the equivalent chat_handler.go using Connect-Go
7. update the routes to use the new chat handler
8. create tests for the new chat handler if there are none
9. run all tests to make sure everything is working
10. run make generate and everything should be working without import cycles
11. We also need to build the profile (profiles) handlers based on the same pattern after chat is done.
12. The client needs this handler (GetUserProfileByID) to be able to connect to the chat service and to the whole app.
13. the client is using the old http://localhost:8080/user/search-profile/default and will need this working to update the new endpoint

___

## Chat example to rewrite with connect:

To migrate your gRPC service to use Connect-Go (from `connectrpc.com/connect`), you'll need to make the following key changes for the streaming methods like `StartChatStream` and `ContinueChatStream`:

- Import `connect "connectrpc.com/connect"`.
- Update the method signatures to use Connect's types: `ctx context.Context`, `*connect.Request[RequestType]`, and `*connect.ServerStream[ResponseType]`.
- Access the request message via `req.Msg` (e.g., `req.Msg.InitialMessage`).
- In the streaming loop, use `stream.Send(pbEvent)` instead of `stream.SendMsg(pbEvent)`.
- For errors, return `connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(...))` or similar, instead of `status.Errorf`. Connect handles errors by encoding them in HTTP trailers (responses are always HTTP 200 OK for streaming).
- Remove `pb.UnsafeChatServiceServer` from the `Service` struct, as Connect doesn't require embedding a generated server interface—your struct just implements the handler methods directly.
- Update the `handleStream` function to take `*connect.ServerStream[pb.ChatEvent]` instead of `grpc.ServerStream`.
- The `context.WithCancel(stream.Context())` remains valid, as Connect's `ServerStream` provides a `Context()` method.
- No changes needed for internal functions like `startStreamProcessing` or non-streaming methods (though unary methods like `GetChatSessions` would change to return `*connect.Response[ResponseType]`).

Here's how the relevant parts of your code would look after migration. I've focused on `StartChatStream`, `ContinueChatStream`, and the supporting `handleStream` function, as those are the streaming endpoints you asked about. (The rest of the file remains largely the same, but you'd apply similar patterns to other methods.)

```go
package chat_prompt

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/FACorreiaa/loci-proto/modules/chat/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/patrickmn/go-cache"
	connect "connectrpc.com/connect" // Add this import
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/lib"

	"github.com/FACorreiaa/go-poi-au-suggestions/internal/domain"
	cityDomain "github.com/FACorreiaa/go-poi-au-suggestions/internal/domain/city"
	interestsDomain "github.com/FACorreiaa/go-poi-au-suggestions/internal/domain/interests"
	poiDomain "github.com/FACorreiaa/go-poi-au-suggestions/internal/domain/poi"
	profilesDomain "github.com/FACorreiaa/go-poi-au-suggestions/internal/domain/profiles"
	tagsDomain "github.com/FACorreiaa/go-poi-au-suggestions/internal/domain/tags"
)

// ... (startStreamProcessing, IntentClassifier, NewService, etc. remain unchanged)

// Remove pb.UnsafeChatServiceServer from the struct
type Service struct {
	logger *zap.Logger
	repo   Repository
	pgpool *pgxpool.Pool
	tracer trace.Tracer

	// Business logic dependencies
	interestRepo      interestsDomain.Repository
	searchProfileRepo profilesDomain.Repository
	searchProfileSvc  *profilesDomain.Service
	tagsRepo          tagsDomain.Repository
	aiClient          *generativeAI.LLMChatClient
	embeddingService  *generativeAI.EmbeddingService
	cityRepo          cityDomain.Repository
	poiRepo           poiDomain.Repository
	cache             *cache.Cache

	// Events
	deadLetterCh     chan StreamEvent
	intentClassifier IntentClassifier
}

// Updated signature for server streaming
func (svc *Service) StartChatStream(ctx context.Context, req *connect.Request[pb.StartChatRequest], stream *connect.ServerStream[pb.ChatEvent]) error {
	ctx, cancel := context.WithCancel(stream.Context()) // Use stream.Context()
	defer cancel()

	userID, err := domain.CheckUserAuth(ctx)
	if err != nil {
		return err // Connect will handle this as an error in trailers
	}

	ctx, span := svc.tracer.Start(ctx, "ChatService.StartChatStream", trace.WithAttributes(
		attribute.String("chat.user_id", userID),
		attribute.String("chat.profile_id", req.Msg.ProfileId), // Access via req.Msg
	))
	defer span.End()

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user ID: %v", err))
	}

	profileUUID, err := uuid.Parse(req.Msg.ProfileId) // Access via req.Msg
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid profile ID: %v", err))
	}

	return svc.handleStream(ctx, stream, func(eventCh chan<- StreamEvent) error {
		return svc.ProcessUnifiedChatMessageStream(ctx, userUUID, profileUUID, req.Msg.InitialMessage, req.Msg.Metadata, eventCh) // Access via req.Msg
	})
}

// Updated to take Connect's ServerStream
func (svc *Service) handleStream(ctx context.Context, stream *connect.ServerStream[pb.ChatEvent], processFunc func(eventCh chan<- StreamEvent) error) error {
	eventCh := make(chan StreamEvent, 200)
	errCh := make(chan error, 1)

	go func() {
		defer close(errCh)
		defer close(eventCh)
		if err := processFunc(eventCh); err != nil {
			errCh <- err
		}
	}()

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				// processFunc has finished
				return nil
			}
			pbEvent, err := svc.convertStreamEventToChatEvent(event)
			if err != nil {
				svc.logger.Error("Failed to convert stream event", zap.Error(err))
				continue // Or handle more gracefully
			}
			if err := stream.Send(pbEvent); err != nil { // Use stream.Send (no SendMsg)
				svc.logger.Error("Failed to send message to stream", zap.Error(err))
				return err // Client disconnected or other stream error
			}

		case err := <-errCh:
			if err != nil {
				svc.logger.Error("Error during stream processing", zap.Error(err))
				// Decide if you want to send an error message to the client
				return err
			}

		case <-ctx.Done():
			svc.logger.Info("Stream cancelled by client")
			return ctx.Err()
		}
	}
}

// Updated signature for server streaming
func (svc *Service) ContinueChatStream(ctx context.Context, req *connect.Request[pb.ContinueChatRequest], stream *connect.ServerStream[pb.ChatEvent]) error {
	ctx, cancel := context.WithCancel(stream.Context()) // Use stream.Context()
	defer cancel()

	userID, err := domain.CheckUserAuth(ctx)
	if err != nil {
		return err
	}

	ctx, span := svc.tracer.Start(ctx, "ChatService.ContinueChatStream", trace.WithAttributes(
		attribute.String("chat.user_id", userID),
		attribute.String("chat.session_id", req.Msg.SessionId), // Access via req.Msg
	))
	defer span.End()

	sessionUUID, err := uuid.Parse(req.Msg.SessionId) // Access via req.Msg
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid session ID: %v", err))
	}

	return svc.handleStream(ctx, stream, func(eventCh chan<- StreamEvent) error {
		return svc.ContinueSessionStreamed(ctx, sessionUUID, req.Msg.Message, nil, eventCh) // Access via req.Msg
	})
}

// ... (FreeChatStream, GetChatSessions, etc. would follow similar patterns; convertStreamEventToChatEvent remains unchanged)
```
Should follow the proto conventions from the gen folder, the current interceptors, etc.
