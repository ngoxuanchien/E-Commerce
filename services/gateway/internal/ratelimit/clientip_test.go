package ratelimit

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientIP_Resolve_NoTrustedProxies_IgnoresForgedXFF(t *testing.T) {
	var c ClientIP // zero value: trusts nothing
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.5:1234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	assert.Equal(t, "203.0.113.5", c.Resolve(r))
}

func TestClientIP_Resolve_UntrustedPeer_IgnoresXFF(t *testing.T) {
	c, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.5:1234" // not in the trusted range
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	assert.Equal(t, "203.0.113.5", c.Resolve(r))
}

func TestClientIP_Resolve_TrustedPeer_PicksRightmostUntrustedHop(t *testing.T) {
	c, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")

	assert.Equal(t, "5.6.7.8", c.Resolve(r))
}

func TestClientIP_Resolve_SkipsTrustedHops(t *testing.T) {
	c, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.2")

	assert.Equal(t, "1.2.3.4", c.Resolve(r))
}

func TestClientIP_Resolve_MultipleHeaderLines(t *testing.T) {
	c, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Add("X-Forwarded-For", "1.2.3.4, 10.0.0.2")
	r.Header.Add("X-Forwarded-For", "10.0.0.3, 10.0.0.4")

	assert.Equal(t, "1.2.3.4", c.Resolve(r))
}

func TestClientIP_Resolve_NoXFF_ReturnsPeer(t *testing.T) {
	c, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"

	assert.Equal(t, "10.0.0.1", c.Resolve(r))
}

func TestClientIP_Resolve_GarbageHopStopsWalk_FallsBackToPeer(t *testing.T) {
	c, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "not-an-ip")

	assert.Equal(t, "10.0.0.1", c.Resolve(r))
}

func TestClientIP_Resolve_IPv6Peer(t *testing.T) {
	var c ClientIP
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[2001:db8::1]:1234"

	assert.Equal(t, "2001:db8::1", c.Resolve(r))
}

func TestParseTrustedProxies_RejectsInvalidEntry(t *testing.T) {
	_, err := ParseTrustedProxies([]string{"bogus"})
	assert.Error(t, err)
}

func TestParseTrustedProxies_IgnoresBlankEntries(t *testing.T) {
	c, err := ParseTrustedProxies([]string{"", "   ", "10.0.0.0/8"})
	require.NoError(t, err)
	assert.True(t, c.isTrusted(net.ParseIP("10.1.2.3")))
}

func TestParseTrustedProxies_AcceptsBareIP(t *testing.T) {
	c, err := ParseTrustedProxies([]string{"192.168.1.1"})
	require.NoError(t, err)
	assert.True(t, c.isTrusted(net.ParseIP("192.168.1.1")))
	assert.False(t, c.isTrusted(net.ParseIP("192.168.1.2")))
}
