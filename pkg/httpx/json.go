package httpx

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetJSON fetches a URL and decodes the body into T.
//
// A free function rather than a method because Go does not allow type
// parameters on methods.
func GetJSON[T any](ctx context.Context, c *Client, source, url string) (T, error) {
	var out T
	body, err := c.Get(ctx, source, url)
	if err != nil {
		return out, err
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
