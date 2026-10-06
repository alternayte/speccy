// Command speccy reviews markdown spec bundles and returns one verdict.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	nethttp "net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/term"

	"github.com/alternayte/speccy/internal/app"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/mcpserver"
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
  speccy init                                            Write .speccy.yaml and ignore .speccy/state/.
  speccy add <url> [--profile <key>]                     Make a GitHub source from a URL, and sync it once.
  speccy init --github                                   Adopt this repo: map its docs, relax the checks that
                                                         fail today, and write the Action's workflow.
  speccy review <path…> [--format text|json|md] [--summary] [--server URL] [--adopt]
                [--stages lint,rubric,grounding,divergence,coherence] [--enforcement advisory|blocking]
                                                         Review bundles. Exit codes: 0 Build Ready or advisory,
                                                         1 Not Build Ready in blocking mode, 2 usage, 3 run error.
                                                         --adopt writes the type and the size the review used
                                                         into each doc's frontmatter.
  speccy action [--server URL] [--stages …] [--enforcement advisory|blocking] [--verify]
                                                         The GitHub Action: review the bundles a pull request
                                                         changes, and comment on it. --verify runs the
                                                         verification gate instead of the review.
  speccy action --pr <pull request URL> --pending | --dry-run [--stages …]
                [--levels must,should] [--no-attribution]
                                                         Review a pull request from this machine. --pending
                                                         posts one review that only you see; --dry-run prints
                                                         it and posts nothing. --levels names the levels that
                                                         go inline. --no-attribution keeps the name of Speccy
                                                         out of the review.
  speccy review-prs <pull request URL>... | --repo <owner/name> [--requested]
                [--parallel N] [--again] [--yes] [--stages …] [--levels must,should]
                [--no-attribution]
                                                         Review many pull requests. Each one gets a pending
                                                         review that only you see. --repo takes each open
                                                         pull request that is not a draft and changes a spec
                                                         doc; --requested keeps the ones that ask for your
                                                         review. A pull request reviewed at its head commit
                                                         is skipped, unless --again.
  speccy review-prs --batch <ID> | --cancel <ID>         Show a batch until it ends, or start no new pull
                                                         request of it.
  speccy ask <pull request URL> "<concern>" [--section "A › B"] [--force] [--no-attribution]
                                                         Turn your concern into one question for the author,
                                                         on its section, in your pending review.
  speccy pending list <pull request URL>                 List the comments of your pending review.
  speccy pending delete <pull request URL> <ID>...       Delete comments of your pending review.
  speccy pending discard <pull request URL>... | --repo <owner/name> | --batch <ID>
                                                         Discard your pending reviews.
  speccy tui                                             Open the terminal UI.
  speccy mcp [--root folder]                             Run the MCP server over stdio.
  speccy profile validate <file> [--conflicts]           Check a profile file. --conflicts asks the reviewer
                                                         model for checks that pull against each other.
  speccy export <path> --format zip|html                 Export a bundle, or its HTML report.
  speccy handoff <path> --out <folder>                   Write a bundle's build packet for a coding agent.
                [--label <name>] [--acknowledged]
  speccy report <path> --handoff <id> --text <text>      Report what a build learned about the doc.
                [--blocked | --note] [--section "A › B"] [--trace-id REQ-012]
  speccy waive <doc path> <finding ID> --reason "<reason>" [--approve]
                [--upstream-change | --send-back]       Ask for a waiver of one finding. The finding ID is
                                                         from speccy review --format json. --approve also
                                                         approves it, as the profile's waiver policy allows.
                                                         --upstream-change answers a conflict: the linked
                                                         doc must change. --send-back answers a downstream
                                                         request: the downstream doc must change.
  speccy waivers list <doc path>                         List the waivers of a spec doc, with their status.
  speccy verify <path> [<GitHub URL or folder>]          Verify one build against the bundle. With no URL,
                                                         the repo the doc's implemented-by link names.
  speccy admin invite --role admin|member                Print an invite link (hosted).
  speccy admin reset-link <email>                        Print a password reset link (hosted).
  speccy version                                         Print the version.

