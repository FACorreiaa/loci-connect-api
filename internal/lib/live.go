package genaisdk

//import (
//	"context"
//	"fmt"
//
//	"google.golang.org/genai"
//)
//
//// LiveSession wraps the genai live session to simplify streaming use-cases like voice-to-LLM.
//type LiveSession struct {
//	session *genai.LiveSession
//}
//
//// StartLiveSession opens a live connection to the specified model for bidirectional streaming (text/audio/video).
//func (ai *LLMChatClient) StartLiveSession(ctx context.Context, model string, cfg *genai.LiveConnectConfig) (*LiveSession, error) {
//	if ai.client == nil {
//		return nil, fmt.Errorf("client not initialized")
//	}
//	if model == "" {
//		model = ai.model
//	}
//	sess, err := ai.client.Live.Connect(ctx, model, cfg)
//	if err != nil {
//		return nil, err
//	}
//	return &LiveSession{session: sess}, nil
//}
//
//// SendRealtimeInput forwards a realtime input payload (including audio chunks) to the live session.
//func (s *LiveSession) SendRealtimeInput(input genai.LiveRealtimeInput) error {
//	if s == nil || s.session == nil {
//		return fmt.Errorf("session not initialized")
//	}
//	return s.session.SendRealtimeInput(input)
//}
//
//// Receive blocks until the next response is returned from the live model.
//func (s *LiveSession) Receive() (*genai.LiveResponse, error) {
//	if s == nil || s.session == nil {
//		return nil, fmt.Errorf("session not initialized")
//	}
//	return s.session.Receive()
//}
//
//// Close tears down the live session.
//func (s *LiveSession) Close() error {
//	if s == nil || s.session == nil {
//		return nil
//	}
//	return s.session.Close()
//}
