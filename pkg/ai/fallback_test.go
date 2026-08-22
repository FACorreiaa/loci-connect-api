package ai

import (
	"context"
	"errors"
	"io"
	"iter"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// fakeClient is a scripted ChatClient. err, when non-nil, is returned by
// every call; otherwise the call succeeds and reports text.
type fakeClient struct {
	model string
	err   error
	text  string
	calls int
	// streamChunks are yielded before streamErr (if any) is emitted, so a
	// test can distinguish a stream that fails before the first chunk
	// from one that fails after content was already delivered.
	streamChunks []string
	streamErr    error
	closed       bool
}

var _ generativeAI.ChatClient = (*fakeClient)(nil)

func (f *fakeClient) Generate(context.Context, string, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &genai.GenerateContentResponse{}, nil
}

func (f *fakeClient) GenerateText(context.Context, string, *genai.GenerateContentConfig) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.text, nil
}

func (f *fakeClient) GenerateStream(context.Context, string, *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		for _, chunk := range f.streamChunks {
			resp := &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{Parts: []*genai.Part{{Text: chunk}}},
				}},
			}
			if !yield(resp, nil) {
				return
			}
		}
		if f.streamErr != nil {
			yield(nil, f.streamErr)
		}
	}, nil
}

func (f *fakeClient) StartChatSession(context.Context, *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &generativeAI.ChatSession{}, nil
}

func (f *fakeClient) Model() string { return f.model }

func (f *fakeClient) Close() error {
	f.closed = true
	return nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestChain(cooldown time.Duration, clients ...*fakeClient) *chainClient {
	entries := make([]*entry, 0, len(clients))
	for _, c := range clients {
		entries = append(entries, &entry{client: c, model: c.model})
	}
	return newChainClient(entries, cooldown, quietLogger())
}

func collect(t *testing.T, seq iter.Seq2[*genai.GenerateContentResponse, error]) (string, error) {
	t.Helper()
	var out string
	for resp, err := range seq {
		if err != nil {
			return out, err
		}
		for _, cand := range resp.Candidates {
			for _, part := range cand.Content.Parts {
				out += part.Text
			}
		}
	}
	return out, nil
}

func TestChainFailsOverOnProviderErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"out of credits", llmerrors.Classify(genai.APIError{Code: 402, Message: "requires more credits"})},
		{"auth failed", llmerrors.Classify(genai.APIError{Code: 401, Message: "no auth credentials"})},
		{"rate limited", llmerrors.Classify(genai.APIError{Code: 429})},
		{"unavailable", llmerrors.Classify(genai.APIError{Code: 503})},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			primary := &fakeClient{model: "paid/model", err: tt.err}
			backup := &fakeClient{model: "free/model", text: "from fallback"}
			chain := newTestChain(time.Minute, primary, backup)

			got, err := chain.GenerateText(context.Background(), "hi", nil)
			if err != nil {
				t.Fatalf("GenerateText: unexpected error: %v", err)
			}
			if got != "from fallback" {
				t.Fatalf("GenerateText = %q, want %q", got, "from fallback")
			}
			if primary.calls != 1 {
				t.Fatalf("primary called %d times, want 1", primary.calls)
			}
			if backup.calls != 1 {
				t.Fatalf("backup called %d times, want 1", backup.calls)
			}
			if chain.Model() != "free/model" {
				t.Fatalf("Model() = %q, want the model that answered", chain.Model())
			}
		})
	}
}

func TestChainDoesNotFailOverOnContextCancellation(t *testing.T) {
	// The caller is already gone. Burning a second provider produces a
	// response nobody reads and, on a shared free key, spends quota.
	for _, cancelErr := range []error{context.Canceled, context.DeadlineExceeded} {
		primary := &fakeClient{model: "paid/model", err: cancelErr}
		backup := &fakeClient{model: "free/model", text: "should not be reached"}
		chain := newTestChain(time.Minute, primary, backup)

		_, err := chain.GenerateText(context.Background(), "hi", nil)
		if !errors.Is(err, cancelErr) {
			t.Fatalf("GenerateText err = %v, want %v", err, cancelErr)
		}
		if backup.calls != 0 {
			t.Fatalf("backup called %d times on %v, want 0", backup.calls, cancelErr)
		}
	}
}

func TestChainDoesNotFailOverOnBadRequest(t *testing.T) {
	// A malformed prompt fails identically everywhere; replaying it down
	// the chain just multiplies latency.
	badReq := llmerrors.Classify(genai.APIError{Code: 400, Message: "bad request"})
	primary := &fakeClient{model: "paid/model", err: badReq}
	backup := &fakeClient{model: "free/model", text: "unreached"}
	chain := newTestChain(time.Minute, primary, backup)

	if _, err := chain.GenerateText(context.Background(), "hi", nil); err == nil {
		t.Fatal("GenerateText: want error, got nil")
	}
	if backup.calls != 0 {
		t.Fatalf("backup called %d times, want 0", backup.calls)
	}
}

