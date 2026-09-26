package share

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	sharev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/share"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type ownedRepo struct {
	owner   uuid.UUID
	created []*Share
}

func (r *ownedRepo) Create(_ context.Context, s *Share) error {
	r.created = append(r.created, s)
	return nil
}
func (r *ownedRepo) GetByCode(context.Context, string) (*Share, error) { return nil, ErrNotFound }
func (r *ownedRepo) IncrementView(context.Context, string) (*Share, error) {
	return nil, ErrNotFound
}

func (r *ownedRepo) OwnsContent(_ context.Context, uid uuid.UUID, ct int32, _ string) (bool, error) {
	if _, owned := ownedContentTables[ct]; !owned {
		return true, nil
	}
	return uid == r.owner, nil
}

func TestCreateShareLinkChecksOwnership(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	repo := &ownedRepo{owner: owner}
	h := NewHandler("https://api.example", repo)
	as := func(uid uuid.UUID) context.Context {
		return context.WithValue(context.Background(), interceptors.UserIDKey, uid.String())
	}
	req := func(ct sharev1.ShareContentType) *connect.Request[sharev1.CreateShareLinkRequest] {
		return connect.NewRequest(&sharev1.CreateShareLinkRequest{
			UserId: "x", ContentType: ct, ContentId: uuid.NewString(), Title: "t",
		})
	}

	if _, err := h.CreateShareLink(as(other), req(sharev1.ShareContentType_SHARE_CONTENT_TYPE_TRIP)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("someone else's trip: code = %v, want NotFound", connect.CodeOf(err))
	}
	if _, err := h.CreateShareLink(as(other), req(sharev1.ShareContentType_SHARE_CONTENT_TYPE_LIST)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("someone else's list: code = %v, want NotFound", connect.CodeOf(err))
	}
	if _, err := h.CreateShareLink(as(owner), req(sharev1.ShareContentType_SHARE_CONTENT_TYPE_TRIP)); err != nil {
		t.Fatalf("owner sharing a trip: %v", err)
	}
	// Places are public: anyone may share one.
	if _, err := h.CreateShareLink(as(other), req(sharev1.ShareContentType_SHARE_CONTENT_TYPE_POI)); err != nil {
		t.Fatalf("sharing a place: %v", err)
	}
	if _, err := h.CreateShareLink(context.Background(), req(sharev1.ShareContentType_SHARE_CONTENT_TYPE_POI)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous share: code = %v, want Unauthenticated", connect.CodeOf(err))
	}
	if len(repo.created) != 2 {
		t.Fatalf("shares created = %d, want 2", len(repo.created))
	}
}
