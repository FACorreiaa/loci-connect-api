package handler

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	userpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/user"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// recordingUserService captures the id the handler asked for, which is the
// only thing these tests care about.
type recordingUserService struct {
	askedFor uuid.UUID
}

func (r *recordingUserService) GetUserProfile(_ context.Context, userID uuid.UUID) (*locitypes.UserProfile, error) {
	r.askedFor = userID
	return &locitypes.UserProfile{ID: userID}, nil
}

func (r *recordingUserService) UpdateUserProfile(context.Context, uuid.UUID, locitypes.UpdateProfileParams) error {
	return nil
}
func (r *recordingUserService) UpdateLastLogin(context.Context, uuid.UUID) error     { return nil }
func (r *recordingUserService) MarkEmailAsVerified(context.Context, uuid.UUID) error { return nil }
func (r *recordingUserService) DeactivateUser(context.Context, uuid.UUID) error      { return nil }
func (r *recordingUserService) ReactivateUser(context.Context, uuid.UUID) error      { return nil }
func (r *recordingUserService) DeleteAccount(context.Context, uuid.UUID) error       { return nil }

// GetUserProfile used to read user_id out of the request body and fall back to
// the token only when it was empty. Anyone with an account could then read
// anyone else's profile by naming them in the body. The token is now the only
// thing that decides whose profile comes back.
func TestGetUserProfileIgnoresUserIDInRequestBody(t *testing.T) {
	caller := uuid.New()
	victim := uuid.New()

	svc := &recordingUserService{}
	h := NewUserHandler(svc)

	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, caller.String())
	victimID := victim.String()
	resp, err := h.GetUserProfile(ctx, connect.NewRequest(&userpb.GetUserProfileRequest{
		UserId: &victimID,
	}))
	if err != nil {
		t.Fatalf("GetUserProfile returned %v, want nil", err)
	}

	if svc.askedFor != caller {
		t.Errorf("service was asked for %s, want the caller %s", svc.askedFor, caller)
	}
	if svc.askedFor == victim {
		t.Fatal("handler fetched the profile named in the request body — the IDOR is back")
	}
	if got := resp.Msg.GetProfile().GetId(); got != caller.String() {
		t.Errorf("returned profile id = %s, want the caller %s", got, caller)
	}
}

// Without a token there is nobody to act as, whatever the body claims.
func TestGetUserProfileWithoutTokenIsUnauthenticated(t *testing.T) {
	someone := uuid.New().String()
	h := NewUserHandler(&recordingUserService{})

	_, err := h.GetUserProfile(context.Background(), connect.NewRequest(&userpb.GetUserProfileRequest{
		UserId: &someone,
	}))
	if err == nil {
		t.Fatal("GetUserProfile with no token = nil error, want unauthenticated")
	}
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want %v", code, connect.CodeUnauthenticated)
	}
}
