package main

import (
	"context"
	"database/sql"
	"io"
	nethttp "net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/owner"
	"github.com/alternayte/speccy/internal/source/local"
)

// #87: one process owns a local state. The first seat is the owner; the second is a client
// process that opens no store and calls the owner with the token of the lock file. When the
// owner stops, the client process takes over on its next call, and ends the run that the old
// owner left active.
func TestSeat_OwnerClientAndTakeover(t *testing.T) {
	dir := fixtures(t, "draft-prd")
	root, err := local.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root.Dir(), ".speccy", "state")
	key := filepath.Join(state, "key")
	ctx := context.Background()
	t.Cleanup(func() { closeSeats(io.Discard) })

	first, err := openSeat(root, state, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openSeat(root, state, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	info, isClient := second.ownerInfo()
	if _, c := first.ownerInfo(); c || !isClient || info.Address == "" || info.Token == "" || info.Kind != processKind || info.Version != kernel.Version {
		t.Fatalf("the first seat must own the folder and the second must be its client; the client reads %+v", info)
	}
	if second.own != nil {
		t.Fatal("a client process opened the store")
	}

	// The listener for processes refuses a call with no token, and takes one with it.
	res, err := nethttp.Get("http://" + info.Address + "/api/v1/meta")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("a call with no token: status %d, want 401", res.StatusCode)
	}
	c, err := second.client()
	if err != nil {
		t.Fatal(err)
	}
	docs, err := listAll(ctx, c)
	if err != nil || len(docs) != 1 {
		t.Fatalf("the client process lists %d docs through the owner, err %v; want 1", len(docs), err)
	}

	// The owner leaves a run active, as a hard kill does: a queued job and its run.
	q := first.own.app.Reviews.DB.Queries()
	run := kernel.NewID()
	now := time.Now().UTC()
	if err := q.InsertRun(ctx, pgdb.InsertRunParams{ID: run, WorkspaceID: first.own.app.Workspace, SpecDocID: docs[0].Id,
		VersionID: docs[0].CurrentVersion.Id, ProfileKey: docs[0].ProfileKey, Kind: "full", Status: "running", Stage: "rubric",
		Notes: dbtype.JSON(`[]`), StartedAt: now, FinishedAt: sql.NullTime{}}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertJob(ctx, pgdb.InsertJobParams{ID: kernel.NewID(), WorkspaceID: first.own.app.Workspace, Kind: "review_run",
		Payload: dbtype.JSON(`{"run_id":"` + run.String() + `"}`), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ClaimJob(ctx, pgdb.ClaimJobParams{LockUntil: sql.NullTime{Time: now.Add(time.Hour), Valid: true}, Now: sql.NullTime{Time: now, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	// The owner stops with no drain, as a kill does: its listener and its lock go.
	first.mu.Lock()
	first.closed = true
	first.mu.Unlock()
	first.own.stop()
	<-first.own.served
	first.own.lock.Release()

	// The next call of the client process works: it took the lock and is the owner now.
	got, err := c.GetRunWithResponse(ctx, run)
	if err != nil || got.JSON200 == nil {
		t.Fatalf("the call after the owner stopped: %v, %v", err, got)
	}
	if _, client := second.ownerInfo(); client {
		t.Fatal("the client process did not take over")
	}
	if got.JSON200.Status != api.RunStatusFailed || got.JSON200.Error != review.StoppedCause {
		t.Errorf("the run of the old owner: status %s, error %q; want failed with the cause that its process stopped", got.JSON200.Status, got.JSON200.Error)
	}
}

// A client process of another Speccy version stops with a message, and changes nothing.
func TestSeat_OtherVersion(t *testing.T) {
	dir := fixtures(t, "draft-prd")
	root, err := local.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root.Dir(), ".speccy", "state")
	t.Cleanup(func() { closeSeats(io.Discard) })
	// Make the folder, then hold its lock as another version does.
	if _, err := openSeat(root, state, filepath.Join(state, "key"), nil); err != nil {
		t.Fatal(err)
	}
	closeSeats(io.Discard)
	lock, err := owner.Take(state)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := lock.Write(owner.Info{Address: "127.0.0.1:1", Token: "t", Version: "v0.0.1", Kind: "the app", PID: 4242}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Take(state); err != owner.ErrHeld {
		t.Fatalf("a second take of the lock: %v, want ErrHeld", err)
	}
	_, err = openSeat(root, state, filepath.Join(state, "key"), nil)
	if err == nil {
		t.Fatal("a client of another version took a seat")
	}
	for _, want := range []string{"v0.0.1", "the app", "4242", kernel.Version} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q does not name %q", err, want)
		}
	}
}
