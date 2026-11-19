package queue_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alternayte/speccy/internal/queue"
	"github.com/alternayte/speccy/internal/store"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// The workers run a job inserted in a transaction once it commits, on each store, and a job
// whose transaction rolls back never runs. River's SQLite driver is experimental, so this pins
// that it works on the store as Speccy opens it.
func TestQueueRunsCommittedJobs(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db := e.Open(t)
			q, err := queue.Open(ctx, db, queue.Config{Workers: map[string]int{queue.Interactive: 2}, Poll: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			var ran atomic.Int32
			q.Register("count", queue.Interactive, 1, func(context.Context, []byte) error { ran.Add(1); return nil }, nil)
			if err := q.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.InTx(ctx, func(tx store.Tx) error {
				_, err := q.Insert(ctx, &tx, "count", map[string]int{"n": 1}, nil)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			_ = db.InTx(ctx, func(tx store.Tx) error {
				if _, err := q.Insert(ctx, &tx, "count", map[string]int{"n": 2}, nil); err != nil {
					t.Fatal(err)
				}
				return errors.New("roll back")
			})
			waitIdle(t, q)
			if n := ran.Load(); n != 1 {
				t.Fatalf("ran %d jobs, want 1", n)
			}
		})
	}
}

// EndOrphans deletes the jobs a stopped process left and ends their work.
func TestEndOrphans(t *testing.T) {
	ctx := context.Background()
	db := storetest.Engines()[0].Open(t)
	q, err := queue.Open(ctx, db, queue.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var ended []string
	q.Register("run", queue.Interactive, 1, func(context.Context, []byte) error { return nil },
		func(_ context.Context, payload []byte) error { ended = append(ended, string(payload)); return nil })
	if _, err := q.Insert(ctx, nil, "run", map[string]string{"run_id": "a"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.EndOrphans(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ended) != 1 || ended[0] != `{"run_id":"a"}` {
		t.Fatalf("ended %q", ended)
	}
	if idle, err := q.Idle(ctx); !idle || err != nil {
		t.Fatalf("idle %v, err %v", idle, err)
	}
}

func waitIdle(t *testing.T, q *queue.Queue) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if idle, err := q.Idle(context.Background()); err == nil && idle {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the queue did not run its jobs")
}

// A saved "reviews at a time" resizes a running queue: the queue stops and starts again with
// the new size, and still runs jobs after it.
func TestSetWorkersKeepsTheQueueRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := storetest.Engines()[0].Open(t)
	q, err := queue.Open(ctx, db, queue.Config{Workers: map[string]int{queue.Interactive: 1}, Poll: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int32
	q.Register("count", queue.Interactive, 1, func(context.Context, []byte) error { ran.Add(1); return nil }, nil)
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	q.SetWorkers(ctx, queue.Interactive, 4)
	time.Sleep(300 * time.Millisecond)
	for i := range 3 {
		if _, err := q.Insert(ctx, nil, "count", map[string]int{"n": i}, nil); err != nil {
			t.Fatal(err)
		}
	}
	waitIdle(t, q)
	if n := ran.Load(); n != 3 {
		t.Fatalf("ran %d jobs after the resize, want 3", n)
	}
}
