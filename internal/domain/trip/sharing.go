package trip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Visibility is who besides its owner may open a trip. The values are the
// TripVisibility numbers in trip.proto and the trips.visibility column.
type Visibility int32

const (
	VisibilityPrivate Visibility = 1
	VisibilityFriends Visibility = 2
	VisibilityLink    Visibility = 3
	VisibilityPublic  Visibility = 4
)

// ShareOrigin is where a shared trip opens: the web app's /t/{code}, which
// the iOS app also claims as a Universal Link.
const ShareOrigin = "https://lociai.fyi"

// ShareURL is the link a share code opens.
func ShareURL(code string) string { return ShareOrigin + "/t/" + code }

// Access is how a viewer reached a trip.
type Access int

const (
	// AccessByID is opening a trip by its id: the owner, or someone the
	// owner's visibility lists it for.
	AccessByID Access = iota
	// AccessByLink is opening a trip with its share code.
	AccessByLink
)

// CanView is the one rule for who may open a trip that is not theirs. It is
// pure, so every read path asks the same question and the table test covers
// every case.
//
// viewer is uuid.Nil for an anonymous caller; friends and blocked describe
// the viewer's relation to the owner (blocked in either direction).
func CanView(viewer, owner uuid.UUID, vis Visibility, access Access, friends, blocked bool) bool {
	if viewer != uuid.Nil && viewer == owner {
		return true
	}
	if blocked {
		return false
	}
	switch vis {
	case VisibilityPublic:
		return true
	case VisibilityLink:
		return access == AccessByLink
	case VisibilityFriends:
		// A friend may open it by id or by a link the owner once minted; a
		// link alone does not make a stranger a friend.
		return viewer != uuid.Nil && friends
	default:
		return false
	}
}

// SocialGraph is what trip sharing needs from the friends layer. The social
// package implements it; trip does not import social.
type SocialGraph interface {
	// Relation reports whether a and b are friends and whether either
	// blocked the other.
	Relation(ctx context.Context, a, b uuid.UUID) (friends, blocked bool, err error)
	// FriendIDs lists a user's friends.
	FriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	// PublicUsers returns the public card of each user that exists.
	PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error)
}

// SharingRepository reads and writes trip sharing. Kept apart from
// Repository so the many fakes of that interface do not all need these.
type SharingRepository interface {
	// SetVisibility changes an owned trip's visibility, minting a share code
	// on first use (code is used only when the trip has none).
	SetVisibility(ctx context.Context, id, owner uuid.UUID, vis Visibility, shareDetails bool, code string) (*Trip, error)
	// TripByShareCode loads any trip by its code, days included.
	TripByShareCode(ctx context.Context, code string) (*Trip, error)
	// TripByID loads any trip by id, days included, whoever owns it.
	TripByID(ctx context.Context, id uuid.UUID) (*Trip, error)
	// ListVisibleTrips lists owners' trips with one of the given
	// visibilities, most recently updated first.
	ListVisibleTrips(ctx context.Context, owners []uuid.UUID, vis []Visibility, limit, offset int) ([]*Trip, int, error)
}

// WithSharing attaches trip sharing. Without it the sharing RPCs answer
// Unimplemented and ShareTrip keeps its old behaviour.
func (h *Handler) WithSharing(repo SharingRepository, graph SocialGraph) *Handler {
	h.sharing = repo
	h.graph = graph
	return h
}

type sharingRepository struct {
	db   *pgxpool.Pool
	days *repository
}

// NewSharingRepository is the Postgres SharingRepository.
func NewSharingRepository(db *pgxpool.Pool, logger *slog.Logger) SharingRepository {
	return &sharingRepository{db: db, days: &repository{db: db, logger: logger}}
}

