package breakdown

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// Answer is one build question with the answer the readers agreed on.
type Answer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	// TraceIDs and Sections are what the question cites, to match it to work items.
	TraceIDs []string   `json:"-"`
	Sections [][]string `json:"-"`
}

// View is a breakdown with everything a screen and a push read from the doc now: whether it is
// stale, what changed, the findings, the coverage and the pushed issues.
type View struct {
	Stored
	Bundle         pgdb.SpecDoc
	VersionNumber  int64
	CurrentVersion int64
	Stale          bool
	// Changes are the items a newer doc version changed, by item ID. Empty when not stale.
	Changes  map[string][]string
	NewIDs   []string
	TraceIDs []string
	Findings []Finding
	Issues   []pgdb.BreakdownIssue
	Answers  []Answer
	// Versions are the version numbers of the issues' versions, by version ID.
	Versions map[uuid.UUID]int64
}

// view loads the breakdown of docID with the doc it quotes.
func (a *API) view(ctx context.Context, q store.Querier, docID uuid.UUID) (View, error) {
	b, err := version.Bundle(ctx, q, a.Workspace, docID)
	if err != nil {
		return View{}, err
	}
	s, ok, err := a.load(ctx, q, b.ID)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, noBreakdown()
	}
	d, err := a.read(ctx, q, b, true)
	if err != nil {
		return View{}, err
	}
	v := View{Stored: s, Bundle: b, CurrentVersion: d.version.Number, Changes: map[string][]string{}}
	if ver, err := q.GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: s.Row.VersionID}); err == nil {
		v.VersionNumber = ver.Number
	}
	v.Stale = s.Row.VersionID != b.CurrentVersionID.UUID
	if v.Stale {
		v.Changes = changes(s.Body, d)
		v.NewIDs = newTraceIDs(s.Body, d)
	}
	// Coverage is over the trace IDs of the version the breakdown quotes.
	for id := range s.Body.Defs {
		v.TraceIDs = append(v.TraceIDs, id)
	}
	order := map[string]int{}
	for i, def := range d.defs {
		order[def.ID] = i
	}
	sort.SliceStable(v.TraceIDs, func(i, j int) bool {
		oi, iok := order[v.TraceIDs[i]]
		oj, jok := order[v.TraceIDs[j]]
		if iok != jok {
			return iok
		}
		if oi != oj {
			return oi < oj
		}
		return v.TraceIDs[i] < v.TraceIDs[j]
	})
	overrides := map[string]string{}
	for k, o := range d.prof.Lint.Overrides {
		overrides[k] = o.Level
	}
	v.Findings = checks(s.Body, v.TraceIDs, d.prof.Breakdown.MaxTraceIDs, grammarOn(overrides))
	if v.Issues, err = q.ListBreakdownIssues(ctx, s.Row.ID); err != nil {
		return View{}, err
	}
	v.Versions = map[uuid.UUID]int64{}
	for _, is := range v.Issues {
		if _, ok := v.Versions[is.VersionID]; ok {
			continue
		}
		if ver, err := q.GetVersion(ctx, pgdb.GetVersionParams{SpecDocID: b.ID, ID: is.VersionID}); err == nil {
			v.Versions[is.VersionID] = ver.Number
		}
	}
	if v.Answers, err = a.answers(ctx, b); err != nil {
		return View{}, err
	}
	return v, nil
}

// answers are the build questions of the doc's newest full run that the readers agreed on.
func (a *API) answers(ctx context.Context, b pgdb.SpecDoc) ([]Answer, error) {
	if a.Questions == nil {
		return nil, nil
	}
	v, _, err := review.Summary(ctx, a.DB.Queries(), b)
	if err != nil || v == nil || v.Kind != api.BundleVerdictKindFull {
		return nil, err
	}
	res, err := a.Questions.ListQuestions(ctx, api.ListQuestionsRequestObject{RunId: v.RunId})
	if err != nil {
		return nil, err
	}
	list, ok := res.(api.ListQuestions200JSONResponse)
	if !ok {
		return nil, nil
	}
	var out []Answer
	for _, q := range list.Items {
		if q.Result != api.BuildQuestionResultAgree {
			continue
		}
		ans := ""
		for _, r := range q.Answers {
			if r.Answered && strings.TrimSpace(r.Answer) != "" {
				ans = strings.TrimSpace(r.Answer)
				break
			}
		}
		if ans == "" {
			continue
		}
		a := Answer{Question: q.Text, Answer: ans}
		for _, c := range q.Cites {
			switch {
			case c.Kind == api.CiteKindTrace && c.Id != nil:
				a.TraceIDs = append(a.TraceIDs, *c.Id)
			case c.Kind == api.CiteKindSection && c.Path != nil:
				a.Sections = append(a.Sections, *c.Path)
			}
		}
		if len(q.Anchor.HeadingPath) > 0 {
			a.Sections = append(a.Sections, q.Anchor.HeadingPath)
		}
		out = append(out, a)
	}
	return out, nil
}

// itemAnswers are the agreed answers that belong to an item: a question that cites one of its
// trace IDs, or a section one of its criteria sits in.
func itemAnswers(all []Answer, it Item) []Answer {
	var out []Answer
	for _, a := range all {
		match := false
		for _, t := range a.TraceIDs {
			if slices.Contains(it.TraceIDs, t) {
				match = true
			}
		}
		for _, sec := range a.Sections {
			if len(it.HeadingPath) > 0 && slices.Equal(sec, it.HeadingPath) {
				match = true
			}
			for _, c := range it.Criteria {
				if c.Doc == "" && slices.Equal(sec, c.HeadingPath) {
					match = true
				}
			}
		}
		if match {
			out = append(out, a)
		}
	}
	return out
}

