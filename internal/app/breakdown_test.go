package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alternayte/speccy/internal/features/breakdown"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/mcpclient"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/store/storetest"
)

const breakdownDoc = "---\ntype: note\ntitle: Pay\n---\n\n# Pay\n\n## Gateway\n\n" +
	"- **REQ-001:** The gateway MUST reject a request with no token.\n" +
	"- **REQ-002:** The gateway MUST log each rejected request.\n\n" +
	"## Retry\n\n" +
	"- **REQ-003:** The client MUST retry a refused call once.\n" +
	"- **REQ-004:** The client MUST wait one second before the retry.\n\n" +
	"## Decisions\n\n" +
	"- **DEC-001:** The store is Postgres.\n"

// The model proposes two epics and two items. It quotes one sentence the doc does not hold,
// names a trace ID the doc does not define, and leaves REQ-004 out.
const breakdownProposal = `{
  "analysis": "The gateway comes first, then the retry.",
  "epics": [{"key": "e1", "title": "Gateway"}, {"key": "e2", "title": "Retry"}],
  "items": [
    {"key": "i1", "epic": "e1", "title": "Reject and log", "trace_ids": ["REQ-001", "REQ-002", "REQ-999"],
     "heading_path": [], "criteria": [{"doc": "", "quote": "The gateway MUST reject a request with no token."},
     {"doc": "", "quote": "The gateway is fast."}], "repos": [], "depends_on": [], "questions": []},
    {"key": "i2", "epic": "e2", "title": "Retry once", "trace_ids": ["REQ-003"], "heading_path": [],
     "criteria": [], "repos": [], "depends_on": ["i1"], "questions": []}
  ]
}`

// fakeLinear is a Linear MCP server with save_issue and list_users. It records each save.
type fakeLinear struct {
	mu    sync.Mutex
	saves []map[string]any
}

func (f *fakeLinear) start(t *testing.T) string {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "fake-linear", Version: "1"}, nil)
	type saveIn struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Team        string   `json:"team"`
		ParentID    string   `json:"parentId,omitempty"`
		Assignee    string   `json:"assignee,omitempty"`
		Delegate    string   `json:"delegate,omitempty"`
		BlockedBy   []string `json:"blockedBy,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "save_issue", Description: "Create or update an issue."},
		func(_ context.Context, _ *mcp.CallToolRequest, in saveIn) (*mcp.CallToolResult, any, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			raw, _ := json.Marshal(in)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			f.saves = append(f.saves, m)
			n := len(f.saves)
			out := fmt.Sprintf(`{"id":"uuid-%d","identifier":"ENG-%d","url":"https://linear.app/acme/issue/ENG-%d/x"}`, n, n, n)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})
	type usersIn struct {
		Team string `json:"team,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "list_users", Description: "List users."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ usersIn) (*mcp.CallToolResult, any, error) {
			out := `{"users":[{"id":"u1","name":"Ann"},{"id":"a1","name":"Builder bot","app":true}]}`
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *fakeLinear) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saves)
}

