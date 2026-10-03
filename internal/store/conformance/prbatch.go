package conformance

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// prReviewsAreOnePerCommit checks the record that makes a second batch skip a pull request with
// no new commit: a second review at the same commit replaces the first, a new commit is a new
// record, and stopping a batch touches only the pull requests that had not finished.
func prReviewsAreOnePerCommit(t *testing.T, db *store.DB) {
	ctx := context.Background()
	q := db.Queries()
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	batch := kernel.NewID()
	if err := q.InsertPrBatch(ctx, pgdb.InsertPrBatchParams{ID: batch, WorkspaceID: ws, Status: "running", Parallel: 3,
		Stages: dbtype.JSON(`{"stages":null}`), Source: "acme/specs", Estimate: dbtype.JSON(`{}`), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	record := func(sha string) {
		t.Helper()
		if err := q.RecordPrReview(ctx, pgdb.RecordPrReviewParams{WorkspaceID: ws, Repo: "acme/specs", Pull: 42, HeadSha: sha,
			BatchID: uuid.NullUUID{UUID: batch, Valid: true}, ReviewedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	has := func(sha string) int64 {
		t.Helper()
		n, err := q.HasPrReview(ctx, pgdb.HasPrReviewParams{WorkspaceID: ws, Repo: "acme/specs", Pull: 42, HeadSha: sha})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	record("aaa")
	record("aaa")
	if n := has("aaa"); n != 1 {
		t.Errorf("two reviews at one commit: %d records, want 1", n)
	}
	if n := has("bbb"); n != 0 {
		t.Errorf("a commit with no review: %d records, want 0", n)
	}

	for i, state := range []string{"posted", "waiting", "reviewing"} {
		if err := q.InsertPrBatchItem(ctx, pgdb.InsertPrBatchItemParams{BatchID: batch, Position: int64(i), Repo: "acme/specs", Pull: int64(40 + i),
			Url: "u", State: state, Result: dbtype.JSON(`[]`), UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.StopPrBatchItems(ctx, pgdb.StopPrBatchItemsParams{BatchID: batch, State: "failed", Reason: "stopped", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	items, err := q.ListPrBatchItems(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.State)
	}
	if want := []string{"posted", "failed", "failed"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("states after the stop: %v, want %v", got, want)
	}
	if err := q.SetPrBatchStatus(ctx, pgdb.SetPrBatchStatusParams{WorkspaceID: ws, ID: batch, Status: "stopped",
		FinishedAt: sql.NullTime{Time: now, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	active, err := q.ListActivePrBatches(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Errorf("a stopped batch is still active")
	}
}
