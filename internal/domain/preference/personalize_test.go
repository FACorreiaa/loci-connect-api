package preference

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type stubReader struct {
	emb []float32
	ok  bool
}

func (s stubReader) GetEmbedding(context.Context, uuid.UUID) ([]float32, bool, error) {
	return s.emb, s.ok, nil
}

func TestPersonalizeQuery(t *testing.T) {
	q := []float32{1, 0}
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	got := PersonalizeQuery(context.Background(), stubReader{emb: []float32{0, 1}, ok: true}, uid, q)
	if got[0] == 1 && got[1] == 0 {
		t.Fatal("expected blend away from pure query")
	}
	unchanged := PersonalizeQuery(context.Background(), stubReader{ok: false}, uid, q)
	if unchanged[0] != 1 || unchanged[1] != 0 {
		t.Fatal("missing prefs should leave query alone")
	}
}

func TestExperimentVariantIsStableAndHasControlHoldout(t *testing.T) {
	control := 0
	for index := 0; index < 1000; index++ {
		uid := uuid.NewSHA1(uuid.NameSpaceOID, []byte{byte(index >> 8), byte(index)})
		first := ExperimentVariant(uid)
		if first != ExperimentVariant(uid) {
			t.Fatal("variant assignment must be stable")
		}
		if first == "control" {
			control++
		}
	}
	if control < 70 || control > 130 {
		t.Fatalf("expected approximately 10%% control, got %d/1000", control)
	}
}
