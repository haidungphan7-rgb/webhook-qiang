package replay

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// This suite deliberately runs *inside* package replay: the seams it uses (dialer,
// lookupIP) are unexported fields, so production code cannot set them. Nothing here
// relaxes the runtime policy - every test exercises the same ValidateTarget path the
// server uses.

// fakeDNS answers fixed names so the suite never depends on external DNS.
func fakeDNS(mapping map[string][]string) func(context.Context, string) ([]net.IPAddr, error) {
	return func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == "dns-error.test" {
			return nil, errors.New("no such host")
		}

		ips, ok := mapping[host]
		if !ok {
			return nil, errors.New("no such host")
		}

		if len(ips) == 0 {
			return []net.IPAddr{}, nil
		}

		out := make([]net.IPAddr, 0, len(ips))
		for _, s := range ips {
			out = append(out, net.IPAddr{IP: net.ParseIP(s)})
		}

		return out, nil
	}
}

// dialTo pins every connection to one address, so a test can serve a "public" host name
// from a local httptest server.
func dialTo(addr string) func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 2 * time.Second}

	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return d.DialContext(ctx, network, addr)
	}
}

func TestValidateTarget_Table(t *testing.T) {
	t.Parallel()

	dns := fakeDNS(map[string][]string{
		"private.test": {"10.0.0.1"},
		"mixed.test":   {"1.1.1.1", "10.0.0.1"},
		"public.test":  {"1.1.1.1"},
		"empty.test":   {},
	})

	cases := []struct {
		name string
		url  string
		want error // nil = accepted
	}{
		// loopback
		{"loopback 127.0.0.1", "http://127.0.0.1:8080/x", ErrBlockedTarget},
		{"loopback 127.0.0.2", "http://127.0.0.2/", ErrBlockedTarget},
		{"loopback 127.255.255.254", "http://127.255.255.254/", ErrBlockedTarget},
		// 0.0.0.0/8 - Linux routes the whole range to the local host
		{"zero net 0.0.0.0", "http://0.0.0.0/", ErrBlockedTarget},
		{"zero net 0.1.2.3", "http://0.1.2.3/", ErrBlockedTarget},
		// RFC1918
		{"private 10/8", "http://10.1.2.3/", ErrBlockedTarget},
		{"private 172.16 lower", "http://172.16.0.1/", ErrBlockedTarget},
		{"private 172.31 upper", "http://172.31.255.255/", ErrBlockedTarget},
		{"public 172.32 allowed", "http://172.32.0.1/", nil},
		{"private 192.168/16", "http://192.168.1.1/", ErrBlockedTarget},
		// special ranges
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", ErrBlockedTarget},
		{"cgnat 100.64/10", "http://100.64.0.1/", ErrBlockedTarget},
		{"benchmarking 198.18/15", "http://198.18.0.1/", ErrBlockedTarget},
		{"protocol assignments 192.0.0/24", "http://192.0.0.1/", ErrBlockedTarget},
		{"multicast 224/4", "http://224.0.0.1/", ErrBlockedTarget},
		{"broadcast", "http://255.255.255.255/", ErrBlockedTarget},
		// IPv6
		{"v6 loopback", "http://[::1]/", ErrBlockedTarget},
		{"v6 unspecified", "http://[::]/", ErrBlockedTarget},
		{"v6 ula fc00", "http://[fc00::1]/", ErrBlockedTarget},
		{"v6 ula fd12", "http://[fd12:3456::1]/", ErrBlockedTarget},
		{"v6 link local", "http://[fe80::1]/", ErrBlockedTarget},
		{"v6 multicast", "http://[ff02::1]/", ErrBlockedTarget},
		{"v4 mapped loopback", "http://[::ffff:127.0.0.1]/", ErrBlockedTarget},
		{"v4 mapped metadata", "http://[::ffff:a9fe:a9fe]/", ErrBlockedTarget},
		{"v6 6to4 embedding 127.0.0.1", "http://[2002:7f00:1::]/", ErrBlockedTarget},
		{"v6 nat64 embedding metadata", "http://[64:ff9b::a9fe:a9fe]/", ErrBlockedTarget},
		// userinfo
		{"userinfo user:pass", "http://user:pass@example.com/", ErrInvalidURL},
		{"userinfo user only", "http://user@example.com/", ErrInvalidURL},
		{"userinfo disguised as port", "http://example.com:8080@127.0.0.1/", ErrInvalidURL},
		// scheme
		{"ftp", "ftp://example.com/", ErrInvalidURL},
		{"file", "file:///etc/passwd", ErrInvalidURL},
		{"gopher", "gopher://127.0.0.1:6379/_INFO", ErrInvalidURL},
		{"javascript", "javascript:alert(1)", ErrInvalidURL},
		{"scheme relative", "//example.com/x", ErrInvalidURL},
		// malformed
		{"no scheme", "example.com", ErrInvalidURL},
		{"empty host", "http:///x", ErrInvalidURL},
		{"spaces", "not a url", ErrInvalidURL},
		// accepted (public.test is resolved by the seam to a public address)
		{"uppercase scheme accepted", "HTTP://public.test/Path", nil},
		{"trimmed whitespace accepted", "  http://public.test/  ", nil},
		{"fragment is not host", "http://public.test#@127.0.0.1", nil},
		// obfuscated hosts must not slip through
		{"unicode digits", "http://①②⑦.0.0.1/", ErrInvalidURL},
		{"decimal ip", "http://2130706433/", ErrInvalidURL},
		{"hex ip", "http://0x7f.0.0.1/", ErrInvalidURL},
		{"short ip", "http://127.1/", ErrInvalidURL},
		// DNS seam cases
		{"dns resolves to private", "http://private.test/", ErrBlockedTarget},
		{"dns round robin with private", "http://mixed.test/", ErrBlockedTarget},
		{"dns resolves to public", "http://public.test/", nil},
		{"dns no answers", "http://empty.test/", ErrInvalidURL},
		{"dns error", "http://dns-error.test/", ErrInvalidURL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := Policy{lookupIP: dns}

			err := p.ValidateTarget(context.Background(), tc.url)

			switch {
			case tc.want == nil && err != nil:
				t.Fatalf("expected %q to be accepted, got %v", tc.url, err)
			case tc.want != nil && err == nil:
				t.Fatalf("expected %q to be rejected with %v, got nil", tc.url, tc.want)
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Fatalf("expected %q to fail with %v, got %v", tc.url, tc.want, err)
			}
		})
	}
}

