package trip

import (
	"testing"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAssignLegIDs(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	old := []TripLeg{
		{ID: a, AfterDay: 0, FromName: "Home", ToName: "Porto"},
		{ID: b, AfterDay: 2, FromName: "Porto", ToName: "Lisbon"},
		{ID: c, AfterDay: 4, FromName: "Lisbon", ToName: "Faro"},
	}

	t.Run("explicit owned id is kept", func(t *testing.T) {
		got := assignLegIDs([]TripLeg{{ID: b, AfterDay: 3, FromName: "Porto", ToName: "Coimbra"}}, old)
		require.Equal(t, b, *got[0])
	})

	t.Run("no id matches the old leg on the same hop", func(t *testing.T) {
		got := assignLegIDs([]TripLeg{
			{AfterDay: 0, FromName: "home", ToName: " Porto "},
			{AfterDay: 2, FromName: "Porto", ToName: "Lisbon"},
		}, old)
		require.Equal(t, a, *got[0], "hop match is case and space insensitive")
		require.Equal(t, b, *got[1])
	})

	t.Run("new hop gets nil, removed legs are not reused", func(t *testing.T) {
		got := assignLegIDs([]TripLeg{{AfterDay: 2, FromName: "Porto", ToName: "Braga"}}, old)
		require.Nil(t, got[0])
	})

	t.Run("foreign id is not honoured but the hop still matches", func(t *testing.T) {
		got := assignLegIDs([]TripLeg{{ID: uuid.New(), AfterDay: 4, FromName: "Lisbon", ToName: "Faro"}}, old)
		require.Equal(t, c, *got[0])
	})

	t.Run("an id is never handed out twice", func(t *testing.T) {
		got := assignLegIDs([]TripLeg{
			{ID: a, AfterDay: 0, FromName: "Home", ToName: "Porto"},
			{ID: a, AfterDay: 0, FromName: "Home", ToName: "Porto"},
		}, old)
		require.Equal(t, a, *got[0])
		require.Nil(t, got[1])
	})

	t.Run("explicit id beats an earlier hop match", func(t *testing.T) {
		// Leg 0 has no id but sits on b's hop; leg 1 sends b explicitly.
		got := assignLegIDs([]TripLeg{
			{AfterDay: 2, FromName: "Porto", ToName: "Lisbon"},
			{ID: b, AfterDay: 2, FromName: "Porto", ToName: "Lisbon"},
		}, old)
		require.Nil(t, got[0])
		require.Equal(t, b, *got[1])
	})

	t.Run("new trip", func(t *testing.T) {
		got := assignLegIDs([]TripLeg{{ID: a, FromName: "Home", ToName: "Porto"}}, nil)
		require.Nil(t, got[0])
	})
}

func TestLegFromProtoCarriesID(t *testing.T) {
	id := uuid.New()
	require.Equal(t, id, legFromProto(&tripv1.TripLeg{Id: id.String(), FromName: "A", ToName: "B"}).ID)
	require.Equal(t, uuid.Nil, legFromProto(&tripv1.TripLeg{Id: "not-a-uuid", FromName: "A", ToName: "B"}).ID)
}
