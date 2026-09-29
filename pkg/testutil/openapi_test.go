package testutil_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

const specDir = "../../api/openapi"

// TestContractsLoad proves every service spec is valid for kin-openapi (the
// validator WS1–WS3 use), including cross-file $refs to common.yaml.
func TestContractsLoad(t *testing.T) {
	for _, name := range []string{"identity.yaml", "catalog.yaml", "order.yaml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(specDir, name)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Skipf("%s not written yet", name)
			}
			doc := testutil.LoadOpenAPI(t, path)
			assert.NotEmpty(t, doc.Paths.Map())
		})
	}
}

func TestAllOpenAPISpecsLoad(t *testing.T) {
	for _, name := range []string{"common", "identity", "catalog", "order", "payment", "ledger"} {
		t.Run(name, func(t *testing.T) {
			doc := testutil.LoadOpenAPI(t, filepath.Join(specDir, name+".yaml"))
			if name != "payment" {
				return
			}
			op := doc.Paths.Find("/api/payments/vnpay/ipn").Get
			require.NotNil(t, op)
			assert.EqualValues(t, true, op.Extensions["x-provider-callback"], "provider callbacks are marked")
			require.NotNil(t, op.Security)
			assert.Empty(t, *op.Security, "provider callbacks are public")
		})
	}
}

func TestContractTransport(t *testing.T) {
	var respBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	defer srv.Close()
	client := &http.Client{Transport: testutil.ContractTransport(t, filepath.Join(specDir, "identity.yaml"), nil)}

	get := func() error {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/users/me", nil)
		req.Header.Set("Authorization", "Bearer x")
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	respBody = `{"id":"0192a1b2-0000-7000-8000-000000000001","email":"a@example.test","full_name":"A","roles":["buyer"],"created_at":"2026-09-28T10:00:00Z"}`
	assert.NoError(t, get())

	respBody = `{"id":"not-a-uuid"}`
	err := get()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response violates contract")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/register", strings.NewReader(`{"email":"a@example.test"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request violates contract")
}

// TestContractTransport_StrictFormatsAndStatus proves ContractTransport
// rejects responses that satisfy the required-fields shape but violate a
// declared string format (uuid, email), and rejects an undocumented status
// code, while still accepting a fully valid response.
func TestContractTransport_StrictFormatsAndStatus(t *testing.T) {
	const validUser = `{"id":"0192a1b2-0000-7000-8000-000000000001","email":"a@example.test","full_name":"A","roles":["buyer"],"created_at":"2026-09-28T10:00:00Z"}`

	tests := []struct {
		name       string
		respStatus int
		respBody   string
		wantErr    string
	}{
		{
			name:       "valid response still passes",
			respStatus: http.StatusOK,
			respBody:   validUser,
		},
		{
			name:       "invalid uuid id is rejected",
			respStatus: http.StatusOK,
			respBody:   `{"id":"not-a-uuid","email":"a@example.test","full_name":"A","roles":["buyer"],"created_at":"2026-09-28T10:00:00Z"}`,
			wantErr:    "response violates contract",
		},
		{
			name:       "invalid email is rejected",
			respStatus: http.StatusOK,
			respBody:   `{"id":"0192a1b2-0000-7000-8000-000000000001","email":"nope","full_name":"A","roles":["buyer"],"created_at":"2026-09-28T10:00:00Z"}`,
			wantErr:    "response violates contract",
		},
		{
			name:       "undocumented status is rejected",
			respStatus: http.StatusTeapot,
			respBody:   validUser,
			wantErr:    "response violates contract",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.respStatus)
				_, _ = io.WriteString(w, tc.respBody)
			}))
			defer srv.Close()
			client := &http.Client{Transport: testutil.ContractTransport(t, filepath.Join(specDir, "identity.yaml"), nil)}

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/users/me", nil)
			req.Header.Set("Authorization", "Bearer x")
			resp, err := client.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
