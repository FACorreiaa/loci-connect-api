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

___

After completing this, analyse the readme, the scope, the routes and the framework decision guide to make sure everything is aligned with Connect-Go and there are no references to the old REST API where not needed.
To migrate your gRPC service to use Connect-Go (from `connectrpc.com/connect`),

you'll need to make the following key changes for the streaming methods like `StartChatStream` and `ContinueChatStream`:
- Import `connect "connectrpc.com/connect"`.
- Update the method signatures to use Connect's types: `ctx context.Context`, `*connect
- Request[RequestType]`, and `*connect.ServerStream[ResponseType]`.
- Access the request message via `req.Msg` (e.g., `req.Msg.InitialMessage
- `).
- In the streaming loop, use `stream.Send(pbEvent)` instead of `stream.SendMsg(pbEvent)`.
- For errors, return `connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(...))` or similar, instead of `status.Errorf`. Connect handles errors by encoding them in HTTP trailers (responses are always HTTP 200 OK for streaming).
- Remove `pb.UnsafeChatServiceServer` from the `Service` struct, as Connect doesn't require embedding a generated server interface—your struct just implements the handler methods directly.
- Update the `handleStream` function to take `*connect.ServerStream[pb.ChatEvent]` instead of `grpc.ServerStream`.
- The `context.WithCancel(stream.Context())` remains valid, as Connect's `ServerStream` provides a `Context()` method.
  - No changes needed for internal functions like `startStreamProcessing` or non-streaming methods (though unary methods like `GetChatSessions` would change to return `*connect.Response[ResponseType]`).
  - Here's how the relevant parts of your code would look after migration. I've focused on `StartChatStream`, `ContinueChatStream`, and the supporting `handleStream` function, as those are the streaming endpoints you asked about. (The rest of the file remains largely the same, but you'd apply similar patterns to other methods.)


___
### Aux example for the chat handler:
Here is how your implementation transforms when moving to **Connect-Go**. Just as an example
Always follow the best practices from the Auth folder migration and Connect Go

The architecture shifts from **Manual HTTP/SSE management** to **Typed RPC Streaming**.

### 1. The Protobuf Definition (`chat.proto`)
First, you must define the data contract. Since your current logic relies on dynamic `map[string]interface{}` (especially for the chunks), we will map that into the Protobuf message using a JSON string or specific fields.

```protobuf
syntax = "proto3";

package chat.v1;

import "google/protobuf/struct.proto";

service ChatService {
  rpc StreamChat(StreamChatRequest) returns (stream StreamChatResponse);
}

message StreamChatRequest {
  string profile_id = 1;
  string message = 2;
  UserLocation user_location = 3;
}

message UserLocation {
  double latitude = 1;
  double longitude = 2;
}

message StreamChatResponse {
  // Mirroing your types.StreamEvent.Type
  string event_type = 1;

  // Connect allows strict typing, but since your service yields
  // dynamic JSON chunks, we can pass the data as a string
  // or a Struct for non-chunk events.
  oneof payload {
    // For "chunk" events containing partial strings
    StreamChunk chunk_data = 2;
    // For "start", "complete", "error" events
    google.protobuf.Struct generic_data = 3;
    // For the final itinerary object
    string itinerary_json = 4;
  }

  // Specific error field
  string error_message = 5;

  // Navigation data for the final redirect
  NavigationData navigation = 6;
}

message StreamChunk {
  string part_type = 1; // e.g., "city_data", "itinerary"
  string content = 2;   // The text fragment
}

message NavigationData {
  string url = 1;
  string route_type = 2;
  map<string, string> query_params = 3;
}
```

---

### 2. The Go Handler (Connect Implementation)

You no longer manage `w.Header()`, `flusher.Flush()`, or manual JSON marshaling. The Connect handler purely focuses on bridging your Service channel to the gRPC stream.

```go
package chat

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	chatv1 "your-module/gen/chat/v1" // The generated code
	"your-module/internal/auth"
	"your-module/types"
    "google.golang.org/protobuf/types/known/structpb"
)

// ChatServer implements the generated ChatServiceHandler interface
type ChatServer struct {
	logger  *slog.Logger
	service LlmInteractionService
}

