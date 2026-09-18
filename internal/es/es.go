// Package es is the event store lite (SDD §11.3, DEC-008): streams with a snapshot row and
// a version check, and inline projections that run in the append transaction.
// Aggregates stay pure: decide(state, command) → events and evolve(state, event) → state.
package es

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/store"
)

// ErrConflict means the stream version is not the expected version.
// The caller loads the stream again and decides again.
var ErrConflict = errors.New("event store: stream version conflict")

// ErrNotFound means the stream does not exist.
var ErrNotFound = errors.New("event store: stream not found")

// Event is one new event to append. Payload holds the event type's fields, including its v field.
type Event struct {
	Type     string
	Payload  json.RawMessage
	Metadata json.RawMessage
}

// Recorded is one stored event.
type Recorded struct {
	StreamID   uuid.UUID
	Version    int64
	Type       string
	Payload    json.RawMessage
	Metadata   json.RawMessage
	OccurredAt time.Time
}

// Stream is the snapshot row of one stream.
type Stream struct {
	ID        uuid.UUID
	Type      string
	Version   int64
	State     json.RawMessage
	UpdatedAt time.Time
}

// Append is one append to one stream.
type Append struct {
	StreamID   uuid.UUID
	StreamType string
	// Expected is the version the caller decided on. 0 means a new stream.
	Expected int64
	// State is the snapshot after the events.
	State  json.RawMessage
	Events []Event
}

// Projection updates a read model. It runs in the append transaction, so a projection
// error rolls back the append.
type Projection func(ctx context.Context, tx store.Tx, events []Recorded) error

// Store appends to and reads streams.
type Store struct {
	db          *store.DB
	q           queries
	now         func() time.Time
	projections map[string][]Projection
}

// New returns a store on db. projections maps a stream type to the projections for its events.
func New(db *store.DB, projections map[string][]Projection) (*Store, error) {
	var q queries
	switch db.Engine {
	case store.SQLite:
		q = sqliteQueries{}
	case store.Postgres:
		q = postgresQueries{}
	default:
		return nil, fmt.Errorf("event store: unknown engine %q", db.Engine)
	}
	return &Store{db: db, q: q, now: func() time.Time { return time.Now().UTC() }, projections: projections}, nil
}

// Append writes the events, updates the snapshot, and runs the inline projections in one
// transaction. It returns ErrConflict when the stream is not at a.Expected.
func (s *Store) Append(ctx context.Context, a Append) ([]Recorded, error) {
	if len(a.Events) == 0 {
		return nil, errors.New("event store: append has no events")
	}
	if a.Expected < 0 {
		return nil, fmt.Errorf("event store: expected version %d is negative", a.Expected)
	}
	now := s.now()
	next := a.Expected + int64(len(a.Events))
	snap := Stream{ID: a.StreamID, Type: a.StreamType, Version: next, State: a.State, UpdatedAt: now}
	recorded := make([]Recorded, len(a.Events))
	for i, e := range a.Events {
		md := e.Metadata
		if len(md) == 0 {
			md = json.RawMessage(`{}`)
		}
		recorded[i] = Recorded{
			StreamID: a.StreamID, Version: a.Expected + int64(i) + 1, Type: e.Type,
			Payload: e.Payload, Metadata: md, OccurredAt: now,
		}
	}
	err := s.db.InTx(ctx, func(tx store.Tx) error {
		var n int64
		var err error
		// T-030: the version check and the write are one statement, so two writers cannot both pass.
		if a.Expected == 0 {
			n, err = s.q.insertStream(ctx, tx, snap)
		} else {
			n, err = s.q.updateStream(ctx, tx, snap, a.Expected)
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrConflict
		}
		for _, r := range recorded {
			if err := s.q.insertEvent(ctx, tx, r); err != nil {
				return err
			}
		}
		// T-031: projections run in the append transaction.
		for _, p := range s.projections[a.StreamType] {
			if err := p(ctx, tx, recorded); err != nil {
				return fmt.Errorf("event store: projection for %s: %w", a.StreamType, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return recorded, nil
}

// Load returns the snapshot row of a stream.
func (s *Store) Load(ctx context.Context, id uuid.UUID) (Stream, error) {
	st, err := s.q.getStream(ctx, s.db.SQL, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Stream{}, ErrNotFound
	}
	return st, err
}

// Events returns every event of a stream in version order.
func (s *Store) Events(ctx context.Context, id uuid.UUID) ([]Recorded, error) {
	return s.q.listEvents(ctx, s.db.SQL, id)
}
