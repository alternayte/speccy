// Package tour builds the guided tour of a bundle (SDD §13.3): the ordered points that need a
// human decision. Findings that the author can fix without a decision are not tour points.
package tour

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/thread"
	"github.com/alternayte/speccy/internal/features/waiver"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the tour.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Reviews   *review.API
	Threads   *thread.API
	Waivers   *waiver.API
}

// decisionChecks are the MUST findings that need a human decision: divergence, contradiction,
// and coherence (SDD §13.3).
var decisionChecks = map[string]bool{
	review.DivergenceAmbiguous: true, review.DivergenceGap: true, review.ContradictionSlug: true,
	review.GroundingContradicted: true, review.CoverageSlug: true, review.HasUpstreamSlug: true,
}

// GetTour returns the tour of the bundle's current review, in the order of SDD §13.3: blocking
// threads, MUST findings that need a decision, pending waivers, then open decisions.
func (a *API) GetTour(ctx context.Context, req api.GetTourRequestObject) (api.GetTourResponseObject, error) {
	q := a.DB.Queries()
	b, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: a.Workspace, ID: req.DocId})
	if err != nil {
		return nil, err
	}
	out := api.Tour{Points: []api.TourPoint{}}
	sectionAnchor := func(path []string) *api.Anchor {
		if path == nil {
			path = []string{}
		}
		return &api.Anchor{File: b.DocPath, HeadingPath: path}
	}

	// The findings of the run behind the verdict, with anchors in the current version.
	findings := map[uuid.UUID]api.Finding{}
	var findingPoints []api.TourPoint
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
		for _, f := range list.Items {
			findings[f.Id] = f
		}
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
			if f.Waived || f.Level != api.FindingLevelMUST || !decisionChecks[f.CheckSlug] {
				continue
			}
			ask, why := findingAsk(f, evidence[f.Id])
			level := api.TourPointLevel(f.Level)
			id, an, slug := f.Id, f.Anchor, f.CheckSlug
			findingPoints = append(findingPoints, api.TourPoint{
				Key: "finding:" + f.Id.String(), Kind: api.TourPointKindFinding, Ask: ask, Context: why,
				Level: &level, CheckSlug: &slug, Anchor: &an, FindingId: &id, TraceId: f.TraceId,
			})
		}
	}

	tres, err := a.Threads.ListBundleThreads(ctx, api.ListBundleThreadsRequestObject{DocId: b.ID})
	if err != nil {
		return nil, err
	}
	threads := tres.(api.ListBundleThreads200JSONResponse).Items
	threadAnchor := func(t api.Thread) *api.Anchor {
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
			return sectionAnchor(path)
		case api.ThreadAnchorKindFinding:
			id, _ := uuid.Parse(fmt.Sprint(t.Anchor["finding_id"]))
			if f, ok := findings[id]; ok {
				return &f.Anchor
			}
		}
		return nil
	}
	for _, t := range threads {
		if t.Status != api.ThreadStatusOpen || !t.Blocking {
			continue
		}
		id := t.Id
		out.Points = append(out.Points, api.TourPoint{
			Key: "thread:" + t.Id.String(), Kind: api.TourPointKindBlockingThread,
			Ask: "Resolve the blocking thread: " + t.Title, Context: "An open blocking thread keeps the doc Not Build Ready.",
			Anchor: threadAnchor(t), ThreadId: &id,
		})
	}
	wres, err := a.Waivers.ListWaivers(ctx, api.ListWaiversRequestObject{DocId: b.ID})
	if err != nil {
		return nil, err
	}
	// A gap whose Acknowledgement waits for approval has one point: the approval. The author
	// answered it, so the tour does not ask again.
	// A missing upstream link whose standalone Acknowledgement waits likewise.
	asked := map[string]bool{}
	askedStandalone := false
	for _, w := range wres.(api.ListWaivers200JSONResponse).Items {
		if w.Status == api.WaiverStatusRequested && w.Trace != nil {
			asked[w.Trace.Id] = true
		}
		if w.Status == api.WaiverStatusRequested && w.Standalone != nil && *w.Standalone {
			askedStandalone = true
		}
	}
	for _, p := range findingPoints {
		if p.TraceId != nil && asked[*p.TraceId] || askedStandalone && *p.CheckSlug == review.HasUpstreamSlug {
			continue
		}
		out.Points = append(out.Points, p)
	}
	for _, w := range wres.(api.ListWaivers200JSONResponse).Items {
		if w.Status != api.WaiverStatusRequested {
			continue
		}
		where := "the whole doc"
		if len(w.Section) > 0 {
			where = strings.Join(w.Section, " › ")
		}
		id, slug, can := w.Id, w.CheckSlug, w.CanApprove
		level := api.TourPointLevel(w.Level)
		ask := fmt.Sprintf("Approve or reject the waiver of %s in %s.", w.CheckSlug, where)
		if t := w.Trace; t != nil {
			ask = fmt.Sprintf("Approve or reject: %s is out of scope for this doc.", t.Id)
			if t.Status == api.TraceAckStatusCoveredBy && t.Target != nil {
				ask = fmt.Sprintf("Approve or reject: %s covers %s.", *t.Target, t.Id)
			}
		}
		if w.Standalone != nil && *w.Standalone {
			ask = "Approve or reject: this doc is standalone, with no upstream doc."
		}
		out.Points = append(out.Points, api.TourPoint{
			Key: "waiver:" + w.Id.String(), Kind: api.TourPointKindWaiver,
			Ask:     ask,
			Context: "Reason: " + w.Reason,
			Level:   &level, CheckSlug: &slug, Anchor: sectionAnchor(w.Section), WaiverId: &id, CanApprove: &can,
		})
	}

	// Open decisions: open threads for humans in which nobody has marked a decision.
	for _, t := range threads {
		if t.Status != api.ThreadStatusOpen || t.Blocking || t.AddressedTo != api.ThreadAddressedToHumans {
			continue
		}
		msgs, err := q.ListThreadMessages(ctx, t.Id)
		if err != nil {
			return nil, err
		}
		decided := false
		for _, m := range msgs {
			if m.Decision != "" {
				decided = true
			}
		}
		if decided {
			continue
		}
		id := t.Id
		out.Points = append(out.Points, api.TourPoint{
			Key: "thread:" + t.Id.String(), Kind: api.TourPointKindOpenDecision,
			Ask: "Decide: " + t.Title, Context: "Nobody has marked a decision in this thread yet.",
			Anchor: threadAnchor(t), ThreadId: &id,
		})
	}
	return api.GetTour200JSONResponse(out), nil
}

