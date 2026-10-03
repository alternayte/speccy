package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	nethttp "net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing/fstest"
	"time"

	"github.com/alternayte/speccy/internal/app"
	speccyhttp "github.com/alternayte/speccy/internal/http"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/owner"
	"github.com/alternayte/speccy/internal/source/local"
)

// processKind names this process for a person, in the lock file and in a message of another
// process: "the app", or the command. run sets it.
var processKind = "the app"

// ownerStartWait is how long a client process waits for an owner that is still starting.
const ownerStartWait = time.Minute

// seat is the place of this process at a state folder (#87). The first process to start in a
// folder is the owner: it holds the store, the review worker, the progress of each run and
// the watch on the files. Each later process is a client process: it opens no store, and
// sends its API calls to the owner. When the owner stops, a client process takes the lock on
// its next call and is the owner.
//
// A seat is the API handler of its process, whichever of the two it is.
type seat struct {
	root  *local.Root
	state string
	key   string
	// spa is the web app that the local app serves. It is nil for a process with no browser.
	spa fs.FS

	mu     sync.Mutex
	own    *owned
	peer   owner.Info
	closed bool
	http   *nethttp.Client
}

// owned is what the owner holds.
type owned struct {
	lock    *owner.Lock
	app     *app.App
	handler nethttp.Handler
	stop    context.CancelFunc
	served  chan struct{}
	// calls counts the API calls in flight that are not a GET. An owner that wants to exit
	// waits for them, so a client's question or review does not fail because the owner ended.
	// A GET can be a stream of run events that stays open, so it does not count.
	calls atomic.Int64
}

// counted counts the calls in flight that are not a GET.
func (o *owned) counted(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method != nethttp.MethodGet {
			o.calls.Add(1)
			defer o.calls.Add(-1)
		}
		next.ServeHTTP(w, r)
	})
}

// seats are the seats this process took. run closes them when the command ends, so an owner
// finishes its queue and frees the lock whichever command opened it.
var (
	seatsMu sync.Mutex
	seats   []*seat
)

// closeSeats gives up each seat of this process.
func closeSeats(stderr io.Writer) {
	seatsMu.Lock()
	open := seats
	seats = nil
	seatsMu.Unlock()
	for _, s := range open {
		s.close(stderr)
	}
}

// openSeat takes the owner lock of the state folder, or finds the owner that holds it.
func openSeat(root *local.Root, state, key string, spa fs.FS) (*seat, error) {
	if err := os.MkdirAll(state, 0o700); err != nil {
		return nil, err
	}
	s := &seat{root: root, state: state, key: key, spa: spa, http: &nethttp.Client{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.settle(); err != nil {
		return nil, err
	}
	seatsMu.Lock()
	seats = append(seats, s)
	seatsMu.Unlock()
	return s, nil
}

// client is the API client of this process: in process when it is the owner, and through the
// owner's listener when it is a client process.
func (s *seat) client() (*api.ClientWithResponses, error) {
	return api.NewClientWithResponses("http://speccy.local/api/v1", api.WithHTTPClient(speccyhttp.InProcess{Handler: s}))
}

// owner returns the process that owns the state folder when this one is a client process.
func (s *seat) ownerInfo() (owner.Info, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peer, s.own == nil
}

// settle makes this process the owner, or a client process of the owner. The caller holds mu.
func (s *seat) settle() error {
	deadline := time.Now().Add(ownerStartWait)
	for {
		lock, err := owner.Take(s.state)
		if err == nil {
			return s.becomeOwner(lock)
		}
		if !errors.Is(err, owner.ErrHeld) {
			return err
		}
		info, ok, err := owner.Read(s.state)
		if err != nil {
			return err
		}
		if ok && info.Version != kernel.Version {
			// A call that the owner's version does not have must not half work.
			return fmt.Errorf("the state of this folder belongs to Speccy %s, which runs as %s (process %d), and this is Speccy %s. Stop that process, or use the same version",
				info.Version, info.Kind, info.PID, kernel.Version)
		}
		if ok && info.Address != "" {
			s.own, s.peer = nil, info
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another Speccy process holds %s and did not finish its start within %s. Stop that process", s.state, ownerStartWait)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// becomeOwner opens the store, starts the worker and the watch on the files, and opens the
// listener for client processes. The lock file then holds the address and the token.
func (s *seat) becomeOwner(lock *owner.Lock) (err error) {
	ctx, stop := context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			stop()
			lock.Release()
		}
	}()
	info := owner.Info{Version: kernel.Version, Kind: processKind, PID: os.Getpid()}
	// A process that starts now reads who the owner is, and waits for the address.
	if err := lock.Write(info); err != nil {
		return err
	}
	a, db, err := openApp(ctx, s.root, s.state, s.key)
	if err != nil {
		return err
	}
	// The owner always watches the folder, also when it is a CLI command: a client app must
	// see an edit on disk while another process is the owner (REQ-005).
	go func() {
		_ = s.root.Watch(ctx, 300*time.Millisecond, func() {
			if err := a.Profiles.Reload(ctx); err != nil && ctx.Err() == nil {
				slog.Error("reload of the profiles failed", "err", err)
			}
			if err := a.Bundles.Sync(ctx); err != nil && ctx.Err() == nil {
				slog.Error("sync after a change on disk failed", "err", err)
			}
		})
	}()
	spa := s.spa
	if spa == nil {
		spa = fstest.MapFS{}
	}
	own := &owned{lock: lock, app: a, stop: stop}
	handler := own.counted(localHandler(spa, a, db))
	own.handler = handler
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	if info.Token, err = owner.NewToken(); err != nil {
		_ = ln.Close()
		return err
	}
	info.Address = ln.Addr().String()
	served := make(chan struct{})
	go func() {
		defer close(served)
		if err := speccyhttp.Serve(ctx, ln, tokenOnly(info.Token, handler)); err != nil && ctx.Err() == nil {
			slog.Error("the listener for client processes stopped", "err", err)
		}
	}()
	if err := lock.Write(info); err != nil {
		stop()
		<-served
		return err
	}
	own.served = served
	s.own, s.peer = own, owner.Info{}
	return nil
}

// tokenOnly accepts a call only with the token of the lock file. A web page in the browser can
// reach a loopback port, and it cannot read a file in the state folder.
func tokenOnly(token string, next nethttp.Handler) nethttp.Handler {
	want := []byte("Bearer " + token)
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			speccyhttp.WriteProblem(w, nethttp.StatusUnauthorized, "owner_token_required",
				"This listener is for the Speccy processes of this folder. A call needs the token in "+owner.File+".")
			return
		}
		// The token is not a credential of the API behind it.
		r.Header.Del("Authorization")
		next.ServeHTTP(w, r)
	})
}

