package boards

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	boardsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/boards/v1"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/boards/v1/boardsv1connect"
	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Cards resolves user IDs to the public cards shown as authors; the social
// service implements it.
type Cards interface {
	PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error)
}

// Handler implements the BoardsService Connect handlers.
type Handler struct {
	boardsv1connect.UnimplementedBoardsServiceHandler
	svc    *Service
	cards  Cards
	logger *slog.Logger
}

func NewHandler(svc *Service, cards Cards, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, cards: cards, logger: logger.With(slog.String("component", "boards-handler"))}
}

// caller is the session behind the request, or a zero Caller when anonymous.
func caller(ctx context.Context) Caller {
	claims, err := interceptors.GetClaimsFromContext(ctx)
	if err != nil {
		return Caller{}
	}
	id, err := uuid.Parse(claims.UserID)
	if err != nil {
		return Caller{}
	}
	return Caller{ID: id, Email: claims.Email}
}

func (h *Handler) toConnectError(ctx context.Context, err error) error {
	var sanctioned *SanctionedError
	switch {
	case errors.As(err, &sanctioned):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrSlugTaken):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, ErrRateLimited):
		return connect.NewError(connect.CodeResourceExhausted, err)
	default:
		h.logger.ErrorContext(ctx, "boards request failed", slog.Any("error", err))
		return connect.NewError(connect.CodeInternal, errors.New("something went wrong"))
	}
}

// authorCards loads the cards for ids. Cards are enrichment: on failure the
// rows go out without authors rather than failing the read.
func (h *Handler) authorCards(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]*socialv1.PublicUser {
	if len(ids) == 0 || h.cards == nil {
		return nil
	}
	cards, err := h.cards.PublicUsers(ctx, ids)
	if err != nil {
		h.logger.WarnContext(ctx, "could not load author cards", slog.Any("error", err))
		return nil
	}
	return cards
}

func ts(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func tsPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func boardProto(b *Board, cards map[uuid.UUID]*socialv1.PublicUser) *boardsv1.Board {
	return &boardsv1.Board{
		Id:             b.ID.String(),
		Slug:           b.Slug,
		Name:           b.Name,
		Description:    b.Description,
		CreatedBy:      cards[b.CreatedBy],
		CreatedAt:      ts(b.CreatedAt),
		PostCount:      int32(b.PostCount),
		LastActivityAt: ts(b.LastActivityAt),
	}
}

var kindToProto = map[string]boardsv1.AttachmentKind{
	KindItinerary: boardsv1.AttachmentKind_ATTACHMENT_KIND_ITINERARY,
	KindPOI:       boardsv1.AttachmentKind_ATTACHMENT_KIND_POI,
	KindCity:      boardsv1.AttachmentKind_ATTACHMENT_KIND_CITY,
}

var kindFromProto = map[boardsv1.AttachmentKind]string{
	boardsv1.AttachmentKind_ATTACHMENT_KIND_ITINERARY: KindItinerary,
	boardsv1.AttachmentKind_ATTACHMENT_KIND_POI:       KindPOI,
	boardsv1.AttachmentKind_ATTACHMENT_KIND_CITY:      KindCity,
}

func postProto(p *Post, cards map[uuid.UUID]*socialv1.PublicUser) *boardsv1.Post {
	out := &boardsv1.Post{
		Id:           p.ID.String(),
		Board:        &boardsv1.BoardRef{Slug: p.BoardSlug, Name: p.BoardName},
		Author:       cards[p.AuthorID],
		Title:        p.Title,
		Url:          p.URL,
		Domain:       Domain(p.URL),
		Body:         p.Body,
		Score:        int32(p.Score),
		CommentCount: int32(p.CommentCount),
		MyVote:       int32(p.MyVote),
		CreatedAt:    ts(p.CreatedAt),
	}
	if a := p.Attachment; a != nil {
		out.Attachment = &boardsv1.Attachment{
			Kind:     kindToProto[a.Kind],
			Ref:      a.Ref,
			Title:    a.Snapshot.Title,
			Subtitle: a.Snapshot.Subtitle,
			ImageUrl: a.Snapshot.ImageURL,
			City:     a.Snapshot.City,
		}
	}
	return out
}

func commentProto(c *Comment, cards map[uuid.UUID]*socialv1.PublicUser) *boardsv1.Comment {
	out := &boardsv1.Comment{
		Id:        c.ID.String(),
		PostId:    c.PostID.String(),
		Body:      c.Body,
		Deleted:   c.Deleted,
		CreatedAt: ts(c.CreatedAt),
	}
	if c.ParentID != nil {
		out.ParentId = c.ParentID.String()
	}
	if !c.Deleted {
		out.Author = cards[c.AuthorID]
	}
	return out
}

var sanctionKindToProto = map[string]boardsv1.SanctionKind{
	SanctionMute: boardsv1.SanctionKind_SANCTION_KIND_MUTE,
	SanctionBan:  boardsv1.SanctionKind_SANCTION_KIND_BAN,
}

func sanctionProto(s *Sanction, cards map[uuid.UUID]*socialv1.PublicUser) *boardsv1.Sanction {
	user := cards[s.UserID]
	if user == nil {
		// The admin list still needs to say who it is.
		user = &socialv1.PublicUser{Id: s.UserID.String()}
	}
	return &boardsv1.Sanction{
		Id:        s.ID.String(),
		User:      user,
		Kind:      sanctionKindToProto[s.Kind],
		Reason:    s.Reason,
		ExpiresAt: tsPtr(s.ExpiresAt),
		CreatedAt: ts(s.CreatedAt),
		LiftedAt:  tsPtr(s.LiftedAt),
	}
}

func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	return id, nil
}

