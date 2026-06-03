package share

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	sharev1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/share"
)

// OGMetaHandler returns an HTTP handler that serves OG meta tags for social sharing
func (h *Handler) OGMetaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract share code from URL path: /share/{code}
		path := strings.TrimPrefix(r.URL.Path, "/share/")
		code := strings.Split(path, "/")[0]

		if code == "" {
			http.Error(w, "Share code required", http.StatusBadRequest)
			return
		}

		// Resolve + bump the view count from the persistent store.
		entry, err := h.repo.IncrementView(r.Context(), code)
		if err != nil {
			http.Error(w, "Share not found", http.StatusNotFound)
			return
		}

		// Generate HTML with OG meta tags
		ogHTML := generateOGHTML(entry, h.baseURL, code)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(ogHTML))
	})
}

// generateOGHTML creates HTML with Open Graph meta tags for social sharing
func generateOGHTML(entry *Share, baseURL, code string) string {
	contentType := getContentTypeName(sharev1.ShareContentType(entry.ContentType))
	canonicalURL := fmt.Sprintf("%s/share/%s", baseURL, code)

	// Use a default image if none provided
	imageURL := entry.ImageURL
	if imageURL == "" {
		imageURL = fmt.Sprintf("%s/og-default.png", baseURL)
	}

	htmlTemplate := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    
    <!-- Primary Meta Tags -->
    <title>{{.Title}} | Loci</title>
    <meta name="title" content="{{.Title}} | Loci">
    <meta name="description" content="{{.Description}}">
    
    <!-- Open Graph / Facebook -->
    <meta property="og:type" content="website">
    <meta property="og:url" content="{{.CanonicalURL}}">
    <meta property="og:title" content="{{.Title}}">
    <meta property="og:description" content="{{.Description}}">
    <meta property="og:image" content="{{.ImageURL}}">
    <meta property="og:site_name" content="Loci">
    
    <!-- Twitter -->
    <meta property="twitter:card" content="summary_large_image">
    <meta property="twitter:url" content="{{.CanonicalURL}}">
    <meta property="twitter:title" content="{{.Title}}">
    <meta property="twitter:description" content="{{.Description}}">
    <meta property="twitter:image" content="{{.ImageURL}}">
    
    <!-- Additional Meta -->
    <meta name="robots" content="index, follow">
    <link rel="canonical" href="{{.CanonicalURL}}">
    
    <!-- Redirect to app after a short delay for crawlers to read meta -->
    <meta http-equiv="refresh" content="0;url={{.AppURL}}">
    
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            margin: 0;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
        }
        .container {
            text-align: center;
            padding: 2rem;
        }
        h1 { margin-bottom: 0.5rem; }
        p { opacity: 0.9; }
        .type { 
            display: inline-block;
            padding: 0.25rem 0.75rem;
            background: rgba(255,255,255,0.2);
            border-radius: 1rem;
            font-size: 0.875rem;
            margin-bottom: 1rem;
        }
        .loader {
            margin-top: 2rem;
            font-size: 0.875rem;
            opacity: 0.8;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="type">{{.ContentType}}</div>
        <h1>{{.Title}}</h1>
        <p>{{.Description}}</p>
        <div class="loader">Opening in Loci...</div>
    </div>
</body>
</html>`

	tmpl, err := template.New("og").Parse(htmlTemplate)
	if err != nil {
		return "<html><body>Error generating page</body></html>"
	}

	data := struct {
		Title        string
		Description  string
		ImageURL     string
		CanonicalURL string
		AppURL       string
		ContentType  string
	}{
		Title:        entry.Title,
		Description:  entry.Description,
		ImageURL:     imageURL,
		CanonicalURL: canonicalURL,
		AppURL:       fmt.Sprintf("%s/%s/%s", baseURL, strings.ToLower(contentType), entry.ContentID),
		ContentType:  contentType,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "<html><body>Error generating page</body></html>"
	}

	return buf.String()
}

// getContentTypeName returns a human-readable name for the content type
func getContentTypeName(ct sharev1.ShareContentType) string {
	switch ct {
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_POI:
		return "Place"
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_HOTEL:
		return "Hotel"
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_RESTAURANT:
		return "Restaurant"
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_ITINERARY:
		return "Itinerary"
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_LIST:
		return "List"
	case sharev1.ShareContentType_SHARE_CONTENT_TYPE_ACTIVITY:
		return "Activity"
	default:
		return "Content"
	}
}
