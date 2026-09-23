// Package verify runs the post-build verification gate: it reads one code repo at one
// commit, finds where each verifiable trace ID is implemented and tested, and gives each one
// an outcome. Speccy never runs the code and never runs the tests, so a test target is a
// citation and never a pass.
package verify

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/ears"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/verify"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/thread"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store"
)

// API runs the gate for one workspace.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Profiles  func() map[string]profile.Versioned
	Gateway   *model.Gateway
	// GitHub returns the client for a GitHub host, as the review service gets it.
	GitHub func(ctx context.Context, apiURL string) (*github.Client, error)
	// Threads opens the blocking thread a MUST missing or a MUST breach becomes. The run
	// changes no verdict itself: the thread does, through the rule that already exists.
	Threads *thread.API
	// Progress receives the events of a running verification, for the bundle page.
	Progress *review.Broker
	// Wake tells the worker that a job is queued.
	Wake func()
	// Local is true in local mode, where a run may read a folder on disk.
	Local bool
	// Now is the clock, for tests.
	Now func() time.Time
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}

// Claim is a builder's statement for one trace ID. It is optional. When it exists it replaces
// the derived targets for that trace ID, and Speccy never merges the two.
type Claim struct {
	TraceID string          `json:"trace_id"`
	Targets []verify.Target `json:"targets"`
}

// Input is one verification run.
type Input struct {
	BundleID uuid.UUID
	// Target is what a person pasted: a GitHub URL, owner/name, or a folder in local mode.
	// Speccy resolves it into Repo and SHA, or Path, before the run is queued.
	Target string
	// Branch is the branch SHA was the head of, when the target named a branch or a repo.
	Branch string
	// Repo and SHA name a GitHub repo, or Path names a folder on disk. One of the two.
	Repo string
	SHA  string
	Path string
	// HandoffID ties the run to the handoff a builder took, when the caller names one.
	HandoffID *uuid.UUID
	// Claims are the builder's claims, by trace ID.
	Claims []Claim
	// APIURL is the GitHub host, empty for github.com.
	APIURL string
}

// Run is one finished verification run.
type Run struct {
	ID      uuid.UUID
	Status  string
	Error   string
	Branch  string
	Verdict verify.Verdict
	Counts  verify.Counts
	Notes   []string
	Results []Outcome
	Repo    string
	SHA     string
	BaseSHA string
	Digest  string
	Stale   bool
	At      time.Time
}

// name is the repo, or the folder, that a run reads.
func (in Input) name() string {
	if in.Path != "" {
		return in.Path
	}
	return in.Repo
}

// Outcome is one trace ID's result, with the evidence behind it.
type Outcome struct {
	verify.Result
	Targets    []verify.Target   `json:"targets"`
	Judgement  Judgement         `json:"judgement"`
	Provenance verify.Provenance `json:"provenance"`
}

// JobKind is the kind of a verification job in the job queue.
const JobKind = "verify"

// The status of a run: it is queued, then running, then done or failed.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// job is the payload of a verification job.
type job struct {
	RunID uuid.UUID `json:"run_id"`
	Input Input     `json:"input"`
}

// Start checks the request, resolves its target, and queues the run. The checks run here, so
// a bundle with no trace ID or a repo Speccy cannot read fails at once, not in the worker.
func (a *API) Start(ctx context.Context, in Input) (Run, error) {
	if err := a.prepare(ctx, &in); err != nil {
		return Run{}, err
	}
	b, err := version.Bundle(ctx, a.DB.Queries(), a.Workspace, in.BundleID)
	if err != nil {
		return Run{}, err
	}
	run := Run{ID: kernel.NewID(), Status: StatusQueued, Repo: in.name(), SHA: in.SHA, Branch: in.Branch, At: a.now()}
	payload, _ := json.Marshal(job{RunID: run.ID, Input: in})
	err = a.DB.InTx(ctx, func(tx store.Tx) error {
		if err := a.insert(ctx, tx.Queries(), b, run, in.HandoffID); err != nil {
			return err
		}
		return tx.Queries().InsertJob(ctx, pgdb.InsertJobParams{ID: kernel.NewID(), WorkspaceID: a.Workspace,
			Kind: JobKind, Payload: dbtype.JSON(payload), CreatedAt: run.At})
	})
	if err != nil {
		return Run{}, err
	}
	a.Progress.Publish(run.ID, review.Event{Type: "stage", Stage: StatusQueued})
	if a.Wake != nil {
		a.Wake()
	}
	return run, nil
}

