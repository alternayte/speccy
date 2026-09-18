// Package http serves the JSON API, the health endpoint, and the embedded SPA.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/alternayte/speccy/internal/features/admin"
	"github.com/alternayte/speccy/internal/features/bundle"
	"github.com/alternayte/speccy/internal/features/export"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// maxRequestBytes bounds every API request body: one bundle import (50 MB) and form overhead.
const maxRequestBytes = 60 << 20

// API is every endpoint in api/openapi.yaml. Each feature serves its own operations.
type API struct {
	core
	*BundleAPI
	*VersionAPI
	*ExportAPI
	*ProfileAPI
	*ReviewAPI
	*AdminAPI
}

// The aliases give each embedded feature API its own field name.
type (
	BundleAPI  = bundle.API
	VersionAPI = version.API
	ExportAPI  = export.API
	ProfileAPI = profile.API
	ReviewAPI  = review.API
	AdminAPI   = admin.API
)

// core serves the operations that belong to no feature.
type core struct{}

// Handler returns the root handler: /healthz, /api/v1, and the SPA for every other path.
func Handler(spa fs.FS, a API) nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	strict := api.NewStrictHandlerWithOptions(a, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w nethttp.ResponseWriter, _ *nethttp.Request, err error) {
			writeProblem(w, nethttp.StatusBadRequest, "bad_request", err.Error())
		},
		ResponseErrorHandlerFunc: func(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
			if ke, ok := kernel.AsError(err); ok {
				writeProblem(w, ke.Status, ke.Code, ke.Detail)
				return
			}
			slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeProblem(w, nethttp.StatusInternalServerError, "internal", "Speccy hit an unexpected error. The server log has the details.")
		},
	})
	api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseURL: "/api/v1", BaseRouter: mux,
		ErrorHandlerFunc: func(w nethttp.ResponseWriter, _ *nethttp.Request, err error) {
			writeProblem(w, nethttp.StatusBadRequest, "bad_request", err.Error())
		},
		Middlewares: []api.MiddlewareFunc{func(next nethttp.Handler) nethttp.Handler {
			return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				r.Body = nethttp.MaxBytesReader(w, r.Body, maxRequestBytes)
				next.ServeHTTP(w, r)
			})
		}},
	})
	mux.HandleFunc("/api/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		writeProblem(w, nethttp.StatusNotFound, "not_found", "No API endpoint matches "+r.Method+" "+r.URL.Path+".")
	})
	mux.Handle("/", spaHandler(spa))
	return mux
}

func (core) GetMeta(context.Context, api.GetMetaRequestObject) (api.GetMetaResponseObject, error) {
	return api.GetMeta200JSONResponse{Version: kernel.Version, Mode: api.MetaModeLocal}, nil
}

// writeProblem writes an RFC 9457 problem details response with a stable code.
func writeProblem(w nethttp.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.Problem{
		Type:   "about:blank",
		Title:  nethttp.StatusText(status),
		Status: status,
		Code:   code,
		Detail: &detail,
	})
}

// spaHandler serves files from the SPA build, and index.html for any path that is not a file,
// so that client-side routes load on a direct visit.
func spaHandler(spa fs.FS) nethttp.Handler {
	files := nethttp.FileServerFS(spa)
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if info, err := fs.Stat(spa, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(spa, "index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(nethttp.StatusServiceUnavailable)
			_, _ = w.Write([]byte("The web app is not in this binary. Build it with `just build`, or run `just dev`.\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// ListenLocal opens a listener for local mode. Local mode has no auth, so it refuses any
// address that is not a loopback IP.
func ListenLocal(addr string) (net.Listener, error) {
	// DEC-015: local mode has no auth and binds to 127.0.0.1 only.
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("address %q is not host:port: %w", addr, err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
		addr = net.JoinHostPort(host, port)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("local mode has no sign-in, so it listens on a loopback address only, not %q. Use an address such as 127.0.0.1:7878", addr)
	}
	return net.Listen("tcp", addr)
}

// Serve runs the handler on the listener until ctx ends, then shuts down within 5 seconds.
func Serve(ctx context.Context, ln net.Listener, h nethttp.Handler) error {
	srv := &nethttp.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-errc; !errors.Is(err, nethttp.ErrServerClosed) {
			return err
		}
		return nil
	}
}
