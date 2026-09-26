package runs

import (
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"

	"github.com/google/uuid"
)

var protoStatus = map[Status]chatv1.RunStatus{
	StatusRunning: chatv1.RunStatus_RUN_STATUS_RUNNING,
	StatusDone:    chatv1.RunStatus_RUN_STATUS_DONE,
	StatusFailed:  chatv1.RunStatus_RUN_STATUS_FAILED,
}

// DomainToProto is the single string->DomainType mapper for the chat domain.
// It used to be duplicated (unexported) in chat/handler/chat_handler.go;
// that copy now calls this one instead.
func DomainToProto(d string) chatv1.DomainType {
	switch strings.ToLower(d) {
	case "accommodation":
		return chatv1.DomainType_DOMAIN_TYPE_ACCOMMODATION
	case "dining":
		return chatv1.DomainType_DOMAIN_TYPE_DINING
	case "activities":
		return chatv1.DomainType_DOMAIN_TYPE_ACTIVITIES
	case "itinerary":
		return chatv1.DomainType_DOMAIN_TYPE_ITINERARY
	case "transport":
		return chatv1.DomainType_DOMAIN_TYPE_TRANSPORT
	case "gastronomy":
		return chatv1.DomainType_DOMAIN_TYPE_GASTRONOMY
	case "general", "nearby":
		return chatv1.DomainType_DOMAIN_TYPE_GENERAL
	default:
		return chatv1.DomainType_DOMAIN_TYPE_UNSPECIFIED
	}
}

// StatusesToProto reports runs the client can act on: ones with a session.
func StatusesToProto(runs []Run) []*chatv1.RunInfo {
	out := make([]*chatv1.RunInfo, 0, len(runs))
	for _, r := range runs {
		if r.SessionID == uuid.Nil {
			continue
		}
		path, _, _ := ResultPath(r.Domain, r.SessionID, r.CityName, uuid.Nil)
		info := &chatv1.RunInfo{
			SessionId: r.SessionID.String(),
			Domain:    DomainToProto(r.Domain),
			CityName:  r.CityName,
			Status:    protoStatus[r.Status],
			ErrorCode: r.ErrorCode,
			Url:       path,
		}
		if r.FinishedAt != nil {
			info.FinishedAt = timestamppb.New(*r.FinishedAt)
		}
		out = append(out, info)
	}
	return out
}