// ServeHTTP serves a call in process when this process is the owner, and sends it to the
// owner otherwise. When the owner is gone, this process takes over and serves the call itself.
func (s *seat) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body []byte
	read := false
	for range 3 {
		s.mu.Lock()
		own, peer := s.own, s.peer
		s.mu.Unlock()
		if own != nil {
			if read {
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			own.handler.ServeHTTP(w, r)
			return
		}
		// A client app serves its own pages; only the API is the owner's.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if s.spa != nil {
				speccyhttp.SPA(s.spa).ServeHTTP(w, r)
				return
			}
			nethttp.NotFound(w, r)
			return
		}
		// The body is read once, so the call can go to this process after a takeover. A call
		// made in process has no body at all.
		if !read && r.Body != nil {
			var err error
			if body, err = io.ReadAll(r.Body); err != nil {
				speccyhttp.WriteProblem(w, nethttp.StatusBadRequest, "bad_request", err.Error())
				return
			}
			read = true
		}
		if s.forward(w, r, peer, body) {
			return
		}
		if err := s.takeOver(peer); err != nil {
			speccyhttp.WriteProblem(w, nethttp.StatusServiceUnavailable, "no_owner", sentence(err))
			return
		}
	}
	speccyhttp.WriteProblem(w, nethttp.StatusServiceUnavailable, "no_owner", "The Speccy process that owns this folder changed three times during one call. Try again.")
}

// forward sends one call to the owner and copies the answer as it arrives, so a stream of run
// events stays live. done is false only when the owner did not take the connection: it is
// gone, and nothing went to w.
func (s *seat) forward(w nethttp.ResponseWriter, r *nethttp.Request, peer owner.Info, body []byte) (done bool) {
	out, err := nethttp.NewRequestWithContext(r.Context(), r.Method, "http://"+peer.Address+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		speccyhttp.WriteProblem(w, nethttp.StatusBadRequest, "bad_request", err.Error())
		return true
	}
	out.Header = r.Header.Clone()
	out.Header.Set("Authorization", "Bearer "+peer.Token)
	resp, err := s.http.Do(out)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return false
		}
		if r.Context().Err() == nil {
			speccyhttp.WriteProblem(w, nethttp.StatusBadGateway, "owner_failed",
				fmt.Sprintf("%s (process %d), which owns this folder, did not answer: %v.", peer.Kind, peer.PID, err))
		}
		return true
	}
	defer func() { _ = resp.Body.Close() }()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(nethttp.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return true
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return true
		}
	}
}

// takeOver runs when the owner is gone: this process takes the lock and is the owner, or finds
// the client process that took it first.
func (s *seat) takeOver(gone owner.Info) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("this Speccy process is stopping")
	}
	if s.own != nil || s.peer != gone {
		return nil // another call of this process settled it already
	}
	return s.settle()
}

// close gives the seat up. An owner first finishes each job in its queue, including a job
// that a client process queued: a review that the app queued must not fail because a CLI
// command ended. A signal during that wait stops the wait.
func (s *seat) close(stderr io.Writer) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	own := s.own
	s.mu.Unlock()
	if own == nil {
		return
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	said := false
wait:
	for {
		idle, err := own.app.Idle(context.Background())
		if (idle || err != nil) && own.calls.Load() == 0 {
			break
		}
		if !said {
			said = true
			fmt.Fprintln(stderr, "Speccy finishes the reviews in its queue and its pull request batches before it stops. Press Ctrl+C to stop now.")
		}
		select {
		case <-sig:
			break wait
		case <-time.After(200 * time.Millisecond):
		}
	}
	own.stop()
	<-own.served
	own.lock.Release()
}

// sentence ends the text of an error with a full stop.
func sentence(err error) string {
	return strings.TrimRight(err.Error(), ".") + "."
}
