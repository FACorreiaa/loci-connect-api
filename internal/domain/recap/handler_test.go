package recap

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/recap/render"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func as(user uuid.UUID) context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, user.String())
}

func TestHandlerRequiresSignIn(t *testing.T) {
	svc, _, _, f := setup(t, noon)
	h := NewHandler(svc, nil)

	_, err := h.GetTripRecap(context.Background(), connect.NewRequest(&recapv1.GetTripRecapRequest{TripId: f.TripID.String()}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = h.ListRecaps(context.Background(), connect.NewRequest(&recapv1.ListRecapsRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestHandlerOwnershipAndShare(t *testing.T) {
	svc, _, _, f := setup(t, noon)
	h := NewHandler(svc, nil)
	ctx := as(f.UserID)

	got, err := h.GetTripRecap(ctx, connect.NewRequest(&recapv1.GetTripRecapRequest{TripId: f.TripID.String()}))
	require.NoError(t, err)
	id := got.Msg.GetRecap().GetId()

	_, err = h.GetRecap(as(uuid.New()), connect.NewRequest(&recapv1.GetRecapRequest{RecapId: id}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "someone else's recap does not exist")

	_, err = h.GetSharedRecap(context.Background(), connect.NewRequest(&recapv1.GetSharedRecapRequest{ShareCode: "nope"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	shared, err := h.ShareRecap(ctx, connect.NewRequest(&recapv1.ShareRecapRequest{RecapId: id}))
	require.NoError(t, err)

	pub, err := h.GetSharedRecap(context.Background(), connect.NewRequest(&recapv1.GetSharedRecapRequest{ShareCode: shared.Msg.GetShareCode()}))
	require.NoError(t, err, "anonymous callers may open a shared recap")
	assert.False(t, pub.Msg.GetRecap().GetIsOwner())

	img, err := h.RenderRecapCardImage(ctx, connect.NewRequest(&recapv1.RenderRecapCardImageRequest{
		RecapId: id, CardId: CardPlaces, Format: recapv1.RecapImageFormat_RECAP_IMAGE_FORMAT_STORY,
	}))
	require.NoError(t, err)
	cfg, err := png.DecodeConfig(bytes.NewReader(img.Msg.GetPng()))
	require.NoError(t, err)
	assert.Equal(t, render.StoryW, cfg.Width)
	assert.Equal(t, render.StoryH, cfg.Height)

	_, err = h.RenderRecapCardImage(ctx, connect.NewRequest(&recapv1.RenderRecapCardImageRequest{
		RecapId: id, CardId: "nope", Format: recapv1.RecapImageFormat_RECAP_IMAGE_FORMAT_STORY,
	}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	list, err := h.ListRecaps(ctx, connect.NewRequest(&recapv1.ListRecapsRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetRecaps(), 1)
	assert.True(t, list.Msg.GetRecaps()[0].GetShared())
	assert.Equal(t, "Lisbon", list.Msg.GetRecaps()[0].GetTitle())
}

func TestImageHandler(t *testing.T) {
	svc, _, _, f := setup(t, noon)
	ctx := context.Background()
	rec, err := svc.TripRecap(ctx, f.UserID, f.TripID, false)
	require.NoError(t, err)
	id := uuid.MustParse(rec.GetId())
	code, _, err := svc.Share(ctx, f.UserID, id)
	require.NoError(t, err)

	h := NewImageHandler(svc, nil)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	w := get(ImagePath + code + "/og.png")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Cache-Control"), "public")
	cfg, err := png.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, render.OGW, cfg.Width)

	assert.Equal(t, http.StatusOK, get(ImagePath+code+"/"+CardPlaces+".png").Code)
	assert.Equal(t, http.StatusNotFound, get(ImagePath+code+"/"+CardHighlights+".png").Code,
		"highlights are not in the public view, so there is no image of them")
	assert.Equal(t, http.StatusNotFound, get(ImagePath+code+"/og.jpg").Code)
	assert.Equal(t, http.StatusNotFound, get(ImagePath+"nope/og.png").Code)

	require.NoError(t, svc.Unshare(ctx, f.UserID, id))
	assert.Equal(t, http.StatusNotFound, get(ImagePath+code+"/og.png").Code, "revoked even when cached")

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, ImagePath+code+"/og.png", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
