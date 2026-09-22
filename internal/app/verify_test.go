package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/model"

	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store/storetest"
)

// specDoc defines two requirements. REQ-001 is named in the repo; REQ-002 is named nowhere.
const specDoc = "---\ntype: note\ntitle: Pay\n---\n\n# Pay\n\n## Requirements\n\n" +
	"- **REQ-001:** The gateway MUST reject a request with no token.\n" +
	"- **REQ-002:** The gateway MUST retry a refused call once.\n" +
	"- **DEC-001:** The store is Postgres.\n"

// TestVerifyFolderRun runs the gate against a folder: a requirement the repo names is found by
// its literal occurrence, a MUST requirement nothing names is missing and opens a blocking
// thread, and a decision ID is skipped.
func TestVerifyFolderRun(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			env := newEnv(t, eng)
			env.edit(t, specDoc)

			fakeModels(t, env)
			tracePrefixes(t, env)
			repo := t.TempDir()
			write(t, repo, "gateway.go", "package gateway\n\n// REQ-001: reject a request with no token.\nfunc Reject() bool { return true }\n")
			write(t, repo, "gateway_test.go", "package gateway\n\n// REQ-001 is tested here.\nfunc TestReject(t *testing.T) {}\n")
			write(t, repo, "vendor/other.go", "package other\n// REQ-002 lives in vendor, which the scan excludes.\n")

			res, err := env.app.API.RunVerification(as("member"), api.RunVerificationRequestObject{
				BundleId: env.b.ID,
				Body:     &api.RunVerificationJSONRequestBody{Path: ptr(repo)},
			})
			if err != nil {
				t.Fatal(err)
			}
			v := api.Verification(res.(api.RunVerification200JSONResponse))

			got := map[string]api.VerificationOutcomeOutcome{}
			for _, o := range v.Outcomes {
				got[o.TraceId] = o.Outcome
			}
			if _, ok := got["DEC-001"]; ok {
				t.Error("a decision ID names no code, so the gate must skip it")
			}
			if v.Counts.Skipped != 1 {
				t.Errorf("Skipped = %d, want the one DEC ID", v.Counts.Skipped)
			}
			if got["REQ-002"] != api.Missing {
				t.Errorf("REQ-002 = %q, want missing: the vendor directory is excluded", got["REQ-002"])
			}
			if v.Verdict != api.VerificationVerdictNotVerified {
				t.Errorf("Verdict = %q, want not_verified: a MUST is missing", v.Verdict)
			}
			if v.Counts.Blocking != 1 {
				t.Fatalf("Blocking = %d, want 1", v.Counts.Blocking)
			}
			// REQ-001 has a code target and a test target that hold, so the run cited both. The
			// fake backend gives no affirmation, so the outcome is unproven and never implemented.
			if o := got["REQ-001"]; o != api.Unproven {
				t.Errorf("REQ-001 = %q; with no affirming judge the outcome is unproven", o)
			}

			// The run opens the blocking thread, and the existing rule turns that into the
			// bundle's verdict. The run itself computes no Build Ready verdict.
			threads := blockingThreads(t, env)
			if threads == 0 {
				t.Fatal("a MUST missing outcome opens a blocking thread")
			}
			if result, _ := env.verdict(t); result == string(api.BuildReady) {
				t.Error("a blocking thread makes the bundle Not Build Ready")
			}

			// A second run of the same bundle lists both.
			list, err := env.app.API.ListVerifications(as("member"), api.ListVerificationsRequestObject{BundleId: env.b.ID})
			if err != nil {
				t.Fatal(err)
			}
			if items := list.(api.ListVerifications200JSONResponse).Items; len(items) != 1 {
				t.Errorf("the bundle lists %d runs, want 1", len(items))
			}
		})
	}
}

