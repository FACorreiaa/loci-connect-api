package journal

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	journalv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/journal"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func asUser(user uuid.UUID) context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, user.String())
}

func TestHandler_RequiresAuth(t *testing.T) {
	h := NewHandler(newService(newFakeRepo(), &fakeSigner{}), nil)
	_, err := h.GetTripJournal(context.Background(), connect.NewRequest(&journalv1.GetTripJournalRequest{TripId: uuid.NewString()}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestHandler_ErrorCodes(t *testing.T) {
	repo := newFakeRepo()
	ctx := asUser(repo.owner)

	_, err := NewHandler(newService(repo, &fakeSigner{}), nil).GetTripJournal(asUser(uuid.New()),
		connect.NewRequest(&journalv1.GetTripJournalRequest{TripId: repo.trip.String()}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "someone else's trip")

	upload := &journalv1.CreatePhotoUploadRequest{TripId: repo.trip.String(), ContentType: "image/jpeg", SizeBytes: 100}
	_, err = NewHandler(newService(repo, nil), nil).CreatePhotoUpload(ctx, connect.NewRequest(upload))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "storage off")

	bad := &journalv1.CreatePhotoUploadRequest{TripId: repo.trip.String(), ContentType: "image/gif", SizeBytes: 100}
	_, err = NewHandler(newService(repo, &fakeSigner{}), nil).CreatePhotoUpload(ctx, connect.NewRequest(bad))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "not a photo type")

	empty := ""
	withEmptyDay := &journalv1.CreatePhotoUploadRequest{TripId: repo.trip.String(), DayId: &empty, ContentType: "image/jpeg", SizeBytes: 100}
	res, err := NewHandler(newService(repo, &fakeSigner{}), nil).CreatePhotoUpload(ctx, connect.NewRequest(withEmptyDay))
	require.NoError(t, err, "an empty optional id means no day")
	require.NotEmpty(t, res.Msg.GetUploadUrl())
	require.NotNil(t, res.Msg.GetExpiresAt())

	_, err = NewHandler(newService(repo, &fakeSigner{}), nil).ConfirmPhoto(ctx, connect.NewRequest(&journalv1.ConfirmPhotoRequest{PhotoId: res.Msg.GetPhotoId()}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "confirmed before the bytes arrived")
}
