package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/local"
)

// fixtures copies testdata/bundles into a new folder, without the golden files.
func fixtures(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		src := filepath.Join("..", "..", "testdata", "bundles", n)
		if err := os.CopyFS(filepath.Join(dir, n), os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		// The sidecar of the bundle's main doc travels with it (DEC-009).
		sid := filepath.Join("..", "..", "testdata", "bundles", ".speccy", "decisions", n)
		if _, err := os.Stat(sid); err == nil {
			if err := os.CopyFS(filepath.Join(dir, ".speccy", "decisions", n), os.DirFS(sid)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func runIn(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// T-090: the exit codes of SDD §12.2.
func TestCLI_ExitCodes(t *testing.T) {
	dir := fixtures(t, "draft-prd", "payments-prd")
	for _, c := range []struct {
		name string
		args []string
		want int
	}{
		{"not build ready, advisory", []string{"review", "draft-prd"}, exitOK},
		{"not build ready, blocking", []string{"review", "draft-prd", "--enforcement", "blocking"}, exitNotReady},
		{"build ready, blocking", []string{"review", "payments-prd", "--enforcement=blocking"}, exitOK},
		{"one of two not ready, blocking", []string{"review", ".", "--enforcement", "blocking"}, exitNotReady},
		{"unknown flag", []string{"review", "draft-prd", "--fast"}, exitUsage},
		{"no path", []string{"review"}, exitUsage},
		{"missing path", []string{"review", "nope"}, exitUsage},
		{"unknown stage", []string{"review", "draft-prd", "--stages", "vibes"}, exitUsage},
		{"model stage with no model", []string{"review", "draft-prd", "--stages", "rubric"}, exitUsage},
		{"bad format", []string{"review", "draft-prd", "--format", "xml"}, exitUsage},
	} {
		t.Run(c.name, func(t *testing.T) {
			if code, out, errOut := runIn(t, dir, c.args...); code != c.want {
				t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, c.want, out, errOut)
			}
		})
	}

	// A run error is 3: the reviewer's backend fails in the middle of the run.
	t.Run("run error", func(t *testing.T) {
		dir := fixtures(t, "draft-prd")
		ctx, cancel := context.WithCancel(context.Background())
		root, err := local.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		a, db, err := openApp(ctx, root, filepath.Join(root.Dir(), ".speccy", "state"))
		if err != nil {
			t.Fatal(err)
		}
		q := db.Queries()
		id := kernel.NewID()
		// The fake backend exists only in tests, so the binary's gateway refuses it: a backend error.
		if err := q.InsertBackend(ctx, pgdb.InsertBackendParams{ID: id, WorkspaceID: a.Workspace, Kind: model.KindFake, Name: "broken",
			Config: dbtype.JSON(`{}`), CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := q.UpsertAssignment(ctx, pgdb.UpsertAssignmentParams{WorkspaceID: a.Workspace, Role: model.RoleReviewer, BackendID: id, Model: "m"}); err != nil {
			t.Fatal(err)
		}
		cancel()
		time.Sleep(100 * time.Millisecond) // the store closes when ctx ends
		code, out, errOut := runIn(t, dir, "review", "draft-prd", "--stages", "rubric")
		if code != exitRun {
			t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, exitRun, out, errOut)
		}
		if !strings.Contains(out, "Review failed") {
			t.Errorf("the output does not name the failed review:\n%s", out)
		}
	})
}

// T-098: --summary works with no server and no speccy init, and leaves no state behind.
func TestCLI_SummaryNoSetup(t *testing.T) {
	dir := fixtures(t, "draft-prd", "payments-prd", "payments-sdd")
	code, out, errOut := runIn(t, dir, "review", ".", "--summary")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"PATH", "draft-prd", "payments-prd", "payments-sdd", "3 bundles, 2 Build Ready.", "Most common failing checks:", "lint.placeholder"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary has no %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".speccy", "state")); !os.IsNotExist(err) {
		t.Errorf("the review left state in %s/.speccy/state: %v", dir, err)
	}

	code, out, _ = runIn(t, dir, "review", ".", "--summary", "--format", "json")
	var js struct {
		Bundles []struct {
			Path    string   `json:"path"`
			Verdict string   `json:"verdict"`
			Top     []string `json:"top_failing_checks"`
		} `json:"bundles"`
		Totals struct {
			Bundles    int `json:"bundles"`
			BuildReady int `json:"build_ready"`
		} `json:"totals"`
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil || code != exitOK {
		t.Fatalf("json summary: %v, exit %d\n%s", err, code, out)
	}
	if js.Totals.Bundles != 3 || js.Totals.BuildReady != 2 || js.Bundles[0].Path != "draft-prd" || js.Bundles[0].Verdict != "not_build_ready" || len(js.Bundles[0].Top) != 3 {
		t.Errorf("json summary = %+v", js)
	}
}

// speccy init offers the guessed profile as its default answer, so Enter adopts the doc with
// the type the app would pick. A person cannot check this by hand without a terminal.
func TestInit_EnterTakesTheGuess(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "spec.md")
	body := "# Payment retries\n\n## Problem\n\nCards fail.\n\n## Goals\n\n- G-1: fewer\n\n## Requirements\n\n- REQ-001: retry\n"
	if err := os.WriteFile(doc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	var out, errOut bytes.Buffer
	if code := runInit(nil, strings.NewReader("\n"), &out, &errOut, true); code != 0 {
		t.Fatalf("speccy init: exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Press Enter for prd") {
		t.Errorf("the prompt does not offer the guess: %s", out.String())
	}
	got, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "---\ntype: prd\n---\n") {
		t.Errorf("the doc did not get the guessed type: %q", string(got[:40]))
	}
	if !strings.HasSuffix(string(got), body) {
		t.Error("speccy init changed the text of the doc")
	}
}

// speccy init --github adopts a repo that Speccy did not write: a mapping for each doc, the
// checks that fail today in adoption mode, and a workflow that needs no secret (REQ-130,
// REQ-133).
func TestCLI_InitGitHub(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/prd-payments.md", "# Payments\n\n## Problem\n\nCustomers wait.\n\n## Requirements\n\n- The system must refund a card payment within 5 working days.\n\n## Out of scope\n\nBank transfers.\n")
	write("README.md", "# The repo\n\nNot a spec.\n")
	code, out, errOut := runIn(t, dir, "init", "--github")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".speccy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := source.ParseRepoConfig(raw)
	if err != nil {
		t.Fatalf("%v:\n%s", err, raw)
	}
	// Every markdown file in docs/ guessed the same profile, so the folder gets one glob.
	if len(cfg.Map) != 1 || cfg.Map[0].Profile != "prd" || cfg.Map[0].Glob != "docs/*.md" {
		t.Errorf("mappings %+v, want one glob for docs/", cfg.Map)
	}
	if len(cfg.Adoption.Relaxed) == 0 {
		t.Errorf("no check is relaxed, so the first verdict is a wall of findings:\n%s", out)
	}
	wf, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "speccy.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wf), "contents: write") || strings.Contains(string(wf), "\n          anthropic-api-key") {
		t.Errorf("the workflow is wrong:\n%s", wf)
	}
	if !strings.Contains(out, "Adoption mode:") || !strings.Contains(out, "/speccy enforce") {
		t.Errorf("the output does not say what happened:\n%s", out)
	}
}
