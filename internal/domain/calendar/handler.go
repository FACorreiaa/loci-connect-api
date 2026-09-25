package calendar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	calendarv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/calendar"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/calendar/calendarv1connect"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	"github.com/FACorreiaa/loci-connect-api/pkg/secret"
)

// FeedStore persists the personal ICS subscribe token.
type FeedStore interface {
	GetOrCreateFeedToken(ctx context.Context, userID uuid.UUID, mint func() string) (string, error)
	UserIDForFeedToken(ctx context.Context, token string) (uuid.UUID, error)
}

// Handler implements CalendarService.
type Handler struct {
	calendarv1connect.UnimplementedCalendarServiceHandler
	trips    trip.Repository
	store    Store
	sealer   *secret.Sealer
	oauth    OAuthConfig
	google   googleAPI
	calendly calendlyAPI
	plans    trip.PlanChecker
	log      *slog.Logger
	baseURL  string
	now      func() time.Time
}

func NewHandler(trips trip.Repository, store Store, baseURL string) *Handler {
	return &Handler{
		trips:    trips,
		store:    store,
		google:   googleAPI{},
		calendly: calendlyAPI{},
		baseURL:  strings.TrimRight(baseURL, "/"),
		now:      time.Now,
		log:      slog.Default(),
	}
}

func (h *Handler) WithOAuth(cfg OAuthConfig) *Handler {
	h.oauth = cfg
	h.google.cfg = cfg
	return h
}

func (h *Handler) WithSealer(s *secret.Sealer) *Handler {
	h.sealer = s
	return h
}

func (h *Handler) WithPlans(p trip.PlanChecker) *Handler {
	h.plans = p
	return h
}

func userID(ctx context.Context) (uuid.UUID, error) {
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	return id, nil
}

func protoProvider(p string) calendarv1.CalendarProvider {
	switch p {
	case providerGoogle:
		return calendarv1.CalendarProvider_CALENDAR_PROVIDER_GOOGLE
	case providerCalendly:
		return calendarv1.CalendarProvider_CALENDAR_PROVIDER_CALENDLY
	default:
		return calendarv1.CalendarProvider_CALENDAR_PROVIDER_UNSPECIFIED
	}
}

func providerName(p calendarv1.CalendarProvider) (string, error) {
	switch p {
	case calendarv1.CalendarProvider_CALENDAR_PROVIDER_GOOGLE:
		return providerGoogle, nil
	case calendarv1.CalendarProvider_CALENDAR_PROVIDER_CALENDLY:
		return providerCalendly, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("unknown calendar provider"))
	}
}

func (h *Handler) StartCalendarConnect(
	ctx context.Context,
	req *connect.Request[calendarv1.StartCalendarConnectRequest],
) (*connect.Response[calendarv1.StartCalendarConnectResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	name, err := providerName(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	state, err := encodeState(h.oauth.StateSecret, uid.String(), name, h.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	authURL, err := h.oauth.authURL(name, state)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&calendarv1.StartCalendarConnectResponse{AuthUrl: authURL, State: state}), nil
}

func (h *Handler) CompleteCalendarConnect(
	ctx context.Context,
	req *connect.Request[calendarv1.CompleteCalendarConnectRequest],
) (*connect.Response[calendarv1.CompleteCalendarConnectResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if h.sealer == nil || h.store == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("calendar connect is not configured"))
	}
	want, err := providerName(req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	stateUser, stateProv, err := decodeState(h.oauth.StateSecret, req.Msg.GetState(), h.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if stateUser != uid.String() || stateProv != want {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("oauth state does not match"))
	}
	tok, err := h.oauth.exchange(ctx, want, req.Msg.GetCode())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("oauth exchange: %w", err))
	}
	if tok.RefreshToken == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("provider did not return a refresh token; reconnect and grant offline access"))
	}
	ts := h.mustTokenSource(ctx, want, tok)
	label, err := h.accountLabel(ctx, want, ts)
	if err != nil {
		h.log.Warn("calendar account label failed", "provider", want, "error", err)
		label = want
	}
	sealed, err := h.sealer.Seal(uid[:], []byte(tok.RefreshToken))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("seal token: %w", err))
	}
	conn, err := h.store.UpsertConnection(ctx, uid, want, label, sealed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("store connection: %w", err))
	}
	return connect.NewResponse(&calendarv1.CompleteCalendarConnectResponse{
		Connection: toProtoConnection(conn),
	}), nil
}

