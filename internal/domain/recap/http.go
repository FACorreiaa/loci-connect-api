package recap

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/recap/render"
)

// ImagePath is where shared recap images are served:
//
//	/recap-img/<code>/og.png        the link preview
//	/recap-img/<code>/<card>.png    one card, story-sized
//
// They are public (a link preview is fetched by a crawler with no session)
// and drawn from the public view, so an image never shows more than the page.
const ImagePath = "/recap-img/"

// imageMaxAge is how long a client or proxy may keep an image. It bounds how
// long an unshared recap's images can linger in caches.
const imageMaxAge = 86400

// imageCacheSize bounds the in-process cache of rendered images.
const imageCacheSize = 64

// ImageHandler serves shared recap images.
type ImageHandler struct {
	svc *Service
	log *slog.Logger

	mu    sync.Mutex
	cache map[string][]byte
}

// NewImageHandler builds the image handler.
func NewImageHandler(svc *Service, log *slog.Logger) *ImageHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ImageHandler{svc: svc, log: log.With(slog.String("component", "recap-images")), cache: map[string][]byte{}}
}

func (h *ImageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code, file, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, ImagePath), "/")
	name, isPNG := strings.CutSuffix(file, ".png")
	if !ok || !isPNG || code == "" || name == "" || len(code) > 50 || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}

	rec, err := h.svc.Shared(r.Context(), code, false)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "shared recap lookup failed", slog.Any("error", err))
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}

	// The recap id and builder version in the key mean a revoked-then-reshared
	// code, or a rebuilt recap, never serves a stale image.
	key := fmt.Sprintf("%s/%d/%s", rec.GetId(), rec.GetBuilderVersion(), name)
	png, hit := h.get(key)
	if !hit {
		png, err = renderNamed(rec, name)
		if errors.Is(err, render.ErrUnsupportedCard) || errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			h.log.ErrorContext(r.Context(), "recap image render failed", slog.String("card", name), slog.Any("error", err))
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		h.put(key, png)
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", imageMaxAge))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(len(png)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(png)
}

func renderNamed(rec *recapv1.Recap, name string) ([]byte, error) {
	if name == "og" {
		return render.OG(rec)
	}
	card := findCard(rec, name)
	if card == nil {
		return nil, ErrNotFound
	}
	return render.Card(rec, card)
}

func (h *ImageHandler) get(key string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.cache[key]
	return b, ok
}

func (h *ImageHandler) put(key string, b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.cache) >= imageCacheSize {
		// Evict one at random (map order); good enough at this size.
		for k := range h.cache {
			delete(h.cache, k)
			break
		}
	}
	h.cache[key] = b
}
