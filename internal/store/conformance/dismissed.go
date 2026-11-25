package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// dismissedDocsDeleteBySource checks that a dismissed doc is removed by its source, or by no
// source, on both engines. Postgres refused the query with uuid = text (#170).
func dismissedDocsDeleteBySource(t *testing.T, db *store.DB) {
	ctx := context.Background()
	q := db.Queries()
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src := uuid.NullUUID{UUID: kernel.NewID(), Valid: true}
	now := time.Now().UTC()
	if err := q.InsertGithubSource(ctx, pgdb.InsertGithubSourceParams{ID: src.UUID, WorkspaceID: ws, Repo: "acme/specs", Branch: "main",
		Path: ".", CreatedBy: "nathan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []uuid.NullUUID{src, {}} {
		if err := q.InsertDismissedDoc(ctx, pgdb.InsertDismissedDocParams{WorkspaceID: ws, SourceID: s, Path: "docs/prd.md",
			DismissedBy: "nathan", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.DeleteDismissedDoc(ctx, pgdb.DeleteDismissedDocParams{WorkspaceID: ws, SourceID: src, Path: "docs/prd.md"}); err != nil {
		t.Fatalf("delete by source: %v", err)
	}
	rows, err := q.ListDismissedDocs(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SourceID.Valid {
		t.Fatalf("after the delete by source, rows = %+v, want the one with no source", rows)
	}
	if err := q.DeleteDismissedDoc(ctx, pgdb.DeleteDismissedDocParams{WorkspaceID: ws, Path: "docs/prd.md"}); err != nil {
		t.Fatalf("delete with no source: %v", err)
	}
	if rows, _ := q.ListDismissedDocs(ctx, ws); len(rows) != 0 {
		t.Fatalf("after both deletes, rows = %+v, want none", rows)
	}
}