// Execute runs one queued job. A run that fails stores its cause on the run, so the job ends
// either way.
func (a *API) Execute(ctx context.Context, payload []byte) error {
	var j job
	if err := json.Unmarshal(payload, &j); err != nil {
		return err
	}
	q := a.DB.Queries()
	row, err := q.GetVerificationRun(ctx, pgdb.GetVerificationRunParams{WorkspaceID: a.Workspace, ID: j.RunID})
	if err != nil {
		return err
	}
	if row.Status == StatusDone || row.Status == StatusFailed {
		return nil
	}
	// The worker has no request, so the run acts as the person who started it: the blocking
	// thread it opens names them.
	ctx = kernel.WithActor(ctx, kernel.Actor{UserID: row.StartedBy})
	if err := q.StartVerificationRun(ctx, j.RunID); err != nil {
		return err
	}
	a.Progress.Publish(j.RunID, review.Event{Type: "stage", Stage: StatusRunning})
	run, err := a.execute(ctx, j.RunID, j.Input)
	if err != nil {
		msg := err.Error()
		if ke, ok := kernel.AsError(err); ok {
			msg = ke.Detail
		}
		if err := q.FailVerificationRun(context.WithoutCancel(ctx), pgdb.FailVerificationRunParams{ID: j.RunID, Error: msg}); err != nil {
			return err
		}
		a.Progress.Publish(j.RunID, review.Event{Type: "failed", Stage: StatusRunning, Message: msg})
		return nil
	}
	a.Progress.Publish(j.RunID, review.Event{Type: "done", Stage: StatusDone, Message: string(run.Verdict)})
	return nil
}

// prepare checks that the bundle can be verified and that the request names code, and fills
// in the repo and the commit that the target names.
func (a *API) prepare(ctx context.Context, in *Input) error {
	q := a.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Workspace, in.BundleID)
	if err != nil {
		return err
	}
	cur, err := version.LoadCurrent(ctx, q, b)
	if err != nil {
		return err
	}
	main := cur.File(b.MainDoc)
	if len(main) == 0 {
		return kernel.Invalid("no_main_doc", "This bundle has no main doc to verify against.")
	}
	prof, ok := a.Profiles()[b.ProfileKey]
	if !ok {
		return kernel.Invalid("no_profile", "No profile is loaded for the doc type %q.", b.ProfileKey)
	}
	if reqs, _ := requirements(main, prof.Profile); len(reqs) == 0 {
		return kernel.Invalid("no_trace_ids",
			"This bundle defines no trace ID that the gate verifies. Accept the suggested IDs on the bundle page, then run this again.")
	}
	for _, role := range []string{model.RoleReviewer, model.RoleJudge} {
		if _, err := a.Gateway.Assigned(ctx, role); err != nil {
			return err
		}
	}
	switch {
	case in.Target != "":
	case in.Path != "":
		// A folder goes through the same check as a pasted one: hosted mode reads no disk.
		in.Target = in.Path
	case in.Repo != "" && in.SHA != "":
		return nil
	case in.Repo != "":
		in.Target = in.Repo
	default:
		defaults, err := a.defaults(ctx, b, main)
		if err != nil {
			return err
		}
		switch len(defaults) {
		case 0:
			return kernel.Invalid("no_code_target",
				"This doc has no implemented-by link. Paste the URL of the repo, the branch, the commit or the pull request to verify.")
		case 1:
			in.Target = defaults[0].Target
		default:
			var names []string
			for _, d := range defaults {
				names = append(names, d.Target)
			}
			return kernel.Invalid("many_code_targets", "This doc links to more than one repo. Name one: %s.", strings.Join(names, ", "))
		}
	}
	r, err := a.resolve(ctx, in.Target)
	if err != nil {
		return err
	}
	in.Repo, in.SHA, in.Branch, in.Path, in.APIURL = r.Repo, r.SHA, r.Branch, "", r.APIURL
	if r.Folder {
		in.Repo, in.Path = "", r.Repo
	}
	return nil
}

// Verify runs the gate at once, outside the queue, and stores the run.
func (a *API) Verify(ctx context.Context, in Input) (Run, error) {
	if err := a.prepare(ctx, &in); err != nil {
		return Run{}, err
	}
	b, err := version.Bundle(ctx, a.DB.Queries(), a.Workspace, in.BundleID)
	if err != nil {
		return Run{}, err
	}
	run := Run{ID: kernel.NewID(), Status: StatusQueued, Repo: in.name(), SHA: in.SHA, Branch: in.Branch, At: a.now()}
	if err := a.insert(ctx, a.DB.Queries(), b, run, in.HandoffID); err != nil {
		return Run{}, err
	}
	return a.execute(ctx, run.ID, in)
}

