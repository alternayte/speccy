// Package queue runs Speccy's background jobs on River (docs/specs/hosted-pr-review.md). Two
// queues share the work: interactive, for the jobs a person waits for, and pr, for the reviews
// the GitHub App starts. Each queue runs a set number of jobs at a time.
//
// A feature registers a handler for its job kind. A job carries its payload as JSON, the same
// payload the job table held before River.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"riverqueue.com/riverui"

	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// The queues.
const (
	Interactive = "interactive"
	PR          = "pr"
)

// Handler runs one job. A job that cannot be done again returns nil and stores its own
// failure; an error means River may try it again.
type Handler func(ctx context.Context, payload []byte) error

// Orphan ends the work of a job that no process runs any more, such as its run.
type Orphan func(ctx context.Context, payload []byte) error

// Config is the setup of the queue.
type Config struct {
	// Workers is the number of jobs each queue runs at a time. A queue with no entry runs one.
	Workers map[string]int
	// JobTimeout is the longest a job runs. A review stops itself at its own limit first.
	JobTimeout time.Duration
	// RescueAfter is how long a job may stay running before River gives it to a worker again:
	// the process that ran it stopped with no chance to say so.
	RescueAfter time.Duration
	// Poll is the fetch interval. Zero is River's default.
	Poll time.Duration
}

type kind struct {
	queue   string
	handle  Handler
	orphan  Orphan
	attempt int
	// every runs the kind on a schedule, with an empty payload. Zero: only when queued.
	every time.Duration
}

// Queue holds the River client and the handler of each job kind.
type Queue struct {
	db     *store.DB
	driver riverdriver.Driver[*sql.Tx]
	cfg    Config

	mu      sync.Mutex
	kinds   map[string]kind
	client  *river.Client[*sql.Tx]
	started bool
	workers map[string]int
	done    chan struct{}
}

// Open migrates River's tables in db and returns the queue. Register every kind before Start.
func Open(ctx context.Context, db *store.DB, cfg Config) (*Queue, error) {
	var driver riverdriver.Driver[*sql.Tx]
	if db.Engine == store.SQLite {
		driver = riversqlite.New(db.SQL)
	} else {
		driver = riverdatabasesql.New(db.SQL)
	}
	m, err := rivermigrate.New(driver, &rivermigrate.Config{Logger: quietLogger()})
	if err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}
	if _, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, fmt.Errorf("queue: migrate: %w", err)
	}
	if cfg.JobTimeout == 0 {
		cfg.JobTimeout = 30 * time.Minute
	}
	if cfg.RescueAfter == 0 {
		cfg.RescueAfter = cfg.JobTimeout
	}
	q := &Queue{db: db, driver: driver, cfg: cfg, kinds: map[string]kind{}, workers: map[string]int{}, done: make(chan struct{})}
	for name, n := range cfg.Workers {
		q.workers[name] = n
	}
	// An insert-only client until Start: a test runs jobs with RunNext.
	if err := q.build(false); err != nil {
		return nil, err
	}
	return q, nil
}

// Register sets the handler of a job kind, the queue it runs in, and how many times River
// tries it. orphan may be nil.
func (q *Queue) Register(name, queue string, attempts int, handle Handler, orphan Orphan) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if attempts < 1 {
		attempts = 1
	}
	q.kinds[name] = kind{queue: queue, handle: handle, orphan: orphan, attempt: attempts}
}

// Every makes a registered kind run on a schedule, with an empty payload, from the first
// start on.
func (q *Queue) Every(name string, every time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	k := q.kinds[name]
	k.every = every
	q.kinds[name] = k
}

// Start builds the client with its workers and starts it. It runs until ctx ends.
func (q *Queue) Start(ctx context.Context) error {
	q.mu.Lock()
	if err := q.build(true); err != nil {
		q.mu.Unlock()
		return err
	}
	q.started = true
	c := q.client
	q.mu.Unlock()
	if err := c.Start(ctx); err != nil {
		close(q.done)
		return fmt.Errorf("queue: start: %w", err)
	}
	go func() {
		defer close(q.done)
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		// A running job sees its context end and returns; River tries it again later.
		_ = c.StopAndCancel(stop)
	}()
	return nil
}

// Done is closed when the workers stopped after the context of Start ended, or at once when
// the queue never started. Close the store only after it.
func (q *Queue) Done() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.started {
		c := make(chan struct{})
		close(c)
		return c
	}
	return q.done
}

