package journal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	journalv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/journal"
	rankingv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/ranking"
	travelhistoryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/storage"
)

// Service is the journal's logic. signer is nil when photo storage is off.
type Service struct {
	repo   Repository
	signer storage.Signer
	log    *slog.Logger
	now    func() time.Time
}

// NewService builds the service. Pass a nil signer (an untyped nil, not a nil
// *storage.S3) to run without photos.
func NewService(repo Repository, signer storage.Signer, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, signer: signer, log: log.With(slog.String("component", "journal")), now: time.Now}
}

// Upload is a request to sign one photo upload.
type Upload struct {
	TripID      uuid.UUID
	DayID       *uuid.UUID
	StopID      *uuid.UUID
	ContentType string
	Size        int64
}

// SignedUpload is where and how the client sends the bytes.
type SignedUpload struct {
	PhotoID   uuid.UUID
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
}

// Journal is the trip, day by day.
func (s *Service) Journal(ctx context.Context, userID, tripID uuid.UUID) (*journalv1.TripJournal, error) {
	trip, err := s.repo.Trip(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}
	days, err := s.repo.Days(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}
	visits, err := s.repo.Visits(ctx, userID, tripID)
	if err != nil {
		return nil, err
	}

	out := &journalv1.TripJournal{TripId: trip.ID.String(), Title: trip.Title, CityName: trip.CityName, PhotosEnabled: s.signer != nil}
	planned := map[string]bool{}
	stops := map[uuid.UUID]*journalv1.JournalStop{}
	dayIndex := map[uuid.UUID]*journalv1.JournalDay{}
	for _, d := range days {
		jd := &journalv1.JournalDay{DayId: d.ID.String(), DayNumber: d.Number, CityName: d.CityName, Diary: d.Diary}
		if d.Date != nil {
			jd.Date = timestamppb.New(*d.Date)
		}
		for _, st := range d.Stops {
			js := &journalv1.JournalStop{
				StopId: st.ID.String(), PoiId: st.POIID, Name: st.Name, Notes: st.Notes,
				StartMinute: st.StartMinute, Verdict: rankingv1.Verdict(st.Verdict),
			}
			if st.VisitedAt != nil {
				js.VisitedAt = timestamppb.New(*st.VisitedAt)
			}
			if st.POIID != "" {
				planned[st.POIID] = true
			}
			stops[st.ID] = js
			jd.Stops = append(jd.Stops, js)
		}
		dayIndex[d.ID] = jd
		out.Days = append(out.Days, jd)
	}

	seen := map[string]bool{}
	for _, v := range visits {
		if planned[v.POIID] || seen[v.POIID] {
			continue
		}
		seen[v.POIID] = true
		i := dayFor(days, visitLocalDate(v.VisitedAt, v.Longitude, v.LocalDate))
		if i < 0 {
			continue
		}
		out.Days[i].ExtraVisits = append(out.Days[i].ExtraVisits, &journalv1.JournalVisit{
			PoiId: v.POIID, Name: v.Name, VisitedAt: timestamppb.New(v.VisitedAt),
			Source: visitSource(v.Source), Verdict: rankingv1.Verdict(v.Verdict),
		})
	}

	if s.signer == nil {
		return out, nil
	}
	photos, err := s.repo.ReadyPhotos(ctx, tripID)
	if err != nil {
		return nil, err
	}
	for _, p := range photos {
		tp, err := s.toProto(ctx, p)
		if err != nil {
			s.log.WarnContext(ctx, "sign photo url", slog.Any("error", err))
			continue
		}
		switch {
		case p.StopID != nil && stops[*p.StopID] != nil:
			stops[*p.StopID].Photos = append(stops[*p.StopID].Photos, tp)
		case p.DayID != nil && dayIndex[*p.DayID] != nil:
			dayIndex[*p.DayID].Photos = append(dayIndex[*p.DayID].Photos, tp)
		default:
			// Unpinned, or its day or stop was removed by an edit.
			out.Photos = append(out.Photos, tp)
		}
	}
	return out, nil
}

