package sourceresolve

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alternayte/speccy/internal/engine/sourcepolicy"
)

// testResolver routes every host to one test server, so a test can use real host names
// without a real name lookup. It keeps the no-redirect rule of the production client.
func testResolver(addr string) *Resolver {
	tr := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	return &Resolver{HTTP: &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func TestResolveStopsBeforeAForbiddenRedirect(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Host+r.URL.Path)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/go") {
			http.Redirect(w, r, "http://forbidden.test/landing", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := sourcepolicy.Policy{Forbid: []string{"forbidden.test"}}
	got := testResolver(srv.Listener.Addr().String()).Resolve(context.Background(), p, "http://allowed.test/go")
	if !got.Dropped {
		t.Fatal("a redirect to a forbidden host must drop the source")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if strings.HasPrefix(s, "forbidden.test") {
			t.Fatalf("Speccy requested the forbidden host: %v", seen)
		}
	}
	if len(got.Chain) != 1 || got.Chain[0] != "http://allowed.test/go" {
		t.Errorf("the chain holds the hops Speccy requested, got %v", got.Chain)
	}
}

func TestResolveRecordsTheFinalURLAndDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "http://docs.example.com/new", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Last-Modified", "Mon, 01 Sep 2026 10:00:00 GMT")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := testResolver(srv.Listener.Addr().String()).Resolve(context.Background(), sourcepolicy.Policy{}, "http://docs.example.com/old")
	if got.Dropped {
		t.Fatalf("the source resolves: %s", got.Reason)
	}
	if got.FinalURL != "http://docs.example.com/new" {
		t.Errorf("FinalURL = %q", got.FinalURL)
	}
	if len(got.Chain) != 2 {
		t.Errorf("the chain holds both hops, got %v", got.Chain)
	}
	if got.RetrievedAt == nil || got.RetrievedAt.IsZero() {
		t.Error("a resolved source carries Speccy's own retrieval time")
	}
	if got.Modified == nil {
		t.Error("Last-Modified is kept")
	}
}

func TestPublicRefusesAddressesThatAreNotPublic(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.169.254", "::1", "fd00::1", "100.64.0.1", "0.0.0.0"} {
		if public(net.ParseIP(s)) {
			t.Errorf("%s is not a public address", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"} {
		if !public(net.ParseIP(s)) {
			t.Errorf("%s is a public address", s)
		}
	}
}
