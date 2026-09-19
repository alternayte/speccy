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

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
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

// Projection updates a read model from the new snapshot and the new events. It runs in the
// append transaction, so a projection error rolls back the append.
type Projection func(ctx context.Context, tx store.Tx, snap Stream, events []Recorded) error

// Store appends to and reads streams.
type Store struct {
	db          *store.DB
	now         func() time.Time
	projections map[string][]Projection
}

// New returns a store on db. projections maps a stream type to the projections for its events.
func New(db *store.DB, projections map[string][]Projection) *Store {
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }, projections: projections}
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
		q := tx.Queries()
		var n int64
		var err error
		// T-030: the version check and the write are one statement, so two writers cannot both pass.
		if a.Expected == 0 {
			n, err = q.InsertStream(ctx, pgdb.InsertStreamParams{
				StreamID: snap.ID, StreamType: snap.Type, Version: snap.Version, State: dbtype.JSON(snap.State), UpdatedAt: snap.UpdatedAt,
			})
		} else {
			n, err = q.UpdateStream(ctx, pgdb.UpdateStreamParams{
				StreamID: snap.ID, StreamType: snap.Type, Version: snap.Version, State: dbtype.JSON(snap.State), UpdatedAt: snap.UpdatedAt,
				ExpectedVersion: a.Expected,
			})
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrConflict
		}
		for _, r := range recorded {
			err := q.InsertEvent(ctx, pgdb.InsertEventParams{
				StreamID: r.StreamID, Version: r.Version, EventType: r.Type,
				Payload: dbtype.JSON(r.Payload), Metadata: dbtype.JSON(r.Metadata), OccurredAt: r.OccurredAt,
			})
			if err != nil {
				return err
			}
		}
		// T-031: projections run in the append transaction.
		for _, p := range s.projections[a.StreamType] {
			if err := p(ctx, tx, snap, recorded); err != nil {
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
	row, err := s.db.Queries().GetStream(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Stream{}, ErrNotFound
	}
	if err != nil {
		return Stream{}, err
	}
	return Stream{ID: row.StreamID, Type: row.StreamType, Version: row.Version, State: json.RawMessage(row.State), UpdatedAt: row.UpdatedAt.UTC()}, nil
}

// Events returns every event of a stream in version order.
func (s *Store) Events(ctx context.Context, id uuid.UUID) ([]Recorded, error) {
	rows, err := s.db.Queries().ListEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Recorded, len(rows))
	for i, r := range rows {
		out[i] = Recorded{
			StreamID: r.StreamID, Version: r.Version, Type: r.EventType,
			Payload: json.RawMessage(r.Payload), Metadata: json.RawMessage(r.Metadata), OccurredAt: r.OccurredAt.UTC(),
		}
	}
	return out, nil
}

// Run loads a stream's snapshot, decides new events from it, evolves the snapshot, and
// appends, in the pure decide/evolve style of DEC-008. A stream that does not exist starts
// from the zero state. On a version conflict it loads and decides again, up to 3 times.
func Run[S any](ctx context.Context, s *Store, streamType string, id uuid.UUID,
	decide func(S) ([]Event, error), evolve func(S, Event) S) (S, error) {
	var state S
	for attempt := 0; ; attempt++ {
		state = *new(S)
		var expected int64
		snap, err := s.Load(ctx, id)
		switch {
		case err == nil:
			if err := json.Unmarshal(snap.State, &state); err != nil {
				return state, fmt.Errorf("read the snapshot of %s %s: %w", streamType, id, err)
			}
			expected = snap.Version
		case errors.Is(err, ErrNotFound):
		default:
			return state, err
		}
		events, err := decide(state)
		if err != nil || len(events) == 0 {
			return state, err
		}
		for _, e := range events {
			state = evolve(state, e)
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return state, err
		}
		_, err = s.Append(ctx, Append{StreamID: id, StreamType: streamType, Expected: expected, State: raw, Events: events})
		if errors.Is(err, ErrConflict) && attempt < 2 {
			continue
		}
		return state, err
	}
}

// NewEvent marshals data as the payload of an event of type t. Every payload carries v.
func NewEvent(t string, data any) Event {
	b, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("event %s does not marshal: %v", t, err))
	}
	return Event{Type: t, Payload: b}
}