// visitLocalDate is the calendar date a visit happened on, where it happened.
// The account's zone is where the traveller lives, not where the trip is, so
// a visit with a longitude is dated by its solar offset (15° an hour). That
// can be an hour off a political zone, which only matters around midnight;
// without a longitude the account's date (homeDate) is all there is.
func visitLocalDate(at time.Time, longitude *float64, homeDate time.Time) time.Time {
	if longitude == nil {
		return homeDate
	}
	offset := time.Duration(math.Round(*longitude/15)) * time.Hour
	local := at.UTC().Add(offset)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

// dayFor is the index of the day a visit on local date belongs to: the day
// dated that day, else the nearest end of the trip; the first day when the
// trip is undated; -1 with no days at all.
func dayFor(days []Day, local time.Time) int {
	if len(days) == 0 {
		return -1
	}
	key := local.Format(time.DateOnly)
	first, last := -1, -1
	for i, d := range days {
		if d.Date == nil {
			continue
		}
		if d.Date.Format(time.DateOnly) == key {
			return i
		}
		if first < 0 || d.Date.Before(*days[first].Date) {
			first = i
		}
		if last < 0 || d.Date.After(*days[last].Date) {
			last = i
		}
	}
	switch {
	case first < 0:
		return 0
	case key > days[last].Date.Format(time.DateOnly):
		return last
	default:
		return first
	}
}

func visitSource(s string) travelhistoryv1.VisitSource {
	switch s {
	case "trip":
		return travelhistoryv1.VisitSource_VISIT_SOURCE_TRIP
	case "visit_event":
		return travelhistoryv1.VisitSource_VISIT_SOURCE_VISIT_EVENT
	case "manual":
		return travelhistoryv1.VisitSource_VISIT_SOURCE_MANUAL
	case "backfill":
		return travelhistoryv1.VisitSource_VISIT_SOURCE_BACKFILL
	}
	return travelhistoryv1.VisitSource_VISIT_SOURCE_UNSPECIFIED
}

// SetDiary writes a day's note, trimmed; empty removes it.
func (s *Service) SetDiary(ctx context.Context, userID, dayID uuid.UUID, text string) (string, error) {
	tripID, err := s.repo.DayTrip(ctx, userID, dayID)
	if err != nil {
		return "", err
	}
	body := truncateRunes(strings.TrimSpace(text), maxDiary)
	if err := s.repo.SetDiary(ctx, tripID, dayID, body); err != nil {
		return "", err
	}
	return body, nil
}

// CreateUpload records a pending photo and signs its PUT.
func (s *Service) CreateUpload(ctx context.Context, userID uuid.UUID, up Upload) (SignedUpload, error) {
	if s.signer == nil {
		return SignedUpload{}, ErrNoStorage
	}
	ext, ok := extensions[up.ContentType]
	if !ok || up.Size < 1 || up.Size > MaxPhotoBytes {
		return SignedUpload{}, ErrBadPhoto
	}
	if _, err := s.repo.Trip(ctx, userID, up.TripID); err != nil {
		return SignedUpload{}, err
	}
	dayID := up.DayID
	if up.StopID != nil {
		d, err := s.repo.StopDay(ctx, up.TripID, *up.StopID)
		if err != nil {
			return SignedUpload{}, err
		}
		dayID = &d
	} else if dayID != nil {
		if err := s.repo.DayOnTrip(ctx, up.TripID, *dayID); err != nil {
			return SignedUpload{}, err
		}
	}

	id := uuid.New()
	p := Photo{
		ID: id, UserID: userID, TripID: up.TripID, DayID: dayID, StopID: up.StopID,
		Key: fmt.Sprintf("trips/%s/%s%s", up.TripID, id, ext), ContentType: up.ContentType, Size: up.Size, Status: statusPending,
	}
	if err := s.repo.InsertPhoto(ctx, p); err != nil {
		return SignedUpload{}, err
	}
	url, headers, err := s.signer.PutURL(ctx, p.Key, p.ContentType, p.Size, putTTL)
	if err != nil {
		// The pending row is swept with the others.
		return SignedUpload{}, err
	}
	return SignedUpload{PhotoID: id, URL: url, Headers: headers, ExpiresAt: s.now().Add(putTTL)}, nil
}

// Confirm checks the uploaded object against what was signed and shows the
// photo. A mismatch removes the photo; a confirm before the bytes arrived
// leaves it pending so the client can try again.
func (s *Service) Confirm(ctx context.Context, userID, photoID uuid.UUID, caption string) (*journalv1.TripPhoto, error) {
	if s.signer == nil {
		return nil, ErrNoStorage
	}
	p, err := s.repo.Photo(ctx, userID, photoID)
	if err != nil {
		return nil, err
	}
	obj, err := s.signer.Head(ctx, p.Key)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return nil, ErrUploadMissing
	case err != nil:
		return nil, err
	}
	if obj.Size != p.Size || !strings.EqualFold(obj.ContentType, p.ContentType) {
		if _, err := s.repo.DeletePhoto(ctx, userID, photoID); err != nil {
			return nil, err
		}
		return nil, ErrBadUpload
	}
	ready, err := s.repo.MarkReady(ctx, photoID, truncateRunes(strings.TrimSpace(caption), maxCaption))
	if err != nil {
		return nil, err
	}
	return s.toProto(ctx, ready)
}

// DeletePhoto removes the photo; the Sweeper removes its object.
func (s *Service) DeletePhoto(ctx context.Context, userID, photoID uuid.UUID) (bool, error) {
	return s.repo.DeletePhoto(ctx, userID, photoID)
}

func (s *Service) toProto(ctx context.Context, p Photo) (*journalv1.TripPhoto, error) {
	url, err := s.signer.GetURL(ctx, p.Key, getTTL)
	if err != nil {
		return nil, err
	}
	out := &journalv1.TripPhoto{Id: p.ID.String(), Url: url, Caption: p.Caption, CreatedAt: timestamppb.New(p.CreatedAt)}
	if p.DayID != nil {
		d := p.DayID.String()
		out.DayId = &d
	}
	if p.StopID != nil {
		st := p.StopID.String()
		out.StopId = &st
	}
	return out, nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
