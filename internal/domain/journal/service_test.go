package journal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/storage"
)

type fakeSigner struct {
	objects map[string]storage.Object
	signed  []string
}

func (f *fakeSigner) PutURL(_ context.Context, key, contentType string, size int64, _ time.Duration) (string, map[string]string, error) {
	f.signed = append(f.signed, key)
	return "https://media.example/" + key + "?sig", map[string]string{"Content-Type": contentType}, nil
}

func (f *fakeSigner) GetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://media.example/" + key + "?get", nil
}

func (f *fakeSigner) Head(_ context.Context, key string) (storage.Object, error) {
	o, ok := f.objects[key]
	if !ok {
		return storage.Object{}, storage.ErrNotFound
	}
	return o, nil
}

func (f *fakeSigner) Delete(context.Context, string) error { return nil }

type fakeRepo struct {
	Repository // unimplemented methods panic
	owner      uuid.UUID
	trip       uuid.UUID
	day        uuid.UUID
	days       []Day
	visits     []Visit
	photos     map[uuid.UUID]Photo
	deleted    []uuid.UUID
	diary      map[uuid.UUID]string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{owner: uuid.New(), trip: uuid.New(), day: uuid.New(), photos: map[uuid.UUID]Photo{}, diary: map[uuid.UUID]string{}}
}

func (f *fakeRepo) Trip(_ context.Context, userID, tripID uuid.UUID) (Trip, error) {
	if userID != f.owner || tripID != f.trip {
		return Trip{}, ErrNotFound
	}
	return Trip{ID: tripID, Title: "Lisbon", CityName: "Lisbon"}, nil
}

func (f *fakeRepo) Days(context.Context, uuid.UUID, uuid.UUID) ([]Day, error) { return f.days, nil }

func (f *fakeRepo) Visits(context.Context, uuid.UUID, uuid.UUID) ([]Visit, error) {
	return f.visits, nil
}

