// Command speccy reviews markdown spec bundles and returns one verdict.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/alternayte/speccy/internal/app"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/store"
	"github.com/alternayte/speccy/web"
)

// Exit codes from SDD §12.2.
const (
	exitOK    = 0
	exitUsage = 2
	exitRun   = 3
)

const usage = `Usage:
  speccy [--dir folder] [--addr host:port] [--no-open]   Start local mode and open the browser.
  speccy serve [--dir folder] [--addr host:port]         Start local mode without opening a browser.
  speccy serve --hosted                                  Start hosted mode (SPECCY_ environment variables).
  speccy admin invite --role admin|member                Print an invite link (hosted).
  speccy admin reset-link <email>                        Print a password reset link (hosted).
  speccy version                                         Print the version.

Local mode serves the bundles under --dir (default: the current folder).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "" && args[0][0] == '-' {
		return runLocal(args, true, stdout, stderr)
	}
	switch args[0] {
	case "serve":
		for _, a := range args[1:] {
			if a == "--hosted" || a == "-hosted" {
				if len(args) != 2 {
					fmt.Fprintf(stderr, "speccy serve --hosted takes no other flags; it reads the SPECCY_ environment variables.\n")
					return exitUsage
				}
				return runHosted(stdout, stderr)
			}
		}
		return runLocal(args[1:], false, stdout, stderr)
	case "admin":
		return runAdmin(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, kernel.Version)
		return exitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "Unknown command %q.\n\n%s", args[0], usage)
		return exitUsage
	}
}

func runLocal(args []string, openBrowser bool, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("speccy", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	addr := fl.String("addr", "127.0.0.1:7878", "address to listen on (loopback only)")
	dir := fl.String("dir", ".", "folder with the bundles to serve")
	noOpen := fl.Bool("no-open", false, "do not open the browser")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(stderr, "Unexpected argument %q.\n\n%s", fl.Arg(0), usage)
		return exitUsage
	}

	ln, err := speccyhttp.ListenLocal(*addr)
	if err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	handler, err := openLocal(ctx, *dir)
	if err != nil {
		_ = ln.Close()
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}

	url := "http://" + ln.Addr().String()
	fmt.Fprintf(stdout, "Speccy is running at %s\nPress Ctrl+C to stop.\n", url)
	spa, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}
	if openBrowser && !*noOpen {
		if err := openURL(url); err != nil {
			fmt.Fprintf(stderr, "Speccy could not open the browser: %v. Open %s yourself.\n", err, url)
		}
	}

	if err := speccyhttp.Serve(ctx, ln, handler(spa)); err != nil {
		fmt.Fprintf(stderr, "Speccy stopped: %v.\n", err)
		return exitRun
	}
	return exitOK
}

// openLocal opens the local store in dir/.speccy/state, syncs the bundles on disk, and
// watches the folder for changes (REQ-005). It returns the handler factory for the server.
func openLocal(ctx context.Context, dir string) (func(fs.FS) nethttp.Handler, error) {
	root, err := local.Open(dir)
	if err != nil {
		return nil, err
	}
	stateDir := filepath.Join(root.Dir(), ".speccy", "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	db, err := store.OpenSQLite(ctx, filepath.Join(stateDir, "speccy.db"))
	if err != nil {
		return nil, err
	}
	context.AfterFunc(ctx, func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		return nil, err
	}
	// SDD §14.1: local mode keeps the secret key in .speccy/state/key, mode 0600.
	sealer, err := kernel.LocalSealer(filepath.Join(stateDir, "key"))
	if err != nil {
		return nil, err
	}
	a, err := app.New(ctx, db, sealer, root, filepath.Join(root.Dir(), ".speccy", "profiles"))
	if err != nil {
		return nil, err
	}
	go func() {
		_ = root.Watch(ctx, 300*time.Millisecond, func() {
			if err := a.Profiles.Reload(ctx); err != nil && ctx.Err() == nil {
				slog.Error("reload of the profiles failed", "err", err)
			}
			if err := a.Bundles.Sync(ctx); err != nil && ctx.Err() == nil {
				slog.Error("sync after a change on disk failed", "err", err)
			}
		})
	}()
	opts := speccyhttp.Options{Actor: speccyhttp.LocalActor, Authz: &speccyhttp.Authz{DB: db, Workspace: a.Workspace}}
	return func(spa fs.FS) nethttp.Handler { return speccyhttp.Handler(spa, a.API, opts) }, nil
}

// openURL opens url in the default browser.
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