// execute runs the gate for one stored run, and stores its outcomes.
func (a *API) execute(ctx context.Context, id uuid.UUID, in Input) (Run, error) {
	q := a.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Workspace, in.BundleID)
	if err != nil {
		return Run{}, err
	}
	cur, err := version.LoadCurrent(ctx, q, b)
	if err != nil {
		return Run{}, err
	}
	main := cur.File(b.MainDoc)
	prof, ok := a.Profiles()[b.ProfileKey]
	if !ok {
		return Run{}, kernel.Invalid("no_profile", "No profile is loaded for the doc type %q.", b.ProfileKey)
	}
	reqs, skipped := requirements(main, prof.Profile)
	if len(reqs) == 0 {
		return Run{}, kernel.Invalid("no_trace_ids",
			"This bundle defines no trace ID that the gate verifies. Accept the suggested IDs on the bundle page, then run this again.")
	}

	a.Progress.Publish(id, review.Event{Type: "stage", Stage: "reading", Message: "Reading " + in.name()})
	repo, err := a.openRepo(ctx, in, prof.Profile.Verify)
	if err != nil {
		return Run{}, err
	}
	base, err := a.baseSHA(ctx, in, repo)
	if err != nil {
		return Run{}, err
	}
	changed, err := repo.Changed(ctx, base)
	if err != nil {
		return Run{}, err
	}

	a.Progress.Publish(id, review.Event{Type: "stage", Stage: "finding", Total: len(reqs)})
	targets, provenance, err := a.targetsFor(ctx, repo, reqs, in.Claims, changed, prof.Profile.Verify)
	if err != nil {
		return Run{}, err
	}

	waived, err := a.waivers(ctx, b, repo.Name(), main)
	if err != nil {
		return Run{}, err
	}

	run := Run{ID: id, Status: StatusDone, Repo: repo.Name(), SHA: repo.SHA(), Branch: in.Branch, BaseSHA: base,
		Digest: repo.Digest(), Notes: repo.Notes(), At: a.now()}
	a.Progress.Publish(id, review.Event{Type: "stage", Stage: "judging", Total: len(reqs)})
	for i, r := range reqs {
		a.Progress.Publish(id, review.Event{Type: "progress", Stage: "judging", Message: r.ID, Done: i, Total: len(reqs)})
		out, err := a.outcome(ctx, repo, r, targets[r.ID], provenance[r.ID], waived[r.ID])
		if err != nil {
			return Run{}, err
		}
		run.Results = append(run.Results, out)
	}
	results := make([]verify.Result, len(run.Results))
	for i, o := range run.Results {
		results[i] = o.Result
	}
	run.Counts = verify.Tally(results)
	run.Counts.Skipped = skipped
	run.Verdict = run.Counts.Verdict()
	if err := a.store(ctx, b, run); err != nil {
		return Run{}, err
	}
	number := int64(0)
	if v, err := q.GetVersion(ctx, pgdb.GetVersionParams{BundleID: b.ID, ID: b.CurrentVersionID.UUID}); err == nil {
		number = v.Number
	}
	if _, err := a.openThreads(ctx, b, prof.Profile, main, run, in.HandoffID, number); err != nil {
		return Run{}, err
	}
	// The run reads as done only after its threads exist, so a reader that sees done sees them.
	counts, _ := json.Marshal(run.Counts)
	notes, _ := json.Marshal(nonNil(run.Notes))
	if err := q.FinishVerificationRun(ctx, pgdb.FinishVerificationRunParams{
		ID: run.ID, BaseSha: run.BaseSHA, Digest: run.Digest, Verdict: string(run.Verdict),
		Counts: counts, Notes: notes,
	}); err != nil {
		return Run{}, err
	}
	return run, nil
}