func (f *fakeRepo) ReadyPhotos(context.Context, uuid.UUID) ([]Photo, error) {
	var out []Photo
	for _, p := range f.photos {
		if p.Status == statusReady {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeRepo) DayTrip(_ context.Context, userID, dayID uuid.UUID) (uuid.UUID, error) {
	if userID != f.owner || dayID != f.day {
		return uuid.Nil, ErrNotFound
	}
	return f.trip, nil
}

func (f *fakeRepo) DayOnTrip(_ context.Context, tripID, dayID uuid.UUID) error {
	if tripID != f.trip || dayID != f.day {
		return ErrNotFound
	}
	return nil
}

func (f *fakeRepo) SetDiary(_ context.Context, _, dayID uuid.UUID, body string) error {
	f.diary[dayID] = body
	return nil
}

func (f *fakeRepo) InsertPhoto(_ context.Context, p Photo) error {
	f.photos[p.ID] = p
	return nil
}

func (f *fakeRepo) Photo(_ context.Context, userID, id uuid.UUID) (Photo, error) {
	p, ok := f.photos[id]
	if !ok || p.UserID != userID {
		return Photo{}, ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) MarkReady(_ context.Context, id uuid.UUID, caption string) (Photo, error) {
	p := f.photos[id]
	p.Status, p.Caption = statusReady, caption
	f.photos[id] = p
	return p, nil
}

func (f *fakeRepo) DeletePhoto(_ context.Context, userID, id uuid.UUID) (bool, error) {
	p, ok := f.photos[id]
	if !ok || p.UserID != userID {
		return false, nil
	}
	delete(f.photos, id)
	f.deleted = append(f.deleted, id)
	return true, nil
}

func newService(repo *fakeRepo, signer storage.Signer) *Service {
	return NewService(repo, signer, nil)
}

func TestCreatePhotoUpload_RejectsOtherUsersTrip(t *testing.T) {
	repo, signer := newFakeRepo(), &fakeSigner{}
	_, err := newService(repo, signer).CreateUpload(context.Background(), uuid.New(), Upload{TripID: repo.trip, ContentType: "image/jpeg", Size: 100})
	require.ErrorIs(t, err, ErrNotFound)
	require.Empty(t, signer.signed)
	require.Empty(t, repo.photos)
}

func TestCreatePhotoUpload_RejectsBadTypeAndSize(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo, &fakeSigner{})
	for _, up := range []Upload{
		{TripID: repo.trip, ContentType: "image/gif", Size: 100},
		{TripID: repo.trip, ContentType: "image/jpeg", Size: MaxPhotoBytes + 1},
		{TripID: repo.trip, ContentType: "image/jpeg", Size: 0},
	} {
		_, err := svc.CreateUpload(context.Background(), repo.owner, up)
		require.ErrorIs(t, err, ErrBadPhoto, up)
	}
	require.Empty(t, repo.photos)
}

func TestCreatePhotoUpload_StorageOffIsFailedPrecondition(t *testing.T) {
	repo := newFakeRepo()
	_, err := newService(repo, nil).CreateUpload(context.Background(), repo.owner, Upload{TripID: repo.trip, ContentType: "image/jpeg", Size: 100})
	require.ErrorIs(t, err, ErrNoStorage)
}

func TestCreatePhotoUpload_PendingRowUnderTheTrip(t *testing.T) {
	repo, signer := newFakeRepo(), &fakeSigner{}
	day := repo.day
	got, err := newService(repo, signer).CreateUpload(context.Background(), repo.owner, Upload{TripID: repo.trip, DayID: &day, ContentType: "image/png", Size: 100})
	require.NoError(t, err)

	p := repo.photos[got.PhotoID]
	require.Equal(t, "pending", p.Status)
	require.Equal(t, &day, p.DayID)
	require.Equal(t, "trips/"+repo.trip.String()+"/"+got.PhotoID.String()+".png", p.Key)
	require.Equal(t, []string{p.Key}, signer.signed)
	require.Equal(t, "image/png", got.Headers["Content-Type"])
	require.True(t, strings.HasPrefix(got.URL, "https://media.example/trips/"))
}

func TestCreatePhotoUpload_DayFromAnotherTripNotFound(t *testing.T) {
	repo := newFakeRepo()
	other := uuid.New()
	_, err := newService(repo, &fakeSigner{}).CreateUpload(context.Background(), repo.owner, Upload{TripID: repo.trip, DayID: &other, ContentType: "image/jpeg", Size: 1})
	require.ErrorIs(t, err, ErrNotFound)
}

func pending(repo *fakeRepo, size int64) Photo {
	p := Photo{ID: uuid.New(), UserID: repo.owner, TripID: repo.trip, Key: "trips/x/" + uuid.NewString() + ".jpg", ContentType: "image/jpeg", Size: size, Status: "pending"}
	repo.photos[p.ID] = p
	return p
}

func TestConfirmPhoto_SizeMismatchDeletes(t *testing.T) {
	repo := newFakeRepo()
	p := pending(repo, 100)
	signer := &fakeSigner{objects: map[string]storage.Object{p.Key: {Size: 999, ContentType: "image/jpeg"}}}

	_, err := newService(repo, signer).Confirm(context.Background(), repo.owner, p.ID, "")
	require.ErrorIs(t, err, ErrBadUpload)
	require.Equal(t, []uuid.UUID{p.ID}, repo.deleted)
}

func TestConfirmPhoto_BeforeUploadKeepsPending(t *testing.T) {
	repo := newFakeRepo()
	p := pending(repo, 100)
	_, err := newService(repo, &fakeSigner{}).Confirm(context.Background(), repo.owner, p.ID, "")
	require.ErrorIs(t, err, ErrUploadMissing)
	require.Equal(t, "pending", repo.photos[p.ID].Status)
}

func TestConfirmPhoto_MatchMakesReady(t *testing.T) {
	repo := newFakeRepo()
	p := pending(repo, 100)
	signer := &fakeSigner{objects: map[string]storage.Object{p.Key: {Size: 100, ContentType: "image/jpeg"}}}

	got, err := newService(repo, signer).Confirm(context.Background(), repo.owner, p.ID, "  Sunset at the miradouro  ")
	require.NoError(t, err)
	require.Equal(t, statusReady, repo.photos[p.ID].Status)
	require.Equal(t, "Sunset at the miradouro", got.GetCaption())
	require.Contains(t, got.GetUrl(), "?get")
}

func TestConfirmPhoto_OtherUsersPhotoNotFound(t *testing.T) {
	repo := newFakeRepo()
	p := pending(repo, 100)
	_, err := newService(repo, &fakeSigner{}).Confirm(context.Background(), uuid.New(), p.ID, "")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDeletePhoto_RemovesRow(t *testing.T) {
	repo := newFakeRepo()
	p := pending(repo, 100)
	svc := newService(repo, &fakeSigner{})

	ok, err := svc.DeletePhoto(context.Background(), uuid.New(), p.ID)
	require.NoError(t, err)
	require.False(t, ok, "another traveller cannot delete it")

	ok, err = svc.DeletePhoto(context.Background(), repo.owner, p.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []uuid.UUID{p.ID}, repo.deleted)
}

func TestSetDayDiary_OtherUsersDayNotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo, nil)
	_, err := svc.SetDiary(context.Background(), uuid.New(), repo.day, "hello")
	require.ErrorIs(t, err, ErrNotFound)
	require.Empty(t, repo.diary)

	got, err := svc.SetDiary(context.Background(), repo.owner, repo.day, "  Pastéis at Manteigaria.  ")
	require.NoError(t, err)
	require.Equal(t, "Pastéis at Manteigaria.", got)
	require.Equal(t, "Pastéis at Manteigaria.", repo.diary[repo.day])
}

func date(d int) *time.Time {
	t := time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestJournal_PlacesVisitsAndPhotos(t *testing.T) {
	repo := newFakeRepo()
	day2 := uuid.New()
	stop := uuid.New()
	repo.days = []Day{
		{ID: repo.day, Number: 1, Date: date(20), CityName: "Lisbon", Diary: "Arrived", Stops: []Stop{{ID: stop, POIID: "poi-a", Name: "Belém", Verdict: 1}}},
		{ID: day2, Number: 2, Date: date(21), CityName: "Lisbon"},
	}
	repo.visits = []Visit{
		{POIID: "poi-a", Name: "Belém", LocalDate: *date(20)},      // on the plan: not an extra
		{POIID: "poi-b", Name: "A Ginjinha", LocalDate: *date(21)}, // off the plan, day 2
		{POIID: "poi-c", Name: "Late bar", LocalDate: *date(25)},   // after the trip: last day
	}
	s := stop
	d2 := day2
	repo.photos[uuid.New()] = Photo{ID: uuid.New(), UserID: repo.owner, TripID: repo.trip, StopID: &s, Key: "k1", Status: statusReady}
	repo.photos[uuid.New()] = Photo{ID: uuid.New(), UserID: repo.owner, TripID: repo.trip, DayID: &d2, Key: "k2", Status: statusReady}
	gone := uuid.New()
	repo.photos[uuid.New()] = Photo{ID: uuid.New(), UserID: repo.owner, TripID: repo.trip, DayID: &gone, Key: "k3", Status: statusReady}
	repo.photos[uuid.New()] = Photo{ID: uuid.New(), UserID: repo.owner, TripID: repo.trip, Key: "k4", Status: "pending"}

	j, err := newService(repo, &fakeSigner{}).Journal(context.Background(), repo.owner, repo.trip)
	require.NoError(t, err)
	require.True(t, j.GetPhotosEnabled())
	require.Len(t, j.GetDays(), 2)
	d1 := j.GetDays()[0]
	require.Equal(t, "Arrived", d1.GetDiary())
	require.Len(t, d1.GetStops()[0].GetPhotos(), 1)
	require.Empty(t, d1.GetExtraVisits())
	require.Equal(t, []string{"A Ginjinha", "Late bar"}, []string{j.GetDays()[1].GetExtraVisits()[0].GetName(), j.GetDays()[1].GetExtraVisits()[1].GetName()})
	require.Len(t, j.GetDays()[1].GetPhotos(), 1)
	require.Len(t, j.GetPhotos(), 1, "a photo whose day was removed falls back to the trip; pending ones never show")

	_, err = newService(repo, nil).Journal(context.Background(), uuid.New(), repo.trip)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestJournal_WithoutStorageHasNoPhotos(t *testing.T) {
	repo := newFakeRepo()
	repo.days = []Day{{ID: repo.day, Number: 1}}
	j, err := newService(repo, nil).Journal(context.Background(), repo.owner, repo.trip)
	require.NoError(t, err)
	require.False(t, j.GetPhotosEnabled())
}

func TestDayFor_UndatedAndOutOfRange(t *testing.T) {
	days := []Day{{Number: 1, Date: date(20)}, {Number: 2, Date: date(21)}}
	require.Equal(t, 0, dayFor(days, *date(19)), "before the trip: first day")
	require.Equal(t, 1, dayFor(days, *date(21)))
	require.Equal(t, 1, dayFor(days, *date(30)), "after the trip: last day")
	require.Equal(t, 0, dayFor([]Day{{Number: 1}, {Number: 2}}, *date(21)), "undated: first day")
	require.Equal(t, -1, dayFor(nil, *date(21)))
}

var _ = errors.New
