// Package questions builds the list of the questions screen: everything that blocks Build Ready
// and needs a person. The server owns the list, so the web app, the TUI and the MCP server show
// the same items, and the next action counts them.
package questions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/thread"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/features/waiver"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the questions of a doc.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Reviews   *review.API
	Threads   *thread.API
	Waivers   *waiver.API
}

// decisionChecks are the MUST checks whose finding needs a human answer that two options do not
// give: a coverage gap, a send-back, a missing upstream link, and a conflict the judge did not
// read.
var decisionChecks = map[string]bool{
	review.CoverageSlug: true, review.DownstreamRequestSlug: true, review.HasUpstreamSlug: true,
	review.ContradictionSlug: true, review.GroundingContradicted: true, review.GroundingFileContradicts: true,
}

// GetQuestions returns everything that blocks Build Ready and needs a person, in doc order.
func (a *API) GetQuestions(ctx context.Context, req api.GetQuestionsRequestObject) (api.GetQuestionsResponseObject, error) {
	out, err := a.List(ctx, req.DocId)
	if err != nil {
		return nil, err
	}
	return api.GetQuestions200JSONResponse(*out), nil
}

// List builds the questions of a doc.
func (a *API) List(ctx context.Context, docID uuid.UUID) (*api.Questions, error) {
	q := a.DB.Queries()
	b, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: docID})
	if err != nil {
		return nil, err
	}
	out := &api.Questions{Items: []api.QuestionItem{}, Waivers: []api.Waiver{}}

	wres, err := a.Waivers.ListWaivers(ctx, api.ListWaiversRequestObject{DocId: b.ID})
	if err != nil {
		return nil, err
	}
	// A gap whose Acknowledgement waits for approval is answered: the approval is what is left.
	// A missing upstream link whose standalone Acknowledgement waits likewise.
	asked := map[string]bool{}
	askedStandalone := false
	for _, w := range wres.(api.ListWaivers200JSONResponse).Items {
		if w.Status != api.WaiverStatusRequested {
			continue
		}
		if w.Trace != nil {
			asked[w.Trace.Id] = true
		}
		if w.Standalone != nil && *w.Standalone {
			askedStandalone = true
		}
		if w.CanApprove {
			out.Waivers = append(out.Waivers, w)
		}
	}

	findings := map[uuid.UUID]api.Finding{}
	v, _, err := review.Summary(ctx, q, b)
	if err != nil {
		return nil, err
	}
	if v != nil {
		out.RunId = &v.RunId
		res, err := a.Reviews.ListFindings(ctx, api.ListFindingsRequestObject{RunId: v.RunId})
		if err != nil {
			return nil, err
		}
		list := res.(api.ListFindings200JSONResponse)
		rows, err := q.ListFindings(ctx, v.RunId)
		if err != nil {
			return nil, err
		}
		evidence := map[uuid.UUID]map[string]any{}
		for _, r := range rows {
			e := map[string]any{}
			_ = json.Unmarshal(r.Evidence, &e)
			evidence[r.ID] = e
		}
		for _, f := range list.Items {
			findings[f.Id] = f
			if f.Level != api.FindingLevelMUST || f.Waived || f.UpstreamChange != nil && !f.UpstreamChange.Blocks {
				continue
			}
			if f.Unanswered != nil && *f.Unanswered {
				out.Recheck = true
				continue
			}
			if f.TraceId != nil && asked[*f.TraceId] || askedStandalone && f.CheckSlug == review.HasUpstreamSlug {
				continue
			}
			out.Items = append(out.Items, findingItem(f, evidence[f.Id]))
		}
	}

	tres, err := a.Threads.ListBundleThreads(ctx, api.ListBundleThreadsRequestObject{DocId: b.ID})
	if err != nil {
		return nil, err
	}
	for _, t := range tres.(api.ListBundleThreads200JSONResponse).Items {
		if t.Status != api.ThreadStatusOpen || !t.Blocking {
			continue
		}
		t := t
		out.Items = append(out.Items, api.QuestionItem{
			Key: "thread:" + t.Id.String(), Kind: api.QuestionItemKindBlockingThread,
			Ask: t.Title, Context: ptr("An open blocking thread keeps the doc Not Build Ready until someone resolves it."),
			Anchor: threadAnchor(t, b.DocPath, findings), Thread: &t,
		})
	}

	if err := a.order(ctx, b, out.Items); err != nil {
		return nil, err
	}
	return out, nil
}

