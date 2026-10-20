// Package storage signs uploads to and downloads from an S3-compatible bucket
// (MinIO in production, apps/loci/data/minio.yaml in the infra repo).
//
// The API never streams object bytes: clients PUT to a presigned URL whose
// signature covers the content length and type, so the bucket itself refuses
// anything larger or of another type, and read through presigned GETs.
package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// ErrNotFound is an object that is not in the bucket.
var ErrNotFound = errors.New("object not found")

// Config is where the bucket lives. Endpoint must be the public name clients
// use: SigV4 signs the Host header, so a URL signed for an in-cluster name
// fails when a phone opens it.
type Config struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

// Enabled reports whether there is enough configuration to sign anything.
func (c Config) Enabled() bool {
	return c.Endpoint != "" && c.Bucket != "" && c.AccessKey != "" && c.SecretKey != ""
}

// Object is what HEAD reports about a stored object.
type Object struct {
	Size        int64
	ContentType string
}

// Signer signs uploads and downloads and manages objects. *S3 is the real one.
type Signer interface {
	// PutURL signs a PUT of exactly size bytes of contentType. The returned
	// headers must be sent with the request as they are.
	PutURL(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, map[string]string, error)
	GetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	Head(ctx context.Context, key string) (Object, error)
	Delete(ctx context.Context, key string) error
}

// S3 is a Signer over an S3-compatible bucket.
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3 builds the signer. It returns nil, nil when cfg is not Enabled, so
// callers treat storage as switched off rather than failing to boot.
func NewS3(cfg Config) (*S3, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	client := s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(strings.TrimRight(cfg.Endpoint, "/")),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		UsePathStyle: cfg.UsePathStyle,
		// MinIO does not need the SDK's default request checksums, and a
		// checksum header in a presigned PUT would have to be sent by the
		// client too.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &S3{client: client, presign: s3.NewPresignClient(client), bucket: cfg.Bucket}, nil
}

func (s *S3) PutURL(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, map[string]string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", nil, fmt.Errorf("sign put %s: %w", key, err)
	}
	return req.URL, signedHeaders(req.SignedHeader), nil
}

// signedHeaders is what the client must send: everything signed except Host,
// which the HTTP client sets itself from the URL.
func signedHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		if strings.EqualFold(k, "Host") || len(v) == 0 {
			continue
		}
		out[k] = v[0]
	}
	return out
}

func (s *S3) GetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("sign get %s: %w", key, err)
	}
	return req.URL, nil
}

func (s *S3) Head(ctx context.Context, key string) (Object, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey") {
			return Object{}, ErrNotFound
		}
		return Object{}, fmt.Errorf("head %s: %w", key, err)
	}
	return Object{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType)}, nil
}

// Delete removes an object; a missing one is not an error.
func (s *S3) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}