// TestVerifyRefusesABundleWithNoVerifiableTraceID names the action that fixes it.
func TestVerifyRefusesABundleWithNoVerifiableTraceID(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			env := newEnv(t, eng)
			fakeModels(t, env)
			env.edit(t, "---\ntype: note\ntitle: Pay\n---\n\n# Pay\n\nThe gateway rejects a request with no token.\n")
			_, err := env.app.API.RunVerification(as("member"), api.RunVerificationRequestObject{
				BundleId: env.b.ID,
				Body:     &api.RunVerificationJSONRequestBody{Path: ptr(t.TempDir())},
			})
			if err == nil {
				t.Fatal("a bundle with no verifiable trace ID refuses the run")
			}
			ke, ok := kernel.AsError(err)
			if !ok || ke.Code != "no_trace_ids" {
				t.Fatalf("err = %v, want no_trace_ids", err)
			}
		})
	}
}

// fakeModels assigns the fake backend to every role the gate uses, and makes it say nothing.
// A judge that says nothing must give an unproven outcome, never an implemented one.
func fakeModels(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	q := e.app.Bundles.DB.Queries()
	id := kernel.NewID()
	if err := q.InsertBackend(ctx, pgdb.InsertBackendParams{ID: id, WorkspaceID: e.app.Workspace, Kind: model.KindFake,
		Name: "fake", Config: dbtype.JSON(`{}`), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, role := range model.Roles {
		if err := q.UpsertAssignment(ctx, pgdb.UpsertAssignmentParams{WorkspaceID: e.app.Workspace, Role: role,
			BackendID: id, Model: "fake-" + role}); err != nil {
			t.Fatal(err)
		}
	}
	e.app.Reviews.Gateway.Fake = model.BackendFunc(func(_ context.Context, _ string, c model.Call) (model.Raw, error) {
		if c.PromptVersion == "verify-map-1" {
			return model.Raw{Text: `{"targets":[]}`}, nil
		}
		return model.Raw{Text: `{"verdict":"silent","requirement_quote":"","code_quote":"","reason":"I cannot tell."}`}, nil
	})
}

// tracePrefixes gives the profile the prefixes the doc uses, so the gate can see the DEC it
// does not verify and count it as skipped.
func tracePrefixes(t *testing.T, e *env) {
	t.Helper()
	yaml := profileYAML + "trace: { prefixes: [REQ, DEC] }\nverify: { prefixes: [REQ] }\n"
	if _, err := e.app.Profiles.Save(context.Background(), "note", []byte(yaml), []byte("# Note\n"), "admin"); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func blockingThreads(t *testing.T, e *env) int {
	t.Helper()
	q := e.app.Bundles.DB.Queries()
	n, err := q.CountOpenBlockingThreads(context.Background(), uuid.NullUUID{UUID: e.b.ID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

func ptr[T any](v T) *T { return &v }

// An approved verification waiver stops the block, and an edit to the requirement's section
// ends it, so the next run blocks again. The waiver never goes in the doc's sidecar.
func TestVerifyWaiverStopsTheBlockUntilTheSectionChanges(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			env := newEnv(t, eng)
			fakeModels(t, env)
			tracePrefixes(t, env)
			env.edit(t, specDoc)
			repo := t.TempDir()
			write(t, repo, "gateway.go", "package gateway\n\n// REQ-001: reject a request with no token.\nfunc Reject() bool { return true }\n")

			res, err := env.app.API.RequestVerificationWaiver(as("author"), api.RequestVerificationWaiverRequestObject{
				BundleId: env.b.ID,
				Body: &api.RequestVerificationWaiverJSONRequestBody{
					TraceId: "REQ-002", Repo: repo,
					Reason: "The retry lives in the client library, which this repo does not hold.",
				}})
			if err != nil {
				t.Fatal(err)
			}
			w := api.Waiver(res.(api.RequestVerificationWaiver200JSONResponse))
			if _, err := env.app.API.ApproveWaiver(as("keeper"), api.ApproveWaiverRequestObject{WaiverId: w.Id}); err != nil {
				t.Fatal(err)
			}

			v := runVerify(t, env, repo)
			if v.Counts.Waived != 1 || v.Counts.Blocking != 0 {
				t.Fatalf("waived %d, blocking %d; an approved waiver stops the block", v.Counts.Waived, v.Counts.Blocking)
			}
			if v.Verdict != api.VerificationVerdictVerified {
				t.Errorf("Verdict = %q, want verified", v.Verdict)
			}
			// The waiver excuses one build, so it never reaches the doc's sidecar.
			dec, err := env.app.Bundles.Decisions(context.Background(), env.b)
			if err != nil {
				t.Fatal(err)
			}
			for _, sw := range dec.Waivers {
				if sw.Check == "verify.req-002" {
					t.Error("a verification waiver must not go in the sidecar")
				}
			}

			// Editing the requirement's section ends the waiver, so the gate blocks again.
			env.edit(t, specDoc+"\nThe gateway retries twice, not once.\n")
			again := runVerify(t, env, repo)
			if again.Counts.Waived != 0 || again.Counts.Blocking == 0 {
				t.Errorf("after the edit: waived %d, blocking %d; the waiver ended with the section",
					again.Counts.Waived, again.Counts.Blocking)
			}
		})
	}
}

func runVerify(t *testing.T, e *env, repo string) api.Verification {
	t.Helper()
	res, err := e.app.API.RunVerification(as("member"), api.RunVerificationRequestObject{
		BundleId: e.b.ID, Body: &api.RunVerificationJSONRequestBody{Path: ptr(repo)}})
	if err != nil {
		t.Fatal(err)
	}
	return api.Verification(res.(api.RunVerification200JSONResponse))
}

// A new bundle version makes an earlier verification run stale: the requirements moved, so the
// run's statement is about a doc that no longer stands. The run keeps its outcomes.
func TestVerifyRunGoesStaleWithANewVersion(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			env := newEnv(t, eng)
			fakeModels(t, env)
			tracePrefixes(t, env)
			env.edit(t, specDoc)
			repo := t.TempDir()
			write(t, repo, "gateway.go", "package gateway\n\n// REQ-001 and REQ-002 live here.\nfunc Reject() bool { return true }\n")
			first := runVerify(t, env, repo)

			env.edit(t, specDoc+"\nThe gateway also logs the refusal.\n")
			list, err := env.app.API.ListVerifications(as("member"), api.ListVerificationsRequestObject{BundleId: env.b.ID})
			if err != nil {
				t.Fatal(err)
			}
			items := list.(api.ListVerifications200JSONResponse).Items
			for _, v := range items {
				if v.Id == first.Id {
					if !v.Stale {
						t.Error("a new bundle version makes the earlier run stale")
					}
					if len(v.Outcomes) != len(first.Outcomes) {
						t.Error("a stale run keeps its outcomes for the history")
					}
					return
				}
			}
			t.Fatal("the run is not listed")
		})
	}
}

