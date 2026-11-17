package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Proxy is an HTTP forward proxy (CONNECT and absolute-form requests) that
// only reaches allowed hosts.
type Proxy struct {
	Allow *Allowlist
	// AllowPrivate are hosts that may resolve to private, loopback or
	// link-local addresses (the server's gateway); every other destination
	// must be public, so an allowed name cannot point into the host's networks.
	AllowPrivate []string
	// Blocked is called once per denied host.
	Blocked func(host string)
	// Resolver and Dial are overridden in tests.
	Resolver *net.Resolver
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error)

	mu       sync.Mutex
	reported map[string]bool
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	if !r.URL.IsAbs() || r.URL.Scheme != "http" {
		http.Error(w, "this is an egress proxy: send absolute http:// URLs or CONNECT", http.StatusBadRequest)
		return
	}
	host, port := splitHostPort(r.URL.Host, "80")
	conn, err := p.dial(r.Context(), host, port)
	if err != nil {
		p.deny(w, host, err)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	tr := &http.Transport{
		DialContext:       func(context.Context, string, string) (net.Conn, error) { return conn, nil },
		DisableKeepAlives: true,
	}
	res, err := tr.RoundTrip(out)
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	for _, h := range hopHeaders {
		res.Header.Del(h)
	}
	for k, vs := range res.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	host, port := splitHostPort(r.Host, "443")
	up, err := p.dial(r.Context(), host, port)
	if err != nil {
		p.deny(w, host, err)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = up.Close()
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	client, buf, err := hj.Hijack()
	if err != nil {
		_ = up.Close()
		return
	}
	if n := buf.Reader.Buffered(); n > 0 {
		b, _ := buf.Peek(n)
		_, _ = up.Write(b)
	}
	go func() {
		_, _ = io.Copy(up, client)
		closeWrite(up)
	}()
	_, _ = io.Copy(client, up)
	_ = client.Close()
	_ = up.Close()
}

func closeWrite(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
		return
	}
	_ = c.Close()
}

// errDenied: not on the allowlist (reported), unlike network errors.
var errDenied = errors.New("not on this project's egress allowlist")

func (p *Proxy) dial(ctx context.Context, host, port string) (net.Conn, error) {
	if !p.Allow.Allows(host, port) {
		return nil, errDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ips, err := p.resolver().LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: %v", host, err)
	}
	private := false
	for _, h := range p.AllowPrivate {
		if strings.EqualFold(h, host) {
			private = true
		}
	}
	var lastErr error
	for _, ip := range ips {
		if !private && !publicIP(ip.IP) {
			lastErr = fmt.Errorf("%s resolves to a non-public address", host)
			continue
		}
		c, err := p.dialer()(ctx, "tcp", net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (p *Proxy) deny(w http.ResponseWriter, host string, err error) {
	if errors.Is(err, errDenied) {
		p.report(host)
		http.Error(w, "blocked by Jungle Monkey: "+host+" is "+errDenied.Error(), http.StatusForbidden)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func (p *Proxy) report(host string) {
	p.mu.Lock()
	if p.reported == nil {
		p.reported = map[string]bool{}
	}
	seen := p.reported[host]
	p.reported[host] = true
	p.mu.Unlock()
	slog.Info("egress blocked", "host", host)
	if !seen && p.Blocked != nil {
		go p.Blocked(host)
	}
}

func (p *Proxy) resolver() *net.Resolver {
	if p.Resolver != nil {
		return p.Resolver
	}
	return net.DefaultResolver
}

func (p *Proxy) dialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.Dial != nil {
		return p.Dial
	}
	return (&net.Dialer{}).DialContext
}

func publicIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsInterfaceLocalMulticast()
}

func splitHostPort(hp, def string) (string, string) {
	if h, p, err := net.SplitHostPort(hp); err == nil {
		return strings.ToLower(h), p
	}
	return strings.ToLower(strings.Trim(hp, "[]")), def
}

// Reporter posts blocked hosts to the gateway with the run token.
func Reporter(url, token string) func(host string) {
	return func(host string) {
		b, _ := json.Marshal(map[string]string{"host": host})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Warn("egress report", "err", err)
			return
		}
		res.Body.Close()
	}
}
