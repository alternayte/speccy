package prreview

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/findingkey"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

// PlanGithubReview is POST /github-reviews/plan: the checklist of a GitHub review of a pull
// request source. It posts nothing (docs/specs/pr-source-and-github-review.md).
func (a *API) PlanGithubReview(ctx context.Context, req api.PlanGithubReviewRequestObject) (api.PlanGithubReviewResponseObject, error) {
	p, err := a.planReview(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	return api.PlanGithubReview200JSONResponse(p.out), nil
}

// PostGithubReview is POST /github-reviews: it writes the items of the checklist into the
// caller's pending review on the pull request. The caller submits it on GitHub.
func (a *API) PostGithubReview(ctx context.Context, req api.PostGithubReviewRequestObject) (api.PostGithubReviewResponseObject, error) {
	p, err := a.planReview(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	if !p.out.CanPost {
		return nil, kernel.Invalid("cannot_post", "%s", deref(p.out.Reason))
	}
	if len(p.pending.Comments) == 0 && p.inBody == 0 {
		return nil, kernel.Invalid("nothing_to_post", "The checklist holds no item to post. The pull request or your pending review already holds the rest.")
	}
	url, added, removed, err := a.post(ctx, p.gh, p.pending)
	if err != nil {
		return nil, kernel.Invalid("github_refused", "GitHub did not take the pending review: %s.", strings.TrimSuffix(err.Error(), "."))
	}
	return api.PostGithubReview200JSONResponse{ReviewUrl: url, Added: added, Removed: removed}, nil
}

// plannedReview is a checklist, the pending review it makes, and the client that posts it.
type plannedReview struct {
	out     api.GithubReviewPlan
	pending action.PendingReview
	inBody  int
	gh      *github.Client
}

// lineKey is a place on a file of the pull request.
type lineKey struct {
	path string
	line int
}

func (a *API) planReview(ctx context.Context, req api.GithubReviewRequest) (plannedReview, error) {
	var out plannedReview
	p, err := parsePR(req.Url)
	if err != nil {
		return out, err
	}
	if a.Bundles == nil || a.Questions == nil {
		return out, kernel.Invalid("no_github_review", "This Speccy cannot prepare a GitHub review.")
	}
	preset := req.Preset
	if !preset.Valid() {
		return out, kernel.Invalid("bad_preset", "The preset is questions, must or all.")
	}
	src, docs, dirs, err := a.Bundles.PullDocs(ctx, p.build.Repo, p.build.Pull)
	if err != nil {
		return out, err
	}
	if src.ID == uuid.Nil {
		return out, kernel.NotFound("no_pull_source", "No source follows pull request #%d of %s. Add it with From GitHub: paste its URL.", p.build.Pull, p.build.Repo)
	}
	actor := kernel.ActorFrom(ctx)
	q := a.DB.Queries()
	var readable []pgdb.SpecDoc
	for _, d := range docs {
		if ok, err := share.CanRead(ctx, q, actor, d); err != nil {
			return out, err
		} else if ok {
			readable = append(readable, d)
		}
	}
	if len(readable) == 0 {
		return out, kernel.NotFound("no_pull_source", "You cannot see a spec doc of pull request #%d of %s.", p.build.Pull, p.build.Repo)
	}

	// The reviewer's own client reads the pending review and posts it. Without one, the
	// workspace client reads the pull request, and the checklist says why nothing can post.
	out.out = api.GithubReviewPlan{Repo: src.Repo, Pull: p.build.Pull, Docs: []api.GithubReviewDoc{}, CanPost: true}
	gh, gerr := a.reviewer(ctx, p.build)
	read := gh
	if gerr != nil {
		out.out.CanPost = false
		reason := problemText(gerr)
		out.out.Reason = &reason
		if ke, ok := kernel.AsError(gerr); ok && ke.Code == "github_link_required" {
			yes := true
			out.out.LinkRequired = &yes
		}
		if read, err = a.client(ctx, p.build); err != nil {
			return out, err
		}
	}
	out.gh = gh
	info, err := read.PullRequest(ctx, src.Repo, p.build.Pull)
	if err != nil {
		return out, github.UnreadableRepo(src.Repo, err)
	}
	if info.State != "open" {
		out.out.CanPost = false
		reason := fmt.Sprintf("Pull request #%d is %s. GitHub takes a review on an open pull request only.", p.build.Pull, info.State)
		out.out.Reason = &reason
	}
	out.out.HeadSha = info.HeadSHA
	files, err := read.PRFiles(ctx, src.Repo, p.build.Pull)
	if err != nil {
		return out, github.UnreadableRepo(src.Repo, err)
	}
	changed := map[string]map[int]bool{}
	for _, f := range files {
		changed[f.Filename] = action.ChangedLines(f.Patch)
	}
	threads, err := read.ReviewThreads(ctx, src.Repo, p.build.Pull)
	if err != nil {
		return out, github.UnreadableRepo(src.Repo, err)
	}
	// A key an open comment holds is on the pull request already, whoever posted it. A thread
	// with no marker is a person's, and the item on its line says how many there are.
	held := map[string]bool{}
	human := map[lineKey]int{}
	for _, t := range threads {
		if t.Resolved {
			continue
		}
		if k := action.MarkedKey(t.Body); k != "" {
			held[k] = true
		} else if t.Line > 0 {
			human[lineKey{t.Path, t.Line}]++
		}
	}
	if gh != nil {
		pend, err := gh.Pending(ctx, src.Repo, p.build.Pull)
		if err != nil {
			return out, github.UnreadableRepo(src.Repo, err)
		}
		if pend != nil {
			for _, c := range pend.Comments {
				if k := action.MarkedKey(c.Body); k != "" {
					held[k] = true
				} else if c.Line > 0 {
					human[lineKey{c.Path, c.Line}]++
				}
			}
		}
	}
	exclude := map[string]bool{}
	for _, k := range deref(req.Exclude) {
		exclude[k] = true
	}

	// A finding names a linked doc by its slug. The author knows the doc by its slug on the
	// branch, without the -pr-<n> that keeps the bundles of the pull request apart.
	var pairs []string
	for _, d := range docs {
		if bare := findingkey.DocSlug(d.Slug); bare != d.Slug {
			pairs = append(pairs, d.Slug, bare)
		}
	}
	bareSlugs := strings.NewReplacer(pairs...)
	current := map[string]bool{}
	var dirList []string
	var comments []github.ReviewComment
	var keys []string
	var rest []string
	file := ""
	for _, d := range readable {
		dir := dirs[d.ID]
		dirList = append(dirList, dir)
		doc, items, err := a.docItems(ctx, d, dir, preset, current)
		if err != nil {
			return out, err
		}
		if p := path.Join(dir, d.DocPath); file == "" && changed[p] != nil {
			file = p
		}
		for _, it := range items {
			if held[it.Key] {
				out.out.Already++
				continue
			}
			if exclude[it.Key] {
				continue
			}
			it.Body = bareSlugs.Replace(it.Body)
			it.Inline = it.Line > 0 && changed[it.Path][it.Line]
			it.ExistingComments = human[lineKey{it.Path, it.Line}]
			doc.Items = append(doc.Items, it)
			body := it.Body + a.bundleLink(d) + "\n\n" + findingkey.Mark(it.Key)
			if it.Inline {
				comments = append(comments, github.ReviewComment{Path: it.Path, Line: it.Line, Side: "RIGHT", Body: body})
				keys = append(keys, it.Key)
			} else {
				rest = append(rest, fmt.Sprintf("- %s: %s", place(it), firstLine(it.Body)))
			}
		}
		out.out.Docs = append(out.out.Docs, doc)
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Speccy reviewed %d spec doc%s at %s.\n", len(readable), plural(len(readable)), short(info.HeadSHA))
	if len(rest) > 0 {
		body.WriteString("\nOn lines that this pull request does not change:\n" + strings.Join(rest, "\n") + "\n")
	}
	out.inBody = len(rest)
	out.pending = action.Prepared(src.Repo, p.build.Pull, info.HeadSHA, body.String(), file, comments, keys, current, dirList)
	return out, nil
}

// docItems are the checklist items of one spec doc for a preset, before the pull request
// filters them. current gets the key of every open finding and blocking thread of the doc.
func (a *API) docItems(ctx context.Context, d pgdb.SpecDoc, dir string, preset api.GithubReviewRequestPreset,
	current map[string]bool) (api.GithubReviewDoc, []api.GithubReviewItem, error) {
	q := a.DB.Queries()
	doc := api.GithubReviewDoc{DocId: d.ID, Path: path.Join(dir, d.DocPath), Title: d.Title, Profile: d.ProfileKey, Items: []api.GithubReviewItem{}}
	v, _, err := review.Summary(ctx, q, d)
	if err != nil || v == nil {
		return doc, nil, err
	}
	verdict := api.GithubReviewDocVerdict(v.Result)
	doc.Verdict = &verdict
	res, err := a.ReviewAPI.ListFindings(ctx, api.ListFindingsRequestObject{RunId: v.RunId})
	if err != nil {
		return doc, nil, err
	}
	findings := res.(api.ListFindings200JSONResponse).Items
	vfiles := map[string][]byte{}
	if d.CurrentVersionID.Valid {
		fs, err := version.Files(ctx, q, d.CurrentVersionID.UUID)
		if err != nil {
			return doc, nil, err
		}
		for _, f := range fs {
			vfiles[f.Path] = f.Content
		}
	}
	slug := findingkey.DocSlug(d.Slug)
	keys := findingkey.Keys(slug, findings, vfiles)
	byID := map[uuid.UUID]int{}
	for i, f := range findings {
		byID[f.Id] = i
		if !f.Waived {
			current[keys[i]] = true
		}
	}
	item := func(i int, kind api.GithubReviewItemKind) api.GithubReviewItem {
		f := findings[i]
		level := api.GithubReviewItemLevel(f.Level)
		id := f.Id
		line := f.Line
		if f.Anchor.Detached != nil && *f.Anchor.Detached {
			line = 0
		}
		return api.GithubReviewItem{Key: keys[i], Kind: kind, Level: &level, FindingId: &id, Path: path.Join(dir, f.Anchor.File),
			Line: line, Body: action.ItemBody(f)}
	}
	var items []api.GithubReviewItem
	qs, err := a.Questions.List(ctx, d.ID)
	if err != nil {
		return doc, nil, err
	}
	for _, it := range qs.Items {
		if it.Thread != nil {
			current[findingkey.Thread(slug, it.Thread.Id.String())] = true
		}
	}
	switch preset {
	case api.GithubReviewRequestPresetQuestions:
		for _, it := range qs.Items {
			switch {
			case it.Finding != nil:
				if i, ok := byID[it.Finding.Id]; ok {
					gi := item(i, api.GithubReviewItemKind(it.Kind))
					// A material gap leads with its build question. A decision's comment leads with its
					// own question already.
					if it.Ask != "" && it.Kind == api.QuestionItemKindMaterialGap && !strings.HasPrefix(gi.Body, it.Ask) {
						gi.Body = it.Ask + "\n\n" + gi.Body
					}
					items = append(items, gi)
				}
			case it.Thread != nil:
				line := 0
				file := d.DocPath
				if an := it.Anchor; an != nil && an.File != "" {
					file = an.File
					if an.Quote != "" && (an.Detached == nil || !*an.Detached) {
						line = findingkey.LineOf(vfiles[an.File], an.Start)
					}
				}
				body := "**Blocking thread:** " + it.Ask
				if it.Context != nil {
					body += "\n\n" + *it.Context
				}
				items = append(items, api.GithubReviewItem{Key: findingkey.Thread(slug, it.Thread.Id.String()), Kind: api.GithubReviewItemKindBlockingThread,
					Path: path.Join(dir, file), Line: line, Body: body})
			}
		}
	case api.GithubReviewRequestPresetMust, api.GithubReviewRequestPresetAll:
		for i, f := range findings {
			if f.Waived || (f.UpstreamChange != nil && !f.UpstreamChange.Blocks) {
				continue
			}
			if preset == api.GithubReviewRequestPresetMust && f.Level != api.FindingLevelMUST {
				continue
			}
			items = append(items, item(i, api.GithubReviewItemKindFinding))
		}
		// MUST first, as the Action orders its comments, then in doc order.
		rank := map[api.GithubReviewItemLevel]int{api.GithubReviewItemLevelMUST: 0, api.GithubReviewItemLevelSHOULD: 1, api.GithubReviewItemLevelINFO: 2}
		sort.SliceStable(items, func(i, j int) bool { return rank[*items[i].Level] < rank[*items[j].Level] })
	}
	return doc, items, nil
}

// bundleLink is the line that opens the doc in Speccy, or "" when GitHub cannot reach Speccy.
func (a *API) bundleLink(d pgdb.SpecDoc) string {
	if a.BaseURL == "" {
		return ""
	}
	return fmt.Sprintf("\n\n<sub>[Open %s in Speccy](%s/bundles/%s/docs/%s)</sub>", d.Title, strings.TrimRight(a.BaseURL, "/"), d.BundleID, d.ID)
}

// place is where an item sits, for the review body.
func place(it api.GithubReviewItem) string {
	level := ""
	if it.Level != nil {
		level = "**" + string(*it.Level) + "** "
	}
	if it.Line > 0 {
		return fmt.Sprintf("%s%s:%d", level, it.Path, it.Line)
	}
	return level + it.Path
}

// firstLine is the first paragraph of a comment, for the review body.
func firstLine(body string) string {
	if i := strings.Index(body, "\n\n"); i >= 0 {
		return body[:i]
	}
	return body
}

// problemText is the text of an error for a person.
func problemText(err error) string {
	if ke, ok := kernel.AsError(err); ok {
		return ke.Detail
	}
	return strings.TrimRight(err.Error(), ".") + "."
}
