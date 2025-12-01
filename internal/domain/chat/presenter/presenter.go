package presenter

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat"
	cityv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/city"
	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/poi"

	"github.com/FACorreiaa/loci-connect-api/internal/types"
)

func ToChatResponse(resp *types.ChatResponse) *chatv1.ChatResponse {
	if resp == nil {
		return nil
	}
	return &chatv1.ChatResponse{
		SessionId:             resp.SessionID.String(),
		Message:               resp.Message,
		UpdatedItinerary:      ToAiCityResponse(resp.UpdatedItinerary),
		IsNewSession:          resp.IsNewSession,
		RequiresClarification: resp.RequiresClarification,
		SuggestedActions:      resp.SuggestedActions,
	}
}

func ToAiCityResponse(resp *types.AiCityResponse) *chatv1.AiCityResponse {
	if resp == nil {
		return nil
	}
	return &chatv1.AiCityResponse{
		GeneralCityData:   ToGeneralCityData(resp.GeneralCityData),
		PointsOfInterest:  ToPOIDetailedInfoSlice(resp.PointsOfInterest),
		ItineraryResponse: ToAIItineraryResponse(resp.AIItineraryResponse),
		SessionId:         resp.SessionID.String(),
	}
}

func ToGeneralCityData(data types.GeneralCityData) *cityv1.GeneralCityData {
	resp := &cityv1.GeneralCityData{
		City:        data.City,
		Country:     data.Country,
		Description: data.Description,
		Population:  data.Population,
		Area:        data.Area,
		Timezone:    data.Timezone,
		Language:    data.Language,
		Weather:     data.Weather,
		Attractions: data.Attractions,
		History:     data.History,
	}

	if data.StateProvince != "" {
		resp.StateProvince = proto.String(data.StateProvince)
	}
	if data.CenterLatitude != 0 {
		resp.CenterLatitude = proto.Float64(data.CenterLatitude)
	}
	if data.CenterLongitude != 0 {
		resp.CenterLongitude = proto.Float64(data.CenterLongitude)
	}

	return resp
}

func ToPOIDetailedInfoSlice(pois []types.POIDetailedInfo) []*poiv1.POIDetailedInfo {
	if pois == nil {
		return nil
	}
	result := make([]*poiv1.POIDetailedInfo, len(pois))
	for i, p := range pois {
		result[i] = ToPOIDetailedInfo(p)
	}
	return result
}

func ToPOIDetailedInfo(poi types.POIDetailedInfo) *poiv1.POIDetailedInfo {
	resp := &poiv1.POIDetailedInfo{
		Id:               poi.ID.String(),
		City:             poi.City,
		CityId:           poi.CityID.String(),
		Name:             poi.Name,
		Distance:         poi.Distance,
		Category:         poi.Category,
		Description:      poi.Description,
		Rating:           poi.Rating,
		Address:          poi.Address,
		PhoneNumber:      poi.PhoneNumber,
		Website:          poi.Website,
		OpeningHours:     poi.OpeningHours,
		Images:           poi.Images,
		PriceRange:       poi.PriceRange,
		PriceLevel:       poi.PriceLevel,
		Reviews:          poi.Reviews,
		LlmInteractionId: poi.LlmInteractionID.String(),
		Tags:             poi.Tags,
		Amenities:        poi.Amenities,
	}

	if poi.DescriptionPOI != "" {
		resp.DescriptionPoi = proto.String(poi.DescriptionPOI)
	}
	if poi.Latitude != 0 {
		resp.Latitude = proto.Float64(poi.Latitude)
	}
	if poi.Longitude != 0 {
		resp.Longitude = proto.Float64(poi.Longitude)
	}
	if poi.Priority != 0 {
		priority := int32(poi.Priority)
		resp.Priority = &priority
	}
	if !poi.CreatedAt.IsZero() {
		resp.CreatedAt = timestamppb.New(poi.CreatedAt)
	}
	if poi.CuisineType != "" {
		resp.CuisineType = proto.String(poi.CuisineType)
	}
	if poi.StarRating != "" {
		resp.StarRating = proto.String(poi.StarRating)
	}
	if poi.Source != "" {
		resp.Source = proto.String(poi.Source)
	}

	return resp
}

