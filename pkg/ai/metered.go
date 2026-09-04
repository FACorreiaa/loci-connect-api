package ai

import (
	"context"
	"iter"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// Method labels for recorded calls. Bounded set, safe as a metric label.
const (
	methodGenerate       = "generate"
	methodGenerateText   = "generate_text"
	methodGenerateStream = "generate_stream"
)

// tokenUsage is the provider-reported cost of a single model call.
type tokenUsage struct {
	Prompt     int32
	Completion int32
	Thoughts   int32
	Cached     int32
	Total      int32
}

// tokenRecorder receives the usage of one completed model call. Injected so the
// decorator can be tested without reading Prometheus state.
type tokenRecorder func(model, method string, u tokenUsage)

// metered wraps a ChatClient and reports what each call cost.
//
// Token accounting has to live here rather than at the RPC boundary: one
// metered chat request fans out into several model calls across parallel
// workers plus a background pass after the answer is delivered, so counting
// requests says nothing about the bill. Dividing tokens by
// loci_quota_consumed_total gives the real cost of one user action.
type metered struct {
	inner  generativeAI.ChatClient
	record tokenRecorder
}

func newMetered(inner generativeAI.ChatClient, record tokenRecorder) generativeAI.ChatClient {
	if inner == nil || record == nil {
		return inner
	}
	return &metered{inner: inner, record: record}
}

// recordPrometheus is the production recorder.
func recordPrometheus(model, method string, u tokenUsage) {
	observability.LLMCallsTotal.WithLabelValues(model, method).Inc()
	observability.LLMTokensTotal.WithLabelValues(model, "prompt").Add(float64(u.Prompt))
	observability.LLMTokensTotal.WithLabelValues(model, "completion").Add(float64(u.Completion))
	if u.Thoughts > 0 {
		observability.LLMTokensTotal.WithLabelValues(model, "thoughts").Add(float64(u.Thoughts))
	}
	if u.Cached > 0 {
		observability.LLMTokensTotal.WithLabelValues(model, "cached").Add(float64(u.Cached))
	}
}

func usageOf(resp *genai.GenerateContentResponse) (tokenUsage, bool) {
	if resp == nil || resp.UsageMetadata == nil {
		return tokenUsage{}, false
	}
	m := resp.UsageMetadata
	return tokenUsage{
		Prompt:     m.PromptTokenCount,
		Completion: m.CandidatesTokenCount,
		Thoughts:   m.ThoughtsTokenCount,
		Cached:     m.CachedContentTokenCount,
		Total:      m.TotalTokenCount,
	}, true
}

func (m *metered) Generate(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	resp, err := m.inner.Generate(ctx, prompt, config)
	if err != nil {
		return resp, err
	}
	if u, ok := usageOf(resp); ok {
		m.record(m.inner.Model(), methodGenerate, u)
	}
	return resp, nil
}

func (m *metered) GenerateText(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (string, error) {
	// The SDK discards the response envelope here, so there is no usage to
	// read. Count the call so the model still shows up in the call rate.
	text, err := m.inner.GenerateText(ctx, prompt, config)
	if err != nil {
		return text, err
	}
	m.record(m.inner.Model(), methodGenerateText, tokenUsage{})
	return text, nil
}

// GenerateStream records the last usage the provider reported, once, when the
// caller finishes with the sequence.
//
// Providers report usage cumulatively on each chunk, so summing chunks would
// multiply the reported bill by the chunk count. Recording on iteration end
// rather than on the final chunk means a caller who abandons the stream still
// gets charged for what was produced before they left.
func (m *metered) GenerateStream(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	seq, err := m.inner.GenerateStream(ctx, prompt, config)
	if err != nil {
		return seq, err
	}
	model := m.inner.Model()
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		var (
			last tokenUsage
			seen bool
		)
		defer func() {
			if seen {
				m.record(model, methodGenerateStream, last)
			}
		}()
		for resp, rerr := range seq {
			if u, ok := usageOf(resp); ok {
				last, seen = u, true
			}
			if !yield(resp, rerr) {
				return
			}
		}
	}, nil
}

func (m *metered) Model() string { return m.inner.Model() }
func (m *metered) Close() error  { return m.inner.Close() }

func (m *metered) StartChatSession(ctx context.Context, config *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	return m.inner.StartChatSession(ctx, config)
}
