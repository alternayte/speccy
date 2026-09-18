// Command speccy reviews markdown spec bundles and returns one verdict.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/web"
)

// Exit codes from SDD §12.2.
const (
	exitOK    = 0
	exitUsage = 2
	exitRun   = 3
)

const usage = `Usage:
  speccy [--addr host:port] [--no-open]   Start local mode and open the browser.
  speccy serve [--addr host:port]         Start local mode without opening a browser.
  speccy version                          Print the version.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "" && args[0][0] == '-' {
		return local(args, true, stdout, stderr)
	}
	switch args[0] {
	case "serve":
		return local(args[1:], false, stdout, stderr)
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

func local(args []string, openBrowser bool, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("speccy", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	addr := fl.String("addr", "127.0.0.1:7878", "address to listen on (loopback only)")
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
	spa, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		fmt.Fprintf(stderr, "Speccy did not start: %v.\n", err)
		return exitRun
	}

	url := "http://" + ln.Addr().String()
	fmt.Fprintf(stdout, "Speccy is running at %s\nPress Ctrl+C to stop.\n", url)
	if openBrowser && !*noOpen {
		if err := openURL(url); err != nil {
			fmt.Fprintf(stderr, "Speccy could not open the browser: %v. Open %s yourself.\n", err, url)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := speccyhttp.Serve(ctx, ln, speccyhttp.Handler(spa)); err != nil {
		fmt.Fprintf(stderr, "Speccy stopped: %v.\n", err)
		return exitRun
	}
	return exitOK
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