func ToAIItineraryResponse(resp types.AIItineraryResponse) *chatv1.AIItineraryResponse {
	return &chatv1.AIItineraryResponse{
		ItineraryName:      resp.ItineraryName,
		OverallDescription: resp.OverallDescription,
		PointsOfInterest:   ToPOIDetailedInfoSlice(resp.PointsOfInterest),
		Restaurants:        ToPOIDetailedInfoSlice(resp.Restaurants),
		Bars:               ToPOIDetailedInfoSlice(resp.Bars),
	}
}

func ToConversationMessage(msg types.ConversationMessage) *chatv1.ConversationMessage {
	role := chatv1.MessageRole_MESSAGE_ROLE_UNSPECIFIED
	switch msg.Role {
	case types.RoleUser:
		role = chatv1.MessageRole_MESSAGE_ROLE_USER
	case types.RoleAssistant:
		role = chatv1.MessageRole_MESSAGE_ROLE_ASSISTANT
	case types.RoleSystem:
		role = chatv1.MessageRole_MESSAGE_ROLE_SYSTEM
	}

	msgType := chatv1.MessageType_MESSAGE_TYPE_RESPONSE
	switch msg.MessageType {
	case types.TypeInitialRequest:
		msgType = chatv1.MessageType_MESSAGE_TYPE_INITIAL_REQUEST
	case types.TypeModificationRequest:
		msgType = chatv1.MessageType_MESSAGE_TYPE_MODIFICATION_REQUEST
	case types.TypeClarification:
		msgType = chatv1.MessageType_MESSAGE_TYPE_CLARIFICATION
	case types.TypeItineraryResponse:
		msgType = chatv1.MessageType_MESSAGE_TYPE_ITINERARY_RESPONSE
	case types.TypeError:
		msgType = chatv1.MessageType_MESSAGE_TYPE_ERROR
	}

	return &chatv1.ConversationMessage{
		Id:          msg.ID.String(),
		Role:        role,
		Content:     msg.Content,
		MessageType: msgType,
		Timestamp:   timestamppb.New(msg.Timestamp),
	}
}

func ToConversationMessages(msgs []types.ConversationMessage) []*chatv1.ConversationMessage {
	out := make([]*chatv1.ConversationMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, ToConversationMessage(m))
	}
	return out
}

func ToChatSession(session *types.ChatSession) *chatv1.ChatSession {
	if session == nil {
		return nil
	}
	var itinerary *chatv1.AiCityResponse
	if session.CurrentItinerary != nil {
		itinerary = ToAiCityResponse(session.CurrentItinerary)
	}

	status := chatv1.SessionStatus_SESSION_STATUS_UNSPECIFIED
	switch session.Status {
	case types.StatusActive:
		status = chatv1.SessionStatus_SESSION_STATUS_ACTIVE
	case types.StatusExpired:
		status = chatv1.SessionStatus_SESSION_STATUS_EXPIRED
	case types.StatusClosed:
		status = chatv1.SessionStatus_SESSION_STATUS_CLOSED
	}

	return &chatv1.ChatSession{
		Id:                  session.ID.String(),
		UserId:              session.UserID.String(),
		ProfileId:           session.ProfileID.String(),
		CityName:            session.CityName,
		CurrentItinerary:    itinerary,
		ConversationHistory: ToConversationMessages(session.ConversationHistory),
		CreatedAt:           timestamppb.New(session.CreatedAt),
		UpdatedAt:           timestamppb.New(session.UpdatedAt),
		ExpiresAt:           timestamppb.New(session.ExpiresAt),
		Status:              status,
	}
}

func ToGetChatSessionsResponse(resp *types.ChatSessionsResponse) *chatv1.GetChatSessionsResponse {
	if resp == nil {
		return &chatv1.GetChatSessionsResponse{}
	}
	out := &chatv1.GetChatSessionsResponse{
		Sessions: make([]*chatv1.ChatSession, 0, len(resp.Sessions)),
		Pagination: &commonpb.PaginationMetadata{
			TotalRecords: int32(resp.Total),
			Page:         int32(resp.Page),
			PageSize:     int32(resp.Limit),
		},
	}
	for _, s := range resp.Sessions {
		out.Sessions = append(out.Sessions, ToChatSession(&s))
	}
	return out
}
