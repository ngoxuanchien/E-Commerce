package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ClientIP resolves the originating client address for a request, honoring
// X-Forwarded-For only when the immediate peer (r.RemoteAddr) is a trusted
// reverse proxy. The zero value trusts no proxies, so Resolve always returns
// the peer address and ignores X-Forwarded-For entirely — safe for a
// gateway with no proxy in front of it.
type ClientIP struct {
	trusted []*net.IPNet
}

// ParseTrustedProxies parses entries as CIDRs (e.g. "10.0.0.0/8") or bare IPs
// (treated as a /32 or /128 network). Blank entries are ignored. It returns
// an error if any non-blank entry is neither a valid CIDR nor a valid IP.
func ParseTrustedProxies(entries []string) (ClientIP, error) {
	var nets []*net.IPNet
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(e); err == nil {
			nets = append(nets, ipnet)
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		return ClientIP{}, fmt.Errorf("ratelimit: invalid trusted proxy %q", e)
	}
	return ClientIP{trusted: nets}, nil
}

// isTrusted reports whether ip falls within any of c's trusted proxy ranges.
func (c ClientIP) isTrusted(ip net.IP) bool {
	for _, n := range c.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Resolve returns the client address for r. If the immediate peer is not a
// trusted proxy (including when c trusts no proxies at all), it returns the
// peer address and ignores X-Forwarded-For entirely — an untrusted peer
// could forge that header. Otherwise it walks the combined X-Forwarded-For
// hops right to left (the order proxies append in) and returns the first
// hop that is not itself a trusted proxy. An unparsable hop stops the walk,
// since it can no longer be trusted to identify a further client; in that
// case Resolve falls back to the peer address.
func (c ClientIP) Resolve(r *http.Request) string {
	peer := peerHost(r)
	peerIP := net.ParseIP(peer)
	if peerIP == nil || !c.isTrusted(peerIP) {
		return peer
	}

	hops := forwardedHops(r)
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(hops[i])
		if ip == nil {
			break
		}
		if !c.isTrusted(ip) {
			return hops[i]
		}
	}
	return peer
}

// peerHost extracts the host part of r.RemoteAddr, falling back to the raw
// value if it has no port (as net/http/httptest requests sometimes don't).
func peerHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// forwardedHops flattens every X-Forwarded-For header line into one ordered
// list of trimmed hops, left (original client) to right (most recent
// proxy) — multiple header lines are logically equivalent to one
// comma-joined list, in the order they appear.
func forwardedHops(r *http.Request) []string {
	var hops []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		for _, h := range strings.Split(line, ",") {
			hops = append(hops, strings.TrimSpace(h))
		}
	}
	return hops
}
