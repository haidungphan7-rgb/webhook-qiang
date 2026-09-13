// Package replay re-sends a captured event to a target URL.
//
// This is the security-critical part of the product: a replay is a server-side HTTP
// request whose target is chosen by the user, i.e. a classic SSRF primitive. Three
// independent controls are applied:
//
//  1. ValidateTarget - rejects non http(s) schemes, credentials in the URL and resolves
//     the host: if ANY resolved address is loopback/private/link-local/multicast/
//     unspecified or otherwise reserved, the request is refused before it is sent.
//
//  2. transport.DialContext - re-checks the address of every TCP connection. This catches
//     DNS rebinding (a name that resolves public during validation and private at
//     connect time) and, because it runs per connection, it also covers redirect hops.
//
//     NOTE: this is DailContext and not net.Dialer.Control on purpose. Both hooks receive
//     the address as "host:port" - resolution happens INSIDE net.Dialer, so neither ever
//     sees a resolved IP. An earlier version assumed Control got an IP and treated every
//     host name as "unverifiable", which broke replays to every public domain. The
//     wrapper therefore splits the host, resolves it itself (with a short timeout) and
//     then applies the same blockedIP() rules to every answer.
//
//  3. CheckRedirect - redirects are DISABLED by default; when enabled, each hop is
//     validated again with the exact same rules.
//
// Nothing about these controls can be relaxed at runtime. The only escape hatches
// (AllowHosts, and the injected dialer used by tests) are explicit: AllowHosts is a
// deployment decision made in configuration, and the test dialer is only reachable from
// _test.go code.
package replay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Errors returned by ValidateTarget. They are part of the API contract: the handler maps
// them to 400 (invalid) vs 403 (blocked).
var (
	// ErrInvalidURL means the target is not a usable http(s) URL.
	ErrInvalidURL = errors.New("invalid target url")
	// ErrBlockedTarget means the target is refused by the policy.
	ErrBlockedTarget = errors.New("target address is not allowed")
)

// Policy configures how replays are executed.
type Policy struct {
	// Timeout bounds a single attempt (dial + request + read of the preview).
	Timeout time.Duration
	// MaxPreview is the maximum number of response bytes kept (and stored).
	MaxPreview int
	// MaxRedirects is the number of followed redirects; 0 disables redirects.
	MaxRedirects int
	// MaxRetries is the number of extra attempts for retryable failures.
	MaxRetries int
	// Backoff is the base delay between retries (exponential, with jitter).
	Backoff time.Duration
	// AllowHosts is an optional explicit allow-list of host names. When non empty, only
	// these hosts may be targeted.
	//
	// It can only NARROW the policy, never widen it: the reserved-address checks still run
	// for every allowed host unless AllowPrivate is also switched on. That combination is
	// explicit and off by default - it exists so a developer can replay into a local test
	// receiver without turning the protection off for everything else.
	AllowHosts []string

	// AllowPrivate lets an allow-listed host resolve to a reserved address (loopback,
	// RFC1918, ...). Requires AllowHosts to be set for that host. Default false.
	AllowPrivate bool

	// dialer overrides the network dialer. Only set from tests - it lets the suite point
	// the client at a local httptest server without weakening the production policy.
	dialer func(ctx context.Context, network, addr string) (net.Conn, error)

	// lookupIP overrides name resolution. Only set from tests - it lets the suite cover
	// "this name resolves to a private address" without depending on real DNS.
	lookupIP func(ctx context.Context, host string) ([]net.IPAddr, error)
}

// resolveTimeout bounds a single name lookup so a slow or malicious DNS server cannot
// park a replay goroutine forever.
const resolveTimeout = 5 * time.Second

// ValidateTarget checks a user supplied URL. The context bounds the DNS lookup.
func (p Policy) ValidateTarget(ctx context.Context, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidURL, err.Error())
	}

	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%w: scheme must be http or https, got %q", ErrInvalidURL, u.Scheme)
	}

	// `http://user:pass@host/` is a classic way to smuggle credentials (and to confuse
	// parsers): refuse it outright, as required by the task.
	if u.User != nil {
		return fmt.Errorf("%w: url must not contain user info", ErrInvalidURL)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidURL)
	}

	allowedByList := false

	if len(p.AllowHosts) > 0 {
		for _, h := range p.AllowHosts {
			if strings.EqualFold(strings.TrimSpace(h), host) || strings.EqualFold(strings.TrimSpace(h), u.Host) {
				allowedByList = true

				break
			}
		}

		if !allowedByList {
			return fmt.Errorf("%w: host %q is not in the allow list", ErrBlockedTarget, host)
		}
	}

	// An explicitly allow-listed host may target a reserved address, but only when the
	// operator asked for it (documented as "local demos only, never in production").
	if allowedByList && p.AllowPrivate {
		return nil
	}

	// A literal IP can be checked without DNS.
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return fmt.Errorf("%w: %s is a reserved address", ErrBlockedTarget, ip.String())
		}

		return nil
	}

	// Names: resolve first, then judge every answer. Judging "any" instead of "the first"
	// prevents a round-robin record from sneaking a private address through.
	lookup := p.lookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}

	resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	ips, err := lookup(resolveCtx, host)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve %q", ErrInvalidURL, host)
	}

	if len(ips) == 0 {
		return fmt.Errorf("%w: cannot resolve %q", ErrInvalidURL, host)
	}

	for _, ia := range ips {
		if blockedIP(ia.IP) {
			return fmt.Errorf("%w: %s resolves to %s", ErrBlockedTarget, host, ia.IP.String())
		}
	}

	return nil
}

