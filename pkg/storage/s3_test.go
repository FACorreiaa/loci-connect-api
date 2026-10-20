package storage

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewS3_DisabledWithoutKeys(t *testing.T) {
	s, err := NewS3(Config{Endpoint: "https://media.example", Bucket: "b"})
	require.NoError(t, err)
	require.Nil(t, s)
}

// The PUT signature must cover length and type, so the bucket refuses a
// bigger body or another type; and the client must be told to send both.
func TestPutURL_SignsLengthAndType(t *testing.T) {
	s, err := NewS3(Config{Endpoint: "https://media.example", Bucket: "loci-media", AccessKey: "k", SecretKey: "s", UsePathStyle: true})
	require.NoError(t, err)

	raw, headers, err := s.PutURL(context.Background(), "trips/t/p.jpg", "image/jpeg", 1234, 10*time.Minute)
	require.NoError(t, err)

	u, err := url.Parse(raw)
	require.NoError(t, err)
	require.Equal(t, "media.example", u.Host)
	require.Equal(t, "/loci-media/trips/t/p.jpg", u.Path)
	signed := strings.Split(u.Query().Get("X-Amz-SignedHeaders"), ";")
	require.Contains(t, signed, "content-length")
	require.Contains(t, signed, "content-type")
	require.Equal(t, "600", u.Query().Get("X-Amz-Expires"))
	require.Equal(t, "image/jpeg", headers["Content-Type"])
	require.Equal(t, "1234", headers["Content-Length"])
	require.NotContains(t, headers, "Host")
}

func TestGetURL_PathStyle(t *testing.T) {
	s, err := NewS3(Config{Endpoint: "https://media.example/", Bucket: "loci-media", AccessKey: "k", SecretKey: "s", UsePathStyle: true})
	require.NoError(t, err)
	raw, err := s.GetURL(context.Background(), "trips/t/p.jpg", time.Hour)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(raw, "https://media.example/loci-media/trips/t/p.jpg?"), raw)
}
