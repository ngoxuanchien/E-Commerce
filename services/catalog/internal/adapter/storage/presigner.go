// Package storage issues presigned MinIO (S3) POST uploads for product images.
package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// Config configures the presigner. PublicBaseURL is the browser-reachable,
// path-style bucket URL (PUBLIC_ASSET_BASE_URL); uploads are POSTed there and
// images are served from it. Endpoint may be an in-cluster address: a POST
// policy signature does not cover the host.
type Config struct {
	Endpoint      string
	AccessKey     string
	SecretKey     string
	Bucket        string
	UseSSL        bool
	PublicBaseURL string
}

// Presigner creates upload forms and checks bucket readiness.
type Presigner struct {
	client *minio.Client
	bucket string
	public domain.AssetBase
}

// New returns a Presigner. It does not contact MinIO.
func New(cfg Config) (*Presigner, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: "us-east-1", // fixed so presigning never needs a bucket-location round trip
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	return &Presigner{client: client, bucket: cfg.Bucket, public: domain.AssetBase(strings.TrimRight(cfg.PublicBaseURL, "/"))}, nil
}

// PresignImageUpload returns a POST form for one image of shopID: exact key
// shops/{shop_id}/{uuid}.{ext}, exact content type, 1 B–5 MB, valid 10 min.
func (p *Presigner) PresignImageUpload(ctx context.Context, shopID uuid.UUID, contentType string) (domain.Upload, error) {
	ext, ok := domain.ImageExtension(contentType)
	if !ok {
		return domain.Upload{}, &domain.ValidationError{Fields: map[string]string{
			"content_type": "must be one of: image/jpeg image/png image/webp",
		}}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.Upload{}, err
	}
	key := domain.ImageKeyPrefix(shopID) + id.String() + "." + ext

	policy := minio.NewPostPolicy()
	if err := errors.Join(
		policy.SetBucket(p.bucket),
		policy.SetKey(key),
		policy.SetContentType(contentType),
		policy.SetContentLengthRange(1, domain.MaxImageBytes),
		policy.SetExpires(time.Now().UTC().Add(domain.UploadTTL)),
	); err != nil {
		return domain.Upload{}, fmt.Errorf("build post policy: %w", err)
	}
	_, fields, err := p.client.PresignedPostPolicy(ctx, policy)
	if err != nil {
		return domain.Upload{}, fmt.Errorf("presign post policy: %w", err)
	}
	return domain.Upload{URL: string(p.public), Fields: fields, ObjectKey: key, PublicURL: p.public.URL(key)}, nil
}

// Ping reports whether the bucket exists and is reachable.
func (p *Presigner) Ping(ctx context.Context) error {
	ok, err := p.client.BucketExists(ctx, p.bucket)
	if err != nil {
		return fmt.Errorf("minio: %w", err)
	}
	if !ok {
		return fmt.Errorf("minio: bucket %q does not exist", p.bucket)
	}
	return nil
}