func (r *sharingRepository) SetVisibility(ctx context.Context, id, owner uuid.UUID, vis Visibility, shareDetails bool, code string) (*Trip, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE trips
		SET visibility = $1::smallint, share_details = $2,
		    is_public = ($1::smallint <> 1),
		    share_code = CASE WHEN $1::smallint = 1 THEN share_code ELSE COALESCE(share_code, $3) END,
		    updated_at = NOW()
		WHERE id = $4 AND user_id = $5`, vis, shareDetails, code, id, owner)
	if err != nil {
		return nil, fmt.Errorf("set visibility: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.days.GetTrip(ctx, id, owner)
}

func (r *sharingRepository) loadOne(ctx context.Context, where string, arg any) (*Trip, error) {
	t, err := scanTrip(r.db.QueryRow(ctx, `SELECT `+tripColumns+` FROM trips WHERE `+where, arg))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load trip: %w", err)
	}
	if err := r.days.loadDays(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (r *sharingRepository) TripByShareCode(ctx context.Context, code string) (*Trip, error) {
	return r.loadOne(ctx, "share_code = $1", code)
}

func (r *sharingRepository) TripByID(ctx context.Context, id uuid.UUID) (*Trip, error) {
	return r.loadOne(ctx, "id = $1", id)
}

func (r *sharingRepository) ListVisibleTrips(ctx context.Context, owners []uuid.UUID, vis []Visibility, limit, offset int) ([]*Trip, int, error) {
	if len(owners) == 0 || len(vis) == 0 {
		return nil, 0, nil
	}
	vals := make([]int32, len(vis))
	for i, v := range vis {
		vals[i] = int32(v)
	}
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM trips WHERE user_id = ANY($1) AND visibility = ANY($2)`,
		owners, vals).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count visible trips: %w", err)
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+tripColumns+` FROM trips
		WHERE user_id = ANY($1) AND visibility = ANY($2)
		ORDER BY updated_at DESC
		LIMIT $3 OFFSET $4`, owners, vals, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list visible trips: %w", err)
	}
	defer rows.Close()
	var trips []*Trip
	for rows.Next() {
		t, err := scanTrip(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan trip: %w", err)
		}
		trips = append(trips, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for _, t := range trips {
		if err := r.days.loadDays(ctx, t); err != nil {
			return nil, 0, err
		}
	}
	return trips, total, nil
}

// --- RPCs ---

func (h *Handler) sharingReady() error {
	if h.sharing == nil || h.graph == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("trip sharing is not enabled"))
	}
	return nil
}

