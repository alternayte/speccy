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

	"github.com/alternayte/speccy/db/dbtype"
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
	// From is set for content that Speccy read from a repo at a commit: the repo's own
	// .speccy.yaml, the sidecar of the doc, and the other spec docs of that commit.
	From *FromRepo
}

// FromRepo is what a doc of a repo at one commit brings with it, in place of what a served
// folder or a saved source gives a saved doc.
type FromRepo struct {
	// Dir is the folder of the doc, relative to the repo root: a relative link starts there.
	Dir string
	// Config is the repo's .speccy.yaml, and Decisions the sidecar of the doc.
	Config    source.RepoConfig
	Decisions source.Decisions
	// Siblings are the other spec docs of the same commit. A link resolves to one of them
	// before it resolves to a saved bundle.
	Siblings []Sibling
	// Refs are the links of the doc outside its folder, as the scan of the commit found them.
	Refs []source.Ref

	// docs and files are the siblings as the link resolver reads them.
	docs  []pgdb.SpecDoc
	files map[uuid.UUID][]source.File
}

// Sibling is another spec doc of the same commit.
type Sibling struct {
	Slug    string
	Dir     string
	DocPath string
	Profile string
	Title   string
	Files   []source.File
}

// prepare gives each sibling the row that the link resolver reads.
func (f *FromRepo) prepare(workspace uuid.UUID) {
	if f == nil || f.files != nil {
		return
	}
	f.files = map[uuid.UUID][]source.File{}
	for _, sib := range f.Siblings {
		ref, _ := json.Marshal(bundleRef{Dir: sib.Dir})
		doc := pgdb.SpecDoc{ID: kernel.NewID(), WorkspaceID: workspace, Slug: sib.Slug, Title: sib.Title, ProfileKey: sib.Profile,
			DocPath: sib.DocPath, SourceRef: dbtype.JSON(ref)}
		f.docs = append(f.docs, doc)
		f.files[doc.ID] = sib.Files
	}
}

// contentProfile picks the profile of content: the type of its main doc, or, for a doc that
// names no type, the profile whose headings it fits. The run says which one it used (REQ-135).
func (s *Service) contentProfile(c Content) (p profile.Versioned, key string, guessed bool, err error) {
	main, err := contentMainDoc(c)
	if err != nil {
		return p, "", false, err
	}
	key = main.Frontmatter.Type
	if key == "" {
		k, ok := profile.Guess(s.Profiles(), mainContent(c, main.Path))
		if !ok {
			return p, "", false, kernel.Invalid("no_profile", "This doc names no type, and its headings match no profile. Add \"type:\" to the frontmatter. The types are: %s.",
				strings.Join(profileKeys(s.Profiles()), ", "))
		}
		key, guessed = k, true
	}
	p, ok := s.Profiles()[key]
	if !ok {
		return p, "", false, kernel.Invalid("no_profile", "%s", s.noProfile(key))
	}
	return p, key, guessed, nil
}

// contentInput loads content as the stages read it, with the profile p.
func (s *Service) contentInput(ctx context.Context, c Content, p profile.Versioned) (input, error) {
	main, err := contentMainDoc(c)
	if err != nil {
		return input{}, err
	}
	slug := c.Slug
	if slug == "" {
		slug = strings.TrimSuffix(path.Base(main.Path), path.Ext(main.Path))
	}
	b := pgdb.SpecDoc{WorkspaceID: s.Workspace, Slug: slug, Title: main.Title, ProfileKey: p.Profile.Key, DocPath: main.Path}
	if c.From != nil {
		ref, _ := json.Marshal(bundleRef{Dir: c.From.Dir})
		b.SourceRef = dbtype.JSON(ref)
		c.From.prepare(s.Workspace)
	}
	return s.loadFiles(ctx, b, uuid.Nil, c.Files, p, c.From)
}

