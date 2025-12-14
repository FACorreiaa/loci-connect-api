package presenter

import (
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	tagsv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/tags"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ToTagProto converts a domain Tags to a proto Tag
func ToTagProto(tag *locitypes.Tags) *tagsv1.Tag {
	if tag == nil {
		return nil
	}

	protoTag := &tagsv1.Tag{
		Id:        tag.ID.String(),
		Name:      tag.Name,
		TagType:   tag.TagType,
		CreatedAt: timestamppb.New(tag.CreatedAt),
	}

	if tag.Description != nil {
		protoTag.Description = tag.Description
	}
	if tag.Source != nil {
		protoTag.Source = tag.Source
	}
	if tag.Active != nil {
		protoTag.Active = *tag.Active
	}
	if tag.UpdatedAt != nil {
		protoTag.UpdatedAt = timestamppb.New(*tag.UpdatedAt)
	}

	return protoTag
}

// ToTagProtos converts a slice of domain Tags to proto Tags
func ToTagProtos(tags []*locitypes.Tags) []*tagsv1.Tag {
	result := make([]*tagsv1.Tag, 0, len(tags))
	for _, tag := range tags {
		result = append(result, ToTagProto(tag))
	}
	return result
}

// ToPersonalTagProto converts a domain PersonalTag to a proto PersonalTag
func ToPersonalTagProto(tag *locitypes.PersonalTag) *tagsv1.PersonalTag {
	if tag == nil {
		return nil
	}

	protoTag := &tagsv1.PersonalTag{
		Id:        tag.ID.String(),
		UserId:    tag.UserID.String(),
		Name:      tag.Name,
		TagType:   tag.TagType,
		Source:    tag.Source,
		CreatedAt: timestamppb.New(tag.CreatedAt),
	}

	if tag.Description != nil {
		protoTag.Description = tag.Description
	}
	if tag.UpdatedAt != nil {
		protoTag.UpdatedAt = timestamppb.New(*tag.UpdatedAt)
	}

	return protoTag
}

// FromCreateRequest converts a CreateTagRequest to CreatePersonalTagParams
func FromCreateRequest(req *tagsv1.CreateTagRequest) locitypes.CreatePersonalTagParams {
	active := req.Active
	return locitypes.CreatePersonalTagParams{
		Name:        req.Name,
		Description: req.Description,
		TagType:     req.TagType,
		Active:      &active,
	}
}

// FromUpdateRequest converts an UpdateTagRequest to UpdatePersonalTagParams
func FromUpdateRequest(req *tagsv1.UpdateTagRequest) locitypes.UpdatePersonalTagParams {
	return locitypes.UpdatePersonalTagParams{
		Name:        req.Name,
		Description: req.Description,
		TagType:     req.TagType,
		Active:      req.Active,
	}
}
