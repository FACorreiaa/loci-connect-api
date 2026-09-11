package poi

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// commonsEndpoint is the Wikimedia Commons API. Commons rather than a language
// Wikipedia: it is the media repository, its files carry structured licence and
// author metadata, and it is the same corpus every Wikipedia draws from.
const commonsEndpoint = "https://commons.wikimedia.org/w/api.php"

// commonsUserAgent identifies this client to Wikimedia.
//
// Required, not polite: Wikimedia blocks requests with a generic or absent
// User-Agent, and their policy asks for a contact address so they can reach an
// operator whose client misbehaves rather than silently banning it.
const commonsUserAgent = "LociBot/1.0 (https://lociai.fyi; contact via https://lociai.fyi) Go-http-client"

// commonsMinInterval paces requests to Commons.
//
// The backfill is the only caller and it is not in a hurry — it runs nightly
// against places nobody is waiting on. One request every 200ms is well inside
// what Wikimedia asks of an unauthenticated client, and being a good guest of a
// donated service costs nothing here.
const commonsMinInterval = 200 * time.Millisecond

// ImageFetcher finds pictures for places on Wikimedia Commons.
type ImageFetcher struct {
	httpClient *http.Client
	endpoint   string
	lastCall   time.Time
}

// NewImageFetcher builds a fetcher. A nil client gets one with a timeout: this
// runs in a batch job where a hung connection would stall the whole sweep.
func NewImageFetcher(client *http.Client) *ImageFetcher {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &ImageFetcher{httpClient: client, endpoint: commonsEndpoint}
}

// WithEndpoint points the fetcher at another base URL. The test seam, matching
// telegram.Client.WithBaseURL — an instance field rather than a package global,
// so tests cannot leak into each other.
func (f *ImageFetcher) WithEndpoint(endpoint string) *ImageFetcher {
	f.endpoint = endpoint
	return f
}

// commonsResponse is the slice of the API's reply this needs.
type commonsResponse struct {
	Query struct {
		Pages map[string]struct {
			Title      string             `json:"title"`
			ImageInfo  []commonsImageInfo `json:"imageinfo"`
			MissingKey *struct{}          `json:"missing,omitempty"`
		} `json:"pages"`
	} `json:"query"`
}

type commonsImageInfo struct {
	URL            string `json:"url"`
	DescriptionURL string `json:"descriptionurl"`
	ExtMetadata    struct {
		LicenseShortName struct {
			Value string `json:"value"`
		} `json:"LicenseShortName"`
		Artist struct {
			Value string `json:"value"`
		} `json:"Artist"`
		Credit struct {
			Value string `json:"value"`
		} `json:"Credit"`
	} `json:"extmetadata"`
}

// FetchForPOI returns pictures for one place, or nothing.
//
// Nothing is an ordinary outcome, not a failure: Commons covers landmarks,
// museums and monuments well and restaurants and bars barely at all. A place
// without a picture renders without one.
//
// limit caps how many images are returned for a single place.
func (f *ImageFetcher) FetchForPOI(ctx context.Context, poiID uuid.UUID, name, cityName string, limit int) ([]POIImage, error) {
	if limit <= 0 {
		limit = 1
	}
	query := strings.TrimSpace(name)
	if query == "" {
		return nil, nil
	}
	if city := strings.TrimSpace(cityName); city != "" {
		query += " " + city
	}

	f.pace()

	params := url.Values{
		"action":              {"query"},
		"format":              {"json"},
		"formatversion":       {"1"},
		"generator":           {"search"},
		"gsrsearch":           {"filetype:bitmap " + query},
		"gsrnamespace":        {"6"}, // File: namespace
		"gsrlimit":            {fmt.Sprint(limit)},
		"prop":                {"imageinfo"},
		"iiprop":              {"url|extmetadata"},
		"iiurlwidth":          {"1024"},
		"iiextmetadatafilter": {"LicenseShortName|Artist|Credit"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build commons request: %w", err)
	}
	req.Header.Set("User-Agent", commonsUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call commons: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("commons returned %d", resp.StatusCode)
	}

	// Bounded like every other external read in this codebase: a surprise
	// gigabyte should fail, not exhaust the batch job's memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read commons response: %w", err)
	}

	var parsed commonsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode commons response: %w", err)
	}

	var out []POIImage
	position := 0
	for _, page := range parsed.Query.Pages {
		if len(page.ImageInfo) == 0 {
			continue
		}
		info := page.ImageInfo[0]

		licence := strings.TrimSpace(info.ExtMetadata.LicenseShortName.Value)
		attribution := cleanHTML(info.ExtMetadata.Artist.Value)
		if attribution == "" {
			attribution = cleanHTML(info.ExtMetadata.Credit.Value)
		}

		// No credit, no image. The licence requires naming the author, so an
		// image we cannot attribute is one we are not allowed to display —
		// storing it would only move the breach downstream.
		if licence == "" || attribution == "" || info.URL == "" {
			continue
		}

		out = append(out, POIImage{
			POIID:         poiID,
			URL:           info.URL,
			Source:        "wikimedia",
			Licence:       licence,
			Attribution:   attribution,
			SourcePageURL: info.DescriptionURL,
			Position:      position,
		})
		position++
		if len(out) >= limit {
			break
		}
	}

	return out, nil
}

// pace holds the minimum gap between calls to Commons.
func (f *ImageFetcher) pace() {
	if !f.lastCall.IsZero() {
		if wait := commonsMinInterval - time.Since(f.lastCall); wait > 0 {
			time.Sleep(wait)
		}
	}
	f.lastCall = time.Now()
}

var htmlTagRE = regexp.MustCompile(`<[^>]*>`)

// cleanHTML turns Commons' markup-bearing metadata into a credit line.
//
// Artist and Credit arrive as HTML — usually a link to the author's user page.
// The text is what a credit line needs; the markup would be rendered literally
// by every surface that shows it.
func cleanHTML(value string) string {
	stripped := htmlTagRE.ReplaceAllString(value, " ")
	stripped = html.UnescapeString(stripped)
	return strings.Join(strings.Fields(stripped), " ")
}
