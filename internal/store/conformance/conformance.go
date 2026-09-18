// Package conformance is the store conformance suite (T-020). Every check runs against
// both engines, so SQLite and Postgres behave the same for the code above them.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/store"
)

// Run runs every conformance check on a fresh database from open.
func Run(t *testing.T, open func(t *testing.T) *store.DB) {
	t.Run("MigrateIsIdempotent", func(t *testing.T) { migrateIsIdempotent(t, open(t)) })
	t.Run("MigrationsRollBackAndReapply", func(t *testing.T) { migrationsRollBackAndReapply(t, open(t)) })
	t.Run("EventStoreRoundTrip", func(t *testing.T) { eventStoreRoundTrip(t, open(t)) })
	t.Run("EventStoreStaleVersionConflicts", func(t *testing.T) { eventStoreStaleVersionConflicts(t, open(t)) })
	t.Run("EventStoreNewStreamTwiceConflicts", func(t *testing.T) { eventStoreNewStreamTwiceConflicts(t, open(t)) })
	t.Run("EventStoreMissingStream", func(t *testing.T) { eventStoreMissingStream(t, open(t)) })
	t.Run("BundleHeadMovesOnlyFromExpected", func(t *testing.T) { bundleHeadMovesOnlyFromExpected(t, open(t)) })
	t.Run("BlobsAreContentAddressed", func(t *testing.T) { blobsAreContentAddressed(t, open(t)) })
	t.Run("ListsPage", func(t *testing.T) { listsPage(t, open(t)) })
}

func migrateIsIdempotent(t *testing.T, db *store.DB) {
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func migrationsRollBackAndReapply(t *testing.T, db *store.DB) {
	ctx := context.Background()
	p, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

func newStore(t *testing.T, db *store.DB) *es.Store {
	return es.New(db, nil)
}

func eventStoreRoundTrip(t *testing.T, db *store.DB) {
	ctx := context.Background()
	s := newStore(t, db)
	id := uuid.Must(uuid.NewV7())
	before := time.Now().UTC().Add(-time.Second)
	_, err := s.Append(ctx, es.Append{
		StreamID: id, StreamType: "thread", Expected: 0,
		State: json.RawMessage(`{"open":true,"n":1}`),
		Events: []es.Event{
			{Type: "ThreadOpened", Payload: json.RawMessage(`{"v":1,"title":"Retries"}`), Metadata: json.RawMessage(`{"by":"nathan"}`)},
			{Type: "MessagePosted", Payload: json.RawMessage(`{"v":1,"text":"Who owns retries? ünïcödé"}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	st, err := s.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != id || st.Type != "thread" || st.Version != 2 {
		t.Errorf("stream = %+v, want id %s, type thread, version 2", st, id)
	}
	assertJSON(t, st.State, `{"open":true,"n":1}`)
	if st.UpdatedAt.Location() != time.UTC || st.UpdatedAt.Before(before) {
		t.Errorf("updated_at = %v, want UTC and after %v", st.UpdatedAt, before)
	}

	events, err := s.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	for i, want := range []struct {
		typ, payload, metadata string
	}{
		{"ThreadOpened", `{"v":1,"title":"Retries"}`, `{"by":"nathan"}`},
		{"MessagePosted", `{"v":1,"text":"Who owns retries? ünïcödé"}`, `{}`},
	} {
		e := events[i]
		if e.Version != int64(i+1) || e.Type != want.typ || e.StreamID != id {
			t.Errorf("event %d = version %d type %s, want version %d type %s", i, e.Version, e.Type, i+1, want.typ)
		}
		assertJSON(t, e.Payload, want.payload)
		assertJSON(t, e.Metadata, want.metadata)
		if e.OccurredAt.Location() != time.UTC {
			t.Errorf("event %d occurred_at %v is not UTC", i, e.OccurredAt)
		}
	}

	_, err = s.Append(ctx, es.Append{
		StreamID: id, StreamType: "thread", Expected: 2, State: json.RawMessage(`{"open":false}`),
		Events: []es.Event{{Type: "ThreadResolved", Payload: json.RawMessage(`{"v":1}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err = s.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != 3 {
		t.Errorf("version = %d, want 3", st.Version)
	}
	assertJSON(t, st.State, `{"open":false}`)
}

func eventStoreStaleVersionConflicts(t *testing.T, db *store.DB) {
	ctx := context.Background()
	s := newStore(t, db)
	id := uuid.Must(uuid.NewV7())
	appendOne(t, s, id, 0)
	appendOne(t, s, id, 1)
	_, err := s.Append(ctx, es.Append{
		StreamID: id, StreamType: "thread", Expected: 1, State: json.RawMessage(`{}`),
		Events: []es.Event{{Type: "MessagePosted", Payload: json.RawMessage(`{"v":1}`)}},
	})
	if !errors.Is(err, es.ErrConflict) {
		t.Fatalf("append at stale version: err = %v, want ErrConflict", err)
	}
	events, err := s.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("got %d events after the rejected append, want 2", len(events))
	}
}

func eventStoreNewStreamTwiceConflicts(t *testing.T, db *store.DB) {
	s := newStore(t, db)
	id := uuid.Must(uuid.NewV7())
	appendOne(t, s, id, 0)
	_, err := s.Append(context.Background(), es.Append{
		StreamID: id, StreamType: "thread", Expected: 0, State: json.RawMessage(`{}`),
		Events: []es.Event{{Type: "ThreadOpened", Payload: json.RawMessage(`{"v":1}`)}},
	})
	if !errors.Is(err, es.ErrConflict) {
		t.Fatalf("second create: err = %v, want ErrConflict", err)
	}
}

func eventStoreMissingStream(t *testing.T, db *store.DB) {
	_, err := newStore(t, db).Load(context.Background(), uuid.Must(uuid.NewV7()))
	if !errors.Is(err, es.ErrNotFound) {
		t.Fatalf("load missing stream: err = %v, want ErrNotFound", err)
	}
}

func appendOne(t *testing.T, s *es.Store, id uuid.UUID, expected int64) {
	t.Helper()
	_, err := s.Append(context.Background(), es.Append{
		StreamID: id, StreamType: "thread", Expected: expected, State: json.RawMessage(`{}`),
		Events: []es.Event{{Type: "MessagePosted", Payload: json.RawMessage(`{"v":1}`)}},
	})
	if err != nil {
		t.Fatalf("append at %d: %v", expected, err)
	}
}

// assertJSON compares by value: Postgres jsonb does not keep key order or spacing.
func assertJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("stored JSON %q does not parse: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Errorf("JSON = %s, want %s", gb, wb)
	}
}
