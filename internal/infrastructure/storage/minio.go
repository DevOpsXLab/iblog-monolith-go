// Package storage keeps uploaded files in MinIO (S3 compatible).
package storage

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type MinIO struct {
	client *minio.Client
	bucket string
}

// NewMinIO connects and creates the bucket when missing.
func NewMinIO(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) (*MinIO, error) {
	transport, err := minio.DefaultTransport(useSSL)
	if err != nil {
		return nil, err
	}
	c, err := minio.New(endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:    useSSL,
		Transport: otelhttp.NewTransport(transport), // span per S3 call
	})
	if err != nil {
		return nil, err
	}
	// MinIO may still be starting (compose has no healthcheck for it).
	var ok bool
	for attempt := 0; ; attempt++ {
		if ok, err = c.BucketExists(ctx, bucket); err == nil || attempt == 30 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, err
		}
	}
	return &MinIO{client: c, bucket: bucket}, nil
}

func (m *MinIO) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := m.client.PutObject(ctx, m.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (m *MinIO) Get(ctx context.Context, key string) (application.Object, error) {
	obj, err := m.client.GetObject(ctx, m.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return application.Object{}, err
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.Code == minio.NoSuchKey {
			return application.Object{}, domain.ErrNotFound
		}
		return application.Object{}, err
	}
	return application.Object{Body: obj, ContentType: info.ContentType, Size: info.Size}, nil
}

// Ping checks the bucket is reachable (health).
func (m *MinIO) Ping(ctx context.Context) error {
	_, err := m.client.BucketExists(ctx, m.bucket)
	return err
}
