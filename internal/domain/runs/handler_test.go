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
