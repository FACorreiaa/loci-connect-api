package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// GetJSON fetches a URL and decodes the body into T.
//
// A free function rather than a method because Go does not allow type
// parameters on methods.
func GetJSON[T any](ctx context.Context, c *Client, source, url string) (T, error) {
	body, err := c.Get(ctx, source, url)
	if err != nil {
		var out T
		return out, err
	}
	return decodeJSON[T](source, body)
}

// GetJSONWithHeader is GetJSON with extra request headers.
func GetJSONWithHeader[T any](ctx context.Context, c *Client, source, url string, header http.Header) (T, error) {
	body, err := c.GetWithHeader(ctx, source, url, header)
	if err != nil {
		var out T
		return out, err
	}
	return decodeJSON[T](source, body)
}

// PostJSON sends in as a JSON body and decodes the response into T. The same
// idempotency rule as Client.Post applies.
func PostJSON[T any](ctx context.Context, c *Client, source, url string, in any, header http.Header) (T, error) {
	var out T
	payload, err := json.Marshal(in)
	if err != nil {
		return out, fmt.Errorf("%s encode: %w", source, err)
	}
	body, err := c.Post(ctx, source, url, "application/json", payload, header)
	if err != nil {
		return out, err
	}
	return decodeJSON[T](source, body)
}

func decodeJSON[T any](source string, body []byte) (T, error) {
	var out T
	// A 204, or any empty 2xx body, means "nothing to report". Decoding it
	// would fail on unexpected end of JSON input and turn a valid empty answer
	// into a provider failure.
	if len(bytes.TrimSpace(body)) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(body, &out); err != nil {
		// Providers answering an error as an HTML page is common enough that the
		// decode failure, not the status, is what the caller sees. Include a
		// snippet so the log says which provider started returning HTML.
		snippet := body
		if len(snippet) > maxErrorBodyBytes {
			snippet = snippet[:maxErrorBodyBytes]
		}
		return out, fmt.Errorf("%s decode: %w (body: %s)", source, err, snippet)
	}
	return out, nil
}
