package testutil_test

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

func TestRedisAnswersPing(t *testing.T) {
	url := testutil.Redis(t)
	require.True(t, strings.HasPrefix(url, "redis://"))

	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", strings.TrimPrefix(url, "redis://"))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte("PING\r\n"))
	require.NoError(t, err)
	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "+PONG\r\n", line)
}

// Kafka returns only once the broker accepts connections, so the very first
// request a test makes (no retry of its own) succeeds.
func TestKafkaAnswersAtOnce(t *testing.T) {
	brokers := testutil.Kafka(t)
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	require.NoError(t, err)
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.Ping(ctx))
}

func TestMinIOIsLive(t *testing.T) {
	info := testutil.MinIO(t)
	assert.NotEmpty(t, info.AccessKey)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+info.Endpoint+"/minio/health/live", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