func (q *Queue) build(work bool) error {
	cfg := &river.Config{
		Logger:               quietLogger(),
		JobTimeout:           q.cfg.JobTimeout,
		RescueStuckJobsAfter: q.cfg.RescueAfter,
		FetchPollInterval:    q.cfg.Poll,
		PollOnly:             true,
	}
	if work {
		workers := river.NewWorkers()
		queues := map[string]river.QueueConfig{}
		for name, k := range q.kinds {
			river.AddWorkerArgs(workers, jobArgs{Name: name}, river.Worker[jobArgs](&worker{name: name, handle: k.handle}))
			if _, ok := queues[k.queue]; !ok {
				queues[k.queue] = river.QueueConfig{MaxWorkers: max(1, q.workers[k.queue])}
			}
		}
		for _, name := range []string{Interactive, PR} {
			if _, ok := queues[name]; !ok {
				queues[name] = river.QueueConfig{MaxWorkers: max(1, q.workers[name])}
			}
		}
		cfg.Workers, cfg.Queues = workers, queues
		for name, k := range q.kinds {
			if k.every == 0 {
				continue
			}
			args, opts := jobArgs{Name: name, Payload: json.RawMessage(`{}`)}, &river.InsertOpts{Queue: k.queue, MaxAttempts: k.attempt}
			cfg.PeriodicJobs = append(cfg.PeriodicJobs, river.NewPeriodicJob(river.PeriodicInterval(k.every),
				func() (river.JobArgs, *river.InsertOpts) { return args, opts }, &river.PeriodicJobOpts{RunOnStart: true}))
		}
	}
	c, err := river.NewClient(q.driver, cfg)
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}
	q.client = c
	return nil
}

// SetWorkers changes how many jobs a queue runs at a time. The running jobs of the queue end
// first, so the change waits for them, in the background.
func (q *Queue) SetWorkers(ctx context.Context, queue string, n int) {
	q.mu.Lock()
	if n < 1 {
		n = 1
	}
	if q.workers[queue] == n {
		q.mu.Unlock()
		return
	}
	q.workers[queue] = n
	c, started := q.client, q.started
	q.mu.Unlock()
	if !started {
		return
	}
	go func() {
		ctx := context.WithoutCancel(ctx)
		if err := c.Queues().Remove(ctx, queue); err != nil {
			slog.ErrorContext(ctx, "the queue did not stop for its new size", "queue", queue, "err", err)
			return
		}
		if err := c.Queues().Add(queue, river.QueueConfig{MaxWorkers: n}); err != nil {
			slog.ErrorContext(ctx, "the queue did not start with its new size", "queue", queue, "err", err)
		}
	}()
}

// Opts shapes one insert.
type Opts struct {
	// Queue overrides the kind's queue, as for a review the App starts.
	Queue string
	// Unique makes a second insert with the same key, while the first waits or runs, do nothing.
	Unique bool
}

// Insert queues a job of kind with payload in tx, so the job exists only when the work that
// asked for it commits. It returns the job's ID.
func (q *Queue) Insert(ctx context.Context, tx *store.Tx, name string, payload any, o *Opts) (int64, error) {
	args, opts, err := q.args(name, payload, o)
	if err != nil {
		return 0, err
	}
	q.mu.Lock()
	c := q.client
	q.mu.Unlock()
	var res *rivertype.JobInsertResult
	if tx != nil {
		res, err = c.InsertTx(ctx, tx.Tx, args, opts)
	} else {
		res, err = c.Insert(ctx, args, opts)
	}
	if err != nil {
		return 0, fmt.Errorf("queue: insert %s: %w", name, err)
	}
	return res.Job.ID, nil
}

func (q *Queue) args(name string, payload any, o *Opts) (jobArgs, *river.InsertOpts, error) {
	q.mu.Lock()
	k, ok := q.kinds[name]
	q.mu.Unlock()
	if !ok {
		return jobArgs{}, nil, fmt.Errorf("queue: no handler for %s", name)
	}
	raw, ok := payload.([]byte)
	if !ok {
		var err error
		if raw, err = json.Marshal(payload); err != nil {
			return jobArgs{}, nil, err
		}
	}
	opts := &river.InsertOpts{Queue: k.queue, MaxAttempts: k.attempt}
	if o != nil && o.Queue != "" {
		opts.Queue = o.Queue
	}
	if o != nil && o.Unique {
		opts.UniqueOpts = river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
			rivertype.JobStateRunning, rivertype.JobStateScheduled,
		}}
	}
	return jobArgs{Name: name, Payload: raw}, opts, nil
}

// Job is one job that waits or runs.
type Job struct {
	ID      int64
	Kind    string
	Queue   string
	State   string
	Payload []byte
}

var activeStates = []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
	rivertype.JobStateRunning, rivertype.JobStateScheduled,
}

// Active lists the jobs that wait or run, oldest first.
func (q *Queue) Active(ctx context.Context) ([]Job, error) {
	q.mu.Lock()
	c := q.client
	q.mu.Unlock()
	var out []Job
	params := river.NewJobListParams().States(activeStates...).OrderBy(river.JobListOrderByID, river.SortOrderAsc).First(1000)
	for {
		res, err := c.JobList(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("queue: list: %w", err)
		}
		for _, j := range res.Jobs {
			out = append(out, toJob(j))
		}
		if len(res.Jobs) < 1000 || res.LastCursor == nil {
			return out, nil
		}
		params = params.After(res.LastCursor)
	}
}