// viewerID is the caller, or uuid.Nil when signed out.
func viewerID(ctx context.Context) uuid.UUID {
	id, err := userID(ctx)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func (h *Handler) SetTripVisibility(ctx context.Context, req *connect.Request[tripv1.SetTripVisibilityRequest]) (*connect.Response[tripv1.SetTripVisibilityResponse], error) {
	if err := h.sharingReady(); err != nil {
		return nil, err
	}
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	vis := Visibility(req.Msg.GetVisibility())
	if vis < VisibilityPrivate || vis > VisibilityPublic {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid visibility"))
	}
	t, err := h.sharing.SetVisibility(ctx, id, uid, vis, req.Msg.GetShareDetails(), newShareCode())
	if err != nil {
		return nil, toConnectErr(err)
	}
	resp := &tripv1.SetTripVisibilityResponse{Visibility: tripv1.TripVisibility(t.Visibility)}
	if t.Visibility != VisibilityPrivate && t.ShareCode != nil {
		resp.ShareCode = *t.ShareCode
		resp.ShareUrl = ShareURL(*t.ShareCode)
	}
	return connect.NewResponse(resp), nil
}

// viewable loads the relation and applies CanView. A trip the viewer may not
// see is NotFound, never PermissionDenied: its existence is not theirs to know.
func (h *Handler) viewable(ctx context.Context, viewer uuid.UUID, t *Trip, access Access) error {
	friends, blocked := false, false
	if viewer != uuid.Nil && viewer != t.UserID {
		var err error
		friends, blocked, err = h.graph.Relation(ctx, viewer, t.UserID)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
	}
	if !CanView(viewer, t.UserID, t.Visibility, access, friends, blocked) {
		return connect.NewError(connect.CodeNotFound, ErrNotFound)
	}
	return nil
}

// respondShared is a trip as someone other than its owner sees it: the owner's
// public card, no share code, and no stop notes or booking links unless the
// owner chose to share them. The owner gets the full trip.
func (h *Handler) respondShared(ctx context.Context, viewer uuid.UUID, t *Trip) *tripv1.TripDraft {
	p := h.respond(ctx, t)
	if viewer == t.UserID {
		return p
	}
	return redactForViewer(p, t.ShareDetails, h.owner(ctx, t.UserID))
}

func redactForViewer(p *tripv1.TripDraft, shareDetails bool, owner *socialv1.PublicUser) *tripv1.TripDraft {
	// A public trip's link is no secret (the trip is listed on the owner's
	// profile) and it is how a signed-out visitor opens it. Every other
	// level keeps its link to the owner: a friend re-sharing it would widen
	// who can see the trip.
	if p.GetVisibility() != tripv1.TripVisibility_TRIP_VISIBILITY_PUBLIC {
		p.ShareCode = ""
	}
	p.SourceSessionId = nil
	p.Owner = owner
	if !shareDetails {
		for _, d := range p.GetDays() {
			for _, s := range d.GetStops() {
				s.Notes = ""
				s.BookingUrl = nil
			}
		}
		for _, l := range p.GetLegs() {
			l.BookingUrl = nil
		}
		for _, s := range p.GetStays() {
			s.BookingUrl = nil
		}
		// What the traveller typed about a flight can place them (a flight
		// number, a booking reference in notes); friends see the route and date.
		for _, f := range p.GetFlights() {
			f.Notes = nil
			f.PriceText = nil
			f.FlightNo = nil
			f.Carrier = nil
		}
	}
	return p
}

func (h *Handler) owner(ctx context.Context, id uuid.UUID) *socialv1.PublicUser {
	users, err := h.graph.PublicUsers(ctx, []uuid.UUID{id})
	if err != nil {
		h.logWarn("load trip owner", err)
		return nil
	}
	return users[id]
}

func (h *Handler) logWarn(msg string, err error) {
	if h.log != nil {
		h.log.Warn(msg, slog.Any("error", err))
	}
}

func (h *Handler) GetSharedTrip(ctx context.Context, req *connect.Request[tripv1.GetSharedTripRequest]) (*connect.Response[tripv1.TripDraft], error) {
	if err := h.sharingReady(); err != nil {
		return nil, err
	}
	viewer := viewerID(ctx)
	t, err := h.sharing.TripByShareCode(ctx, req.Msg.GetShareCode())
	if err != nil {
		return nil, toConnectErr(err)
	}
	if err := h.viewable(ctx, viewer, t, AccessByLink); err != nil {
		return nil, err
	}
	return connect.NewResponse(h.respondShared(ctx, viewer, t)), nil
}

func (h *Handler) GetFriendTrip(ctx context.Context, req *connect.Request[tripv1.GetFriendTripRequest]) (*connect.Response[tripv1.TripDraft], error) {
	if err := h.sharingReady(); err != nil {
		return nil, err
	}
	viewer, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	t, err := h.sharing.TripByID(ctx, id)
	if err != nil {
		return nil, toConnectErr(err)
	}
	if err := h.viewable(ctx, viewer, t, AccessByID); err != nil {
		return nil, err
	}
	return connect.NewResponse(h.respondShared(ctx, viewer, t)), nil
}

func pageArgs(p interface {
	GetPage() int32
	GetPageSize() int32
},
) (page, limit int) {
	page, limit = int(p.GetPage()), int(p.GetPageSize())
	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return page, limit
}

func (h *Handler) listShared(ctx context.Context, viewer uuid.UUID, trips []*Trip, total, page, limit int) *tripv1.ListTripsResponse {
	owners := make([]uuid.UUID, 0, len(trips))
	seen := map[uuid.UUID]bool{}
	for _, t := range trips {
		if !seen[t.UserID] {
			seen[t.UserID] = true
			owners = append(owners, t.UserID)
		}
	}
	cards, err := h.graph.PublicUsers(ctx, owners)
	if err != nil {
		h.logWarn("load trip owners", err)
	}
	out := h.respondAll(ctx, trips)
	for i, t := range trips {
		out[i] = redactForViewer(out[i], t.ShareDetails, cards[t.UserID])
	}
	return &tripv1.ListTripsResponse{Trips: out, Pagination: paginationMeta(page, limit, total)}
}

func (h *Handler) ListFriendTrips(ctx context.Context, req *connect.Request[tripv1.ListFriendTripsRequest]) (*connect.Response[tripv1.ListTripsResponse], error) {
	if err := h.sharingReady(); err != nil {
		return nil, err
	}
	viewer, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	friends, err := h.graph.FriendIDs(ctx, viewer)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	page, limit := pageArgs(req.Msg.GetPagination())
	trips, total, err := h.sharing.ListVisibleTrips(ctx, friends,
		[]Visibility{VisibilityFriends, VisibilityPublic}, limit, (page-1)*limit)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(h.listShared(ctx, viewer, trips, total, page, limit)), nil
}

func (h *Handler) ListUserTrips(ctx context.Context, req *connect.Request[tripv1.ListUserTripsRequest]) (*connect.Response[tripv1.ListTripsResponse], error) {
	if err := h.sharingReady(); err != nil {
		return nil, err
	}
	viewer := viewerID(ctx)
	owner, err := uuid.Parse(req.Msg.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	vis := []Visibility{VisibilityPublic}
	switch {
	case viewer == owner:
		vis = []Visibility{VisibilityPrivate, VisibilityFriends, VisibilityLink, VisibilityPublic}
	case viewer != uuid.Nil:
		friends, blocked, err := h.graph.Relation(ctx, viewer, owner)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if blocked {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
		}
		if friends {
			vis = []Visibility{VisibilityFriends, VisibilityPublic}
		}
	}
	page, limit := pageArgs(req.Msg.GetPagination())
	trips, total, err := h.sharing.ListVisibleTrips(ctx, []uuid.UUID{owner}, vis, limit, (page-1)*limit)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(h.listShared(ctx, viewer, trips, total, page, limit)), nil
}

func (h *Handler) CopyTrip(ctx context.Context, req *connect.Request[tripv1.CopyTripRequest]) (*connect.Response[tripv1.CopyTripResponse], error) {
	if err := h.sharingReady(); err != nil {
		return nil, err
	}
	viewer, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	var (
		src    *Trip
		access = AccessByID
	)
	switch s := req.Msg.GetSource().(type) {
	case *tripv1.CopyTripRequest_ShareCode:
		access = AccessByLink
		src, err = h.sharing.TripByShareCode(ctx, s.ShareCode)
	case *tripv1.CopyTripRequest_TripId:
		id, perr := uuid.Parse(s.TripId)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
		}
		src, err = h.sharing.TripByID(ctx, id)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a trip id or share code is required"))
	}
	if err != nil {
		return nil, toConnectErr(err)
	}
	if err := h.viewable(ctx, viewer, src, access); err != nil {
		return nil, err
	}
	saved, err := h.repo.SaveTrip(ctx, copyOf(src, viewer), 0)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&tripv1.CopyTripResponse{TripId: saved.ID.String()}), nil
}