// outcome checks a trace ID's targets, asks the judges when a code target holds, and decides.
func (a *API) outcome(ctx context.Context, repo Repo, r requirement, ts []verify.Target,
	prov verify.Provenance, waived bool) (Outcome, error) {

	out := Outcome{Targets: ts, Provenance: prov}
	item := verify.Item{ID: r.ID, Level: r.Level, Parsed: r.Parsed, Targets: ts, Provenance: prov, Waived: waived}

	var cited []citedCode
	for _, t := range ts {
		if !t.Holds {
			continue
		}
		body, ok, err := repo.Read(ctx, t.Path)
		if err != nil {
			return Outcome{}, err
		}
		if ok {
			cited = append(cited, citedCode{Path: t.Path, Kind: t.Kind, Body: string(body)})
		}
	}
	if len(cited) > 0 {
		j, err := a.judge(ctx, model.RoleJudge, r, cited)
		if err != nil {
			return Outcome{}, err
		}
		item.Judgement = j.Verdict
		// Only a contradiction can turn a build red, so only a contradiction gets a second,
		// independently prompted judge. Speccy prefers a different model fingerprint.
		if j.Verdict == verify.Contradicted {
			role := a.confirmRole(ctx, j.Fingerprint)
			v, why, fp, err := a.confirm(ctx, role, r, cited)
			if err != nil {
				return Outcome{}, err
			}
			j.Confirmed = v == verify.Contradicted
			j.ConfirmFingerprint, j.ConfirmReason = fp, why
			item.Confirmed = j.Confirmed
		}
		out.Judgement = j
	}
	out.Result = verify.Decide(item)
	if out.Outcome == verify.Missing && out.Note == "" && prov == verify.FromMapper {
		if len(ts) == 0 {
			out.Note = "No file in the scan names this requirement, and the mapper picked no file for it."
		} else {
			out.Note = "The mapper proposed targets, and none of their anchor quotes is in its file once."
		}
	}
	return out, nil
}

// confirmRole picks the second judge. It prefers a role whose model fingerprint differs from
// the first judge's, because two calls to one model agree with themselves.
func (a *API) confirmRole(ctx context.Context, first string) string {
	for _, role := range []string{model.RoleReader1, model.RoleReader2, model.RoleReader3, model.RoleReviewer} {
		as, err := a.Gateway.Assigned(ctx, role)
		if err != nil {
			continue
		}
		if as.Backend.Kind+":"+as.Model != first {
			return role
		}
	}
	return model.RoleJudge
}

// waivers returns the trace IDs an approved verification waiver excuses in this repo. A
// waiver whose requirement's section changed has ended, so it no longer applies.
func (a *API) waivers(ctx context.Context, b pgdb.Bundle, repo string, main []byte) (map[string]bool, error) {
	rows, err := a.DB.Queries().ListVerificationWaivers(ctx, pgdb.ListVerificationWaiversParams{
		BundleID: b.ID, Repo: repo})
	if err != nil {
		return nil, err
	}
	doc := section.Parse(main)
	out := map[string]bool{}
	for _, w := range rows {
		var path []string
		_ = json.Unmarshal(w.SectionPath, &path)
		if path == nil {
			path = []string{}
		}
		h, ok := section.HashAt(doc, main, path)
		if !ok || h != w.SectionHash {
			continue // the section changed, so the waiver ended
		}
		out[w.TraceID] = true
	}
	return out, nil
}

// requirements returns the trace ID definitions the gate verifies, and the number it skipped.
func requirements(main []byte, p profile.Profile) ([]requirement, int) {
	// Scan with every prefix the doc may use, so a trace ID the gate does not verify is
	// counted as skipped rather than passed over in silence.
	prefixes := append(append([]string(nil), p.Trace.Prefixes...), p.Verify.Prefixes...)
	var out []requirement
	skipped := 0
	seen := map[string]bool{}
	for _, d := range lint.Definitions(main, prefixes) {
		if seen[d.ID] {
			continue
		}
		seen[d.ID] = true
		if !verify.Verifiable(d.ID, p.Verify.Prefixes) {
			skipped++
			continue
		}
		r := requirement{ID: d.ID, Text: strings.TrimSpace(d.Text)}
		r.Level = kernel.Should
		if strings.Contains(d.Text, "MUST") {
			r.Level = kernel.Must
		}
		r.EARS, r.Parsed = ears.Parse(d.Text)
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, skipped
}

// openRepo reads the code the run names: a GitHub repo at a SHA, or a folder on disk.
func (a *API) openRepo(ctx context.Context, in Input, limits profile.Verify) (Repo, error) {
	switch {
	case in.Path != "":
		return NewFolderRepo(in.Path, limits)
	case in.Repo == "" || in.SHA == "":
		return nil, kernel.Invalid("no_code_target", "Name a repo and a commit, or a folder.")
	case a.GitHub == nil:
		return nil, kernel.Invalid("no_github", "Speccy has no GitHub credential, so it cannot read %s.", in.Repo)
	}
	c, err := a.GitHub(ctx, in.APIURL)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, kernel.Invalid("no_github", "Speccy has no GitHub credential, so it cannot read %s.", in.Repo)
	}
	return NewGitHubRepo(ctx, c, in.Repo, in.SHA, limits)
}