func (h *Handler) ListBoards(ctx context.Context, req *connect.Request[boardsv1.ListBoardsRequest]) (*connect.Response[boardsv1.ListBoardsResponse], error) {
	boards, next, err := h.svc.ListBoards(ctx, req.Msg.Cursor, int(req.Msg.PageSize))
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	ids := make([]uuid.UUID, 0, len(boards))
	for _, b := range boards {
		ids = append(ids, b.CreatedBy)
	}
	cards := h.authorCards(ctx, ids)
	out := &boardsv1.ListBoardsResponse{NextCursor: next}
	for _, b := range boards {
		out.Boards = append(out.Boards, boardProto(b, cards))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) GetBoard(ctx context.Context, req *connect.Request[boardsv1.GetBoardRequest]) (*connect.Response[boardsv1.GetBoardResponse], error) {
	b, err := h.svc.GetBoard(ctx, req.Msg.Slug)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	cards := h.authorCards(ctx, []uuid.UUID{b.CreatedBy})
	return connect.NewResponse(&boardsv1.GetBoardResponse{Board: boardProto(b, cards)}), nil
}

func (h *Handler) ListPosts(ctx context.Context, req *connect.Request[boardsv1.ListPostsRequest]) (*connect.Response[boardsv1.ListPostsResponse], error) {
	in := ListInput{BoardSlug: req.Msg.BoardSlug, Cursor: req.Msg.Cursor, PageSize: int(req.Msg.PageSize)}
	if req.Msg.Sort == boardsv1.PostSort_POST_SORT_TOP {
		in.Sort = SortTop
	}
	switch req.Msg.Window {
	case boardsv1.TopWindow_TOP_WINDOW_DAY:
		in.Window = WindowDay
	case boardsv1.TopWindow_TOP_WINDOW_ALL:
		in.Window = WindowAll
	default:
		in.Window = WindowWeek
	}
	posts, next, err := h.svc.ListPosts(ctx, caller(ctx), in)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	ids := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.AuthorID)
	}
	cards := h.authorCards(ctx, ids)
	out := &boardsv1.ListPostsResponse{NextCursor: next}
	for _, p := range posts {
		out.Posts = append(out.Posts, postProto(p, cards))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) GetPost(ctx context.Context, req *connect.Request[boardsv1.GetPostRequest]) (*connect.Response[boardsv1.GetPostResponse], error) {
	id, err := parseID(req.Msg.Id)
	if err != nil {
		return nil, err
	}
	p, comments, err := h.svc.GetPost(ctx, caller(ctx), id)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	ids := []uuid.UUID{p.AuthorID}
	for _, c := range comments {
		ids = append(ids, c.AuthorID)
	}
	cards := h.authorCards(ctx, ids)
	out := &boardsv1.GetPostResponse{Post: postProto(p, cards)}
	for _, c := range comments {
		out.Comments = append(out.Comments, commentProto(c, cards))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) GetBoardsViewer(ctx context.Context, _ *connect.Request[boardsv1.GetBoardsViewerRequest]) (*connect.Response[boardsv1.GetBoardsViewerResponse], error) {
	c := caller(ctx)
	isAdmin, active, err := h.svc.Viewer(ctx, c)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	out := &boardsv1.GetBoardsViewerResponse{SignedIn: c.SignedIn(), IsAdmin: isAdmin}
	if active != nil {
		out.ActiveSanction = sanctionProto(active, nil)
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) CreateBoard(ctx context.Context, req *connect.Request[boardsv1.CreateBoardRequest]) (*connect.Response[boardsv1.CreateBoardResponse], error) {
	c := caller(ctx)
	b, err := h.svc.CreateBoard(ctx, c, req.Msg.Slug, req.Msg.Name, req.Msg.Description)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	cards := h.authorCards(ctx, []uuid.UUID{c.ID})
	return connect.NewResponse(&boardsv1.CreateBoardResponse{Board: boardProto(b, cards)}), nil
}

func (h *Handler) CreatePost(ctx context.Context, req *connect.Request[boardsv1.CreatePostRequest]) (*connect.Response[boardsv1.CreatePostResponse], error) {
	c := caller(ctx)
	in := PostInput{BoardSlug: req.Msg.BoardSlug, Title: req.Msg.Title, URL: req.Msg.Url, Body: req.Msg.Body}
	if a := req.Msg.Attachment; a != nil {
		in.AttachKind = kindFromProto[a.Kind]
		in.AttachRef = a.Ref
	}
	p, err := h.svc.CreatePost(ctx, c, in)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	cards := h.authorCards(ctx, []uuid.UUID{c.ID})
	return connect.NewResponse(&boardsv1.CreatePostResponse{Post: postProto(p, cards)}), nil
}

func (h *Handler) DeletePost(ctx context.Context, req *connect.Request[boardsv1.DeletePostRequest]) (*connect.Response[boardsv1.DeletePostResponse], error) {
	id, err := parseID(req.Msg.Id)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeletePost(ctx, caller(ctx), id); err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	return connect.NewResponse(&boardsv1.DeletePostResponse{}), nil
}

func (h *Handler) CreateComment(ctx context.Context, req *connect.Request[boardsv1.CreateCommentRequest]) (*connect.Response[boardsv1.CreateCommentResponse], error) {
	postID, err := parseID(req.Msg.PostId)
	if err != nil {
		return nil, err
	}
	var parentID *uuid.UUID
	if req.Msg.ParentId != "" {
		id, err := parseID(req.Msg.ParentId)
		if err != nil {
			return nil, err
		}
		parentID = &id
	}
	c := caller(ctx)
	cm, err := h.svc.CreateComment(ctx, c, postID, parentID, req.Msg.Body)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	cards := h.authorCards(ctx, []uuid.UUID{c.ID})
	return connect.NewResponse(&boardsv1.CreateCommentResponse{Comment: commentProto(cm, cards)}), nil
}

func (h *Handler) DeleteComment(ctx context.Context, req *connect.Request[boardsv1.DeleteCommentRequest]) (*connect.Response[boardsv1.DeleteCommentResponse], error) {
	id, err := parseID(req.Msg.Id)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteComment(ctx, caller(ctx), id); err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	return connect.NewResponse(&boardsv1.DeleteCommentResponse{}), nil
}

func (h *Handler) VotePost(ctx context.Context, req *connect.Request[boardsv1.VotePostRequest]) (*connect.Response[boardsv1.VotePostResponse], error) {
	id, err := parseID(req.Msg.PostId)
	if err != nil {
		return nil, err
	}
	score, err := h.svc.Vote(ctx, caller(ctx), id, int(req.Msg.Value))
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	return connect.NewResponse(&boardsv1.VotePostResponse{Score: int32(score), MyVote: req.Msg.Value}), nil
}

func (h *Handler) DeleteBoard(ctx context.Context, req *connect.Request[boardsv1.DeleteBoardRequest]) (*connect.Response[boardsv1.DeleteBoardResponse], error) {
	if err := h.svc.DeleteBoard(ctx, caller(ctx), req.Msg.Slug); err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	return connect.NewResponse(&boardsv1.DeleteBoardResponse{}), nil
}

func (h *Handler) SanctionUser(ctx context.Context, req *connect.Request[boardsv1.SanctionUserRequest]) (*connect.Response[boardsv1.SanctionUserResponse], error) {
	userID, err := parseID(req.Msg.UserId)
	if err != nil {
		return nil, err
	}
	kind := SanctionMute
	if req.Msg.Kind == boardsv1.SanctionKind_SANCTION_KIND_BAN {
		kind = SanctionBan
	}
	var expires *time.Time
	if req.Msg.ExpiresAt != nil {
		t := req.Msg.ExpiresAt.AsTime()
		expires = &t
	}
	s, err := h.svc.SanctionUser(ctx, caller(ctx), userID, kind, req.Msg.Reason, expires)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	cards := h.authorCards(ctx, []uuid.UUID{s.UserID})
	return connect.NewResponse(&boardsv1.SanctionUserResponse{Sanction: sanctionProto(s, cards)}), nil
}

func (h *Handler) LiftSanction(ctx context.Context, req *connect.Request[boardsv1.LiftSanctionRequest]) (*connect.Response[boardsv1.LiftSanctionResponse], error) {
	id, err := parseID(req.Msg.Id)
	if err != nil {
		return nil, err
	}
	if err := h.svc.LiftSanction(ctx, caller(ctx), id); err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	return connect.NewResponse(&boardsv1.LiftSanctionResponse{}), nil
}

func (h *Handler) ListSanctions(ctx context.Context, req *connect.Request[boardsv1.ListSanctionsRequest]) (*connect.Response[boardsv1.ListSanctionsResponse], error) {
	sanctions, err := h.svc.ListSanctions(ctx, caller(ctx), req.Msg.ActiveOnly)
	if err != nil {
		return nil, h.toConnectError(ctx, err)
	}
	ids := make([]uuid.UUID, 0, len(sanctions))
	for _, s := range sanctions {
		ids = append(ids, s.UserID)
	}
	cards := h.authorCards(ctx, ids)
	out := &boardsv1.ListSanctionsResponse{}
	for _, s := range sanctions {
		out.Sanctions = append(out.Sanctions, sanctionProto(s, cards))
	}
	return connect.NewResponse(out), nil
}