func toJob(j *rivertype.JobRow) Job {
	var a jobArgs
	_ = json.Unmarshal(j.EncodedArgs, &a)
	return Job{ID: j.ID, Kind: j.Kind, Queue: j.Queue, State: string(j.State), Payload: a.Payload}
}

// Cancel cancels a job that waits or runs. A running job sees its context end.
func (q *Queue) Cancel(ctx context.Context, id int64) error {
	q.mu.Lock()
	c := q.client
	q.mu.Unlock()
	if _, err := c.JobCancel(ctx, id); err != nil && !errors.Is(err, river.ErrNotFound) {
		return fmt.Errorf("queue: cancel: %w", err)
	}
	return nil
}

// Idle reports whether no job waits or runs.
func (q *Queue) Idle(ctx context.Context) (bool, error) {
	jobs, err := q.Active(ctx)
	return len(jobs) == 0, err
}

// EndOrphans ends every job that waits or runs, and the work of each one. The owner of a
// local state calls it before Start: one process owns the state, so a job it finds belongs
// to a process that stopped (#87).
func (q *Queue) EndOrphans(ctx context.Context) error {
	jobs, err := q.Active(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		q.mu.Lock()
		k := q.kinds[j.Kind]
		q.mu.Unlock()
		if k.orphan != nil {
			if err := k.orphan(ctx, j.Payload); err != nil {
				return err
			}
		}
		// River refuses to delete a running job. No process runs it, so the row goes.
		if _, err := q.db.SQL.ExecContext(ctx, q.sql("DELETE FROM river_job WHERE id = $1"), j.ID); err != nil {
			return fmt.Errorf("queue: end orphan: %w", err)
		}
	}
	return nil
}

// sql adapts a statement with $n placeholders to the store's engine.
func (q *Queue) sql(stmt string) string {
	if q.db.Engine == store.SQLite {
		return strings.ReplaceAll(stmt, "$1", "?")
	}
	return stmt
}

// RunNext runs the oldest job that waits, in this goroutine, and deletes it. It is for tests,
// which run a job at a known point instead of starting the workers.
func (q *Queue) RunNext(ctx context.Context) (ran bool, err error) {
	jobs, err := q.Active(ctx)
	if err != nil {
		return false, err
	}
	for _, j := range jobs {
		if j.State != string(rivertype.JobStateAvailable) {
			continue
		}
		q.mu.Lock()
		k, ok := q.kinds[j.Kind]
		q.mu.Unlock()
		if !ok {
			return false, fmt.Errorf("queue: no handler for %s", j.Kind)
		}
		runErr := k.handle(kernel.WithQueue(ctx, j.Queue), j.Payload)
		if _, err := q.client.JobDelete(ctx, j.ID); err != nil && !errors.Is(err, river.ErrNotFound) {
			return true, err
		}
		return true, runErr
	}
	return false, nil
}

// jobArgs is the River args of every kind: the kind name and the JSON payload.
type jobArgs struct {
	Name    string          `json:"-"`
	Payload json.RawMessage `json:"payload"`
}

func (a jobArgs) Kind() string { return a.Name }

// worker runs the jobs of one kind.
type worker struct {
	river.WorkerDefaults[jobArgs]
	name   string
	handle Handler
}

func (w *worker) Work(ctx context.Context, job *river.Job[jobArgs]) error {
	// The model calls of the job spend its queue's budget (docs/specs/hosted-operations.md).
	return w.handle(kernel.WithQueue(ctx, job.Queue), job.Args.Payload)
}

// quietLogger drops River's info logs; warnings and errors go to the default logger.
func quietLogger() *slog.Logger {
	return slog.New(levelHandler{slog.Default().Handler(), slog.LevelWarn})
}

type levelHandler struct {
	slog.Handler
	min slog.Level
}

func (h levelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

// Ready reports an error when the workers do not run.
func (q *Queue) Ready() error {
	q.mu.Lock()
	started := q.started
	q.mu.Unlock()
	if !started {
		return errors.New("the job workers have not started")
	}
	select {
	case <-q.done:
		return errors.New("the job workers stopped")
	default:
		return nil
	}
}

// UI returns River UI under prefix, and starts it until ctx ends.
func (q *Queue) UI(ctx context.Context, prefix string) (http.Handler, error) {
	q.mu.Lock()
	c := q.client
	q.mu.Unlock()
	h, err := riverui.NewHandler(&riverui.HandlerOpts{Endpoints: riverui.NewEndpoints(c, nil), Logger: quietLogger(), Prefix: prefix})
	if err != nil {
		return nil, fmt.Errorf("queue: River UI: %w", err)
	}
	if err := h.Start(ctx); err != nil {
		return nil, fmt.Errorf("queue: River UI: %w", err)
	}
	return h, nil
}
