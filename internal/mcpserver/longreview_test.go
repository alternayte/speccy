package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alternayte/speccy/internal/http/api"
)

// docs/specs/mcp-long-reviews.md: a review that outlasts the wait answers status running and a
// run_id, not an error. get_run reads it to the end, and get_verdict shows it as pending_run
// while it runs (#177).
func TestLongReviewHandsBackTheRun(t *testing.T) {
	const doc, run = "0190a0a0-0000-7000-8000-000000000001", "0190a0a0-0000-7000-8000-000000000009"
	var done atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := "running"
		if done.Load() {
			status = "complete"
		}
		runJSON := `{"id":"` + run + `","doc_id":"` + doc + `","kind":"full","status":"` + status + `","stage":"rubric","started_at":"2026-11-26T10:00:00Z"}`
		switch {
		case r.URL.Path == "/docs/"+doc:
			_, _ = w.Write([]byte(`{"id":"` + doc + `","bundle_id":"` + doc + `","slug":"pay","profile_key":"sdd"}`))
		case r.URL.Path == "/docs/"+doc+"/runs" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(runJSON))
		case r.URL.Path == "/docs/"+doc+"/runs":
			_, _ = w.Write([]byte(`{"items":[` + runJSON + `]}`))
		case r.URL.Path == "/runs/"+run:
			_, _ = w.Write([]byte(runJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	was := waitFor
	waitFor = 50 * time.Millisecond
	defer func() { waitFor = was }()
	ctx := context.Background()
	var tl tools

	out, err := tl.reviewBundle(ctx, c, reviewArg{Bundle: doc})
	got, _ := json.Marshal(out)
	if err != nil || !strings.Contains(string(got), `"status":"running"`) || !strings.Contains(string(got), run) {
		t.Fatalf("review_bundle past the wait = %s, %v; want status running and the run_id", got, err)
	}
	out, err = tl.getVerdict(ctx, c, bundleArg{Bundle: doc})
	got, _ = json.Marshal(out)
	if err != nil || !strings.Contains(string(got), `"pending_run":{`) {
		t.Errorf("get_verdict while the review runs = %s, %v; want a pending_run", got, err)
	}

	done.Store(true)
	out, err = tl.getRun(ctx, c, runArg{RunID: run})
	r, ok := out.(*api.Run)
	if err != nil || !ok || r.Status != api.RunStatusComplete {
		t.Errorf("get_run after the end = %#v, %v; want the complete run", out, err)
	}
	out, _ = tl.getVerdict(ctx, c, bundleArg{Bundle: doc})
	if got, _ = json.Marshal(out); strings.Contains(string(got), "pending_run") {
		t.Errorf("get_verdict after the end = %s; want no pending_run", got)
	}
}
