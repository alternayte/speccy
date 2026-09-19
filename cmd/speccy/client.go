package main

import (
	"context"
	"fmt"
	"io/fs"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing/fstest"

	"github.com/alternayte/speccy/internal/app"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source/local"
)

// handlerDoer sends API requests to a handler in this process: the CLI, the TUI, and the MCP
// server use the same API and role table as the browser, with no port.
type handlerDoer struct{ h nethttp.Handler }

func (d handlerDoer) Do(req *nethttp.Request) (*nethttp.Response, error) {
	rec := httptest.NewRecorder()
	d.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// session is an open workspace for a headless command: local mode over a folder, in process.
type session struct {
	root   *local.Root
	app    *app.App
	client *api.ClientWithResponses
	// temp is true when the store is a temporary one: the folder has no .speccy/state.
	temp bool
}

// openSession opens local mode over dir. It uses dir/.speccy/state when it exists, so the
// models, the cache, and the runs of the app are shared; otherwise a temporary store that is
// removed when ctx ends (T-098: no speccy init needed).
func openSession(ctx context.Context, dir string) (*session, error) {
	root, err := local.Open(dir)
	if err != nil {
		return nil, err
	}
	state := filepath.Join(root.Dir(), ".speccy", "state")
	s := &session{root: root}
	if _, err := os.Stat(filepath.Join(state, "speccy.db")); err != nil {
		tmp, err := os.MkdirTemp("", "speccy-review-")
		if err != nil {
			return nil, err
		}
		context.AfterFunc(ctx, func() { _ = os.RemoveAll(tmp) })
		state, s.temp = tmp, true
	}
	a, db, err := openApp(ctx, root, state)
	if err != nil {
		return nil, err
	}
	s.app = a
	h := localHandler(fs.FS(fstest.MapFS{}), a, db)
	if s.client, err = api.NewClientWithResponses("http://speccy.local/api/v1", api.WithHTTPClient(handlerDoer{h})); err != nil {
		return nil, err
	}
	return s, nil
}

// remoteClient is the API of a Speccy server, with the token in SPECCY_TOKEN (SDD §12.1).
func remoteClient(server string) (*api.ClientWithResponses, error) {
	token := os.Getenv("SPECCY_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("--server needs an API token in SPECCY_TOKEN. Make one in Account → API tokens on %s", server)
	}
	return api.NewClientWithResponses(server+"/api/v1", api.WithRequestEditorFn(func(_ context.Context, req *nethttp.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}))
}
