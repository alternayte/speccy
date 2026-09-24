package review

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// Content is a bundle that is not saved: files sent by `speccy review --server` or by the MCP
// tool review_content.
type Content struct {
	Slug    string
	MainDoc string // set for a single-file bundle (REQ-001 form b)
	Profile string // the profile when the main doc has no type (REQ-130)
	Files   []source.File
}

// ContentResult is the review of Content. The API stores it for the report (SDD §12.4).
type ContentResult struct {
	Title          string
	Slug           string
	ProfileKey     string
	ProfileVersion int64
	MainDoc        string
	Findings       []api.Finding
	Verdict        verdict.Verdict
	RelaxedCount   int
	Notes          []string
	// Size is the doc size the review used.
	Size      string
	TokensIn  int64
	TokensOut int64
	CostUSD   float64
	CacheHits int
}

// ReviewContent runs the chosen stages on content that is not saved (SDD §12.2 --server). Its
// links resolve against the saved bundles, so coherence checks the server's upstream docs.
func (s *Service) ReviewContent(ctx context.Context, c Content, stages Stages) (ContentResult, error) {
	var out ContentResult
	main, err := contentMainDoc(c)
	if err != nil {
		return out, err
	}
	// A doc that names no type is still reviewable: the profile comes from its headings, and
	// the run says which one it used (REQ-135).
	key, guessed := main.Frontmatter.Type, false
	if key == "" {
		k, ok := profile.Guess(s.Profiles(), mainContent(c, main.Path))
		if !ok {
			return out, kernel.Invalid("no_profile", "This doc names no type, and its headings match no profile. Add \"type:\" to the frontmatter. The types are: %s.",
				strings.Join(profileKeys(s.Profiles()), ", "))
		}
		key, guessed = k, true
	}
	p, ok := s.Profiles()[key]
	if !ok {
		return out, kernel.Invalid("no_profile", "%s", s.noProfile(key))
	}
	for _, role := range stages.roles(p.Profile) {
		if _, err := s.Gateway.Assigned(ctx, role); err != nil {
			return out, err
		}
	}
	slug := c.Slug
	if slug == "" {
		slug = strings.TrimSuffix(path.Base(main.Path), path.Ext(main.Path))
	}
	b := pgdb.SpecDoc{WorkspaceID: s.Workspace, Slug: slug, Title: main.Title, ProfileKey: p.Profile.Key, DocPath: main.Path}
	in, err := s.loadFiles(ctx, b, uuid.Nil, c.Files, p)
	if err != nil {
		return out, err
	}
	rc := &runCtx{
		progress: s.Progress, roles: map[string]string{}, prompts: map[string]string{},
		prices: map[string][2]float64{}, sem: make(chan struct{}, s.parallel(ctx)),
	}
	if guessed {
		rc.note(profile.GuessNote(key, profileKeys(s.Profiles())))
	}
	if in.sizeInferred {
		rc.note(sizeNote(in.fm.Size, in.size))
	}
	if rc.progress == nil {
		rc.progress = NewBroker()
	}
	var fingerprint string
	var native bool
	if len(stages.roles(p.Profile)) > 0 {
		a, err := s.Gateway.Assigned(ctx, model.RoleReviewer)
		if err != nil {
			return out, err
		}
		fingerprint = a.Backend.Kind + ":" + a.Model
		var preset struct {
			Preset string `json:"preset"`
		}
		_ = json.Unmarshal(a.Backend.Config, &preset)
		native = model.SearchCapable(a.Backend.Kind, preset.Preset, a.Model)
		if roles, err := s.DB.Queries().ListAssignments(ctx, s.Workspace); err == nil {
			for _, r := range roles {
				rc.prices[r.Role] = [2]float64{r.PriceInPerMtok, r.PriceOutPerMtok}
			}
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	ev, err := s.runStages(runCtx, rc, in, stages, fingerprint, native, func(string) {})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return out, kernel.Invalid("run_timeout", "The review passed its limit of %s.", RunTimeout)
		}
		return out, err
	}

	// The verdict rule (SDD §8.6) on the findings, with the waivers in the sidecar. Content
	// has no threads, so none blocks.
	waived := applyWaivers(in, &ev)
	vin := ev.in
	vin.Items = ev.items
	for i, f := range ev.findings {
		id := kernel.NewID()
		vin.Findings = append(vin.Findings, verdict.Finding{ID: id.String(), Level: f.level, Waived: waived[i]})
		af := api.Finding{Id: id, CheckSlug: f.slug, Level: api.FindingLevel(f.level), Stage: f.stage, Relaxed: ev.relaxed[f.slug],
			Message: f.message, Waived: waived[i], Anchor: anchorAPI(f.anchor)}
		if f.fix != "" {
			fix := f.fix
			af.Fix = &fix
		}
		if l := Layer(f.slug, f.stage, f.level); l != "" {
			layer := api.FindingLayer(l)
			af.Layer = &layer
		}
		out.Findings = append(out.Findings, af)
	}
	out.Verdict = verdict.Decide(vin)
	out.ProfileKey, out.ProfileVersion, out.MainDoc = p.Profile.Key, p.Version, main.Path
	out.Title, out.Slug = main.Title, slug
	out.RelaxedCount = relaxedCount(p.Profile, ev.relaxed)
	rc.mu.Lock()
	out.Size = string(in.size)
	out.Notes = append([]string{}, rc.notes...)
	out.TokensIn, out.TokensOut, out.CostUSD, out.CacheHits = rc.tokensIn, rc.tokensOut, rc.cost, rc.cacheHits
	rc.mu.Unlock()
	if stages != nil {
		out.Notes = append(out.Notes, "Stages in this review: "+strings.Join(append([]string{StageLint}, stages...), ", ")+". The verdict counts only these stages.")
	}
	return out, nil
}

