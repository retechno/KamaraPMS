package httpx

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func prefixes(t *testing.T, list ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, s := range list {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a := netip.MustParseAddr(s)
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p)
	}
	return out
}

func request(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestClientIPOfADirectClientIsThePeerWhateverTheHeaderSays(t *testing.T) {
	for _, trusted := range [][]netip.Prefix{nil, prefixes(t, "172.29.0.10")} {
		// a client that sends the header itself: nobody vouches for it
		ip, ok := ResolveClientIP(request("203.0.113.9:51000", "198.51.100.1"), trusted)
		if !ok || ip.String() != "203.0.113.9" {
			t.Fatalf("direct client with a forged header: %v %v", ip, ok)
		}
	}
	// IPv6 and IPv4-mapped peers
	if ip, _ := ResolveClientIP(request("[2001:db8::7]:443"), nil); ip.String() != "2001:db8::7" {
		t.Fatalf("ipv6: %v", ip)
	}
	if ip, _ := ResolveClientIP(request("[::ffff:203.0.113.9]:443"), nil); ip.String() != "203.0.113.9" {
		t.Fatalf("mapped: %v", ip)
	}
	if _, ok := ResolveClientIP(request("not an address"), nil); ok {
		t.Fatal("an unreadable peer has no client address")
	}
}

func TestClientIPBehindATrustedProxy(t *testing.T) {
	trusted := prefixes(t, "172.29.0.10", "10.0.0.0/8")
	cases := []struct {
		name   string
		remote string
		xff    []string
		want   string
	}{
		{"the proxy forwards the client", "172.29.0.10:40000", []string{"198.51.100.23"}, "198.51.100.23"},
		{"the port of the hop is dropped", "172.29.0.10:40000", []string{"198.51.100.23:5555"}, "198.51.100.23"},
		{"an IPv6 client", "172.29.0.10:40000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"an IPv6 client in brackets with a port", "172.29.0.10:40000", []string{"[2001:db8::1]:8443"}, "2001:db8::1"},
		{"a second proxy of ours is skipped", "172.29.0.10:40000", []string{"198.51.100.23, 10.1.2.3"}, "198.51.100.23"},
		{"two header lines are one list", "172.29.0.10:40000", []string{"198.51.100.23", "10.1.2.3"}, "198.51.100.23"},
		{"what the client wrote to the left is not read", "172.29.0.10:40000", []string{"1.2.3.4, 198.51.100.23"}, "198.51.100.23"},
		{"a forged first entry cannot hide the real one", "172.29.0.10:40000", []string{"1.1.1.1, 2.2.2.2, 198.51.100.23, 10.9.9.9"}, "198.51.100.23"},
		{"no header: the proxy is the best known", "172.29.0.10:40000", nil, "172.29.0.10"},
		{"every hop is ours: the origin is the leftmost", "172.29.0.10:40000", []string{"10.5.5.5, 10.6.6.6"}, "10.5.5.5"},
		{"garbage at the right decides nothing about the left", "172.29.0.10:40000", []string{"198.51.100.23, not-an-ip"}, "172.29.0.10"},
		{"garbage to the left of a good hop is not read", "172.29.0.10:40000", []string{"junk, 198.51.100.23"}, "198.51.100.23"},
		{"a range member as the proxy", "10.8.8.8:1234", []string{"198.51.100.24"}, "198.51.100.24"},
		{"an empty value", "172.29.0.10:40000", []string{" , "}, "172.29.0.10"},
	}
	for _, c := range cases {
		ip, ok := ResolveClientIP(request(c.remote, c.xff...), trusted)
		if !ok || ip.String() != c.want {
			t.Errorf("%s: got %v, want %s", c.name, ip, c.want)
		}
	}
}

// A peer that is not a trusted proxy cannot choose its address by sending the header, whatever it puts in it, even the address of a trusted proxy.
func TestHeaderSpoofingFromAnUntrustedPeerIsIgnored(t *testing.T) {
	trusted := prefixes(t, "172.29.0.10")
	for _, xff := range []string{"1.2.3.4", "172.29.0.10", "1.2.3.4, 172.29.0.10", "::1", "127.0.0.1"} {
		ip, _ := ResolveClientIP(request("203.0.113.50:9999", xff), trusted)
		if ip.String() != "203.0.113.50" {
			t.Errorf("X-Forwarded-For %q from an untrusted peer moved the client to %v", xff, ip)
		}
	}
	// the other headers a proxy might add are never read, even from a trusted peer
	r := request("172.29.0.10:40000")
	r.Header.Set("X-Real-IP", "198.51.100.77")
	r.Header.Set("Forwarded", "for=198.51.100.78")
	if ip, _ := ResolveClientIP(r, trusted); ip.String() != "172.29.0.10" {
		t.Errorf("only X-Forwarded-For is read: %v", ip)
	}
}

// The middleware stores the resolved address, and the rate limiter counts it: clients behind one trusted proxy have a bucket each, and a client that forges the header from
// outside is still counted as itself.
func TestRateLimitCountsTheRealClientBehindATrustedProxy(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	handler := func(trusted []netip.Prefix) http.Handler {
		ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
		return Chain(ok, RequestID(slog.New(slog.NewTextHandler(io.Discard, nil)), trusted...), RateLimit(3, func() time.Time { return now }))
	}
	call := func(h http.Handler, remote string, xff string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	// without a trusted-proxy setting every client looks like the proxy: one bucket for everybody (the failure this setting exists for)
	blind := handler(nil)
	for i := 0; i < 3; i++ {
		call(blind, "172.29.0.10:1000", "198.51.100."+string(rune('1'+i)))
	}
	if code := call(blind, "172.29.0.10:1000", "198.51.100.9"); code != http.StatusTooManyRequests {
		t.Fatalf("without the setting a fourth person is refused (one bucket): %d", code)
	}

	// with it, each person behind the proxy has a bucket
	aware := handler(prefixes(t, "172.29.0.10"))
	for _, person := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		for i := 0; i < 3; i++ {
			if code := call(aware, "172.29.0.10:1000", person); code != http.StatusNoContent {
				t.Fatalf("%s request %d: %d", person, i, code)
			}
		}
		if code := call(aware, "172.29.0.10:1000", person); code != http.StatusTooManyRequests {
			t.Fatalf("%s is over its own limit: %d", person, code)
		}
	}
	if code := call(aware, "172.29.0.10:1000", "198.51.100.4"); code != http.StatusNoContent {
		t.Fatalf("a fourth person has a bucket of their own: %d", code)
	}

	// a client that talks to the API directly and writes a different address in every request is still one client
	for i := 0; i < 3; i++ {
		if code := call(aware, "203.0.113.50:7000", "9.9.9."+string(rune('1'+i))); code != http.StatusNoContent {
			t.Fatalf("direct request %d: %d", i, code)
		}
	}
	if code := call(aware, "203.0.113.50:7000", "9.9.9.9"); code != http.StatusTooManyRequests {
		t.Fatalf("a forged header must not give a direct client a fresh bucket: %d", code)
	}
	// and one that imitates a person behind the proxy takes that person's bucket from nobody
	if code := call(aware, "203.0.113.51:7000", "198.51.100.1"); code != http.StatusNoContent {
		t.Fatalf("a direct client claiming a person's address uses its own bucket: %d", code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers: %v", w.Header())
	}
}

// A probe that succeeds is not worth a line at the default level; one that fails, and every other request, is.
func TestAccessLogKeepsProbesQuiet(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	status := http.StatusOK
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }), RequestID(logger), AccessLog)
	do := func(path string) {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	do("/healthz")
	do("/readyz")
	if buf.Len() != 0 {
		t.Fatalf("a successful probe is logged at debug level only: %s", buf.String())
	}
	do("/api/v1/properties")
	status = http.StatusServiceUnavailable
	do("/readyz")
	lines := bytes.Count(buf.Bytes(), []byte("\n"))
	if lines != 2 || !bytes.Contains(buf.Bytes(), []byte(`"path":"/readyz"`)) || !bytes.Contains(buf.Bytes(), []byte(`"client_ip"`)) {
		t.Fatalf("an ordinary request and a failing probe are logged, with the client address: %s", buf.String())
	}
}
