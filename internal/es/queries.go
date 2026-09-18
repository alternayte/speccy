package es

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	sqlitedb "github.com/alternayte/speccy/db/sqlite"
)

// dbtx is the part of *sql.DB and *sql.Tx that the generated queries use.
// pgdb.DBTX and sqlitedb.DBTX have the same method set.
type dbtx = pgdb.DBTX

// queries has one implementation per engine (SDD §11.2).
type queries interface {
	insertStream(ctx context.Context, db dbtx, s Stream) (int64, error)
	updateStream(ctx context.Context, db dbtx, s Stream, expected int64) (int64, error)
	insertEvent(ctx context.Context, db dbtx, r Recorded) error
	getStream(ctx context.Context, db dbtx, id uuid.UUID) (Stream, error)
	listEvents(ctx context.Context, db dbtx, id uuid.UUID) ([]Recorded, error)
}

type postgresQueries struct{}

func (postgresQueries) insertStream(ctx context.Context, db dbtx, s Stream) (int64, error) {
	return pgdb.New(db).InsertStream(ctx, pgdb.InsertStreamParams{
		StreamID: s.ID, StreamType: s.Type, Version: s.Version, State: s.State, UpdatedAt: s.UpdatedAt,
	})
}

func (postgresQueries) updateStream(ctx context.Context, db dbtx, s Stream, expected int64) (int64, error) {
	return pgdb.New(db).UpdateStream(ctx, pgdb.UpdateStreamParams{
		StreamID: s.ID, StreamType: s.Type, Version: s.Version, State: s.State, UpdatedAt: s.UpdatedAt,
		ExpectedVersion: expected,
	})
}

func (postgresQueries) insertEvent(ctx context.Context, db dbtx, r Recorded) error {
	return pgdb.New(db).InsertEvent(ctx, pgdb.InsertEventParams{
		StreamID: r.StreamID, Version: r.Version, EventType: r.Type,
		Payload: r.Payload, Metadata: r.Metadata, OccurredAt: r.OccurredAt,
	})
}

func (postgresQueries) getStream(ctx context.Context, db dbtx, id uuid.UUID) (Stream, error) {
	row, err := pgdb.New(db).GetStream(ctx, id)
	if err != nil {
		return Stream{}, err
	}
	return Stream{ID: row.StreamID, Type: row.StreamType, Version: row.Version, State: row.State, UpdatedAt: row.UpdatedAt.UTC()}, nil
}

func (postgresQueries) listEvents(ctx context.Context, db dbtx, id uuid.UUID) ([]Recorded, error) {
	rows, err := pgdb.New(db).ListEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Recorded, len(rows))
	for i, r := range rows {
		out[i] = Recorded{
			StreamID: r.StreamID, Version: r.Version, Type: r.EventType,
			Payload: r.Payload, Metadata: r.Metadata, OccurredAt: r.OccurredAt.UTC(),
		}
	}
	return out, nil
}

type sqliteQueries struct{}

func (sqliteQueries) insertStream(ctx context.Context, db dbtx, s Stream) (int64, error) {
	return sqlitedb.New(db).InsertStream(ctx, sqlitedb.InsertStreamParams{
		StreamID: s.ID, StreamType: s.Type, Version: s.Version, State: string(s.State), UpdatedAt: s.UpdatedAt,
	})
}

func (sqliteQueries) updateStream(ctx context.Context, db dbtx, s Stream, expected int64) (int64, error) {
	return sqlitedb.New(db).UpdateStream(ctx, sqlitedb.UpdateStreamParams{
		StreamID: s.ID, StreamType: s.Type, Version: s.Version, State: string(s.State), UpdatedAt: s.UpdatedAt,
		ExpectedVersion: expected,
	})
}

func (sqliteQueries) insertEvent(ctx context.Context, db dbtx, r Recorded) error {
	return sqlitedb.New(db).InsertEvent(ctx, sqlitedb.InsertEventParams{
		StreamID: r.StreamID, Version: r.Version, EventType: r.Type,
		Payload: string(r.Payload), Metadata: string(r.Metadata), OccurredAt: r.OccurredAt,
	})
}

func (sqliteQueries) getStream(ctx context.Context, db dbtx, id uuid.UUID) (Stream, error) {
	row, err := sqlitedb.New(db).GetStream(ctx, id)
	if err != nil {
		return Stream{}, err
	}
	return Stream{ID: row.StreamID, Type: row.StreamType, Version: row.Version, State: json.RawMessage(row.State), UpdatedAt: row.UpdatedAt.UTC()}, nil
}

func (sqliteQueries) listEvents(ctx context.Context, db dbtx, id uuid.UUID) ([]Recorded, error) {
	rows, err := sqlitedb.New(db).ListEvents(ctx, id)
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
