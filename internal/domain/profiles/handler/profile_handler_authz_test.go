package handler

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// recordingProfileService captures whose profiles were asked for.
type recordingProfileService struct {
	askedFor uuid.UUID
}

func (r *recordingProfileService) GetSearchProfiles(_ context.Context, userID uuid.UUID) ([]locitypes.UserPreferenceProfileResponse, error) {
	r.askedFor = userID
	return nil, nil
}

func (r *recordingProfileService) GetSearchProfile(context.Context, uuid.UUID, uuid.UUID) (*locitypes.UserPreferenceProfileResponse, error) {
	return nil, nil
}

func (r *recordingProfileService) GetDefaultSearchProfile(context.Context, uuid.UUID) (*locitypes.UserPreferenceProfileResponse, error) {
	return nil, nil
}

func (r *recordingProfileService) CreateSearchProfile(context.Context, uuid.UUID, locitypes.CreateUserPreferenceProfileParams) (*locitypes.UserPreferenceProfileResponse, error) {
	return nil, nil
}

func (r *recordingProfileService) UpdateSearchProfile(context.Context, uuid.UUID, uuid.UUID, locitypes.UpdateSearchProfileParams) error {
	return nil
}

func (r *recordingProfileService) DeleteSearchProfile(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (r *recordingProfileService) SetDefaultSearchProfile(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

// Travel profiles carry where somebody wants to go and how they like to
// travel. GetUserPreferenceProfiles used to hand them to whoever named the
// owner in the request body. The token decides now.
func TestGetUserPreferenceProfilesIgnoresUserIDInRequestBody(t *testing.T) {
	caller := uuid.New()
	victim := uuid.New()

	svc := &recordingProfileService{}
	h := NewProfileHandler(svc)

	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, caller.String())
	victimID := victim.String()
	_, err := h.GetUserPreferenceProfiles(ctx, connect.NewRequest(&profilev1.GetUserPreferenceProfilesRequest{
		UserId: &victimID,
	}))
	if err != nil {
		t.Fatalf("GetUserPreferenceProfiles returned %v, want nil", err)
	}

	if svc.askedFor != caller {
		t.Errorf("service was asked for %s, want the caller %s", svc.askedFor, caller)
	}
	if svc.askedFor == victim {
		t.Fatal("handler listed the travel profiles named in the request body — the IDOR is back")
	}
}

func TestGetUserPreferenceProfilesWithoutTokenIsUnauthenticated(t *testing.T) {
	h := NewProfileHandler(&recordingProfileService{})

	someone := uuid.New().String()
	_, err := h.GetUserPreferenceProfiles(context.Background(), connect.NewRequest(&profilev1.GetUserPreferenceProfilesRequest{
		UserId: &someone,
	}))
	if err == nil {
		t.Fatal("GetUserPreferenceProfiles with no token = nil error, want unauthenticated")
	}
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want %v", code, connect.CodeUnauthenticated)
	}
}