Local mode serves the bundles under --dir (default: the current folder). The other commands work
on the folder with .speccy.yaml, from the current folder up, or else on the current folder.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	// An owner of a local state finishes its queue and frees the lock when the command ends.
	defer closeSeats(stderr)
	processKind = "the app"
	if len(args) == 0 || args[0] != "" && args[0][0] == '-' {
		return runLocal(args, true, stdout, stderr)
	}
	if args[0] != "serve" {
		processKind = "speccy " + args[0]
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
	case "review":
		return runReview(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], os.Stdin, stdout, stderr, isTerminal(os.Stdin))
	case "add":
		return runAdd(args[1:], stdout, stderr)
	case "profile":
		return runProfile(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "handoff":
		return runHandoff(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	case "waive":
		return runWaive(args[1:], stdout, stderr)
	case "waivers":
		return runWaivers(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], stderr)
	case "tui":
		return runTUI(args[1:], stderr)
	case "action":
		return runAction(args[1:], stdout, stderr)
	case "review-prs":
		return runReviewPRs(args[1:], os.Stdin, isTerminal(os.Stdin), stdout, stderr)
	case "ask":
		return runAsk(args[1:], stdout, stderr)
	case "pending":
		return runPending(args[1:], stdout, stderr)
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
	spa, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		_ = ln.Close()
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}
	handler, err := openLocal(*dir, spa)
	if err != nil {
		_ = ln.Close()
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}

	url := "http://" + ln.Addr().String()
	fmt.Fprintf(stdout, "Speccy is running at %s\nAn agent connects to the MCP server at %s/mcp\nPress Ctrl+C to stop.\n", url, url)
	if o, client := handler.ownerInfo(); client {
		fmt.Fprintf(stdout, "%s (process %d) owns the state of this folder, so the app sends its calls to it.\n", o.Kind, o.PID)
	}
	if openBrowser && !*noOpen {
		if err := openURL(url); err != nil {
			fmt.Fprintf(stderr, "Speccy could not open the browser: %v. Open %s yourself.\n", err, url)
		}
	}

	// Local mode serves MCP on the app's port too (#88): an agent connects to the running app,
	// and starts no process of its own. The port takes calls from this machine only.
	client, err := handler.client()
	if err != nil {
		_ = ln.Close()
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}
	mux := nethttp.NewServeMux()
	mux.Handle("/mcp", mcpserver.LocalHTTP(func(context.Context, nethttp.Header) (*api.ClientWithResponses, error) { return client, nil }))
	mux.Handle("/", handler)
	if err := speccyhttp.Serve(ctx, ln, speccyhttp.LoopbackOnly(mux)); err != nil {
		fmt.Fprintf(stderr, "Speccy stopped: %v.\n", err)
		return exitRun
	}
	return exitOK
}

// openLocal takes the seat of the app at dir/.speccy/state: the owner, which opens the local
// store, syncs the bundles on disk and watches the folder for changes (REQ-005), or a client
// process of the owner. The seat is the handler of the server, and it serves spa.
func openLocal(dir string, spa fs.FS) (*seat, error) {
	root, err := local.Open(dir)
	if err != nil {
		return nil, err
	}
	state := filepath.Join(root.Dir(), ".speccy", "state")
	return openSeat(root, state, filepath.Join(state, "key"), spa)
}

// openApp opens the SQLite store in stateDir and builds the services over the folder of root,
// with the secret key in keyFile. It syncs the bundles on disk once. The store closes when ctx
// ends.
func openApp(ctx context.Context, root *local.Root, stateDir, keyFile string) (*app.App, *store.DB, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, nil, err
	}
	db, err := store.OpenSQLite(ctx, filepath.Join(stateDir, "speccy.db"))
	if err != nil {
		return nil, nil, err
	}
	context.AfterFunc(ctx, func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		return nil, nil, err
	}
	// SDD §14.1: local mode keeps the secret key in .speccy/state/key, mode 0600.
	sealer, err := kernel.LocalSealer(keyFile)
	if err != nil {
		return nil, nil, err
	}
	a, err := app.New(ctx, db, sealer, app.Options{Root: root, ProfilesDir: filepath.Join(root.Dir(), ".speccy", "profiles"), FolderRepo: actionRepo})
	if err != nil {
		return nil, nil, err
	}
	return a, db, nil
}

// localHandler is the local-mode handler: the one local user, with the role table (DEC-015).
func localHandler(spa fs.FS, a *app.App, db *store.DB) nethttp.Handler {
	opts := speccyhttp.Options{Actor: speccyhttp.LocalActor, Authz: &speccyhttp.Authz{DB: db, Workspace: a.Workspace}}
	return speccyhttp.Handler(spa, a.API, opts)
}

// isTerminal reports whether f is a terminal, so a command may ask questions.
func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

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
