package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// newBundle makes a bundle slug that holds one spec doc, and returns the spec doc.
func newBundle(t *testing.T, db *store.DB, ws uuid.UUID, slug string) pgdb.SpecDoc {
	t.Helper()
	now := time.Now().UTC()
	folder := pgdb.InsertBundleParams{ID: kernel.NewID(), WorkspaceID: ws, Slug: slug, Title: slug, SourceKind: "db",
		SourceRef: dbtype.JSON(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := db.Queries().InsertBundle(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	b := pgdb.InsertSpecDocParams{
		ID: kernel.NewID(), WorkspaceID: ws, BundleID: folder.ID, Slug: slug, Title: slug, ProfileKey: "sdd", DocPath: "SPEC.md",
		SourceKind: "db", SourceRef: dbtype.JSON(`{}`), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Queries().InsertSpecDoc(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	return pgdb.SpecDoc{ID: b.ID, WorkspaceID: ws, BundleID: folder.ID, Slug: slug}
}

func newVersion(t *testing.T, db *store.DB, b pgdb.SpecDoc, n int64) uuid.UUID {
	t.Helper()
	id := kernel.NewID()
	err := db.Queries().InsertVersion(context.Background(), pgdb.InsertVersionParams{
		ID: id, WorkspaceID: b.WorkspaceID, SpecDocID: b.ID, Number: n, CreatedBy: "u", Message: "m", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// bundleHeadMovesOnlyFromExpected checks the optimistic head update. The SQL differs per engine
// (IS against IS NOT DISTINCT FROM), so both must agree on NULL and on a mismatch.
func bundleHeadMovesOnlyFromExpected(t *testing.T, db *store.DB) {
	ctx := context.Background()
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := newBundle(t, db, ws, "pay")
	v1, v2 := newVersion(t, db, b, 1), newVersion(t, db, b, 2)
	move := func(to, expected uuid.NullUUID) int64 {
		n, err := db.Queries().UpdateSpecDocHead(ctx, pgdb.UpdateSpecDocHeadParams{
			ID: b.ID, Title: "t", ProfileKey: "sdd", DocPath: "SPEC.md", CurrentVersionID: to, ExpectedVersionID: expected, UpdatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	some := func(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: true} }
	if n := move(some(v1), some(v2)); n != 0 {
		t.Errorf("from no head, expecting v2: %d rows, want 0", n)
	}
	if n := move(some(v1), uuid.NullUUID{}); n != 1 {
		t.Errorf("from no head, expecting none: %d rows, want 1", n)
	}
	if n := move(some(v2), uuid.NullUUID{}); n != 0 {
		t.Errorf("from v1, expecting none: %d rows, want 0", n)
	}
	if n := move(some(v2), some(v1)); n != 1 {
		t.Errorf("from v1, expecting v1: %d rows, want 1", n)
	}
	if err := db.Queries().InsertVersion(ctx, pgdb.InsertVersionParams{
		ID: kernel.NewID(), WorkspaceID: ws, SpecDocID: b.ID, Number: 2, CreatedBy: "u", Message: "m", CreatedAt: time.Now().UTC(),
	}); err == nil {
		t.Error("a second version 2 of one bundle was inserted")
	}
}

func blobsAreContentAddressed(t *testing.T, db *store.DB) {
	ctx := context.Background()
	q := db.Queries()
	for range 2 {
		if err := q.InsertBlob(ctx, pgdb.InsertBlobParams{Sha256: "abc", Content: []byte{0, 1, 2, 255}, Size: 4}); err != nil {
			t.Fatalf("insert the same blob: %v", err)
		}
	}
	got, err := q.GetBlob(ctx, "abc")
	if err != nil || string(got) != string([]byte{0, 1, 2, 255}) {
		t.Errorf("blob = %v, %v", got, err)
	}
}

func listsPage(t *testing.T, db *store.DB) {
	ctx := context.Background()
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"c", "a", "b"} {
		newBundle(t, db, ws, s)
	}
	page, err := db.Queries().ListSpecDocs(ctx, pgdb.ListSpecDocsParams{WorkspaceID: ws, AfterSlug: "a", PageSize: 1})
	if err != nil || len(page) != 1 || page[0].Slug != "b" {
		t.Errorf("bundles after a, 1 per page: %v, %v", page, err)
	}
	b := newBundle(t, db, ws, "v")
	for n := int64(1); n <= 3; n++ {
		newVersion(t, db, b, n)
	}
	vs, err := db.Queries().ListVersions(ctx, pgdb.ListVersionsParams{SpecDocID: b.ID, BeforeNumber: 3, PageSize: 5})
	if err != nil || len(vs) != 2 || vs[0].Number != 2 || vs[1].Number != 1 {
		t.Errorf("versions before 3: %v, %v", vs, err)
	}
}

// jsonDefaultsScan checks that a JSON column filled by its SQL default reads back as JSON.
// SQLite returns default text as a string, which a []byte-only scanner rejects.
func jsonDefaultsScan(t *testing.T, db *store.DB) {
	ctx := context.Background()
	ws, err := db.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := newBundle(t, db, ws, "j")
	v := newVersion(t, db, b, 1)
	now := time.Now().UTC()
	run := pgdb.InsertRunParams{ID: kernel.NewID(), WorkspaceID: ws, SpecDocID: b.ID, VersionID: v, ProfileKey: "sdd",
		ProfileVersion: 1, Kind: "lint", Status: "complete", Stage: "lint", StartedAt: now}
	if err := db.Queries().InsertRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := db.Queries().LatestRun(ctx, b.ID)
	if err != nil {
		t.Fatalf("read a run with default JSON columns: %v", err)
	}
	if string(got.Roles) != "{}" || string(got.PromptVersions) != "{}" {
		t.Errorf("roles %q, prompt_versions %q; want {}", got.Roles, got.PromptVersions)
	}
}