// Deleting a bundle removes everything that hangs off it. A row left behind would go on
// answering queries and counting in Insights for a doc that is gone.
func TestDeleteBundleRemovesEverything(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			env := newEnv(t, eng)
			fakeModels(t, env)
			tracePrefixes(t, env)
			env.edit(t, specDoc)
			repo := t.TempDir()
			write(t, repo, "gateway.go", "package gateway\n\n// REQ-001 lives here.\nfunc Reject() bool { return true }\n")
			runVerify(t, env, repo) // a run, outcomes and a blocking thread to delete with it
			id := env.b.ID

			if _, err := env.app.API.DeleteBundle(as("author"), api.DeleteBundleRequestObject{
				BundleId: id, Params: api.DeleteBundleParams{Slug: "wrong"}}); err == nil {
				t.Fatal("a wrong slug must not delete a bundle")
			}
			if _, err := env.app.API.DeleteBundle(as("author"), api.DeleteBundleRequestObject{
				BundleId: id, Params: api.DeleteBundleParams{Slug: env.b.Slug}}); err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			q := env.app.Bundles.DB.Queries()
			if _, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: env.app.Workspace, ID: id}); err == nil {
				t.Error("the bundle row is still there")
			}
			if n, err := q.CountOpenBlockingThreads(ctx, uuid.NullUUID{UUID: id, Valid: true}); err != nil || n != 0 {
				t.Errorf("blocking threads = %d (%v), want none", n, err)
			}
			if rows, err := q.ListVerificationRuns(ctx, id); err != nil || len(rows) != 0 {
				t.Errorf("verification runs = %d (%v), want none", len(rows), err)
			}
			if rows, err := q.ListBundleWaivers(ctx, id); err != nil || len(rows) != 0 {
				t.Errorf("waivers = %d (%v), want none", len(rows), err)
			}
		})
	}
}