func TestChainCooldownSuppressesDeadCredential(t *testing.T) {
	primary := &fakeClient{
		model: "paid/model",
		err:   llmerrors.Classify(genai.APIError{Code: 402}),
	}
	backup := &fakeClient{model: "free/model", text: "ok"}
	chain := newTestChain(time.Hour, primary, backup)

	for i := range 3 {
		if _, err := chain.GenerateText(context.Background(), "hi", nil); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}
	if primary.calls != 1 {
		t.Fatalf("primary called %d times, want 1 (cooldown should suppress the rest)", primary.calls)
	}
	if backup.calls != 3 {
		t.Fatalf("backup called %d times, want 3", backup.calls)
	}
}

func TestChainCooldownExpires(t *testing.T) {
	primary := &fakeClient{
		model: "paid/model",
		err:   llmerrors.Classify(genai.APIError{Code: 402}),
	}
	backup := &fakeClient{model: "free/model", text: "ok"}
	chain := newTestChain(time.Millisecond, primary, backup)

	if _, err := chain.GenerateText(context.Background(), "hi", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := chain.GenerateText(context.Background(), "hi", nil); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if primary.calls != 2 {
		t.Fatalf("primary called %d times, want 2 after cooldown expiry", primary.calls)
	}
}

func TestChainAllProvidersFailed(t *testing.T) {
	primary := &fakeClient{model: "paid/model", err: llmerrors.Classify(genai.APIError{Code: 402})}
	backup := &fakeClient{model: "free/model", err: llmerrors.Classify(genai.APIError{Code: 429})}
	chain := newTestChain(time.Minute, primary, backup)

	_, err := chain.GenerateText(context.Background(), "hi", nil)
	if err == nil {
		t.Fatal("want error when every provider fails")
	}
	if !errors.Is(err, errAllProvidersFailed) {
		t.Fatalf("err = %v, want errAllProvidersFailed", err)
	}
	// Both underlying causes must stay inspectable for triage.
	if !errors.Is(err, llmerrors.ErrOutOfCredits) || !errors.Is(err, llmerrors.ErrRateLimited) {
		t.Fatalf("err = %v, want both provider causes preserved", err)
	}
}

func TestChainStreamFailsOverBeforeFirstChunk(t *testing.T) {
	primary := &fakeClient{model: "paid/model", err: llmerrors.Classify(genai.APIError{Code: 402})}
	backup := &fakeClient{model: "free/model", streamChunks: []string{"hello ", "world"}}
	chain := newTestChain(time.Minute, primary, backup)

	seq, err := chain.GenerateStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	got, err := collect(t, seq)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("stream = %q, want %q", got, "hello world")
	}
}

func TestChainStreamFailsOverWhenFirstChunkErrors(t *testing.T) {
	// The provider accepted the request but rejected it lazily, before
	// emitting content. Nothing has reached the consumer, so switching
	// is still safe.
	primary := &fakeClient{
		model:     "paid/model",
		streamErr: llmerrors.Classify(genai.APIError{Code: 402}),
	}
	backup := &fakeClient{model: "free/model", streamChunks: []string{"recovered"}}
	chain := newTestChain(time.Minute, primary, backup)

	seq, err := chain.GenerateStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	got, err := collect(t, seq)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got != "recovered" {
		t.Fatalf("stream = %q, want %q", got, "recovered")
	}
}

func TestChainStreamSurfacesMidStreamError(t *testing.T) {
	// Content already reached the consumer. A second provider cannot
	// resume a half-written answer, so the error must surface rather
	// than the chain silently restarting with a different model.
	primary := &fakeClient{
		model:        "paid/model",
		streamChunks: []string{"partial "},
		streamErr:    llmerrors.Classify(genai.APIError{Code: 503}),
	}
	backup := &fakeClient{model: "free/model", streamChunks: []string{"unreached"}}
	chain := newTestChain(time.Minute, primary, backup)

	seq, err := chain.GenerateStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	got, err := collect(t, seq)
	if err == nil {
		t.Fatal("want mid-stream error to surface, got nil")
	}
	if got != "partial " {
		t.Fatalf("stream = %q, want the partial content already emitted", got)
	}
	if backup.calls != 0 {
		t.Fatalf("backup called %d times mid-stream, want 0", backup.calls)
	}
}

func TestChainCloseClosesEveryProvider(t *testing.T) {
	primary := &fakeClient{model: "paid/model"}
	backup := &fakeClient{model: "free/model"}
	chain := newTestChain(time.Minute, primary, backup)

	if err := chain.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !primary.closed || !backup.closed {
		t.Fatalf("Close did not reach every provider: primary=%v backup=%v", primary.closed, backup.closed)
	}
}

func TestReasonForIsBounded(t *testing.T) {
	// Provider error text must never reach a Prometheus label.
	cases := map[error]string{
		llmerrors.Classify(genai.APIError{Code: 402}): "out_of_credits",
		llmerrors.Classify(genai.APIError{Code: 401}): "auth_failed",
		llmerrors.Classify(genai.APIError{Code: 429}): "rate_limited",
		llmerrors.Classify(genai.APIError{Code: 500}): "unavailable",
		errors.New("something odd"):                   "unknown",
	}
	for err, want := range cases {
		if got := reasonFor(err); got != want {
			t.Fatalf("reasonFor(%v) = %q, want %q", err, got, want)
		}
	}
}