// copyOf is src as a new, private trip of newOwner's. Every id is dropped so
// the save mints fresh ones; the chat session it came from is the source
// owner's and is not carried over. Notes and booking links travel only when
// the source owner shared them.
func copyOf(src *Trip, newOwner uuid.UUID) *Trip {
	srcID := src.ID
	t := &Trip{
		UserID:           newOwner,
		CityID:           src.CityID,
		CityName:         src.CityName,
		Title:            src.Title,
		Constraints:      src.Constraints,
		CopiedFromTripID: &srcID,
		Visibility:       VisibilityPrivate,
	}
	keepDetails := src.ShareDetails || src.UserID == newOwner
	for _, d := range src.Days {
		nd := d
		nd.ID = uuid.Nil
		nd.Stops = make([]TripStop, 0, len(d.Stops))
		for _, s := range d.Stops {
			ns := s
			ns.ID = uuid.Nil
			ns.RecommendationTrace = nil
			if !keepDetails {
				ns.Notes = ""
				ns.BookingURL = nil
			}
			nd.Stops = append(nd.Stops, ns)
		}
		t.Days = append(t.Days, nd)
	}
	for _, l := range src.Legs {
		nl := l
		nl.ID = uuid.Nil
		if !keepDetails {
			nl.BookingURL = nil
		}
		t.Legs = append(t.Legs, nl)
	}
	for _, c := range src.Cities {
		nc := c
		nc.SessionID = nil
		t.Cities = append(t.Cities, nc)
	}
	return t
}