func (s *ChatServer) StreamChat(
	ctx context.Context,
	req *connect.Request[chatv1.StreamChatRequest],
	stream *connect.ServerStream[chatv1.StreamChatResponse],
) error {
	// 1. Auth & Validation (Same as before)
	userIDStr, ok := auth.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, _ := uuid.Parse(userIDStr)

	profileID, err := uuid.Parse(req.Msg.ProfileId)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid profile ID"))
	}

	// Map Proto UserLocation to Internal UserLocation
	var userLoc *types.UserLocation
	if req.Msg.UserLocation != nil {
		userLoc = &types.UserLocation{
			UserLat: req.Msg.UserLocation.Latitude,
			UserLon: req.Msg.UserLocation.Longitude,
		}
	}

	// 2. Create the channel (Same pattern)
	eventCh := make(chan types.StreamEvent, 100)

	// 3. Launch Service Logic (Exactly the same service call)
	go func() {
		// Note: We use ctx from the request. If client disconnects, ctx is canceled automatically.
		err := s.service.ProcessUnifiedChatMessageStream(
			ctx,
			userID,
			profileID,
			"",
			req.Msg.Message,
			userLoc,
			eventCh,
		)

		// Handle service startup errors that happen before the stream starts
		if err != nil {
			select {
			case eventCh <- types.StreamEvent{Type: types.EventTypeError, Error: err.Error()}:
			case <-ctx.Done():
			}
		}
	}()

	// 4. Stream Loop (Much cleaner than HTTP/SSE)
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				return nil // Stream finished successfully
			}

			// Convert internal types.StreamEvent to Proto Message
			resp, err := mapEventToProto(event)
			if err != nil {
				s.logger.Error("Failed to map event", "error", err)
				continue
			}

			// Send to client (Handles framing and flushing automatically)
			if err := stream.Send(resp); err != nil {
				// If send fails (e.g., client disconnected), return error to stop handler
				return err
			}

			// Stop if we see a completion or error type
			if event.Type == types.EventTypeComplete || event.Type == types.EventTypeError {
				return nil
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
```

### 3. The Mapper Helper
Since your Service yields `interface{}`/`any` in the `Data` field, we need a helper to safely put that into the strict Protobuf message.

```go
func mapEventToProto(event types.StreamEvent) (*chatv1.StreamChatResponse, error) {
	resp := &chatv1.StreamChatResponse{
		EventType: string(event.Type),
	}

	if event.Error != "" {
		resp.ErrorMessage = event.Error
	}

	if event.Navigation != nil {
		resp.Navigation = &chatv1.NavigationData{
			Url:         event.Navigation.URL,
			RouteType:   event.Navigation.RouteType,
			QueryParams: event.Navigation.QueryParams,
		}
	}

	// Handle the payload based on type
	switch event.Type {
	case types.EventTypeChunk:
		// Expecting Data to be map[string]interface{} with "part" and "chunk"
		if dataMap, ok := event.Data.(map[string]interface{}); ok {
			part, _ := dataMap["part"].(string)
			chunk, _ := dataMap["chunk"].(string)
			resp.Payload = &chatv1.StreamChatResponse_ChunkData{
				ChunkData: &chatv1.StreamChunk{
					PartType: part,
					Content:  chunk,
				},
			}
		}

	case types.EventTypeItinerary:
		// Itinerary is complex, serialize to JSON string for safety
		// or define a full Proto message for Itineraries if you want strict typing
		jsonBytes, _ := json.Marshal(event.Data)
		resp.Payload = &chatv1.StreamChatResponse_ItineraryJson{
			ItineraryJson: string(jsonBytes),
		}

	default:
		// For other events (Start, Progress), try to use Struct
		// Use structpb to convert map[string]interface{} to google.protobuf.Struct
		if event.Data != nil {
			if dataMap, ok := event.Data.(map[string]interface{}); ok {
				s, err := structpb.NewStruct(dataMap)
				if err == nil {
					resp.Payload = &chatv1.StreamChatResponse_GenericData{
						GenericData: s,
					}
				}
			}
		}
	}

	return resp, nil
}
```

### Key Differences & Improvements

1.  **Request Parsing:**
  *   **Old:** Manually parsing URL params (`chi.URLParam`) and decoding JSON Body.
  *   **New:** `req.Msg` contains `ProfileId`, `Message`, and `UserLocation` already typed and parsed.
2.  **Stream Management:**
  *   **Old:** Manually writing `data: ...\n\n` strings and calling `flusher.Flush()`.
  *   **New:** Calling `stream.Send(msg)`. Connect handles the HTTP/2 framing and buffer flushing.
3.  **Type Safety:**
  *   The frontend (if generated via Buf) will know that if `event_type` is "chunk", they should check `payload.chunk_data`. In your old code, the frontend had to blindly trust that `data` contained specific fields.
4.  **Service Layer Unchanged:**
  *   Notice that `ProcessUnifiedChatMessageStream` **did not change**. We simply wrapped the output channel in the Handler layer to convert the generic events into Protobuf messages.
___

Connect RPC streaming as an example:

Streaming
Connect supports several types of streaming RPCs. Streaming is exciting — it's fundamentally different from the web's typical request-response model, and in the right circumstances it can be very efficient. If you've been writing the same pagination or polling code for years, streaming may look like the answer to all your problems.

Temper your enthusiasm. Streaming also comes with many drawbacks:

It requires excellent HTTP libraries. At the very least, the client and server must be able to stream HTTP/1.1 request and response bodies. For bidirectional streaming, both parties must support HTTP/2. Long-lived streams are much more likely to encounter bugs and edge cases in HTTP/2 flow control.
It requires excellent proxies. Every proxy between the server and client — including those run by cloud providers — must support HTTP/2.
It weakens the protections offered to your unary handlers, since streaming typically requires proxies to be configured with much longer timeouts.
It requires complex tools. Streaming RPC protocols are much more involved than unary protocols, so cURL and your browser's network inspector are useless.
In general, streaming ties your application more closely to your networking infrastructure and makes your application inaccessible to less-sophisticated clients. You can minimize these downsides by keeping streams short-lived.

Also, if your http.Server has the ReadTimeout or WriteTimeout field configured, it applies to the entire operation duration, even for streaming calls. See the FAQ for more information.

All that said, connect-go fully supports all three types of streaming. All streaming subtypes work with the gRPC, gRPC-Web, and Connect protocols.

Streaming variants
In client streaming, the client sends multiple messages. Once the server receives all the messages, it responds with a single message. In Protobuf schemas, client streaming methods look like this:

service GreetService {
rpc Greet(stream GreetRequest) returns (GreetResponse) {}
}
In Go, client streaming RPCs use the ClientStream and ClientStreamForClient types.

In server streaming, the client sends a single message and the server responds with multiple messages. In Protobuf schemas, server streaming methods look like this:

service GreetService {
rpc Greet(GreetRequest) returns (stream GreetResponse) {}
}
In Go, server streaming RPCs use the ServerStream and ServerStreamForClient types.

In bidirectional streaming (often called bidi), the client and server may both send multiple messages. Often, the exchange is structured like a conversation: the client sends a message, the server responds, the client sends another message, and so on. Keep in mind that this always requires end-to-end HTTP/2 support (regardless of RPC protocol)! net/http clients and servers support HTTP/2 by default if you're using TLS, but they need some special configuration to support HTTP/2 without TLS. In Protobuf schemas, bidi streaming methods look like this:

service GreetService {
rpc Greet(stream GreetRequest) returns (stream GreetResponse) {}
}
In Go, bidi streaming RPCs use the BidiStream and BidiStreamForClient types.

HTTP representation
In all three protocols, streaming responses always have an HTTP status of 200 OK. This may seem unusual, but it's unavoidable: the server may encounter an error after sending a few messages, when the HTTP status has already been sent to the client. Rather than relying on the HTTP status, streaming handlers encode any errors in HTTP trailers or at the end of the response body (depending on the protocol).

The body of streaming requests and responses envelopes your schema-defined messages with a few bytes of protocol-specific binary framing data. Because of the interspersed framing data, the payloads are no longer valid Protobuf or JSON: instead, they use protocol-specific Content-Types like application/connect+proto, application/grpc+json, or application/grpc-web+proto.

Headers and trailers
As in unary RPC, headers are plain HTTP headers, with the same ASCII-only restrictions and binary header support.

Each protocol sends response trailers differently: they may be sent as HTTP trailers, a block of HTTP-formatted data at the end of the response body, or a blob of JSON at the end of the body. Regardless of the wire encoding, all three protocols give trailers the same semantics and restrictions as headers.

Headers and trailers are exposed on streaming RPCs in the same way as unary, via a CallInfo type in context.

Interceptors
Streaming interceptors are naturally more complex than unary interceptors. Rather than using UnaryInterceptorFunc, streaming interceptors must implement the full Interceptor interface. This may require implementing a StreamingClientConn or StreamingHandlerConn wrapper.

An example
Let's start by amending the GreetService we defined in Getting Started to make the Greet method use client streaming:

syntax = "proto3";

package greet.v1;

import "buf/validate/validate.proto";

message GreetRequest {
string name = 1 [(buf.validate.field).string = {
min_len: 1,
max_len: 50,
}];
}

message GreetResponse {
string greeting = 1;
}

service GreetService {
rpc Greet(stream GreetRequest) returns (GreetResponse) {}
}
After running buf generate to update our generated code, we can amend our handler implementation in cmd/server/main.go:

package main

import (
"context"
"errors"
"fmt"
"log"
"net/http"
"strings"

"connectrpc.com/connect"
"connectrpc.com/validate"

greetv1 "example/gen/greet/v1"
"example/gen/greet/v1/greetv1connect"
)

type GreetServer struct{}

func (s *GreetServer) Greet(
ctx context.Context,
stream *connect.ClientStream[greetv1.GreetRequest],
) (*greetv1.GreetResponse, error) {
callInfo, ok := connect.CallInfoForHandlerContext(ctx)
if !ok {
return nil, errors.New("can't access headers: no CallInfo for handler context")
}
log.Println("Request headers: ", callInfo.RequestHeader())
var greeting strings.Builder
for stream.Receive() {
g := fmt.Sprintf("Hello, %s!\n", stream.Msg().Name)
if _, err := greeting.WriteString(g); err != nil {
return nil, connect.NewError(connect.CodeInternal, err)
}
}
if err := stream.Err(); err != nil {
return nil, connect.NewError(connect.CodeUnknown, err)
}
callInfo.ResponseHeader().Set("Greet-Version", "v1")
res := &greetv1.GreetResponse{
Greeting: greeting.String(),
}
return res, nil
}

func main() {
greeter := &GreetServer{}
mux := http.NewServeMux()
path, handler := greetv1connect.NewGreetServiceHandler(
greeter,
// Validation via Protovalidate is almost always recommended
connect.WithInterceptors(validate.NewInterceptor()),
)
mux.Handle(path, handler)
p := new(http.Protocols)
p.SetHTTP1(true)
// Use h2c so we can serve HTTP/2 without TLS.
p.SetUnencryptedHTTP2(true)
s := http.Server{
Addr:      "localhost:8080",
Handler:   mux,
Protocols: p,
}
s.ListenAndServe()
}
Now that we've implemented our new client streaming RPC, we'll also need to update our simple authentication interceptor. To support streaming, we must implement the full Interceptor interface:

const tokenHeader = "Acme-Token"

var errNoToken = errors.New("no token provided")

type authInterceptor struct{}

func NewAuthInterceptor() *authInterceptor {
return &authInterceptor{}
}

func (i *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
// Same as previous UnaryInterceptorFunc.
return func(
ctx context.Context,
req connect.AnyRequest,
) (connect.AnyResponse, error) {
if req.Spec().IsClient {
// Send a token with client requests.
req.Header().Set(tokenHeader, "sample")
} else if req.Header().Get(tokenHeader) == "" {
// Check token in handlers.
return nil, connect.NewError(connect.CodeUnauthenticated, errNoToken)
}
return next(ctx, req)
}
}

func (*authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
return func(
ctx context.Context,
spec connect.Spec,
) connect.StreamingClientConn {
conn := next(ctx, spec)
conn.RequestHeader().Set(tokenHeader, "sample")
return conn
}
}

func (i *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
return func(
ctx context.Context,
conn connect.StreamingHandlerConn,
) error {
if conn.RequestHeader().Get(tokenHeader) == "" {
return connect.NewError(connect.CodeUnauthenticated, errNoToken)
}
return next(ctx, conn)
}
}
We apply our interceptor just as we did before, using WithInterceptors.

___

### Tests
Create tests for chat and profile handlers if there are none.\
___
# Save changes
Save the changes under docs/CONNECT_MIGRATION_CHAT.md so I can instruct the frontend later on how to connect to the new chat service using Connect-Go.
Analyse the readme, the scope, the routes and the framework decision guide and the proto gen and check of its worth to build two separate native mobile clients.
Would people pay for an app like this one?
