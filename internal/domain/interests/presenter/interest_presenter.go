package presenter

import (
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	interestv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/interest"
)

func ToInterestProto(interest *locitypes.Interest) *interestv1.Interest {
	if interest == nil {
		return nil
	}
	// interest.Description and interest.Active are already pointers in internal type
	return &interestv1.Interest{
		Id:          interest.ID.String(),
		Name:        interest.Name,
		Description: interest.Description,
		Active:      interest.Active,
	}
}

func ToInterestProtos(interests []*locitypes.Interest) []*interestv1.Interest {
	protos := make([]*interestv1.Interest, len(interests))
	for i, interest := range interests {
		protos[i] = ToInterestProto(interest)
	}
	return protos
}

func FromUpdateProto(req *interestv1.UpdateInterestRequest) (locitypes.UpdateinterestsParams, error) {
	return locitypes.UpdateinterestsParams{
		Name:        &req.Name,
		Description: req.Description,
		Active:      &req.Active,
	}, nil
}
