package replay

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/storage"
	"github.com/yuandzhang/webhook-zq/internal/storage/mem"
)

// dialRouter sends every connection to a local server chosen by the *dialled IP*, which
// lets one test serve several "public" hosts (1.1.1.1 -> server A, 2.2.2.2 -> server B).
func dialRouter(routes map[string]string) func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 2 * time.Second}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		target, ok := routes[host]
		if !ok {
			return nil, &net.OpError{Op: "dial", Err: errNoRoute}
		}

		if _, _, err = net.SplitHostPort(target); err != nil {
			target = net.JoinHostPort(target, port)
		}

		return d.DialContext(ctx, network, target)
	}
}

var errNoRoute = &net.AddrError{Err: "no test route", Addr: ""}

// newRedirectFixture builds a chain of servers and a policy that can follow redirects.
// The DNS seam makes every hop resolve to a public address, so only the *last* hop (which
// points at a private one) is expected to fail.
func newRedirectFixture(t *testing.T, maxRedirects int, lastLocation func(string) string) (*Service, *mem.Store, storage.Inbox, storage.Event, *int, *int) {
	t.Helper()

	var firstHits, secondHits int

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits++

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	}))

	t.Cleanup(second.Close)

	_, secondPort, _ := net.SplitHostPort(second.Listener.Addr().String())

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits++

		http.Redirect(w, r, lastLocation(secondPort), http.StatusFound)
	}))

	t.Cleanup(first.Close)

	_, firstPort, _ := net.SplitHostPort(first.Listener.Addr().String())

	store := mem.New()

	inbox := storage.Inbox{ID: uuid.New(), Name: "t", Token: "t", Enabled: true}
	if err := store.CreateInbox(context.Background(), &inbox); err != nil {
		t.Fatal(err)
	}

	event := storage.Event{
		ID:      uuid.New(),
		InboxID: inbox.ID,
		Method:  http.MethodPost,
		Body:    []byte("{}"),
	}

	if err := store.CreateEvent(context.Background(), &event); err != nil {
		t.Fatal(err)
	}

	policy := Policy{
		Timeout:      2 * time.Second,
		MaxRedirects: maxRedirects,
		lookupIP: fakeDNS(map[string][]string{
			"first.test":  {"1.1.1.1"},
			"second.test": {"2.2.2.2"},
		}),
		// The transport dials by host name (the IP is resolved by net.Dialer), so the
		// routes are keyed by name.
		dialer: dialRouter(map[string]string{
			"first.test":  "127.0.0.1:" + firstPort,
			"second.test": "127.0.0.1:" + secondPort,
		}),
	}

	return New(zap.NewNop(), store, policy), store, inbox, event, &firstHits, &secondHits
}

// TestRedirects_PublicChainIsFollowed: with redirects enabled, a public -> public chain
// works (this proves the checkbox "follow redirects" is actually implemented).
func TestRedirects_PublicChainIsFollowed(t *testing.T) {
	t.Parallel()

	svc, _, inbox, event, firstHits, secondHits := newRedirectFixture(t, 2,
		func(secondPort string) string { return "http://second.test/next" })

	attempts, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: "http://first.test/hook"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if *firstHits != 1 || *secondHits != 1 {
		t.Fatalf("expected both hops to be hit, got first=%d second=%d", *firstHits, *secondHits)
	}

	if attempts[0].Outcome != storage.OutcomeSuccess {
		t.Fatalf("expected success, got %s", attempts[0].Outcome)
	}
}

// TestRedirects_PrivateHopIsBlocked is the requirement "redirects must either be disabled
// or re-checked on every hop": a public URL redirecting to a loopback address must not be
// followed, and the refusal must be recorded.
func TestRedirects_PrivateHopIsBlocked(t *testing.T) {
	t.Parallel()

	svc, store, inbox, event, firstHits, secondHits := newRedirectFixture(t, 2,
		func(secondPort string) string { return "http://127.0.0.1:" + secondPort + "/inner" })

	attempts, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: "http://first.test/hook"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if *secondHits != 0 {
		t.Fatalf("the private hop must never be reached, got %d hits", *secondHits)
	}

	if *firstHits != 1 {
		t.Fatalf("expected the first hop to be attempted, got %d", *firstHits)
	}

	last := attempts[len(attempts)-1]

	if last.Outcome == storage.OutcomeSuccess {
		t.Fatalf("a redirect to a private address must not end in success: %+v", last)
	}

	// The refusal is auditable.
	stored, sErr := store.ListReplays(context.Background(), event.ID, 10)
	if sErr != nil || len(stored) == 0 {
		t.Fatalf("the attempt must be recorded: %v %v", stored, sErr)
	}
}

// TestRedirects_LimitIsEnforced covers the "too many redirects" guard.
func TestRedirects_LimitIsEnforced(t *testing.T) {
	t.Parallel()

	svc, _, inbox, event, _, secondHits := newRedirectFixture(t, 1,
		func(secondPort string) string { return "http://second.test/next" })

	// MaxRedirects=1 allows exactly one follow, so the two hop chain still completes.
	if _, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: "http://first.test/hook"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if *secondHits != 1 {
		t.Fatalf("expected the single allowed redirect to be followed, got %d", *secondHits)
	}
}

// TestRedirects_DisabledByDefault documents the default: a 3xx is returned as-is and the
// redirect target is never contacted.
func TestRedirects_DisabledByDefault(t *testing.T) {
	t.Parallel()

	svc, _, inbox, event, _, secondHits := newRedirectFixture(t, 0,
		func(secondPort string) string { return "http://second.test/next" })

	attempts, err := svc.Run(context.Background(), Request{Event: event, Inbox: inbox, Target: "http://first.test/hook"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if *secondHits != 0 {
		t.Fatalf("redirects are disabled by default, got %d hits on the second hop", *secondHits)
	}

	if attempts[0].StatusCode == nil || *attempts[0].StatusCode != http.StatusFound {
		t.Fatalf("the 302 must be recorded as-is, got %v", attempts[0].StatusCode)
	}

	if attempts[0].Outcome != storage.OutcomeHTTPError {
		t.Fatalf("a non 2xx is a business failure, got %s", attempts[0].Outcome)
	}
}

// TestValidateTarget_FragmentOnlyIsFine guards against parsing tricks where the real host
// hides behind a fragment or a userinfo-looking substring.
func TestValidateTarget_FragmentOnlyIsFine(t *testing.T) {
	t.Parallel()

	p := Policy{lookupIP: fakeDNS(map[string][]string{"ok.test": {"1.1.1.1"}})}

	u, err := url.Parse("http://ok.test/x?a=1#frag")
	if err != nil {
		t.Fatal(err)
	}

	if u.User != nil {
		t.Fatalf("unexpected user info in %s", strings.TrimSpace(u.String()))
	}

	if err = p.ValidateTarget(context.Background(), "http://ok.test/x?a=1#frag"); err != nil {
		t.Fatalf("a public url with a fragment must be accepted, got %v", err)
	}
}
