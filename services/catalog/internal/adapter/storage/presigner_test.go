package storage_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/storage"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

const bucket = "product-images"

// setup starts MinIO, creates the bucket and returns a presigner plus a raw client.
func setup(t *testing.T) (*storage.Presigner, *minio.Client) {
	t.Helper()
	info := testutil.MinIO(t)
	raw, err := minio.New(info.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(info.AccessKey, info.SecretKey, ""), Region: "us-east-1"})
	require.NoError(t, err)
	require.NoError(t, raw.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{}))
	p, err := storage.New(storage.Config{
		Endpoint: info.Endpoint, AccessKey: info.AccessKey, SecretKey: info.SecretKey, Bucket: bucket,
		PublicBaseURL: "http://" + info.Endpoint + "/" + bucket,
	})
	require.NoError(t, err)
	return p, raw
}

// post submits the presigned form with a file of size bytes, as a browser would.
func post(t *testing.T, u domain.Upload, size int, contentType string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range u.Fields {
		require.NoError(t, w.WriteField(k, v))
	}
	part, err := w.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="img"`},
		"Content-Type":        {contentType},
	})
	require.NoError(t, err)
	_, err = part.Write(bytes.Repeat([]byte{0xff}, size))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u.URL, &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPresignedPostUploadsAnImage(t *testing.T) {
	p, raw := setup(t)
	shop := uuid.Must(uuid.NewV7())

	u, err := p.PresignImageUpload(context.Background(), shop, "image/png")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(u.ObjectKey, domain.ImageKeyPrefix(shop)))
	assert.True(t, strings.HasSuffix(u.ObjectKey, ".png"))
	assert.Equal(t, u.URL+"/"+u.ObjectKey, u.PublicURL)
	assert.Equal(t, u.ObjectKey, u.Fields["key"])

	code, body := post(t, u, 1024, "image/png")
	require.Equal(t, http.StatusNoContent, code, body)

	info, err := raw.StatObject(context.Background(), bucket, u.ObjectKey, minio.StatObjectOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(1024), info.Size)
	assert.Equal(t, "image/png", info.ContentType)
}

func TestPresignedPostRejectsFilesOver5MB(t *testing.T) {
	p, raw := setup(t)
	u, err := p.PresignImageUpload(context.Background(), uuid.Must(uuid.NewV7()), "image/jpeg")
	require.NoError(t, err)

	code, body := post(t, u, domain.MaxImageBytes+1, "image/jpeg")
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "EntityTooLarge")

	_, err = raw.StatObject(context.Background(), bucket, u.ObjectKey, minio.StatObjectOptions{})
	assert.Error(t, err, "nothing stored")
}

func TestPresignRejectsUnsupportedContentType(t *testing.T) {
	p, err := storage.New(storage.Config{Endpoint: "127.0.0.1:9", AccessKey: "a", SecretKey: "b", Bucket: bucket, PublicBaseURL: "http://x/" + bucket})
	require.NoError(t, err)
	_, err = p.PresignImageUpload(context.Background(), uuid.Must(uuid.NewV7()), "image/gif")
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, "content_type")
}

func TestPing(t *testing.T) {
	p, raw := setup(t)
	require.NoError(t, p.Ping(context.Background()))
	require.NoError(t, raw.RemoveBucket(context.Background(), bucket))
	assert.Error(t, p.Ping(context.Background()), "missing bucket is not ready")
}