// TestBreakdown runs the breakdown from proposal to push (#154 to #157): code rejects what the
// model invents, the checks block the push until a person fixes the breakdown, the push makes
// the issues with their parents, blockers and agent once, and a new doc version pins the pushed
// work item and marks the breakdown stale.
func TestBreakdown(t *testing.T) {
	for _, eng := range storetest.Engines() {
		t.Run(eng.Name, func(t *testing.T) {
			e := newEnv(t, eng)
			fakeModels(t, e)
			tracePrefixes(t, e)
			e.edit(t, breakdownDoc)
			if r, _ := e.verdict(t); r != "build_ready" {
				t.Fatalf("fixture verdict %s", r)
			}
			e.app.Reviews.Gateway.Fake = model.BackendFunc(func(_ context.Context, _ string, c model.Call) (model.Raw, error) {
				if c.PromptVersion == breakdown.PromptPropose {
					return model.Raw{Text: breakdownProposal}, nil
				}
				return model.Raw{Text: `{}`}, nil
			})
			linear := &fakeLinear{}
			url := linear.start(t)
			b := e.app.API.BreakdownAPI
			b.Connection = func(_ context.Context, host string) (mcpclient.Connection, bool, error) {
				return mcpclient.Connection{Transport: "http", URL: url}, host == breakdown.LinearHost, nil
			}

			res, err := e.app.API.ProposeBreakdown(as("author"), api.ProposeBreakdownRequestObject{DocId: e.b.ID})
			if err != nil {
				t.Fatal(err)
			}
			bd := api.Breakdown(res.(api.ProposeBreakdown201JSONResponse))
			i1 := item(t, bd, "i1")
			if !slices.Equal(i1.TraceIds, []string{"REQ-001", "REQ-002"}) {
				t.Errorf("i1 trace IDs = %v; REQ-999 is not in the doc", i1.TraceIds)
			}
			for _, c := range i1.Criteria {
				if strings.Contains(c.Quote, "fast") {
					t.Errorf("a quote the doc does not hold became a criterion: %q", c.Quote)
				}
			}
			if len(i1.Criteria) != 2 {
				t.Errorf("i1 has %d criteria, want the definitions of REQ-001 and REQ-002", len(i1.Criteria))
			}
			if !hasFinding(bd, breakdown.CheckCoverage, "REQ-004") || !hasFinding(bd, breakdown.CheckCoverage, "DEC-001") {
				t.Fatalf("findings %+v, want coverage findings for REQ-004 and DEC-001", bd.Findings)
			}

			push := func(basedOn int64) (api.PushResult, error) {
				r, err := e.app.API.PushBreakdown(as("author"), api.PushBreakdownRequestObject{DocId: e.b.ID,
					Body: &api.PushBreakdownJSONRequestBody{BasedOn: basedOn, Tracker: api.PushBreakdownRequestTrackerLinear, Target: "ENG"}})
				if err != nil {
					return api.PushResult{}, err
				}
				return api.PushResult(r.(api.PushBreakdown200JSONResponse)), nil
			}
			if _, err := push(bd.Revision); code(err) != "breakdown_must" {
				t.Fatalf("push with MUST findings: %v, want breakdown_must", err)
			}

			edit := func(ops ...api.BreakdownOp) (api.Breakdown, error) {
				r, err := e.app.API.EditBreakdown(as("author"), api.EditBreakdownRequestObject{DocId: e.b.ID,
					Body: &api.EditBreakdownJSONRequestBody{BasedOn: bd.Revision, Ops: ops}})
				if err != nil {
					return api.Breakdown{}, err
				}
				bd = api.Breakdown(r.(api.EditBreakdown200JSONResponse))
				return bd, nil
			}
			reason := "A decision needs no code."
			noBuild := api.BreakdownOpAnswerNoBuild
			if _, err := edit(api.BreakdownOp{Op: api.MoveTrace, TraceId: ptr("REQ-004"), To: ptr("i2")},
				api.BreakdownOp{Op: api.Cover, TraceId: ptr("DEC-001"), Answer: &noBuild, Reason: &reason}); err != nil {
				t.Fatal(err)
			}
			if bd.Must != 0 {
				t.Fatalf("findings %+v, want no MUST after the fixes", bd.Findings)
			}

			// A split keeps each trace ID in an item, with its definition.
			if _, err := edit(api.BreakdownOp{Op: api.Split, Item: ptr("i1"), TraceIds: &[]string{"REQ-002"}}); err != nil {
				t.Fatal(err)
			}
			if len(bd.Items) != 3 || bd.Must != 0 {
				t.Fatalf("after the split: %d items, %d MUST", len(bd.Items), bd.Must)
			}
			split := bd.Items[1]
			if !slices.Equal(split.TraceIds, []string{"REQ-002"}) || len(split.Criteria) != 1 || *split.Criteria[0].TraceId != "REQ-002" {
				t.Errorf("the new item %+v does not carry REQ-002 and its definition", split)
			}
			// Moving the last trace ID out of an item makes it an orphan.
			if _, err := edit(api.BreakdownOp{Op: api.MoveTrace, TraceId: ptr("REQ-002"), From: ptr(split.Id), To: ptr("i1")}); err != nil {
				t.Fatal(err)
			}
			if !hasItemFinding(bd, breakdown.CheckOrphan, split.Id) {
				t.Errorf("findings %+v, want an orphan finding on %s", bd.Findings, split.Id)
			}
			if _, err := edit(api.BreakdownOp{Op: api.RemoveItem, Item: ptr(split.Id)}); err != nil {
				t.Fatal(err)
			}
			// A cycle names both items.
			if _, err := edit(api.BreakdownOp{Op: api.SetDependencies, Item: ptr("i1"), DependsOn: &[]string{"i2"}}); err != nil {
				t.Fatal(err)
			}
			if !hasItemFinding(bd, breakdown.CheckCycle, "i1") || !hasItemFinding(bd, breakdown.CheckCycle, "i2") {
				t.Errorf("findings %+v, want a cycle finding on i1 and i2", bd.Findings)
			}
			if _, err := edit(api.BreakdownOp{Op: api.SetDependencies, Item: ptr("i1"), DependsOn: &[]string{}}); err != nil {
				t.Fatal(err)
			}
			// An epic's default assignee goes to each of its items with none.
			agent := api.Assignee{Kind: api.AssigneeKindAgent, Id: "a1", Name: "Builder bot"}
			if _, err := edit(api.BreakdownOp{Op: api.SetEpicDefault, Epic: ptr(item(t, bd, "i2").Epic), Assignee: &agent}); err != nil {
				t.Fatal(err)
			}
			if a := item(t, bd, "i2").EffectiveAssignee; a == nil || a.Id != "a1" {
				t.Errorf("i2 assignee = %+v, want the epic's agent", a)
			}

			// A stale revision is refused.
			if _, err := push(bd.Revision - 1); code(err) != "breakdown_changed" {
				t.Errorf("push on an old revision: %v, want breakdown_changed", err)
			}
			out, err := push(bd.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Created) != 4 || linear.count() != 4 {
				t.Fatalf("created %d issues (%d saves), want 2 epics and 2 items", len(out.Created), linear.count())
			}
			linear.mu.Lock()
			retry := linear.saves[3]
			linear.mu.Unlock()
			if retry["parentId"] == nil || retry["delegate"] != "a1" || !slices.Contains(toStrings(retry["blockedBy"]), "uuid-3") {
				t.Errorf("the retry item was saved as %v, want a parent, the agent as delegate and blocked by i1", retry)
			}
			desc, _ := retry["description"].(string)
			blk, ok := breakdown.ParseBlock(desc)
			if !ok || blk.Bundle != e.b.Slug || !slices.Equal(blk.TraceIDs, []string{"REQ-003", "REQ-004"}) || blk.Handoff != out.HandoffId.String() {
				t.Errorf("Speccy block %+v (ok %v) in:\n%s", blk, ok, desc)
			}
			hs, err := e.app.API.ListHandoffs(as("author"), api.ListHandoffsRequestObject{DocId: e.b.ID})
			if err != nil {
				t.Fatal(err)
			}
			if items := hs.(api.ListHandoffs200JSONResponse).Items; len(items) != 1 || items[0].Label == nil || *items[0].Label != "breakdown" {
				t.Errorf("handoffs %+v, want one with the label breakdown", items)
			}
			again, err := push(out.Breakdown.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if len(again.Created) != 0 || linear.count() != 4 {
				t.Errorf("a second push created %d issues", len(again.Created))
			}

			// A scoped run resolves the work item from its issue key.
			wi, err := e.app.API.GetWorkItem(as("member"), api.GetWorkItemRequestObject{Params: api.GetWorkItemParams{Ref: "ENG-4"}})
			if err != nil {
				t.Fatal(err)
			}
			w := api.WorkItemPacket(wi.(api.GetWorkItem200JSONResponse))
			pinned := w.VersionNumber
			if w.Item.Id != "i2" || w.NewerVersion {
				t.Errorf("work item %s newer %v, want i2 at the current version", w.Item.Id, w.NewerVersion)
			}

			// A new version pins the pushed item, and marks the breakdown stale.
			e.edit(t, strings.Replace(breakdownDoc, "retry a refused call once", "retry a refused call twice", 1))
			wi, err = e.app.API.GetWorkItem(as("member"), api.GetWorkItemRequestObject{Params: api.GetWorkItemParams{Ref: "https://linear.app/acme/issue/ENG-4/x"}})
			if err != nil {
				t.Fatal(err)
			}
			w = api.WorkItemPacket(wi.(api.GetWorkItem200JSONResponse))
			if !w.NewerVersion || w.VersionNumber != pinned || !strings.Contains(w.Doc, "retry a refused call once") {
				t.Errorf("work item newer %v version %d, want the pinned version %d and its text", w.NewerVersion, w.VersionNumber, pinned)
			}
			g, err := e.app.API.GetBreakdown(as("author"), api.GetBreakdownRequestObject{DocId: e.b.ID})
			if err != nil {
				t.Fatal(err)
			}
			bd = api.Breakdown(g.(api.GetBreakdown200JSONResponse))
			if !bd.Stale || len(item(t, bd, "i2").Changed) == 0 || len(item(t, bd, "i1").Changed) != 0 {
				t.Errorf("stale %v, i2 changed %v, i1 changed %v; want only i2 named", bd.Stale, item(t, bd, "i2").Changed, item(t, bd, "i1").Changed)
			}
			if _, err := push(bd.Revision); code(err) != "breakdown_stale" {
				t.Errorf("push of a stale breakdown: %v, want breakdown_stale", err)
			}
			u, err := e.app.API.UpdateBreakdown(as("author"), api.UpdateBreakdownRequestObject{DocId: e.b.ID,
				Body: &api.UpdateBreakdownJSONRequestBody{BasedOn: bd.Revision}})
			if err != nil {
				t.Fatal(err)
			}
			bd = api.Breakdown(u.(api.UpdateBreakdown200JSONResponse))
			i2 := item(t, bd, "i2")
			if bd.Stale || i2.Issue == nil || i2.EffectiveAssignee == nil || !strings.Contains(i2.Criteria[0].Quote, "twice") {
				t.Errorf("after the update: stale %v, issue %v, assignee %v, criterion %q", bd.Stale, i2.Issue, i2.EffectiveAssignee, i2.Criteria[0].Quote)
			}
		})
	}
}

func item(t *testing.T, b api.Breakdown, id string) api.WorkItem {
	t.Helper()
	for _, it := range b.Items {
		if it.Id == id {
			return it
		}
	}
	t.Fatalf("the breakdown has no item %s", id)
	return api.WorkItem{}
}

func hasFinding(b api.Breakdown, slug, traceID string) bool {
	return slices.ContainsFunc(b.Findings, func(f api.BreakdownFinding) bool {
		return f.Slug == slug && f.TraceId != nil && *f.TraceId == traceID
	})
}

func hasItemFinding(b api.Breakdown, slug, itemID string) bool {
	return slices.ContainsFunc(b.Findings, func(f api.BreakdownFinding) bool {
		return f.Slug == slug && slices.Contains(f.Items, itemID)
	})
}

func code(err error) string {
	if ke, ok := kernel.AsError(err); ok {
		return ke.Code
	}
	return ""
}

func toStrings(v any) []string {
	var out []string
	if xs, ok := v.([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
