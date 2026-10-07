package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the caller's address. X-Real-IP is honoured only when
// trustProxy is on and the direct peer is one of the trusted proxies, so a
// client talking to the API directly cannot spoof its address.
func ClientIP(trustProxy bool, trusted []netip.Prefix) func(*http.Request) string {
	return func(r *http.Request) string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !trustProxy {
			return host
		}
		peer, err := netip.ParseAddr(host)
		if err != nil {
			return host
		}
		peer = peer.Unmap()
		for _, p := range trusted {
			if p.Contains(peer) {
				if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
					return ip.Unmap().String()
				}
				break
			}
		}
		return host
	}
}
