package http

import (
	nethttp "net/http"
	"net/http/httptest"
)

// InProcess sends API requests to a handler in this process, with no port. The CLI, the TUI,
// and the MCP server use it, so they go through the same API and role table as the browser.
type InProcess struct{ Handler nethttp.Handler }

// Do serves req and returns the response.
func (d InProcess) Do(req *nethttp.Request) (*nethttp.Response, error) {
	rec := httptest.NewRecorder()
	d.Handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}
