package genaisdk

//import (
//	"context"
//	"fmt"
//	"os"
//
//	"google.golang.org/genai"
//)
//
//// FileClient exposes a small surface for file management on the LLM backend.
//type FileClient interface {
//	UploadFromPath(ctx context.Context, path string, cfg *genai.UploadFileConfig) (*genai.File, error)
//	List(ctx context.Context) ([]*genai.File, error)
//	Download(ctx context.Context, file *genai.File, cfg *genai.DownloadFileConfig) ([]byte, error)
//}
//
//// UploadFromPath uploads a local file to the LLM provider.
//func (ai *LLMChatClient) UploadFromPath(ctx context.Context, path string, cfg *genai.UploadFileConfig) (*genai.File, error) {
//	if ai.client == nil {
//		return nil, fmt.Errorf("client not initialized")
//	}
//	if _, err := os.Stat(path); err != nil {
//		return nil, fmt.Errorf("invalid file path %q: %w", path, err)
//	}
//	return ai.client.Files.UploadFromPath(ctx, path, cfg)
//}
//
//// List returns all files currently known to the provider.
//func (ai *LLMChatClient) List(ctx context.Context) ([]*genai.File, error) {
//	if ai.client == nil {
//		return nil, fmt.Errorf("client not initialized")
//	}
//
//	iter := ai.client.Files.All(ctx)
//	var files []*genai.File
//	for file, err := range iter {
//		if err != nil {
//			return nil, err
//		}
//		files = append(files, file)
//	}
//	return files, nil
//}
//
//// Download fetches file bytes for a previously uploaded file.
//func (ai *LLMChatClient) Download(ctx context.Context, file *genai.File, cfg *genai.DownloadFileConfig) ([]byte, error) {
//	if ai.client == nil {
//		return nil, fmt.Errorf("client not initialized")
//	}
//	if file == nil {
//		return nil, fmt.Errorf("file is nil")
//	}
//	return ai.client.Files.Download(ctx, file, cfg)
//}
//
//var _ FileClient = (*LLMChatClient)(nil)
