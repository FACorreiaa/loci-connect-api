package runs

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
)

func TestStatusesToProto(t *testing.T) {
	sid := uuid.New()
	done := time.Now()
	got := StatusesToProto([]Run{
		{SessionID: sid, Domain: "accommodation", CityName: "Crete", Status: StatusDone, FinishedAt: &done},
		{SessionID: uuid.Nil, Status: StatusRunning}, // never attached: not reportable
	})
	require.Len(t, got, 1)
	require.Equal(t, sid.String(), got[0].GetSessionId())
	require.Equal(t, chatv1.RunStatus_RUN_STATUS_DONE, got[0].GetStatus())
	require.Equal(t, chatv1.DomainType_DOMAIN_TYPE_ACCOMMODATION, got[0].GetDomain())
	require.Equal(t, "/hotels?sessionId="+sid.String()+"&cityName=Crete&domain=hotels", got[0].GetUrl())
	require.NotNil(t, got[0].GetFinishedAt())
}

func TestDomainToProto(t *testing.T) {
	tests := []struct {
		domain string
		want   chatv1.DomainType
	}{
		{"accommodation", chatv1.DomainType_DOMAIN_TYPE_ACCOMMODATION},
		{"dining", chatv1.DomainType_DOMAIN_TYPE_DINING},
		{"activities", chatv1.DomainType_DOMAIN_TYPE_ACTIVITIES},
		{"itinerary", chatv1.DomainType_DOMAIN_TYPE_ITINERARY},
		{"transport", chatv1.DomainType_DOMAIN_TYPE_TRANSPORT},
		{"general", chatv1.DomainType_DOMAIN_TYPE_GENERAL},
		{"nearby", chatv1.DomainType_DOMAIN_TYPE_GENERAL},
		{"ACCOMMODATION", chatv1.DomainType_DOMAIN_TYPE_ACCOMMODATION}, // upper-case input
		{"Transport", chatv1.DomainType_DOMAIN_TYPE_TRANSPORT},
		{"made-up-domain", chatv1.DomainType_DOMAIN_TYPE_UNSPECIFIED}, // unknown input
		{"", chatv1.DomainType_DOMAIN_TYPE_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			require.Equal(t, tt.want, DomainToProto(tt.domain))
		})
	}
}
