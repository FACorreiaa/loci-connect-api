package calendar

import (
	"testing"
	"time"
)

func TestEncodeDecodeState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	state, err := encodeState("s3cret", "user-1", providerGoogle, now)
	if err != nil {
		t.Fatal(err)
	}
	uid, prov, err := decodeState("s3cret", state, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if uid != "user-1" || prov != providerGoogle {
		t.Fatalf("got %s %s", uid, prov)
	}
}

func TestDecodeStateRejectsWrongSecretAndExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	state, err := encodeState("s3cret", "user-1", providerGoogle, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeState("other", state, now); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if _, _, err := decodeState("s3cret", state, now.Add(11*time.Minute)); err == nil {
		t.Fatal("expired state accepted")
	}
}
