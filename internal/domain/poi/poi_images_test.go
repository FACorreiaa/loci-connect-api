package poi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// commonsReply builds the shape the Commons API actually returns, so the test
// exercises the decoder rather than a convenient invention.
func commonsReply(t *testing.T, licence, artist, credit string) string {
	t.Helper()
	payload := map[string]any{
		"query": map[string]any{
			"pages": map[string]any{
				"12345": map[string]any{
					"title": "File:Cabo Girao.jpg",
					"imageinfo": []any{map[string]any{
						"url":            "https://upload.wikimedia.org/cabo-girao.jpg",
						"descriptionurl": "https://commons.wikimedia.org/wiki/File:Cabo_Girao.jpg",
						"extmetadata": map[string]any{
							"LicenseShortName": map[string]any{"value": licence},
							"Artist":           map[string]any{"value": artist},
							"Credit":           map[string]any{"value": credit},
						},
					}},
				},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}
	return string(raw)
}

func fetcherAgainst(body string, status int) (*ImageFetcher, *httptest.Server, *[]*http.Request) {
	var seen []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return NewImageFetcher(server.Client()).WithEndpoint(server.URL), server, &seen
}

func TestFetchForPOIReturnsTheImageWithItsCredit(t *testing.T) {
	body := commonsReply(t, "CC BY-SA 4.0", `<a href="/wiki/User:Someone">Someone</a>`, "own work")
	fetcher, server, _ := fetcherAgainst(body, http.StatusOK)
	defer server.Close()

	id := uuid.New()
	got, err := fetcher.FetchForPOI(context.Background(), id, "Cabo Girão", "Madeira", 1)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one image, got %d", len(got))
	}

	img := got[0]
	if img.POIID != id {
		t.Errorf("image is attached to the wrong place: %v", img.POIID)
	}
	if img.URL != "https://upload.wikimedia.org/cabo-girao.jpg" {
		t.Errorf("url: %q", img.URL)
	}
	if img.Licence != "CC BY-SA 4.0" {
		t.Errorf("licence: %q", img.Licence)
	}
	// The markup must not survive into a credit line a person reads.
	if img.Attribution != "Someone" {
		t.Errorf("attribution should be the text, not the markup: %q", img.Attribution)
	}
	if img.Source != "wikimedia" {
		t.Errorf("source: %q", img.Source)
	}
}

// The licence requires naming the author. An image we cannot credit must not be
// stored, because storing it only moves the breach to whatever displays it.
func TestFetchForPOISkipsAnImageItCannotCredit(t *testing.T) {
	for _, tc := range []struct {
		name            string
		licence, artist string
		credit          string
	}{
		{name: "no licence", licence: "", artist: "Someone", credit: "own work"},
		{name: "no author at all", licence: "CC BY-SA 4.0", artist: "", credit: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetcher, server, _ := fetcherAgainst(commonsReply(t, tc.licence, tc.artist, tc.credit), http.StatusOK)
			defer server.Close()

			got, err := fetcher.FetchForPOI(context.Background(), uuid.New(), "Somewhere", "Madeira", 1)
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("an uncreditable image was kept: %+v", got)
			}
		})
	}
}

// Credit is the fallback when Artist is absent — Commons populates one or the
// other depending on how the file was uploaded.
func TestFetchForPOIFallsBackToCredit(t *testing.T) {
	fetcher, server, _ := fetcherAgainst(commonsReply(t, "CC BY 2.0", "", "Photo by Someone Else"), http.StatusOK)
	defer server.Close()

	got, err := fetcher.FetchForPOI(context.Background(), uuid.New(), "Somewhere", "", 1)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 1 || got[0].Attribution != "Photo by Someone Else" {
		t.Errorf("credit was not used as the attribution: %+v", got)
	}
}

// Wikimedia blocks clients that do not identify themselves, so the header is a
// requirement rather than a courtesy.
func TestFetchForPOIIdentifiesItselfToWikimedia(t *testing.T) {
	fetcher, server, seen := fetcherAgainst(commonsReply(t, "CC BY-SA 4.0", "Someone", ""), http.StatusOK)
	defer server.Close()

	if _, err := fetcher.FetchForPOI(context.Background(), uuid.New(), "Cabo Girão", "Madeira", 1); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("expected one request, got %d", len(*seen))
	}
	req := (*seen)[0]
	if ua := req.Header.Get("User-Agent"); ua != commonsUserAgent {
		t.Errorf("User-Agent: %q", ua)
	}
	if q := req.URL.Query().Get("gsrsearch"); q != "filetype:bitmap Cabo Girão Madeira" {
		t.Errorf("the city should narrow the search: %q", q)
	}
}

// A place with no name cannot be searched for, and must not produce a request.
func TestFetchForPOIWithoutANameAsksNothing(t *testing.T) {
	fetcher, server, seen := fetcherAgainst("{}", http.StatusOK)
	defer server.Close()

	got, err := fetcher.FetchForPOI(context.Background(), uuid.New(), "   ", "Madeira", 1)
	if err != nil || got != nil {
		t.Errorf("expected nothing: %+v, %v", got, err)
	}
	if len(*seen) != 0 {
		t.Errorf("a nameless place should not reach Commons")
	}
}

func TestFetchForPOISurfacesAnUnhappyCommons(t *testing.T) {
	fetcher, server, _ := fetcherAgainst("upstream is unwell", http.StatusServiceUnavailable)
	defer server.Close()

	if _, err := fetcher.FetchForPOI(context.Background(), uuid.New(), "Cabo Girão", "", 1); err == nil {
		t.Error("a 503 should be reported, not silently treated as no images")
	}
}
