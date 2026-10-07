package httpx

import (
	"net/http"
	"net/netip"
	"strings"
)

// The client address behind a reverse proxy. The peer of the connection is the proxy, so the address of the person is what the proxy wrote in X-Forwarded-For. That header is
// believed only when the connection comes from a **trusted proxy** (PMS_TRUSTED_PROXIES); from anyone else it is ignored, because anybody can send it. Nothing else is read:
// not X-Real-IP, not Forwarded.
//
// The rule, for a trusted peer: read the list of X-Forwarded-For from the right. Every address on the right that is itself a trusted proxy is another hop of ours and is skipped;
// the first address that is not trusted is the client, because it is the last one a trusted hop vouched for. Whatever stands to the left of it was written by the client or by
// someone before our proxies, and is not read.

// ResolveClientIP returns the address that a request counts as coming from, and false when the peer address cannot be read.
func ResolveClientIP(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		// an address without a port (a test, a unix socket proxy): accept a bare address
		a, aerr := netip.ParseAddr(strings.TrimSpace(r.RemoteAddr))
		if aerr != nil {
			return netip.Addr{}, false
		}
		ap = netip.AddrPortFrom(a, 0)
	}
	peer := normalize(ap.Addr())
	if len(trusted) == 0 || !isTrusted(peer, trusted) {
		return peer, true
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, item := range strings.Split(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				hops = append(hops, item)
			}
		}
	}
	if len(hops) == 0 {
		return peer, true // a proxy that sent no header: the proxy itself is the best we know
	}
	var first netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseHop(hops[i])
		if !ok {
			// garbage in the list: nothing to its left can be believed either, so the last hop that was understood decides
			if first.IsValid() {
				return first, true
			}
			return peer, true
		}
		first = a
		if !isTrusted(a, trusted) {
			return a, true
		}
	}
	return first, true // every hop is one of ours (an internal caller): the leftmost is the origin
}

// parseHop reads one entry of X-Forwarded-For: an address, with or without a port, an IPv6 address in brackets or not.
func parseHop(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return normalize(ap.Addr()), true
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return normalize(a), true
}

func normalize(a netip.Addr) netip.Addr { return a.Unmap().WithZone("") }

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// SecurityHeaders sets the headers that make sense on every answer of the API, wherever it is served from: no sniffing of the content type and no framing. The headers that belong to
// the page (a content security policy, HSTS) are set by the reverse proxy that serves the page and terminates TLS.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
