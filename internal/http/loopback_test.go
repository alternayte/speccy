package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"
)

// The local app has no sign-in, so its port takes a call only from this machine's own pages
// and programs: a page of another site, and a name that DNS rebinding points at the port, get
// 403.
func TestLoopbackOnly(t *testing.T) {
	h := LoopbackOnly(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) { w.WriteHeader(nethttp.StatusNoContent) }))
	for _, c := range []struct {
		name, host, origin string
		want               int
	}{
		{"a program with no origin", "127.0.0.1:7878", "", nethttp.StatusNoContent},
		{"the app's own page", "localhost:7878", "http://localhost:7878", nethttp.StatusNoContent},
		{"the dev server's page", "127.0.0.1:7878", "http://127.0.0.1:5173", nethttp.StatusNoContent},
		{"IPv6 loopback", "[::1]:7878", "http://[::1]:7878", nethttp.StatusNoContent},
		{"a page of another site", "127.0.0.1:7878", "https://evil.example", nethttp.StatusForbidden},
		{"a rebound name", "evil.example:7878", "", nethttp.StatusForbidden},
		{"an origin that only starts like localhost", "127.0.0.1:7878", "http://localhost.evil.example", nethttp.StatusForbidden},
	} {
		r := httptest.NewRequest(nethttp.MethodPost, "/api/v1/meta", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, w.Code, c.want)
		}
	}
}