// BlockedIP exposes blockedIP so the CLI can validate --replay-allow-host at startup and
// fail fast on a configuration that could never match.
func BlockedIP(ip net.IP) bool { return blockedIP(ip) }

// blockedIP reports whether the address is one we must never connect to as a result of
// user input.
func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	switch {
	case ip.IsLoopback(),
		ip.IsPrivate(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsUnspecified(),
		ip.IsMulticast():
		return true
	}

	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 0: // 0.0.0.0/8 - RFC 1122 "this network"; Linux routes it to loopback
			return true
		case v4[0] == 100 && v4[1]&0xc0 == 64: // 100.64/10 carrier grade NAT
			return true
		case v4[0] == 169 && v4[1] == 254: // 169.254/16 link local, incl. cloud metadata
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0/24 protocol assignments
			return true
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19): // 198.18/15 benchmarking
			return true
		case v4[0] >= 224: // 224/4 multicast + 240/4 reserved + 255.255.255.255
			return true
		}

		return false
	}

	// IPv6: unique local (fc00::/7), 6to4 (2002::/16, can embed 127.0.0.1 as
	// 2002:7f00:1::) and NAT64 (64:ff9b::/96, can embed any IPv4).
	switch {
	case ip[0]&0xfe == 0xfc:
		return true
	case ip[0] == 0x20 && ip[1] == 0x02:
		return true
	case ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b:
		return true
	}

	return false
}

// checkDialAddress re-checks the address the transport is about to dial.
//
// It runs once per TCP connection, which is the last line of defence against DNS
// rebinding (the name resolved to a public address during validation but to a private one
// at connect time) and it also covers redirect hops.
//
// Important: http.Transport hands us the *host name* (host:port) here, not the resolved
// IP - name resolution happens inside net.Dialer. So a name is the normal case and has to
// be resolved and judged here; only a literal IP can be checked directly.
// privateAllowedFor reports whether reserved addresses are acceptable for one specific
// host.
//
// The exemption is deliberately per host and requires BOTH switches: the host has to be
// allow-listed and the operator has to ask for private targets. It is not a global
// "protection off" flag, which is why the dial-time check below keeps running even when
// AllowPrivate is set - only the listed hosts get through.
func (p Policy) privateAllowedFor(host string) bool {
	if !p.AllowPrivate {
		return false
	}

	for _, h := range p.AllowHosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}

	return false
}

func (p Policy) checkDialAddress(ctx context.Context, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: malformed address %q", ErrBlockedTarget, address)
	}

	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) && !p.privateAllowedFor(host) {
			return fmt.Errorf("%w: %s is a reserved address", ErrBlockedTarget, ip.String())
		}

		return nil
	}

	lookup := p.lookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}

	resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	ips, err := lookup(resolveCtx, host)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve %q", ErrInvalidURL, host)
	}

	for _, ia := range ips {
		if blockedIP(ia.IP) && !p.privateAllowedFor(host) {
			return fmt.Errorf("%w: %s resolves to %s", ErrBlockedTarget, host, ia.IP.String())
		}
	}

	return nil
}

// Client builds an HTTP client that enforces the policy on every connection.
func (p Policy) Client() *http.Client {
	dialer := &net.Dialer{
		Timeout: p.Timeout,
	}

	transport := &http.Transport{
		Proxy:                 nil, // never honour proxy env vars for user supplied targets
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   p.Timeout,
		ResponseHeaderTimeout: p.Timeout,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
	}

	// Re-check on every connection (validation alone is not enough: the answer can change
	// between validation and connect, and redirect hops never went through validation of
	// their own). AllowPrivate is only honoured together with an allow list, so the set of
	// reachable hosts stays limited to what the operator listed.
	baseDial := dialer.DialContext
	if p.dialer != nil {
		baseDial = p.dialer
	}

	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Always re-check, even when AllowPrivate is on: the exemption is scoped to the
		// allow-listed hosts (see privateAllowedFor), so connections to anything else are
		// still judged by the full policy. Skipping the check entirely would let a listed
		// host drag the client to any private address.
		if err := p.checkDialAddress(ctx, addr); err != nil {
			return nil, err
		}

		return baseDial(ctx, network, addr)
	}

	return &http.Client{
		Timeout:   p.Timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if p.MaxRedirects <= 0 {
				// Default: do not follow. This removes the whole "redirect to a private
				// address" bypass class in one line.
				return http.ErrUseLastResponse
			}

			if len(via) > p.MaxRedirects {
				return fmt.Errorf("%w: too many redirects", ErrInvalidURL)
			}

			// Same rules on every hop - a public URL may redirect to a private one.
			hopCtx, cancel := context.WithTimeout(req.Context(), resolveTimeout)
			defer cancel()

			return p.ValidateTarget(hopCtx, req.URL.String())
		},
	}
}
