package prreview

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

// AskAuthor is POST /pr-asks: one concern of the reviewer becomes one question for the author,
// on the section it is about, in the reviewer's pending review.
func (a *API) AskAuthor(ctx context.Context, req api.AskAuthorRequestObject) (api.AskAuthorResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	concern := strings.TrimSpace(req.Body.Concern)
	if concern == "" {
		return nil, kernel.Invalid("no_concern", "Write the concern, in your own words.")
	}
	p, err := parsePR(req.Body.Url)
	if err != nil {
		return nil, err
	}
	plan, err := a.Reviews.PlanURL(ctx, p.url)
	if err != nil {
		return nil, err
	}
	var docs []review.AskDoc
	for _, it := range plan.Items {
		if _, text, ok := mainDoc(it); ok && it.Doc.Err == nil {
			docs = append(docs, review.AskDoc{Path: it.Doc.Path, Text: text})
		}
	}
	places := review.Sections(docs)
	if want := deref(req.Body.Section); len(want) > 0 {
		var kept []review.AskPlace
		for _, pl := range places {
			if slices.Equal(pl.Path, want) {
				kept = append(kept, pl)
			}
		}
		if len(kept) == 0 {
			return nil, kernel.Invalid("no_such_section", "No spec doc of this pull request has the section %s.", strings.Join(want, " › "))
		}
		places = kept
	}
	draft, err := a.Reviews.DraftAsk(ctx, docs, places, concern)
	if err != nil {
		return nil, err
	}
	out := api.AskResult{Question: draft.Question}
	if len(draft.Places) == 0 {
		return nil, kernel.Invalid("no_section_fits", "No section of the spec docs of this pull request fits that concern. Name the section with --section.")
	}
	if len(draft.Places) > 1 && len(deref(req.Body.Section)) == 0 {
		out.Status = api.AskResultStatusUnclear
		cands := []api.AskSection{}
		for _, pl := range draft.Places {
			cands = append(cands, api.AskSection{Doc: docs[pl.Doc].Path, HeadingPath: pl.Path})
		}
		out.Candidates = &cands
		return api.AskAuthor200JSONResponse(out), nil
	}
	place := draft.Places[0]
	doc := docs[place.Doc].Path
	out.Doc, out.HeadingPath = &doc, &place.Path
	if draft.Answered && !deref(req.Body.Force) {
		out.Status = api.AskResultStatusAnswered
		out.AnswerQuote = &draft.AnswerQuote
		return api.AskAuthor200JSONResponse(out), nil
	}

	gh, err := a.client(ctx, p.build)
	if err != nil {
		return nil, err
	}
	files, err := gh.PRFiles(ctx, plan.Review.Repo, p.build.Pull)
	if err != nil {
		return nil, github.UnreadableRepo(plan.Review.Repo, err)
	}
	inDiff := false
	for _, f := range files {
		if f.Filename == doc && action.ChangedLines(f.Patch)[draft.Line] {
			inDiff = true
		}
	}
	id := askID(plan.Review.Repo, fmt.Sprint(p.build.Pull), doc, strings.Join(place.Path, "\x00"), draft.Question)
	where := doc
	if len(place.Path) > 0 {
		where += " › " + strings.Join(place.Path, " › ")
	}
	existing, err := gh.Pending(ctx, plan.Review.Repo, p.build.Pull)
	if err != nil {
		return nil, err
	}
	var url string
	at := api.AskResultPlaceLine
	if !inDiff {
		at = api.AskResultPlaceBody
	}
	switch {
	case existing != nil && action.Asked(existing, id):
		url = existing.URL
	case existing == nil && inDiff:
		c := github.ReviewComment{Path: doc, Line: draft.Line, Side: "RIGHT", Body: action.AskComment(draft.Question, id)}
		url, err = gh.CreatePendingReview(ctx, plan.Review.Repo, p.build.Pull, plan.Review.Commit, action.AskBody(), []github.ReviewComment{c})
	case existing == nil:
		url, err = gh.CreatePendingReview(ctx, plan.Review.Repo, p.build.Pull, plan.Review.Commit, action.AskInBody("", where, draft.Question, id), nil)
	case inDiff:
		url, err = existing.URL, gh.AddPendingComment(ctx, existing.ID, doc, draft.Line, action.AskComment(draft.Question, id))
	case strings.TrimSpace(existing.Body) != "":
		url, err = existing.URL, gh.SetPendingBody(ctx, existing.ID, action.AskInBody(existing.Body, where, draft.Question, id))
	default:
		// GitHub does not let anyone edit a review body that started empty.
		at = api.AskResultPlaceFile
		url, err = existing.URL, gh.AddPendingFileComment(ctx, existing.ID, doc, action.AskOnFile(where, draft.Question, id))
	}
	if err != nil {
		return nil, fmt.Errorf("GitHub did not take the question: %w", err)
	}
	out.Status, out.ReviewUrl, out.Place = api.AskResultStatusPosted, &url, &at
	if inDiff {
		out.Line = &draft.Line
	}
	return api.AskAuthor200JSONResponse(out), nil
}
