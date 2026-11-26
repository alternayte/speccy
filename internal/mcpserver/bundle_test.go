package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/http/api"
)

// #178: the bundle argument takes the bundle ID of the web app's URL as well as the doc's ID
// and slug. A bundle ID with two spec docs names both. A closed pull request's bundle resolves.
func TestBundleArgument(t *testing.T) {
	const (
		one    = "0190a0a0-0000-7000-8000-000000000001" // a bundle with one doc
		oneDoc = "0190a0a0-0000-7000-8000-000000000002"
		two    = "0190a0a0-0000-7000-8000-000000000003" // a bundle with two docs
	)
	var closedAsked bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/docs/"+oneDoc):
			_, _ = w.Write([]byte(`{"id":"` + oneDoc + `","bundle_id":"` + one + `","slug":"pay","profile_key":"sdd"}`))
		case strings.HasPrefix(r.URL.Path, "/docs/"):
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"bundle_not_found","detail":"No bundle has this ID."}`))
		case r.URL.Path == "/bundles":
			closedAsked = r.URL.Query().Get("include_closed") == "true"
			_, _ = w.Write([]byte(`{"problems":[],"items":[
				{"id":"` + one + `","docs":[{"id":"` + oneDoc + `","bundle_id":"` + one + `","slug":"pay","profile_key":"sdd"}]},
				{"id":"` + two + `","docs":[
					{"id":"0190a0a0-0000-7000-8000-000000000004","bundle_id":"` + two + `","slug":"ship/prd","profile_key":"prd"},
					{"id":"0190a0a0-0000-7000-8000-000000000005","bundle_id":"` + two + `","slug":"ship/sdd","profile_key":"sdd"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, ref := range []string{oneDoc, one, "pay"} {
		if d, err := bundle(ctx, c, ref); err != nil || d.Id.String() != oneDoc {
			t.Errorf("bundle(%s) = %s, %v; want the doc %s", ref, d.Id, err, oneDoc)
		}
	}
	if !closedAsked {
		t.Error("the lookup left out the bundles of closed pull requests")
	}
	if _, err := bundle(ctx, c, two); err == nil || !strings.Contains(err.Error(), "ship/prd") || !strings.Contains(err.Error(), "ship/sdd") {
		t.Errorf("bundle(two docs) = %v; want an error that names both docs", err)
	}
}