// baseSHA is the commit the ranking compares against: the SHA of the previous verification
// run of this bundle and repo. There is no base for the first run, and the candidates then
// carry no ranking.
func (a *API) baseSHA(ctx context.Context, in Input, repo Repo) (string, error) {
	if repo.SHA() == "" {
		return "", nil
	}
	sha, err := a.DB.Queries().LatestVerificationSHA(ctx, pgdb.LatestVerificationSHAParams{
		BundleID: in.BundleID, Repo: repo.Name()})
	if err != nil {
		// The first run of this bundle against this repo has no base, and the candidates then
		// carry no ranking. The scan still covers the whole tree.
		return "", nil //nolint:nilerr // no earlier run is not a fault
	}
	if sha == repo.SHA() {
		return "", nil
	}
	return sha, nil
}

// targetsFor gives each trace ID its targets and its one provenance.
func (a *API) targetsFor(ctx context.Context, repo Repo, reqs []requirement, claims []Claim,
	changed map[string]bool, limits profile.Verify) (map[string][]verify.Target, map[string]verify.Provenance, error) {

	byID := map[string][]verify.Target{}
	prov := map[string]verify.Provenance{}
	claimed := map[string][]verify.Target{}
	for _, c := range claims {
		claimed[c.TraceID] = c.Targets
	}
	var ids []string
	for _, r := range reqs {
		if _, ok := claimed[r.ID]; !ok {
			ids = append(ids, r.ID)
		}
	}
	literal, err := a.literalTargets(ctx, repo, ids)
	if err != nil {
		return nil, nil, err
	}

	ranked := verify.Rank(repo.Files(), changed)
	if len(ranked) > limits.MaxMapperFiles {
		ranked = ranked[:limits.MaxMapperFiles]
	}
	for _, r := range reqs {
		// A builder claim replaces the derived set. Every target in it must hold.
		if ts, ok := claimed[r.ID]; ok {
			prov[r.ID] = verify.FromClaim
			for i := range ts {
				ts[i].Provenance = verify.FromClaim
				body, read, err := repo.Read(ctx, ts[i].Path)
				if err != nil {
					return nil, nil, err
				}
				ts[i] = verify.Check(ts[i], body, read)
			}
			if !allHold(ts) {
				// One target that does not hold fails the claim, so no target of it counts.
				for i := range ts {
					ts[i].Holds = false
					if ts[i].Fault == "" {
						ts[i].Fault = "Another target of this claim does not hold, so the claim fails."
					}
				}
			}
			byID[r.ID] = ts
			continue
		}
		if ts := literal[r.ID]; len(ts) > 0 {
			prov[r.ID] = verify.FromLiteral
			byID[r.ID] = ts
			continue
		}
		ts, err := a.mapTargets(ctx, repo, r, ranked)
		if err != nil {
			return nil, nil, err
		}
		prov[r.ID] = verify.FromMapper
		byID[r.ID] = ts
	}
	return byID, prov, nil
}

func allHold(ts []verify.Target) bool {
	for _, t := range ts {
		if !t.Holds {
			return false
		}
	}
	return len(ts) > 0
}

