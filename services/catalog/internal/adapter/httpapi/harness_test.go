package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi/grpctest"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/httpapi"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

const (
	specPath  = "../../../../../api/openapi/catalog.yaml"
	assetBase = "http://assets.test/product-images"
)

func TestMain(m *testing.M) { os.Exit(testsupport.RunWithPostgres(m)) }

// fakePresigner returns well-formed upload forms without MinIO.
type fakePresigner struct{}

func (fakePresigner) PresignImageUpload(_ context.Context, shopID uuid.UUID, contentType string) (domain.Upload, error) {
	ext, _ := domain.ImageExtension(contentType)
	key := domain.ImageKeyPrefix(shopID) + uuid.Must(uuid.NewV7()).String() + "." + ext
	return domain.Upload{URL: assetBase, Fields: map[string]string{"key": key, "Content-Type": contentType}, ObjectKey: key, PublicURL: assetBase + "/" + key}, nil
}

type harness struct {
	t        *testing.T
	pool     *pgxpool.Pool
	identity *grpctest.FakeIdentity
	url      string
	contract *http.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := testsupport.DB(t)
	store := postgres.New(pool)
	fake, conn := grpctest.StartFakeIdentity(t)
	router := httpapi.NewRouter(httpapi.Deps{
		Shops:    app.NewShopService(store, grpcapi.NewIdentityClient(conn)),
		Products: app.NewProductService(store, fakePresigner{}),
		Search:   app.NewSearchService(store),
		Assets:   assetBase,
	})
	srv := httptest.NewServer(httpx.RequestID(authctx.Middleware(router)))
	t.Cleanup(srv.Close)
	return &harness{
		t: t, pool: pool, identity: fake, url: srv.URL,
		contract: &http.Client{Transport: testutil.ContractTransport(t, specPath, nil)},
	}
}

type user struct {
	ID    uuid.UUID
	Roles string
}

func newUser() user { return user{ID: uuid.Must(uuid.NewV7()), Roles: "buyer"} }

// seller returns u with the seller role, as the gateway would after a token refresh.
func (u user) seller() user {
	u.Roles = "buyer,seller"
	return u
}

type result struct {
	Status int
	Body   []byte
}

func (r result) decode(t *testing.T, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(r.Body, v), string(r.Body))
}

func (r result) code(t *testing.T) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	r.decode(t, &e)
	return e.Error.Code
}

func (r result) fields(t *testing.T) map[string]string {
	t.Helper()
	var e struct {
		Error struct {
			Details struct {
				Fields map[string]string `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	r.decode(t, &e)
	return e.Error.Details.Fields
}

// call sends a request whose request and response must both satisfy catalog.yaml.
func (h *harness) call(method, path string, u *user, body any) result {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(h.t, err)
		rd = bytes.NewReader(b)
	}
	return h.send(h.contract, method, path, u, rd)
}

// raw sends a request that may violate the contract (invalid input tests).
func (h *harness) raw(method, path string, u *user, body string) result {
	h.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	return h.send(http.DefaultClient, method, path, u, rd)
}

func (h *harness) send(c *http.Client, method, path string, u *user, body io.Reader) result {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, h.url+path, body)
	require.NoError(h.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if u != nil {
		req.Header.Set(authctx.HeaderUserID, u.ID.String())
		req.Header.Set(authctx.HeaderUserRoles, u.Roles)
		req.Header.Set("Authorization", "Bearer test") // satisfies bearerAuth in the contract; the gateway strips it in production
	}
	resp, err := c.Do(req)
	require.NoError(h.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	return result{Status: resp.StatusCode, Body: b}
}

// seedProduct inserts an active product of a new shop via SQL (browse tests
// don't depend on the seller endpoints of Task 11).
func (h *harness) seedProduct(name string, price int64, status domain.ProductStatus) testsupport.SkuFixture {
	h.t.Helper()
	f := testsupport.SeedSku(h.t, h.pool, 5)
	_, err := h.pool.Exec(context.Background(),
		`UPDATE products SET name = $2, status = $3, min_price = $4 WHERE id = $1`, f.ProductID, name, string(status), price)
	require.NoError(h.t, err)
	_, err = h.pool.Exec(context.Background(), `UPDATE skus SET price = $2 WHERE id = $1`, f.SkuID, price)
	require.NoError(h.t, err)
	return f
}
