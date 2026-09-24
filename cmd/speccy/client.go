package main

import (
	"context"
	"fmt"
	"io/fs"
	nethttp "net/http"
	"os"
	"path/filepath"
	"testing/fstest"

	"github.com/alternayte/speccy/internal/app"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source/local"
)

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
	return openSessionIn(ctx, dir, false)
}

// openSessionIn opens local mode over dir. keep makes the store in dir/.speccy/state even when
// it does not exist yet, for a command whose result must outlive it, such as speccy add.
func openSessionIn(ctx context.Context, dir string, keep bool) (*session, error) {
	root, err := local.Open(dir)
	if err != nil {
		return nil, err
	}
	state := filepath.Join(root.Dir(), ".speccy", "state")
	key := ""
	s := &session{root: root}
	if dir := os.Getenv("SPECCY_STATE_DIR"); dir != "" {
		// CI keeps the store between runs, for example in the Actions cache (SDD §12.4). A pull
		// request from a fork can restore that cache and run its own steps, so the key that
		// opens the store's secrets lives outside it, for this run only. SPECCY_MODELS writes
		// the API keys again on each run. A key file from an older Speccy leaves the folder.
		state = dir
		if err := os.Remove(filepath.Join(dir, "key")); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		tmp, err := os.MkdirTemp("", "speccy-key-")
		if err != nil {
			return nil, err
		}
		context.AfterFunc(ctx, func() { _ = os.RemoveAll(tmp) })
		key = filepath.Join(tmp, "key")
	} else if keep {
		if err := os.MkdirAll(state, 0o755); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(filepath.Join(state, "speccy.db")); err != nil {
		tmp, err := os.MkdirTemp("", "speccy-review-")
		if err != nil {
			return nil, err
		}
		context.AfterFunc(ctx, func() { _ = os.RemoveAll(tmp) })
		state, s.temp = tmp, true
	}
	if key == "" {
		key = filepath.Join(state, "key")
	}
	a, db, err := openApp(ctx, root, state, key)
	if err != nil {
		return nil, err
	}
	s.app = a
	h := localHandler(fs.FS(fstest.MapFS{}), a, db)
	if s.client, err = api.NewClientWithResponses("http://speccy.local/api/v1", api.WithHTTPClient(speccyhttp.InProcess{Handler: h})); err != nil {
		return nil, err
	}
	// CI sets the models in the environment (SPECCY_MODELS).
	if err := applyEnvModels(ctx, s.client); err != nil {
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
