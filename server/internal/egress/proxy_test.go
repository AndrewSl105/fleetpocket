package egress

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A proxy whose names all resolve to a local test server.
func testProxy(t *testing.T, allow []string, private []string) (*httptest.Server, string, *[]string) {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello from "+r.Host)
	}))
	t.Cleanup(target.Close)
	_, tport, _ := net.SplitHostPort(target.Listener.Addr().String())
	var mu sync.Mutex
	var blocked []string
	p := &Proxy{
		Allow: NewAllowlist(allow), AllowPrivate: private,
		Blocked: func(h string) { mu.Lock(); blocked = append(blocked, h); mu.Unlock() },
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+tport)
		},
		Resolver: &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return nil, fmt.Errorf("no dns in tests")
		}},
	}
	// Names resolve through the resolver; tests use names that /etc/hosts
	// knows (localhost) so resolution does not need DNS.
	ps := httptest.NewServer(p)
	t.Cleanup(ps.Close)
	return ps, tport, &blocked
}

func TestProxyAllowsAndBlocks(t *testing.T) {
	ps, _, blocked := testProxy(t, []string{"localhost:80", "localhost:443"}, []string{"localhost"})
	proxyURL := ps.URL

	// Plain HTTP to an allowed host.
	res, body := viaProxy(t, proxyURL, "GET http://localhost/x HTTP/1.1\r\nHost: localhost\r\n\r\n")
	if res.StatusCode != 200 || !strings.Contains(body, "hello") {
		t.Fatalf("allowed http: %d %s", res.StatusCode, body)
	}
	// CONNECT to an allowed host tunnels.
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxyURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(conn, "CONNECT localhost:443 HTTP/1.1\r\nHost: localhost:443\r\n\r\n")
	br := bufio.NewReader(conn)
	r, err := http.ReadResponse(br, nil)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("connect: %v %v", r, err)
	}
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: tunnel\r\nConnection: close\r\n\r\n")
	r2, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r2.Body)
	if !strings.Contains(string(b), "hello from tunnel") {
		t.Fatalf("tunnel: %s", b)
	}
	conn.Close()

	// Not listed: 403 and reported once.
	for range 2 {
		res, _ = viaProxy(t, proxyURL, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("blocked connect: %d", res.StatusCode)
		}
	}
	res, _ = viaProxy(t, proxyURL, "GET http://localhost:22/ HTTP/1.1\r\nHost: localhost:22\r\n\r\n")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("unlisted port: %d", res.StatusCode)
	}
	time.Sleep(100 * time.Millisecond)
	if len(*blocked) != 2 || (*blocked)[0] != "example.com" {
		t.Fatalf("reports: %v", *blocked)
	}
}

func TestProxyRefusesPrivateAddressesOfAllowedNames(t *testing.T) {
	// localhost is allowed but not in AllowPrivate: it resolves to loopback.
	ps, _, blocked := testProxy(t, []string{"localhost"}, nil)
	res, body := viaProxy(t, ps.URL, "GET http://localhost/ HTTP/1.1\r\nHost: localhost\r\n\r\n")
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(body, "non-public") {
		t.Fatalf("private address: %d %s", res.StatusCode, body)
	}
	if len(*blocked) != 0 {
		t.Fatalf("a resolution refusal is not an allowlist report: %v", *blocked)
	}
}

func viaProxy(t *testing.T, proxyURL, raw string) (*http.Response, string) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxyURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, raw)
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}