// EstimateContent estimates the chosen stages on content that is not saved. Lint calls no
// model, so content with no model stage costs nothing.
func (s *Service) EstimateContent(ctx context.Context, c Content, stages Stages) (Estimate, error) {
	p, _, _, err := s.contentProfile(c)
	if err != nil {
		return Estimate{}, err
	}
	if len(stages.roles(p.Profile)) == 0 {
		return Estimate{}, nil
	}
	if err := s.estimateRoles(ctx, p, stages); err != nil {
		return Estimate{}, err
	}
	in, err := s.contentInput(ctx, c, p)
	if err != nil {
		return Estimate{}, err
	}
	return s.estimate(ctx, in, p, uuid.Nil, stages)
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
	p, key, guessed, err := s.contentProfile(c)
	if err != nil {
		return out, err
	}
	for _, role := range stages.roles(p.Profile) {
		if _, err := s.Gateway.Assigned(ctx, role); err != nil {
			return out, err
		}
	}
	in, err := s.contentInput(ctx, c, p)
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
	if in.sizeNote != "" {
		rc.note(in.sizeNote)
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
			Message: f.message, Waived: waived[i], Anchor: anchorAPI(f.anchor), FixKind: f.kind()}
		af.Line, af.EndLine = lines(mainContent(c, f.anchor.File), f.anchor)
		if f.fix != "" {
			fix := f.fix
			af.Fix = &fix
		}
		evidence, _ := json.Marshal(f.evidence)
		af.Conflict = conflictAPI(source.ConflictOf(f.slug, evidence))
		if source.Unanswered(evidence) {
			yes := true
			af.Unanswered = &yes
		}
		if question := answerQuestion(f.slug, f.message, f.anchor.Quote, f.question, evidence); question != "" {
			af.Question = &question
		}
		if l := Layer(f.slug, f.stage, f.level); l != "" {
			layer := api.FindingLayer(l)
			af.Layer = &layer
		}
		out.Findings = append(out.Findings, af)
	}
	out.Verdict = verdict.Decide(vin)
	out.ProfileKey, out.ProfileVersion, out.MainDoc = p.Profile.Key, p.Version, in.bundle.DocPath
	out.Title, out.Slug = in.bundle.Title, in.bundle.Slug
	out.RelaxedCount = relaxedCount(p.Profile, ev.relaxed)
	rc.mu.Lock()
	out.Size = string(in.size)
	out.Notes = append([]string{}, rc.notes...)
	out.TokensIn, out.TokensOut, out.CostUSD, out.CacheHits = rc.tokensIn, rc.tokensOut, rc.cost, rc.cacheHits
	rc.mu.Unlock()
	if stages != nil {
		out.Notes = append(out.Notes, "Stages in this review: "+strings.Join(append([]string{StageLint}, stages...), ", ")+". The verdict counts only these stages, because this content has no earlier review.")
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
	out, err := a.contentReview(ctx, res, c.Files)
	if err != nil {
		return nil, err
	}
	return api.ReviewContent200JSONResponse(out), nil
}

// contentReview is the result of an unsaved review as the API gives it. It stores the files
// and the result, so the report of the review has a link.
func (a *API) contentReview(ctx context.Context, res ContentResult, files []source.File) (api.ContentReview, error) {
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
	return out, a.storeContentReview(ctx, res, files, out)
}

// ReviewUrl is POST /reviews/url: the review of the spec docs of a GitHub URL (#92).
func (a *API) ReviewUrl(ctx context.Context, req api.ReviewUrlRequestObject) (api.ReviewUrlResponseObject, error) {
	var stages Stages
	if req.Body.Stages != nil {
		stages = Stages{}
		for _, st := range *req.Body.Stages {
			stages = append(stages, string(st))
		}
	}
	res, err := a.Service.ReviewURL(ctx, req.Body.Url, stages)
	if err != nil {
		return nil, err
	}
	out, err := a.URLReviewOut(ctx, res)
	if err != nil {
		return nil, err
	}
	return api.ReviewUrl200JSONResponse(out), nil
}

// URLReviewOut is the review of a URL as the API gives it. It stores the review of each doc,
// so its report has a link.
func (a *API) URLReviewOut(ctx context.Context, res URLReview) (api.UrlReview, error) {
	out := api.UrlReview{Repo: res.Repo, Commit: res.Commit, Docs: []api.UrlReviewDoc{}, Config: ConfigOut(res.Config)}
	if res.Pull > 0 {
		out.Pull = &res.Pull
	}
	for _, d := range res.Docs {
		doc := api.UrlReviewDoc{Slug: d.Slug, Dir: d.Dir, Path: d.Path}
		if d.Err != nil {
			msg := d.Err.Error()
			if ke, ok := kernel.AsError(d.Err); ok {
				msg = ke.Detail
			}
			doc.Error = &msg
		} else {
			review, err := a.contentReview(ctx, d.Result, d.Files)
			if err != nil {
				return out, err
			}
			doc.Review = &review
		}
		out.Docs = append(out.Docs, doc)
	}
	return out, nil
}

// ConfigOut is the .speccy.yaml of a review of a URL as the API gives it.
func ConfigOut(c URLConfig) api.UrlReviewConfig {
	out := api.UrlReviewConfig{Source: api.UrlReviewConfigSource(c.Source), Path: c.Path,
		Pr: api.PrSettings{InlineLimit: c.PR.InlineLimit, Levels: []api.PrSettingsLevels{}, Attribution: api.PrSettingsAttributionSpeccy}}
	if out.Source == "" {
		out.Source = api.UrlReviewConfigSourceNone
	}
	for _, l := range c.PR.Levels {
		out.Pr.Levels = append(out.Pr.Levels, api.PrSettingsLevels(l))
	}
	if c.PR.Attribution == source.AttributionNone {
		out.Pr.Attribution = api.PrSettingsAttributionNone
	}
	return out
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
