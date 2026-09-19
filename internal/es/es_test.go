package es_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/store"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// T-030
func TestEventStore_ConcurrentAppendRejected(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			s := es.New(e.Open(t), nil)
			id := uuid.Must(uuid.NewV7())
			if _, err := s.Append(ctx, message(id, 0)); err != nil {
				t.Fatal(err)
			}

			const writers = 8
			errs := make([]error, writers)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range writers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, errs[i] = s.Append(ctx, message(id, 1))
				}()
			}
			close(start)
			wg.Wait()

			ok, conflicts := 0, 0
			for _, err := range errs {
				switch {
				case err == nil:
					ok++
				case errors.Is(err, es.ErrConflict):
					conflicts++
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}
			if ok != 1 || conflicts != writers-1 {
				t.Errorf("%d appends succeeded and %d conflicted; want 1 and %d", ok, conflicts, writers-1)
			}
			events, err := s.Events(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 2 {
				t.Errorf("stream has %d events, want 2", len(events))
			}
		})
	}
}

// T-031
func TestEventStore_InlineProjectionAtomic(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			db := e.Open(t)
			if _, err := db.SQL.ExecContext(ctx, `CREATE TABLE test_view (stream_id TEXT NOT NULL, version BIGINT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			var failNext bool
			project := func(ctx context.Context, tx store.Tx, _ es.Stream, events []es.Recorded) error {
				for _, r := range events {
					q := `INSERT INTO test_view (stream_id, version) VALUES ($1, $2)`
					if _, err := tx.ExecContext(ctx, q, r.StreamID.String(), r.Version); err != nil {
						return err
					}
				}
				// The rows above are written before the failure, so a rollback must remove them.
				if failNext {
					return errors.New("projection failed")
				}
				return nil
			}
			s := es.New(db, map[string][]es.Projection{"thread": {project}})
			id := uuid.Must(uuid.NewV7())

			if _, err := s.Append(ctx, message(id, 0)); err != nil {
				t.Fatal(err)
			}
			if got := viewRows(t, db); got != 1 {
				t.Fatalf("after a good append the view has %d rows, want 1", got)
			}

			failNext = true
			if _, err := s.Append(ctx, message(id, 1)); err == nil {
				t.Fatal("append with a failing projection returned nil")
			}
			if got := viewRows(t, db); got != 1 {
				t.Errorf("after a failed projection the view has %d rows, want 1", got)
			}
			st, err := s.Load(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if st.Version != 1 {
				t.Errorf("after a failed projection the stream version is %d, want 1", st.Version)
			}
			events, err := s.Events(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 {
				t.Errorf("after a failed projection the stream has %d events, want 1", len(events))
			}

			failNext = false
			if _, err := s.Append(ctx, message(id, 0)); !errors.Is(err, es.ErrConflict) {
				t.Fatalf("conflicting append: err = %v, want ErrConflict", err)
			}
			if got := viewRows(t, db); got != 1 {
				t.Errorf("after a conflict the view has %d rows, want 1", got)
			}
		})
	}
}

func message(id uuid.UUID, expected int64) es.Append {
	return es.Append{
		StreamID: id, StreamType: "thread", Expected: expected, State: json.RawMessage(`{}`),
		Events: []es.Event{{Type: "MessagePosted", Payload: json.RawMessage(`{"v":1}`)}},
	}
}

func viewRows(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM test_view`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