// findingItem is the item of one blocking finding.
func findingItem(f api.Finding, e map[string]any) api.QuestionItem {
	f2 := f
	it := api.QuestionItem{Key: f.Id.String(), Anchor: &f2.Anchor, Finding: &f2}
	switch {
	case f.Material != nil && f.Material.Class != api.MaterialClassNone:
		it.Kind = api.QuestionItemKindMaterialGap
		if f.Fork != nil {
			it.Key = *f.Fork
		}
		if f.Material.Question != nil {
			it.Ask = *f.Material.Question
		}
	case decisionChecks[f.CheckSlug]:
		it.Kind = api.QuestionItemKindDecision
		ask, why := decisionAsk(f, e)
		it.Ask = ask
		if why != "" {
			it.Context = &why
		}
	case f.FixKind == api.Answer:
		// An answer finding the judge has not read is still a question with no options: the
		// author writes the fact.
		it.Kind = api.QuestionItemKindMaterialGap
		if f.Fork != nil {
			it.Key = *f.Fork
		}
	default:
		it.Kind = api.QuestionItemKindFinding
		it.Ask = f.Message
		it.Context = f.Fix
	}
	if it.Ask == "" {
		if f.Question != nil && *f.Question != "" {
			it.Ask = *f.Question
		} else {
			it.Ask = f.Message
		}
	}
	return it
}

// decisionAsk states the one decision a decision finding needs, and why.
func decisionAsk(f api.Finding, e map[string]any) (ask, why string) {
	str := func(k string) string { s, _ := e[k].(string); return s }
	switch f.CheckSlug {
	case review.ContradictionSlug:
		return fmt.Sprintf("Decide which is right: this doc or %s.", str("upstream")), str("explanation")
	case review.DownstreamRequestSlug:
		return fmt.Sprintf("Decide which is right: this doc or %s.", str("downstream")), "Reason of " + str("downstream") + ": " + str("reason")
	case review.GroundingContradicted:
		return "Correct this claim, or explain why the source does not apply.", f.Message
	case review.GroundingFileContradicts:
		return fmt.Sprintf("Decide which is right: this claim or %s.", str("file")), f.Message
	case review.CoverageSlug:
		return fmt.Sprintf("Does this doc cover %s?", str("id")), str("text")
	case review.HasUpstreamSlug:
		return "Link this doc to its upstream doc, or mark it standalone with a reason.", f.Message
	}
	return f.Message, ""
}

// threadAnchor is where a thread points in the current doc, or nil.
func threadAnchor(t api.Thread, file string, findings map[uuid.UUID]api.Finding) *api.Anchor {
	switch t.AnchorKind {
	case api.ThreadAnchorKindText:
		raw, _ := json.Marshal(t.Anchor)
		var an api.Anchor
		if json.Unmarshal(raw, &an) == nil {
			return &an
		}
	case api.ThreadAnchorKindSection:
		raw, _ := json.Marshal(t.Anchor["heading_path"])
		var path []string
		_ = json.Unmarshal(raw, &path)
		if path == nil {
			path = []string{}
		}
		return &api.Anchor{File: file, HeadingPath: path}
	case api.ThreadAnchorKindFinding:
		id, _ := uuid.Parse(fmt.Sprint(t.Anchor["finding_id"]))
		if f, ok := findings[id]; ok {
			return &f.Anchor
		}
	}
	return nil
}

// order sorts the items in doc order: by the quote's offset, or by the start of the section
// when the anchor names only a section. An item with no anchor comes first.
func (a *API) order(ctx context.Context, b pgdb.SpecDoc, items []api.QuestionItem) error {
	var src []byte
	if b.CurrentVersionID.Valid {
		files, err := version.Files(ctx, a.DB.Queries(), b.CurrentVersionID.UUID)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.Path == b.DocPath {
				src = f.Content
			}
		}
	}
	doc := section.Parse(src)
	pos := func(it api.QuestionItem) int {
		an := it.Anchor
		if an == nil || an.File != b.DocPath {
			return -1
		}
		if an.Quote != "" && (an.Detached == nil || !*an.Detached) {
			return an.Start
		}
		if start, _, ok := section.RangeAt(doc, src, an.HeadingPath); ok && len(an.HeadingPath) > 0 {
			return start
		}
		return -1
	}
	sort.SliceStable(items, func(i, j int) bool { return pos(items[i]) < pos(items[j]) })
	return nil
}

func ptr[T any](v T) *T { return &v }