// issueOf is the pushed issue of an item or an epic, if it has one.
func (v View) issueOf(kind, id string) *pgdb.BreakdownIssue {
	for i, is := range v.Issues {
		if is.Kind == kind && is.ItemID == id {
			return &v.Issues[i]
		}
	}
	return nil
}

// ToAPI is the API form of a view.
func (a *API) ToAPI(ctx context.Context, v View) api.Breakdown {
	b := v.Body
	out := api.Breakdown{
		Id: v.Row.ID, DocId: v.Bundle.ID, Bundle: v.Bundle.Slug, Title: v.Bundle.Title,
		VersionNumber: v.VersionNumber, CurrentVersionNumber: v.CurrentVersion, Stale: v.Stale,
		Verdict: v.Row.Verdict, Acknowledged: v.Row.Acknowledged, Revision: v.Row.Revision,
		Epics: []api.BreakdownEpic{}, Items: []api.WorkItem{}, Covered: []api.BreakdownCover{},
		Findings: []api.BreakdownFinding{}, Coverage: []api.BreakdownCoverage{}, NewTraceIds: nonNil(v.NewIDs),
		Must: mustCount(v.Findings), CreatedAt: v.Row.CreatedAt.UTC(), UpdatedAt: v.Row.UpdatedAt.UTC(),
		CoverAnswers: CoverAnswers,
	}
	for _, e := range b.Epics {
		ae := api.BreakdownEpic{Id: e.ID, Title: e.Title, DefaultAssignee: assigneeAPI(e.Default)}
		if is := v.issueOf("epic", e.ID); is != nil {
			ae.Issue = v.issueAPI(*is)
		}
		out.Epics = append(out.Epics, ae)
	}
	for _, it := range b.Items {
		out.Items = append(out.Items, a.itemAPI(ctx, v, it))
	}
	for _, c := range b.Covered {
		out.Covered = append(out.Covered, api.BreakdownCover{TraceId: c.TraceID, Answer: api.BreakdownCoverAnswer(c.Answer), Reason: c.Reason,
			By: kernel.PersonByID(ctx, a.People, c.By).Label()})
	}
	for _, f := range v.Findings {
		af := api.BreakdownFinding{Slug: f.Slug, Level: api.BreakdownFindingLevel(f.Level), Message: f.Message,
			Items: nonNil(f.Items)}
		if f.TraceID != "" {
			af.TraceId = &f.TraceID
		}
		out.Findings = append(out.Findings, af)
	}
	for _, t := range v.TraceIDs {
		c := api.BreakdownCoverage{TraceId: t, Text: b.Defs[t], Items: []string{}}
		for _, it := range b.Items {
			if slices.Contains(it.TraceIDs, t) {
				c.Items = append(c.Items, it.ID)
			}
		}
		for _, cv := range b.Covered {
			if cv.TraceID == t {
				ans := cv.Answer
				c.Cover = &ans
			}
		}
		out.Coverage = append(out.Coverage, c)
	}
	return out
}

func (a *API) itemAPI(ctx context.Context, v View, it Item) api.WorkItem {
	w := api.WorkItem{
		Id: it.ID, Ref: v.Bundle.Slug + "/" + it.ID, Epic: it.Epic, Title: it.Title, TraceIds: nonNil(it.TraceIDs),
		HeadingPath: nonNil(it.HeadingPath), Criteria: []api.WorkItemCriterion{}, Answers: []api.BuildAnswer{},
		Repos: nonNil(it.Repos), DependsOn: nonNil(it.DependsOn), Questions: nonNil(it.Questions),
		Assignee: assigneeAPI(it.Assignee), EffectiveAssignee: assigneeAPI(effective(v.Body, it)),
		Changed: nonNil(append(append([]string(nil), it.Changed...), v.Changes[it.ID]...)),
	}
	if it.Note != "" {
		w.Note = &it.Note
	}
	for _, c := range it.Criteria {
		ac := api.WorkItemCriterion{Quote: c.Quote, HeadingPath: nonNil(c.HeadingPath)}
		if c.TraceID != "" {
			ac.TraceId = &c.TraceID
		}
		if c.Doc != "" {
			ac.Doc = &c.Doc
		}
		w.Criteria = append(w.Criteria, ac)
	}
	for _, an := range itemAnswers(v.Answers, it) {
		w.Answers = append(w.Answers, api.BuildAnswer{Question: an.Question, Answer: an.Answer})
	}
	if r := it.Readers; r != nil {
		w.Readers = &api.ReaderCheck{Diverge: r.Diverge, Readings: nonNil(r.Readings), Reason: r.Reason,
			Current: r.Hash == hashItem(it)}
	}
	if is := v.issueOf("item", it.ID); is != nil {
		w.Issue = v.issueAPI(*is)
	}
	return w
}

func assigneeAPI(as *Assignee) *api.Assignee {
	if as == nil {
		return nil
	}
	return &api.Assignee{Kind: api.AssigneeKind(as.Kind), Id: as.ID, Name: as.Name}
}

func (v View) issueAPI(is pgdb.BreakdownIssue) *api.PushedIssue {
	return &api.PushedIssue{Tracker: api.PushedIssueTracker(is.Tracker), Key: is.IssueKey, Url: is.IssueUrl,
		HandoffId: is.HandoffID, VersionNumber: v.Versions[is.VersionID], CreatedAt: is.CreatedAt.UTC()}
}
