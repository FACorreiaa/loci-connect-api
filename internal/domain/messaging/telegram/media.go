package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// ErrTooLarge reports that a file is bigger than the caller allowed.
//
// Named because the answer to it is a reply asking for something shorter, not
// a retry: the file will be the same size next time.
var ErrTooLarge = errors.New("telegram: the file is larger than allowed")

// File is what getFile answers: where a file's bytes can be fetched from.
type File struct {
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}

// GetFile resolves a file id to a download path.
//
// Telegram refuses this for anything over 20 MB, which is one reason a
// recording's declared size is checked against the update before this is
// called: the alternative is finding out after a round trip.
func (c *Client) GetFile(ctx context.Context, fileID string) (File, error) {
	if strings.TrimSpace(fileID) == "" {
		return File{}, errors.New("telegram: no file id to resolve")
	}
	var file File
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &file); err != nil {
		return File{}, err
	}
	if file.FilePath == "" {
		return File{}, errors.New("telegram: getFile answered without a path")
	}
	return file, nil
}

// Download fetches a file's bytes, up to limit.
//
// Not routed through call: the bytes live on a different path — /file/bot<token>
// rather than /bot<token> — and the response is the file itself rather than a
// Bot API envelope. The same discipline applies, though. The token is in the
// path, so no error from here wraps one that would print the URL.
func (c *Client) Download(ctx context.Context, filePath string, limit int64) ([]byte, error) {
	if c.token == "" {
		return nil, errors.New("telegram: no bot token configured")
	}
	if limit <= 0 {
		return nil, errors.New("telegram: no download limit set")
	}
	if err := checkFilePath(filePath); err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/file/bot%s/%s", c.baseURL, c.token, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("telegram: could not build a download request")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Deliberately not wrapped: see the comment on call.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("telegram: the file could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The status alone, never the body: a failed file request answers with
		// a Bot API error whose description can echo the request back.
		return nil, fmt.Errorf("telegram: the file could not be downloaded (status %d)", resp.StatusCode)
	}

	// One byte past the limit is read so "too large" can be told apart from
	// "exactly at the limit"; a silently truncated recording would transcribe
	// to a sentence that stops mid-word.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("telegram: could not read the file")
	}
	if int64(len(raw)) > limit {
		return nil, ErrTooLarge
	}
	if len(raw) == 0 {
		return nil, errors.New("telegram: the file was empty")
	}
	return raw, nil
}

// checkFilePath refuses a path that would escape the file endpoint.
//
// It is a string from somebody else's server going straight into a URL path.
// Telegram has never abused it, which is not a reason to let it.
func checkFilePath(filePath string) error {
	if filePath == "" {
		return errors.New("telegram: no file path to download")
	}
	if strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "://") {
		return fmt.Errorf("telegram: refusing a file path that is not relative")
	}
	for _, segment := range strings.Split(path.Clean(filePath), "/") {
		if segment == ".." {
			return fmt.Errorf("telegram: refusing a file path that climbs out of the file endpoint")
		}
	}
	if u, err := url.Parse(filePath); err != nil || u.IsAbs() {
		return fmt.Errorf("telegram: refusing a file path that is not a plain path")
	}
	return nil
}

// SendVoice delivers a voice note.
//
// multipart/form-data rather than call's JSON, because the Bot API takes an
// upload as a file part. Telegram plays this inline as a recording only if it
// is Opus in an Ogg container; anything else arrives as a file attachment,
// which is a worse reply than no reply.
func (c *Client) SendVoice(ctx context.Context, chatID string, ogg []byte, seconds int, caption string) error {
	if len(ogg) == 0 {
		return errors.New("telegram: nothing to send as a voice note")
	}

	fields := map[string]string{"chat_id": chatID}
	if seconds > 0 {
		fields["duration"] = strconv.Itoa(seconds)
	}
	if caption != "" {
		// Telegram's caption limit is well under a message's; a caption that
		// is too long fails the whole send, and losing the voice note over
		// decoration would be the wrong trade.
		fields["caption"] = truncate(caption, maxCaptionChars)
	}

	return c.postMultipart(ctx, "sendVoice", fields, "voice", "reply.ogg", ogg)
}

// maxCaptionChars is Telegram's limit on a caption.
const maxCaptionChars = 1024

// postMultipart performs one Bot API method with a file attached.
//
// It mirrors call's three rules exactly, because the response is the same
// whichever way the request went: the transport error is never wrapped, the
// body is read bounded, and the description is sanitised.
func (c *Client) postMultipart(ctx context.Context, method string, fields map[string]string, fileField, fileName string, content []byte) error {
	if c.token == "" {
		return errors.New("telegram: no bot token configured")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return fmt.Errorf("telegram: could not encode a %s request", method)
		}
	}
	part, err := writer.CreateFormFile(fileField, fileName)
	if err != nil {
		return fmt.Errorf("telegram: could not encode a %s request", method)
	}
	if _, err := part.Write(content); err != nil {
		return fmt.Errorf("telegram: could not encode a %s request", method)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("telegram: could not encode a %s request", method)
	}

	endpoint := fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return fmt.Errorf("telegram: could not build a %s request", method)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		// Deliberately not wrapped: see the comment on call.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("telegram: %s could not be reached", method)
	}
	defer func() { _ = resp.Body.Close() }()

	return c.decodeEnvelope(resp, method, nil)
}

// truncate shortens text to at most limit characters, on a rune boundary.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
