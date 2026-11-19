package trip

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/recap/render"
)

// ShareImagePath is where a shared trip's link preview is served:
//
//	/trip-img/<share code>/og.png
//
// It is public, because a link preview is fetched by a crawler with no
// session, and it is drawn from the same view the share link shows a
// signed-out visitor, so the image never says more than the page.
const ShareImagePath = "/trip-img/"

const (
	// shareImageMaxAge is how long a client or proxy may keep the image. It
	// bounds how long an unshared trip's preview can linger in caches, and
	// how stale the count on it can be.
	shareImageMaxAge = 3600
	// shareImageCacheSize bounds the in-process cache of rendered images.
	shareImageCacheSize = 64
)

// ShareImages serves shared trips' link previews.
type ShareImages struct {
	h   *Handler
	log *slog.Logger

	mu    sync.Mutex
	cache map[string][]byte
}

// ShareImages builds the handler for ShareImagePath.
func (h *Handler) ShareImages(log *slog.Logger) *ShareImages {
	if log == nil {
		log = slog.Default()
	}
	return &ShareImages{h: h, log: log.With(slog.String("component", "trip-images")), cache: map[string][]byte{}}
}

func (s *ShareImages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code, file, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, ShareImagePath), "/")
	if !ok || file != "og.png" || code == "" || len(code) > 50 || s.h.sharingReady() != nil {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	t, err := s.h.sharing.TripByShareCode(ctx, code)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.log.ErrorContext(ctx, "shared trip lookup failed", slog.Any("error", err))
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	// Always the signed-out view, whoever asks: the image is cached and
	// shared, so it must be the one anybody with the link may see.
	if s.h.viewable(ctx, uuid.Nil, t, AccessByLink) != nil {
		http.NotFound(w, r)
		return
	}
	card := shareCard(s.h.respondShared(ctx, uuid.Nil, t))

	// The trip's version changes on every edit and the counts change as
	// stops are checked, so neither can serve a stale image.
	key := fmt.Sprintf("%s/%d/%d-%d-%d", t.ID, t.Version, card.Stops, card.Checked, card.Answered)
	png, hit := s.get(key)
	if !hit {
		png, err = render.TripOG(card)
		if err != nil {
			s.log.ErrorContext(ctx, "trip image render failed", slog.Any("error", err))
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		s.put(key, png)
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", shareImageMaxAge))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(len(png)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(png)
}

// shareCard reads the preview's content off a trip as a visitor sees it.
func shareCard(d *tripv1.TripDraft) render.TripCard {
	card := render.TripCard{Title: d.GetTitle(), City: d.GetCityName(), Days: len(d.GetDays())}
	for _, day := range d.GetDays() {
		for _, stop := range day.GetStops() {
			card.Stops++
			if stop.Latitude != nil && stop.Longitude != nil {
				card.Route = append(card.Route, [2]float64{stop.GetLatitude(), stop.GetLongitude()})
			}
			switch stop.GetCheck().GetStatus() {
			case tripv1.StopCheckStatus_STOP_CHECK_STATUS_CHECKED:
				card.Checked++
				card.Answered++
			case tripv1.StopCheckStatus_STOP_CHECK_STATUS_NOT_FOUND:
				card.Answered++
			case tripv1.StopCheckStatus_STOP_CHECK_STATUS_UNSPECIFIED:
			}
		}
	}
	return card
}

func (s *ShareImages) get(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.cache[key]
	return b, ok
}

func (s *ShareImages) put(key string, b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= shareImageCacheSize {
		// Evict one at random (map order); good enough at this size.
		for k := range s.cache {
			delete(s.cache, k)
			break
		}
	}
	s.cache[key] = b
}