// findingAsk states the one decision that a finding needs, and why.
func findingAsk(f api.Finding, e map[string]any) (ask, why string) {
	str := func(k string) string { s, _ := e[k].(string); return s }
	switch f.CheckSlug {
	case review.DivergenceAmbiguous:
		return "Decide: " + str("question"), "Readers gave different answers. State one answer in the doc."
	case review.DivergenceGap:
		return "Decide: " + str("question"), "The doc does not answer this. Answer it, or state that it is out of scope."
	case review.ContradictionSlug:
		return fmt.Sprintf("Decide which is right: this doc or %s.", str("upstream")), str("explanation")
	case review.GroundingContradicted:
		return "Decide: correct this claim, or explain why the source does not apply.", f.Message
	case review.CoverageSlug:
		return fmt.Sprintf("Decide: does this doc cover %s?", str("id")), str("text")
	case review.HasUpstreamSlug:
		return "Decide: link this doc to its upstream doc, or mark it standalone with a reason.", f.Message
	}
	return "Decide: " + f.Message, ""
}

// FirstPoint returns the first point of a bundle's tour, or nil when nothing needs a person.
// The next action reads it (SDD §13.4).
func (a *API) FirstPoint(ctx context.Context, bundleID uuid.UUID) (*api.TourPoint, error) {
	res, err := a.GetTour(ctx, api.GetTourRequestObject{DocId: bundleID})
	if err != nil {
		return nil, err
	}
	t, ok := res.(api.GetTour200JSONResponse)
	if !ok || len(t.Points) == 0 {
		return nil, nil
	}
	return &t.Points[0], nil
}