func (h *Handler) mustTokenSource(ctx context.Context, provider string, tok *oauth2.Token) oauth2.TokenSource {
	src, err := h.oauth.tokenSource(ctx, provider, tok.RefreshToken)
	if err != nil {
		return oauth2.StaticTokenSource(tok)
	}
	return src
}

func (h *Handler) accountLabel(ctx context.Context, provider string, ts oauth2.TokenSource) (string, error) {
	switch provider {
	case providerGoogle:
		return h.google.accountLabel(ctx, ts)
	case providerCalendly:
		return h.calendly.accountLabel(ctx, ts)
	default:
		return provider, nil
	}
}

func (h *Handler) ListCalendarConnections(
	ctx context.Context,
	_ *connect.Request[calendarv1.ListCalendarConnectionsRequest],
) (*connect.Response[calendarv1.ListCalendarConnectionsResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if h.store == nil {
		return connect.NewResponse(&calendarv1.ListCalendarConnectionsResponse{}), nil
	}
	conns, err := h.store.ListConnections(ctx, uid)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := &calendarv1.ListCalendarConnectionsResponse{}
	for _, c := range conns {
		out.Connections = append(out.Connections, toProtoConnection(c))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) DisconnectCalendar(
	ctx context.Context,
	req *connect.Request[calendarv1.DisconnectCalendarRequest],
) (*connect.Response[calendarv1.DisconnectCalendarResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetConnectionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid connection ID"))
	}
	if err := h.store.DeleteConnection(ctx, uid, id); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&calendarv1.DisconnectCalendarResponse{}), nil
}

func (h *Handler) ListCalendarEvents(
	ctx context.Context,
	req *connect.Request[calendarv1.ListCalendarEventsRequest],
) (*connect.Response[calendarv1.ListCalendarEventsResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	from := req.Msg.GetFrom().AsTime()
	to := req.Msg.GetTo().AsTime()
	if to.Before(from) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("to must be after from"))
	}
	trips, _, err := h.trips.ListTrips(ctx, uid, 50, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list trips: %w", err))
	}
	out := &calendarv1.ListCalendarEventsResponse{}
	for _, e := range EventsFromTrips(trips, from, to) {
		out.Events = append(out.Events, overlayToProto(e, calendarv1.CalendarEventSource_CALENDAR_EVENT_SOURCE_LOCI_TRIP))
	}
	if h.store != nil && h.sealer != nil {
		conns, err := h.store.ListConnections(ctx, uid)
		if err != nil {
			h.log.Warn("list calendar connections failed", "error", err)
		} else {
			for _, c := range conns {
				refresh, err := h.sealer.Open(uid[:], c.RefreshToken)
				if err != nil {
					h.log.Warn("open calendar token failed", "provider", c.Provider)
					continue
				}
				ts, err := h.oauth.tokenSource(ctx, c.Provider, string(refresh))
				if err != nil {
					continue
				}
				events, err := h.listProviderEvents(ctx, c.Provider, ts, from, to)
				if err != nil {
					h.log.Warn("provider calendar list failed", "provider", c.Provider, "error", err)
					continue
				}
				src := calendarv1.CalendarEventSource_CALENDAR_EVENT_SOURCE_GOOGLE
				if c.Provider == providerCalendly {
					src = calendarv1.CalendarEventSource_CALENDAR_EVENT_SOURCE_CALENDLY
				}
				for _, e := range events {
					out.Events = append(out.Events, overlayToProto(e, src))
				}
			}
		}
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) listProviderEvents(ctx context.Context, provider string, ts oauth2.TokenSource, from, to time.Time) ([]OverlayEvent, error) {
	switch provider {
	case providerGoogle:
		return h.google.listEvents(ctx, ts, from, to)
	case providerCalendly:
		return h.calendly.listEvents(ctx, ts, from, to)
	default:
		return nil, nil
	}
}

func (h *Handler) PushTripToCalendar(
	ctx context.Context,
	req *connect.Request[calendarv1.PushTripToCalendarRequest],
) (*connect.Response[calendarv1.PushTripToCalendarResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if h.sealer == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("calendar connect is not configured"))
	}
	connID, err := uuid.Parse(req.Msg.GetConnectionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid connection ID"))
	}
	tripID, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	conn, err := h.store.GetConnection(ctx, uid, connID)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if conn.Provider != providerGoogle {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("trips can only be pushed to Google Calendar"))
	}
	t, err := h.trips.GetTrip(ctx, tripID, uid)
	if err != nil {
		if errors.Is(err, trip.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	exportTrip := t
	if h.plans != nil {
		plan, perr := h.plans.EffectivePlan(ctx, uid)
		if perr == nil && !subscription.Entitled(plan) && len(t.Days) > 1 {
			clone := *t
			clone.Days = append([]trip.TripDay(nil), t.Days[0])
			exportTrip = &clone
		}
	}
	refresh, err := h.sealer.Open(uid[:], conn.RefreshToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("stored calendar token could not be opened"))
	}
	ts, err := h.oauth.tokenSource(ctx, providerGoogle, string(refresh))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	ids, err := h.google.pushTrip(ctx, ts, exportTrip)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("google calendar: %w", err))
	}
	return connect.NewResponse(&calendarv1.PushTripToCalendarResponse{EventIds: ids}), nil
}

