package ideas

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Link previews fetch untrusted URLs from the server, so every connection
// goes through a dialer that refuses non-public addresses (checked on the
// resolved IP of each connection, so redirects and DNS rebinding are
// covered), the proxy environment is ignored, and time and size are capped.
const (
	previewTimeout   = 5 * time.Second
	previewMaxHTML   = 512 << 10
	previewMaxImage  = 5 << 20
	previewRedirects = 3
)

// ErrUnsafeAddress is returned for a host that resolves to a non-public address.
var ErrUnsafeAddress = errors.New("the address is not public")

// blocked are special-purpose ranges that netip's predicates do not cover.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can reach IPv4 private ranges
	netip.MustParsePrefix("2002::/16"),    // 6to4 likewise
}

// PublicAddr reports whether a link preview may connect to ip.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// dialCheck is the dialer's Control: only public addresses on ports 80 and 443.
func dialCheck(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !PublicAddr(ap.Addr()) {
		return ErrUnsafeAddress
	}
	if p := ap.Port(); p != 80 && p != 443 && defaultPortsOnly {
		return fmt.Errorf("port %d is not allowed", p)
	}
	return nil
}

// defaultPortsOnly limits previews to ports 80 and 443 (tests turn it off).
var defaultPortsOnly = true

// SafeClient is the HTTP client for link previews.
func SafeClient() *http.Client { return safeClient(dialCheck) }

func safeClient(control func(network, address string, c syscall.RawConn) error) *http.Client {
	d := &net.Dialer{Timeout: previewTimeout, Control: control}
	tr := &http.Transport{
		Proxy:                 nil, // never through HTTP(S)_PROXY: the check must see the real address
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   previewTimeout,
		ResponseHeaderTimeout: previewTimeout,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   2 * previewTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > previewRedirects {
				return errors.New("too many redirects")
			}
			return checkURL(req.URL)
		},
	}
}

// checkURL allows http(s) URLs on the default ports, without credentials.
func checkURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("only http and https links are fetched")
	}
	if u.User != nil {
		return errors.New("links with credentials are not fetched")
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" && defaultPortsOnly {
		return errors.New("only the default ports are fetched")
	}
	if u.Hostname() == "" {
		return errors.New("the link has no host")
	}
	return nil
}

// ParseLink validates a link the owner saved.
func ParseLink(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return nil, errors.New("the link must be 1 to 2048 characters")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the link must be an http or https URL")
	}
	return u, nil
}

// Preview is what a page says about itself.
type Preview struct {
	Title    string
	ImageURL string
}

var (
	reTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reMeta  = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	reAttr  = regexp.MustCompile(`(?is)([a-z:_-]+)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
)

// parseHead extracts the title (og:title first) and og:image from HTML.
func parseHead(doc string, base *url.URL) Preview {
	var p Preview
	for _, tag := range reMeta.FindAllString(doc, 200) {
		attrs := map[string]string{}
		for _, m := range reAttr.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = m[3] + m[4] + m[5]
		}
		key := strings.ToLower(attrs["property"])
		if key == "" {
			key = strings.ToLower(attrs["name"])
		}
		val := strings.TrimSpace(html.UnescapeString(attrs["content"]))
		switch key {
		case "og:title", "twitter:title":
			if p.Title == "" {
				p.Title = val
			}
		case "og:image", "og:image:url", "twitter:image":
			if p.ImageURL == "" && val != "" {
				if u, err := base.Parse(val); err == nil && checkURL(u) == nil {
					p.ImageURL = u.String()
				}
			}
		}
	}
	if p.Title == "" {
		if m := reTitle.FindStringSubmatch(doc); m != nil {
			p.Title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}
	p.Title = strings.Join(strings.Fields(p.Title), " ")
	if utf8.RuneCountInString(p.Title) > 200 {
		p.Title = string([]rune(p.Title)[:200]) + "…"
	}
	return p
}

// fetch GETs u with the safe client, refusing bodies over limit. want is
// the media type prefix the response must have ("text/html", "image/").
func fetch(ctx context.Context, c *http.Client, u string, limit int64, want string) ([]byte, *url.URL, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil, nil, err
	}
	if err := checkURL(parsed); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*previewTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "JungleMonkey-LinkPreview/1.0")
	req.Header.Set("Accept", want+"*;q=1, */*;q=0.1")
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, nil, fmt.Errorf("%s answered %s", parsed.Host, resp.Status)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(mt, want) {
		return nil, nil, fmt.Errorf("not %s (%s)", want, mt)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(b)) > limit {
		if want == "text/html" {
			return b[:limit], resp.Request.URL, nil // the head is at the start
		}
		return nil, nil, errors.New("too large")
	}
	return b, resp.Request.URL, nil
}

// FetchPreview reads a page's title and og:image, and the image itself
// (nil when there is none or it fails).
func FetchPreview(ctx context.Context, c *http.Client, link string) (Preview, []byte, error) {
	doc, final, err := fetch(ctx, c, link, previewMaxHTML, "text/html")
	if err != nil {
		return Preview{}, nil, err
	}
	p := parseHead(string(doc), final)
	if p.ImageURL == "" {
		return p, nil, nil
	}
	img, _, err := fetch(ctx, c, p.ImageURL, previewMaxImage, "image/")
	if err != nil {
		return p, nil, nil
	}
	return p, img, nil
}
