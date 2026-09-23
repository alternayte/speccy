package bundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/store/storetest"
)

const mainDoc = "---\ntype: sdd\n---\n# Payments\n\nRetries.\n"

func newLocal(t *testing.T, open func(*testing.T) *Service) (*Service, string) {
	t.Helper()
	s := open(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pay", "SPEC.md"), []byte(mainDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := local.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Local = root
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func services() []struct {
	name string
	open func(*testing.T) *Service
} {
	var out []struct {
		name string
		open func(*testing.T) *Service
	}
	for _, e := range storetest.Engines() {
		out = append(out, struct {
			name string
			open func(*testing.T) *Service
		}{e.Name, func(t *testing.T) *Service {
			db := e.Open(t)
			ws, err := db.Workspace(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			return &Service{DB: db, Workspace: ws}
		}})
	}
	return out
}

func head(t *testing.T, s *Service, slug string) pgdb.SpecDoc {
	t.Helper()
	b, err := s.DB.Queries().GetSpecDocBySlug(context.Background(), pgdb.GetSpecDocBySlugParams{WorkspaceID: s.Workspace, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func versions(t *testing.T, s *Service, b pgdb.SpecDoc) int {
	t.Helper()
	vs, err := s.DB.Queries().ListVersions(context.Background(), pgdb.ListVersionsParams{SpecDocID: b.ID, BeforeNumber: 1 << 62, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(vs)
}

// REQ-005
func TestLocalSync_VersionsFollowDisk(t *testing.T) {
	for _, svc := range services() {
		t.Run(svc.name, func(t *testing.T) {
			ctx := context.Background()
			s, dir := newLocal(t, svc.open)
			b := head(t, s, "pay")
			if versions(t, s, b) != 1 || b.Title != "Payments" || b.ProfileKey != "sdd" || b.DocPath != "SPEC.md" {
				t.Fatalf("after the first scan: %d versions, bundle %+v", versions(t, s, b), b)
			}

			if err := s.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if n := versions(t, s, head(t, s, "pay")); n != 1 {
				t.Errorf("a scan with no change made %d versions, want 1", n)
			}

			if err := os.WriteFile(filepath.Join(dir, "pay", "SPEC.md"), []byte(mainDoc+"More.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := s.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if n := versions(t, s, head(t, s, "pay")); n != 2 {
				t.Errorf("an edit on disk made %d versions in total, want 2", n)
			}

			if err := os.Rename(filepath.Join(dir, "pay"), filepath.Join(dir, "gone")); err != nil {
				t.Fatal(err)
			}
			if err := s.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if !head(t, s, "pay").ArchivedAt.Valid {
				t.Error("a bundle whose folder is gone is not archived")
			}
			if err := os.Rename(filepath.Join(dir, "gone"), filepath.Join(dir, "pay")); err != nil {
				t.Fatal(err)
			}
			if err := s.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if head(t, s, "pay").ArchivedAt.Valid {
				t.Error("a bundle whose folder is back is still archived")
			}
		})
	}
}

// DEC-004: one editor at a time. A change based on an old version never overwrites a newer one.
func TestChange_StaleBaseIsRejected(t *testing.T) {
	for _, svc := range services() {
		t.Run(svc.name, func(t *testing.T) {
			ctx := context.Background()
			s, dir := newLocal(t, svc.open)
			b := head(t, s, "pay")
			v1 := b.CurrentVersionID.UUID

			write := func(base [16]byte, content string) error {
				_, _, err := s.Change(ctx, b.ID, base, source.Op{Kind: source.OpWrite, Path: "SPEC.md", Content: []byte(content)}, LocalUser, "Saved")
				return err
			}
			if err := write(v1, mainDoc+"A.\n"); err != nil {
				t.Fatal(err)
			}
			if err := write(v1, mainDoc+"B.\n"); !errors.Is(err, version.ErrConflict) {
				t.Fatalf("a save on an old base: err = %v, want a version conflict", err)
			}

			// An edit on disk that the watcher has not seen yet also moves the head.
			v2 := head(t, s, "pay").CurrentVersionID.UUID
			if err := os.WriteFile(filepath.Join(dir, "pay", "SPEC.md"), []byte(mainDoc+"Disk.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := write(v2, mainDoc+"C.\n"); !errors.Is(err, version.ErrConflict) {
				t.Fatalf("a save over an unseen disk edit: err = %v, want a version conflict", err)
			}
			got, _ := os.ReadFile(filepath.Join(dir, "pay", "SPEC.md"))
			if !strings.HasSuffix(string(got), "Disk.\n") {
				t.Errorf("the disk edit was overwritten: %q", got)
			}
		})
	}
}

// REQ-009
func TestDBSource_Limits(t *testing.T) {
	for _, svc := range services() {
		t.Run(svc.name, func(t *testing.T) {
			ctx := context.Background()
			s := svc.open(t)
			b, err := s.CreateDB(ctx, "pay", []source.File{{Path: "SPEC.md", Content: []byte(mainDoc)}}, "u")
			if err != nil {
				t.Fatal(err)
			}
			big := make([]byte, source.MaxFileBytes+1)
			_, _, err = s.Change(ctx, b.ID, b.CurrentVersionID.UUID, source.Op{Kind: source.OpWrite, Path: "big.bin", Content: big}, "u", "Upload")
			if ke, ok := kernel.AsError(err); !ok || ke.Code != "file_too_large" {
				t.Fatalf("an 11 MB file: err = %v, want file_too_large", err)
			}
			v, changed, err := s.Change(ctx, b.ID, b.CurrentVersionID.UUID, source.Op{Kind: source.OpWrite, Path: "assets/a.sql", Content: []byte("select 1;")}, "u", "Upload")
			if err != nil || !changed || v.Number != 2 {
				t.Fatalf("a small file: version %d, changed %v, err %v", v.Number, changed, err)
			}
			files, err := version.Files(ctx, s.DB.Queries(), v.ID)
			if err != nil || len(files) != 2 {
				t.Fatalf("files of version 2: %v, %v", files, err)
			}
			if _, err := s.CreateDB(ctx, "pay", []source.File{{Path: "SPEC.md", Content: []byte(mainDoc)}}, "u"); err == nil {
				t.Error("a second bundle with the same slug was created")
			}
		})
	}
}

// An empty file reads back from SQLite as nil. Recording it again must still work.
func TestChange_EmptyFileSurvivesRename(t *testing.T) {
	for _, svc := range services() {
		t.Run(svc.name, func(t *testing.T) {
			ctx := context.Background()
			s := svc.open(t)
			b, err := s.CreateDB(ctx, "pay", []source.File{{Path: "SPEC.md", Content: []byte(mainDoc)}}, "u")
			if err != nil {
				t.Fatal(err)
			}
			v, _, err := s.Change(ctx, b.ID, b.CurrentVersionID.UUID, source.Op{Kind: source.OpWrite, Path: "a.sql", Content: []byte{}}, "u", "New")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Change(ctx, b.ID, v.ID, source.Op{Kind: source.OpRename, Path: "a.sql", To: "db/a.sql"}, "u", "Move"); err != nil {
				t.Fatalf("rename an empty file: %v", err)
			}
		})
	}
}

// A folder with a PRD and an SDD is one bundle with two spec docs. An edit to one spec doc
// makes a new version of that doc only; an edit to an asset, through the app or on disk, makes
// a new version of each.
func TestLocalSync_FolderHoldsSpecDocs(t *testing.T) {
	for _, svc := range services() {
		t.Run(svc.name, func(t *testing.T) {
			ctx := context.Background()
			s, dir := newLocal(t, svc.open)
			write := func(rel, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "pay", rel), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write("PRD.md", "---\ntype: prd\n---\n# Payments PRD\n")
			write("flow.txt", "a")
			if err := s.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			q := s.DB.Queries()
			folder, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: "pay"})
			if err != nil {
				t.Fatal(err)
			}
			docs, err := q.ListSpecDocsOfBundle(ctx, folder.ID)
			if err != nil || len(docs) != 2 || docs[0].DocPath != "PRD.md" || docs[1].DocPath != "SPEC.md" {
				t.Fatalf("spec docs of pay = %+v, %v; want PRD.md and SPEC.md", docs, err)
			}
			// The first doc kept its history when the second one took the folder's slug from it.
			sdd, prd := head(t, s, "pay/SPEC"), head(t, s, "pay/PRD")
			if n := versions(t, s, sdd); n != 2 {
				t.Errorf("SPEC.md has %d versions, want 2: the one before PRD.md, and the one with flow.txt", n)
			}
			files, _ := version.Files(ctx, q, prd.CurrentVersionID.UUID)
			if len(files) != 2 {
				t.Errorf("PRD.md version files = %+v, want PRD.md and flow.txt", files)
			}

			write("PRD.md", "---\ntype: prd\n---\n# Payments PRD\n\nMore.\n")
			if err := s.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			if versions(t, s, head(t, s, "pay/PRD")) != 2 || versions(t, s, head(t, s, "pay/SPEC")) != 2 {
				t.Error("an edit to PRD.md changed the versions of SPEC.md")
			}

			prd = head(t, s, "pay/PRD")
			op := source.Op{Kind: source.OpWrite, Path: "flow.txt", Content: []byte("b")}
			if _, _, err := s.Change(ctx, prd.ID, prd.CurrentVersionID.UUID, op, LocalUser, "Edit flow"); err != nil {
				t.Fatal(err)
			}
			if versions(t, s, head(t, s, "pay/PRD")) != 3 || versions(t, s, head(t, s, "pay/SPEC")) != 3 {
				t.Error("an edit to flow.txt did not make a new version of each spec doc")
			}

			sdd = head(t, s, "pay/SPEC")
			op = source.Op{Kind: source.OpWrite, Path: "PRD.md", Content: []byte("x")}
			if _, _, err := s.Change(ctx, sdd.ID, sdd.CurrentVersionID.UUID, op, LocalUser, "Overwrite"); err == nil {
				t.Error("a change through SPEC.md overwrote PRD.md")
			}
		})
	}
}