// mainContent is the bytes of the main doc in c.
func mainContent(c Content, path string) []byte {
	for _, f := range c.Files {
		if f.Path == path {
			return f.Content
		}
	}
	return nil
}

func contentMainDoc(c Content) (source.MainDoc, error) {
	if len(c.Files) == 0 {
		return source.MainDoc{}, kernel.Invalid("no_files", "The review has no files. Send the bundle's main doc and its assets.")
	}
	if c.MainDoc != "" {
		for _, f := range c.Files {
			if f.Path == c.MainDoc {
				m, err := source.SingleFileMainDoc(f.Path, f.Content, c.Profile)
				if err != nil {
					return m, kernel.Invalid("bad_main_doc", "%s", err.Error())
				}
				return m, nil
			}
		}
		return source.MainDoc{}, kernel.Invalid("bad_main_doc", "The files do not include the main doc %s.", c.MainDoc)
	}
	m, err := source.FindMainDoc(c.Files)
	if err != nil {
		return m, kernel.Invalid("bad_main_doc", "The bundle has %s. A bundle needs exactly one markdown file with a type in its frontmatter (REQ-001).", err.Error())
	}
	return m, nil
}

// ReviewContent is POST /reviews.
func (a *API) ReviewContent(ctx context.Context, req api.ReviewContentRequestObject) (api.ReviewContentResponseObject, error) {
	c := Content{}
	if req.Body.Slug != nil {
		c.Slug = *req.Body.Slug
	}
	if req.Body.MainDoc != nil {
		c.MainDoc = *req.Body.MainDoc
	}
	if req.Body.Profile != nil {
		c.Profile = *req.Body.Profile
	}
	for _, f := range req.Body.Files {
		p, err := source.CleanPath(f.Path)
		if err != nil {
			return nil, kernel.Invalid("bad_path", "The file path %q is not valid: %v.", f.Path, err)
		}
		content := []byte(f.Content)
		if f.Encoding != nil && *f.Encoding == api.ContentFileEncodingBase64 {
			if content, err = base64.StdEncoding.DecodeString(f.Content); err != nil {
				return nil, kernel.Invalid("bad_content", "The file %s is not valid base64.", f.Path)
			}
		}
		c.Files = append(c.Files, source.File{Path: p, Content: content})
	}
	source.Sort(c.Files)
	var stages Stages
	if req.Body.Stages != nil {
		stages = Stages{}
		for _, st := range *req.Body.Stages {
			stages = append(stages, string(st))
		}
	}
	res, err := a.Service.ReviewContent(ctx, c, stages)
	if err != nil {
		return nil, err
	}
	out := api.ContentReview{ProfileKey: res.ProfileKey, ProfileVersion: res.ProfileVersion, MainDoc: res.MainDoc, Size: &res.Size,
		Findings: res.Findings, Notes: res.Notes}
	if out.Findings == nil {
		out.Findings = []api.Finding{}
	}
	if out.Notes == nil {
		out.Notes = []string{}
	}
	out.Verdict = contentVerdict(res)
	cost := float32(res.CostUSD)
	out.TokensIn, out.TokensOut, out.CostEstimate, out.CacheHits = &res.TokensIn, &res.TokensOut, &cost, &res.CacheHits
	out.Id = kernel.NewID()
	out.ReportPath = "/reviews/" + out.Id.String()
	if err := a.storeContentReview(ctx, res, c.Files, out); err != nil {
		return nil, err
	}
	return api.ReviewContent200JSONResponse(out), nil
}

// ContentReviewDays is how long the server keeps a content review for its report (SDD §15.3).
const ContentReviewDays = 90

// StoredFile is one file of a stored content review.
type StoredFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"` // base64 in JSON
}

// storeContentReview keeps the files and the result, so the Action can link to the report
// (SDD §12.4). It also removes the reviews older than ContentReviewDays.
func (a *API) storeContentReview(ctx context.Context, res ContentResult, files []source.File, out api.ContentReview) error {
	stored := make([]StoredFile, len(files))
	for i, f := range files {
		stored[i] = StoredFile{Path: f.Path, Content: f.Content}
	}
	fj, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	rj, err := json.Marshal(out)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	q := a.DB.Queries()
	if err := q.DeleteContentReviewsBefore(ctx, pgdb.DeleteContentReviewsBeforeParams{WorkspaceID: a.Workspace,
		Before: now.AddDate(0, 0, -ContentReviewDays)}); err != nil {
		return err
	}
	return q.InsertContentReview(ctx, pgdb.InsertContentReviewParams{ID: out.Id, WorkspaceID: a.Workspace, Slug: res.Slug,
		Title: res.Title, MainDoc: res.MainDoc, ProfileKey: res.ProfileKey, ProfileVersion: res.ProfileVersion,
		Files: fj, Result: rj, CreatedBy: kernel.ActorFrom(ctx).UserID, CreatedAt: now})
}

func contentVerdict(res ContentResult) api.ContentVerdict {
	v := api.ContentVerdict{Result: api.VerdictResult(res.Verdict.Result), Score: res.Verdict.Score, Radar: map[string]int{},
		WaiverCount: res.Verdict.WaiverCount, RelaxedCount: res.RelaxedCount}
	for c, n := range res.Verdict.Radar {
		v.Radar[string(c)] = n
	}
	for _, f := range res.Findings {
		if f.Waived {
			continue
		}
		switch f.Level {
		case api.FindingLevelMUST:
			v.Must++
		case api.FindingLevelSHOULD:
			v.Should++
		default:
			v.Info++
		}
	}
	return v
}