func TestBlockedIP_Table(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"0.0.0.0":            true,
		"0.1.2.3":            true,
		"127.0.0.1":          true,
		"10.0.0.1":           true,
		"172.16.0.1":         true,
		"192.168.0.1":        true,
		"169.254.169.254":    true,
		"100.64.0.1":         true,
		"198.18.0.1":         true,
		"224.0.0.1":          true,
		"::1":                true,
		"::":                 true,
		"fc00::1":            true,
		"fe80::1":            true,
		"::ffff:127.0.0.1":   true,
		"2002:7f00:1::":      true,
		"64:ff9b::a9fe:a9fe": true,
		// must stay reachable
		"8.8.8.8":         false,
		"1.1.1.1":         false,
		"172.32.0.1":      false,
		"192.0.2.1":       false,
		"203.0.113.5":     false,
		"2606:4700::1111": false,
	}

	for ip, want := range cases {
		t.Run(ip, func(t *testing.T) {
			t.Parallel()

			parsed := net.ParseIP(ip)
			if parsed == nil {
				t.Fatalf("cannot parse %s", ip)
			}

			if got := blockedIP(parsed); got != want {
				t.Fatalf("blockedIP(%s) = %v, want %v", ip, got, want)
			}
		})
	}
}

func TestCheckDialAddress_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		address string
		blocked bool
	}{
		{"127.0.0.1:80", true},
		{"10.0.0.1:443", true},
		{"[::ffff:127.0.0.1]:80", true},
		{"0.1.2.3:80", true},
		{"8.8.8.8:443", false},
		// A host name is the normal case here (the transport passes host:port, the IP is
		// resolved inside net.Dialer), so it is resolved and judged here too.
		{"localhost:80", true},
		{"no-port", true}, // malformed
		{"[fe80::1%25eth0]:80", true},
	}

	for _, tc := range cases {
		t.Run(tc.address, func(t *testing.T) {
			t.Parallel()

			// Real DNS on purpose: "localhost" must resolve to loopback on any machine.
			err := Policy{}.checkDialAddress(context.Background(), tc.address)

			if tc.blocked && !errors.Is(err, ErrBlockedTarget) {
				t.Fatalf("expected %q to be blocked, got %v", tc.address, err)
			}

			if !tc.blocked && err != nil {
				t.Fatalf("expected %q to be allowed, got %v", tc.address, err)
			}
		})
	}
}

func TestClient_Hardening(t *testing.T) {
	t.Parallel()

	client := Policy{Timeout: 3 * time.Second}.Client()

	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type %T", client.Transport)
	}

	if client.Timeout <= 0 {
		t.Error("client timeout must be set (the task requires a bounded HTTP client)")
	}

	if tr.TLSHandshakeTimeout <= 0 || tr.ResponseHeaderTimeout <= 0 {
		t.Error("TLS and response header timeouts must be set")
	}

	// Proxy must be nil, or a function that always returns nil: an outbound proxy would
	// turn every SSRF check into a no-op because the request would leave through the proxy.
	if tr.Proxy != nil {
		if got, err := tr.Proxy(&http.Request{}); err != nil || got != nil {
			t.Fatalf("proxy must always be nil (never honour env proxies), got %v %v", got, err)
		}
	}
}

// TestClient_RedirectsDisabled proves the default: a 302 to a private address is never
// followed. The dialer seam points the client at a local test server while ValidateTarget
// (which runs on the initial URL and on every hop) keeps the real policy in force.
func TestClient_RedirectsDisabled(t *testing.T) {
	t.Parallel()

	var innerHits int

	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerHits++

		w.WriteHeader(http.StatusOK)
	}))

	defer inner.Close()

	_, innerPort, _ := net.SplitHostPort(inner.Listener.Addr().String())

	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:"+innerPort+"/inner", http.StatusFound)
	}))

	defer outer.Close()

	_, outerPort, _ := net.SplitHostPort(outer.Listener.Addr().String())

	p := Policy{
		Timeout:  2 * time.Second,
		lookupIP: fakeDNS(map[string][]string{"outer.test": {"1.1.1.1"}}),
		dialer:   dialTo("127.0.0.1:" + outerPort),
	}

	client := p.Client()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://outer.test/hook", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected the 302 to be returned as-is, got %d", resp.StatusCode)
	}

	if innerHits != 0 {
		t.Fatalf("redirect must not be followed by default, inner server was hit %d times", innerHits)
	}
}