// mapTargets finds the files that hold a requirement with no literal mention, in two calls.
// The first picks files from the paths. Speccy reads them, and the second quotes a line from
// each. A proposal whose quote is not in the file stays as a target that does not hold, with
// its fault: a model adds a candidate, never a fact.
func (a *API) mapTargets(ctx context.Context, repo Repo, r requirement, files []string) ([]verify.Target, error) {
	res, err := a.Gateway.Call(ctx, model.Call{
		Role: model.RoleReviewer, PromptVersion: PromptPick, System: systemMapper,
		Prompt: pickPrompt(r, files), Schema: pickSchema, MaxTokens: 1000,
	})
	if err != nil {
		return nil, err
	}
	var picked struct {
		Files []struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(res.JSON, &picked); err != nil {
		return nil, err
	}

	scanned := map[string]bool{}
	for _, f := range files {
		scanned[f] = true
	}
	var bodies []citedCode
	for _, f := range picked.Files {
		if !scanned[f.Path] {
			continue // a path the scan did not list is not in the repo
		}
		body, ok, err := repo.Read(ctx, f.Path)
		if err != nil {
			return nil, err
		}
		if ok {
			bodies = append(bodies, citedCode{Path: f.Path, Kind: verify.Kind(f.Kind), Body: string(body)})
		}
	}
	if len(bodies) == 0 {
		return nil, nil
	}
	res, err = a.Gateway.Call(ctx, model.Call{
		Role: model.RoleReviewer, PromptVersion: PromptMap, System: systemMapper,
		Prompt: mapPrompt(r, bodies), Schema: mapSchema, MaxTokens: 2000,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Targets []struct {
			Kind  string `json:"kind"`
			Path  string `json:"path"`
			Quote string `json:"quote"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		return nil, err
	}
	var ts []verify.Target
	for _, t := range out.Targets {
		var body []byte
		ok := false
		for _, c := range bodies {
			if c.Path == t.Path {
				body, ok = []byte(c.Body), true
			}
		}
		got := verify.Check(verify.Target{Kind: verify.Kind(t.Kind), Path: t.Path, Quote: t.Quote,
			Provenance: verify.FromMapper}, body, ok)
		if !got.Holds && ok {
			// A model often joins lines or drops indentation. When one of its lines is one line
			// of the file, the anchor becomes that line, copied from the file.
			if line, found := snapLine(string(body), t.Quote); found {
				got = verify.Check(verify.Target{Kind: verify.Kind(t.Kind), Path: t.Path, Quote: line,
					Provenance: verify.FromMapper}, body, ok)
			}
		}
		// A proposal that does not hold stays, with its fault, so the run says why a requirement
		// has no code target. Only a target that holds counts toward an outcome.
		ts = append(ts, got)
	}
	return ts, nil
}

// snapLine returns the one line of body that a line of quote names, compared with the
// surrounding whitespace trimmed. It takes the first line of quote that matches exactly one line
// of body, so the anchor Speccy keeps is text from the file, never text from the model.
func snapLine(body, quote string) (string, bool) {
	lines := strings.Split(body, "\n")
	for _, q := range strings.Split(quote, "\n") {
		q = strings.TrimSpace(q)
		if len(q) < 8 {
			continue // a brace or a short keyword names no one place
		}
		match, n := "", 0
		for _, l := range lines {
			if strings.TrimSpace(l) == q {
				match, n = l, n+1
			}
		}
		if n == 1 {
			return match, true
		}
	}
	return "", false
}

// insert writes a queued run: the bundle version, and the repo and the commit it will read.
func (a *API) insert(ctx context.Context, q store.Querier, b pgdb.Bundle, run Run, handoff *uuid.UUID) error {
	var h uuid.NullUUID
	if handoff != nil {
		h = uuid.NullUUID{UUID: *handoff, Valid: true}
	}
	return q.InsertVerificationRun(ctx, pgdb.InsertVerificationRunParams{
		ID: run.ID, WorkspaceID: a.Workspace, BundleID: b.ID, VersionID: b.CurrentVersionID.UUID,
		HandoffID: h, Repo: run.Repo, Sha: run.SHA, Branch: run.Branch, Counts: dbtype.JSON("{}"), Notes: dbtype.JSON("[]"),
		StartedBy: kernel.ActorFrom(ctx).UserID, CreatedAt: run.At,
	})
}

// store writes the run's outcomes, and marks every earlier run of another version stale.
func (a *API) store(ctx context.Context, b pgdb.Bundle, run Run) error {
	return a.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		for _, o := range run.Results {
			targets, _ := json.Marshal(o.Targets)
			judgement, _ := json.Marshal(o.Judgement)
			if err := q.InsertVerificationOutcome(ctx, pgdb.InsertVerificationOutcomeParams{
				ID: kernel.NewID(), RunID: run.ID, TraceID: o.ID, Outcome: string(o.Outcome),
				Level: string(o.Level), Blocks: o.Blocks, Waived: o.Waived, Provenance: string(o.Provenance),
				Note: o.Note, Targets: targets, Judgement: judgement,
			}); err != nil {
				return err
			}
		}
		// A new bundle version makes every earlier run stale: the requirements moved.
		return q.StaleVerificationRuns(ctx, pgdb.StaleVerificationRunsParams{
			BundleID: b.ID, VersionID: b.CurrentVersionID.UUID})
	})
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