func (h *Handler) GetTripCalendarFeedUrl(
	ctx context.Context,
	_ *connect.Request[calendarv1.GetTripCalendarFeedUrlRequest],
) (*connect.Response[calendarv1.GetTripCalendarFeedUrlResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if h.store == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("calendar feed is not configured"))
	}
	token, err := h.store.GetOrCreateFeedToken(ctx, uid, mintFeedToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("feed token: %w", err))
	}
	url := h.baseURL + "/calendar/feed/" + token + ".ics"
	return connect.NewResponse(&calendarv1.GetTripCalendarFeedUrlResponse{Url: url}), nil
}

func mintFeedToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func toProtoConnection(c *Connection) *calendarv1.CalendarConnection {
	return &calendarv1.CalendarConnection{
		Id:           c.ID.String(),
		Provider:     protoProvider(c.Provider),
		AccountLabel: c.AccountLabel,
		ConnectedAt:  timestamppb.New(c.ConnectedAt),
	}
}

func overlayToProto(e OverlayEvent, src calendarv1.CalendarEventSource) *calendarv1.CalendarEvent {
	ev := &calendarv1.CalendarEvent{
		Id:       e.ID,
		Source:   src,
		Title:    e.Title,
		Start:    timestamppb.New(e.Start),
		End:      timestamppb.New(e.End),
		Location: e.Location,
	}
	if e.TripID != "" {
		tid := e.TripID
		ev.TripId = &tid
	}
	return ev
}

// FeedHandler serves the personal ICS subscribe URL.
func (h *Handler) FeedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/calendar/feed/"), ".ics")
		token = strings.TrimSpace(token)
		if token == "" || h.store == nil {
			http.NotFound(w, r)
			return
		}
		uid, err := h.store.UserIDForFeedToken(r.Context(), token)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		trips, _, err := h.trips.ListTrips(r.Context(), uid, 50, 0)
		if err != nil {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		var b strings.Builder
		b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Loci//Trip Feed//EN\r\nCALSCALE:GREGORIAN\r\n")
		for _, t := range trips {
			ics := trip.BuildICS(t)
			body := strings.TrimPrefix(ics, "BEGIN:VCALENDAR\r\n")
			body = strings.TrimSuffix(body, "END:VCALENDAR\r\n")
			for _, line := range strings.Split(body, "\r\n") {
				if strings.HasPrefix(line, "VERSION:") || strings.HasPrefix(line, "PRODID:") || strings.HasPrefix(line, "CALSCALE:") {
					continue
				}
				if line == "" {
					continue
				}
				b.WriteString(line)
				b.WriteString("\r\n")
			}
		}
		b.WriteString("END:VCALENDAR\r\n")
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b.String()))
	})
}
