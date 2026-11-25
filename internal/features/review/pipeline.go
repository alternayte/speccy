package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/queue"
	"github.com/alternayte/speccy/internal/store"
)

// RunTimeout bounds a full run (REQ-103).
const RunTimeout = 15 * time.Minute

// JobRescueAfter is how long a job may stay running before River gives it to a worker again:
// the process that ran it stopped with no chance to say so.
const JobRescueAfter = RunTimeout + 5*time.Minute

// JobKindRun is the job kind of a review run.
const JobKindRun = "review_run"

// ErrRunActive means the bundle already has a queued or running full run.
var ErrRunActive = kernel.Conflict("run_active", "A review of this bundle is already running. Wait for it to finish.")

// ModelStages are the stages after lint, in REQ-020 order. Each calls a model.
var ModelStages = []string{StageRubric, StageGrounding, StageDivergence, StageCoherence}

// Stages is a choice of model stages for a run (SDD §12.2 --stages). Nil means all of them.
// Lint and the verdict always run.
type Stages []string

func (st Stages) has(stage string) bool { return st == nil || slices.Contains(st, stage) }

// ParseStages reads a comma-separated list of stage names. "all" is every stage.
func ParseStages(list string) (Stages, error) {
	if list == "" || list == "all" {
		return nil, nil
	}
	out := Stages{}
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		switch {
		case name == StageLint || name == StageVerdict:
		case slices.Contains(ModelStages, name):
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
		default:
			return nil, kernel.Invalid("bad_stage", "There is no stage %q. The stages are lint, rubric, grounding, divergence, and coherence.", name)
		}
	}
	return out, nil
}

// roles returns the model roles that the chosen stages call.
func (st Stages) roles(p profile.Profile) []string {
	var roles []string
	for _, stage := range ModelStages {
		if st.has(stage) {
			roles = append(roles, model.RoleReviewer)
			break
		}
	}
	if st.has(StageDivergence) {
		roles = append(roles, divergenceRoles(p)...)
	}
	return roles
}

// StartRun queues a review of the bundle's current version with the chosen stages (REQ-020).
// It checks the setup first, so a missing profile or model fails at once, not in the worker.
func (s *Service) StartRun(ctx context.Context, b pgdb.SpecDoc, stages Stages) (pgdb.ReviewRun, error) {
	if !b.CurrentVersionID.Valid {
		return pgdb.ReviewRun{}, kernel.Invalid("no_version", "The bundle has no version to review.")
	}
	if why := share.ReadOnly(b); why != "" {
		return pgdb.ReviewRun{}, kernel.Invalid("read_only", "%s", why)
	}
	p, ok := s.Profiles()[b.ProfileKey]
	if !ok {
		return pgdb.ReviewRun{}, kernel.Invalid("no_profile", "%s", s.noProfile(b.ProfileKey))
	}
	// A link to an upstream doc on GitHub that no source holds: add the source first, so this
	// review reads the doc (#97).
	s.addUpstreamSources(ctx, b, p.Profile)
	for _, role := range stages.roles(p.Profile) {
		if _, err := s.Gateway.Assigned(ctx, role); err != nil {
			return pgdb.ReviewRun{}, err
		}
	}
	// One start at a time: two starts at the same instant must not both find no active run.
	s.startMu.Lock()
	defer s.startMu.Unlock()
	q := s.DB.Queries()
	if _, err := q.RunningRunFor(ctx, b.ID); err == nil {
		return pgdb.ReviewRun{}, ErrRunActive
	} else if !errors.Is(err, sql.ErrNoRows) {
		return pgdb.ReviewRun{}, err
	}
	now := time.Now().UTC()
	run := pgdb.ReviewRun{
		ID: kernel.NewID(), WorkspaceID: s.Workspace, SpecDocID: b.ID, VersionID: b.CurrentVersionID.UUID,
		ProfileKey: b.ProfileKey, ProfileVersion: p.Version, Kind: "full", Status: "queued", Stage: "queued", StartedAt: now,
	}
	if s.Decisions != nil {
		key, err := s.decisionsKey(ctx, b)
		if err != nil {
			return pgdb.ReviewRun{}, err
		}
		run.DecisionsHash = key
	}
	repo, err := s.repoConfig(ctx, b)
	if err != nil {
		return pgdb.ReviewRun{}, err
	}
	run.ConfigHash = configHash(repo, mainDocPath(b, nil))
	payload, _ := json.Marshal(runJob{RunID: run.ID.String(), Stages: stages})
	err = s.DB.InTx(ctx, func(tx store.Tx) error {
		tq := tx.Queries()
		if err := tq.InsertRun(ctx, pgdb.InsertRunParams{
			ID: run.ID, WorkspaceID: run.WorkspaceID, SpecDocID: run.SpecDocID, VersionID: run.VersionID, ProfileKey: run.ProfileKey,
			ProfileVersion: run.ProfileVersion, Kind: run.Kind, Status: run.Status, Stage: run.Stage,
			DecisionsHash: run.DecisionsHash, ConfigHash: run.ConfigHash, StartedAt: now,
		}); err != nil {
			return err
		}
		_, err := s.Queue.Insert(ctx, &tx, JobKindRun, payload, nil)
		return err
	})
	if err != nil {
		return pgdb.ReviewRun{}, err
	}
	s.Progress.Publish(run.ID, Event{Type: "stage", Stage: "queued"})
	return run, nil
}

// RegisterJobs registers the review and answer jobs on s.Queue.
func (s *Service) RegisterJobs() {
	s.Queue.Register(JobKindRun, queue.Interactive, 3, s.HandleRun, s.EndOrphanRun)
	s.Queue.Register(JobKindAnswer, queue.Interactive, 1, s.HandleAnswer, nil)
	s.Queue.Register(JobKindPruneCache, queue.Interactive, 1, s.PruneCache, nil)
	s.Queue.Every(JobKindPruneCache, 24*time.Hour)
}

// HandleRun is the handler of a review job. A run that cannot finish ends as failed, with
// its cause, so the doc can be reviewed again. A job whose context ends, because the process
// stops or the job was cancelled, leaves its run for River to try again or for EndOrphan.
func (s *Service) HandleRun(ctx context.Context, payload []byte) error {
	var p runJob
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil //nolint:nilerr // a payload that does not parse names no run
	}
	err := s.execute(ctx, p.RunID, p.Stages)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return s.EndFailedRun(ctx, payload, err)
}

// EndFailedRun ends the run of a review job that returned err before the run reached an end,
// for example a store error at its first read. A run left queued or running would refuse
// every later review of the doc.
func (s *Service) EndFailedRun(ctx context.Context, payload []byte, err error) error {
	var p runJob
	if json.Unmarshal(payload, &p) != nil || p.RunID == "" {
		return nil //nolint:nilerr // a payload that names no run leaves no run to end
	}
	cause := "The review stopped before it ended: " + strings.TrimSuffix(err.Error(), ".")
	if err := s.DB.Queries().FailActiveRun(context.WithoutCancel(ctx), pgdb.FailActiveRunParams{
		ID: uuidOf(p.RunID), Error: cause, FinishedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
	}); err != nil {
		return err
	}
	s.Progress.Publish(uuidOf(p.RunID), Event{Type: "failed", Message: cause})
	return nil
}

// EndOrphanRun ends the run of a review job that no process runs any more.
func (s *Service) EndOrphanRun(ctx context.Context, payload []byte) error {
	var p runJob
	if json.Unmarshal(payload, &p) != nil || p.RunID == "" {
		return nil //nolint:nilerr // a payload that names no run leaves no run to end
	}
	return s.DB.Queries().FailActiveRun(ctx, pgdb.FailActiveRunParams{ID: uuidOf(p.RunID), Error: StoppedCause,
		FinishedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true}})
}

// runJob is the payload of a review job: the run and its stages, or a thread to answer.
type runJob struct {
	RunID    string `json:"run_id,omitempty"`
	Stages   Stages `json:"stages,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
}

// execute runs one review (SDD §7.3): the chosen stages in REQ-020 order, then the verdict.
func (s *Service) execute(parent context.Context, runIDText string, stages Stages) error {
	q := s.DB.Queries()
	run, err := q.GetRunByID(parent, uuidOf(runIDText))
	if err != nil {
		return err
	}
	b, err := q.GetSpecDoc(parent, pgdb.GetSpecDocParams{WorkspaceID: s.Workspace, ID: run.SpecDocID})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, RunTimeout)
	defer cancel()
	rc := &runCtx{
		id: run.ID, progress: s.Progress, roles: map[string]string{}, prompts: map[string]string{},
		prices: map[string][2]float64{}, sem: make(chan struct{}, s.parallel(parent)),
	}
	stage := StageLint
	fail := func(cause error) error {
		// The process stops or the job was cancelled: the run stays for River to try again,
		// or for EndOrphanRun.
		if parent.Err() != nil {
			return parent.Err()
		}
		msg := cause.Error()
		if ke, ok := kernel.AsError(cause); ok {
			msg = ke.Detail
		}
		if errors.Is(cause, context.DeadlineExceeded) && parent.Err() == nil {
			msg = fmt.Sprintf("the run passed its limit of %s", RunTimeout)
		}
		// REQ-024: a failed run gives no verdict, and names the stage and the cause.
		run.Status, run.Stage = "failed", stage
		run.Error = fmt.Sprintf("The %s stage failed: %s", stage, strings.TrimSuffix(msg, "."))
		s.fillRun(&run, rc)
		if err := finishRun(context.WithoutCancel(parent), q, run, time.Now().UTC()); err != nil {
			return err
		}
		rc.publish(Event{Type: "failed", Stage: stage, Message: run.Error})
		return nil
	}

	p, ok := s.Profiles()[run.ProfileKey]
	if !ok {
		return fail(errors.New(s.noProfile(run.ProfileKey)))
	}
	run.ProfileVersion = p.Version
	if err := q.StartRunExecution(ctx, pgdb.StartRunExecutionParams{ID: run.ID, Stage: stage, ProfileVersion: p.Version}); err != nil {
		return err
	}
	var assigned model.Assignment
	if len(stages.roles(p.Profile)) > 0 {
		if assigned, err = s.Gateway.Assigned(ctx, model.RoleReviewer); err != nil {
			return fail(err)
		}
	}
	if stages != nil {
		names := append([]string{StageLint}, stages...)
		rc.note("Stages in this run: " + strings.Join(names, ", ") + ". The findings of the other stages stay from the last review that ran them.")
	}
	if roles, err := q.ListAssignments(ctx, s.Workspace); err == nil {
		for _, r := range roles {
			rc.prices[r.Role] = [2]float64{r.PriceInPerMtok, r.PriceOutPerMtok}
		}
	}
	fingerprint := assigned.Backend.Kind + ":" + assigned.Model
	var preset struct {
		Preset string `json:"preset"`
	}
	_ = json.Unmarshal(assigned.Backend.Config, &preset)
	native := model.SearchCapable(assigned.Backend.Kind, preset.Preset, assigned.Model)

	// A link rule target may come from an open pull request; this run reads it again (#142).
	in, err := s.load(withPulls(ctx), b, run.VersionID, p)
	if err != nil {
		return fail(err)
	}
	if in.sizeNote != "" {
		rc.note(in.sizeNote)
	}
	for _, n := range in.linkNotes {
		rc.note(n)
	}
	if stages.has(StageRubric) {
		for _, note := range sectionNotes(p, in) {
			rc.note(note)
		}
	}

	ev, err := s.runStages(ctx, rc, in, stages, fingerprint, native, func(st string) {
		stage = st
		if st != StageLint {
			s.setStage(ctx, run, st)
		}
	})
	if err != nil {
		return fail(err)
	}

	// A run with a stage subset judges only its stages. The findings of the stages it skips
	// stay in the verdict, from the last run that did them, so the run never drops a finding
	// by not looking.
	if stages != nil {
		if err := s.carry(ctx, b, in, &ev, stages.has); err != nil {
			return fail(err)
		}
	}

	stage = StageVerdict
	rc.enter(stage)
	run.Status, run.Stage = "complete", StageVerdict
	s.fillRun(&run, rc)
	if err := s.save(ctx, run, in, ev, p.Profile, true); err != nil {
		return fail(err)
	}
	// The author saved an edit while the review ran: the review read an old version. Lint the
	// current version again now, so that it carries this review's findings at once and not
	// after the next save.
	if cur, err := q.GetSpecDoc(ctx, pgdb.GetSpecDocParams{WorkspaceID: s.Workspace, ID: run.SpecDocID}); err == nil &&
		cur.CurrentVersionID.Valid && cur.CurrentVersionID.UUID != run.VersionID {
		s.mu.Lock()
		_, err := s.Lint(ctx, cur, cur.CurrentVersionID.UUID)
		s.mu.Unlock()
		if err != nil {
			slog.Error("lint the current version after a review of an older one", "doc", cur.Slug, "err", err)
		}
	}
	if s.AfterReview != nil {
		if err := s.AfterReview(ctx, b); err != nil {
			slog.Error("after the review", "doc", b.Slug, "err", err)
		}
	}
	rc.publish(Event{Type: "done", Stage: stage})
	return nil
}

func (s *Service) setStage(ctx context.Context, run pgdb.ReviewRun, stage string) {
	_ = s.DB.Queries().UpdateRunProgress(ctx, pgdb.UpdateRunProgressParams{ID: run.ID, Status: "running", Stage: stage})
}

// fillRun copies the run report counters (REQ-022) into the row.
func (s *Service) fillRun(run *pgdb.ReviewRun, rc *runCtx) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	run.TokensIn, run.TokensOut, run.CacheHits, run.CostEstimate = rc.tokensIn, rc.tokensOut, int64(rc.cacheHits), rc.cost
	run.Roles, _ = json.Marshal(rc.roles)
	run.PromptVersions, _ = json.Marshal(rc.prompts)
	notes := slices.Clone(rc.notes)
	if notes == nil {
		notes = []string{}
	}
	if n := temperatureNote(rc.temps); n != "" {
		notes = append(notes, n)
	}
	run.Notes, _ = json.Marshal(notes)
	now := time.Now().UTC()
	stages := slices.Clone(rc.stages)
	if n := len(stages); n > 0 && stages[n-1].FinishedAt == nil {
		stages[n-1].FinishedAt = &now
	}
	if stages == nil {
		stages = []stageTiming{}
	}
	run.Stages, _ = json.Marshal(stages)
}

// temperatureNote names the temperature of the model calls of a run, for the run report: 0 for
// the roles whose model accepts it, and none for the rest. It is "" for a run that called no
// model.
func temperatureNote(temps map[string]bool) string {
	var zero, none []string
	for _, role := range model.Roles {
		if sent, called := temps[role]; called && sent {
			zero = append(zero, role)
		} else if called {
			none = append(none, role)
		}
	}
	var parts []string
	if len(zero) > 0 {
		parts = append(parts, "Temperature 0: "+strings.Join(zero, ", ")+".")
	}
	if len(none) > 0 {
		parts = append(parts, "No temperature, because the model takes none: "+strings.Join(none, ", ")+".")
	}
	return strings.Join(parts, " ")
}

func (s *Service) parallel(ctx context.Context) int {
	if s.Parallel != nil {
		if n := s.Parallel(ctx); n > 0 {
			return n
		}
	}
	return 8
}

// Estimate is the expected cost of a full run (REQ-104). It counts the steps the cache
// cannot answer.
type Estimate struct {
	Calls      int
	CachedHits int
	TokensIn   int64
	TokensOut  int64
	CostUSD    float64
	Priced     bool
}

// EstimateRun estimates a full run of b's current version.
func (s *Service) EstimateRun(ctx context.Context, b pgdb.SpecDoc) (Estimate, error) {
	p, ok := s.Profiles()[b.ProfileKey]
	if !ok {
		return Estimate{}, kernel.Invalid("no_profile", "%s", s.noProfile(b.ProfileKey))
	}
	if err := s.estimateRoles(ctx, p, nil); err != nil {
		return Estimate{}, err
	}
	in, err := s.load(ctx, b, b.CurrentVersionID.UUID, p)
	if err != nil {
		return Estimate{}, err
	}
	return s.estimate(ctx, in, p, b.ID, nil)
}

// estimateRoles checks that each role the chosen stages call has a model.
func (s *Service) estimateRoles(ctx context.Context, p profile.Versioned, stages Stages) error {
	if _, err := s.Gateway.Assigned(ctx, model.RoleReviewer); err != nil {
		return err
	}
	if !stages.has(StageDivergence) {
		return nil
	}
	for _, role := range divergenceRoles(p.Profile) {
		if _, err := s.Gateway.Assigned(ctx, role); err != nil {
			return err
		}
	}
	return nil
}

// estimate counts the model calls of the chosen stages on in that the cache cannot answer.
// docID is the saved doc, whose pinned build questions need no call, or uuid.Nil.
func (s *Service) estimate(ctx context.Context, in input, p profile.Versioned, docID uuid.UUID, stages Stages) (Estimate, error) {
	var est Estimate
	assigned, err := s.Gateway.Assigned(ctx, model.RoleReviewer)
	if err != nil {
		return est, err
	}
	fp := assigned.Backend.Kind + ":" + assigned.Model
	bundleTokens := int64(len(bundleData(in.bundle.DocPath, in.main, textAssets(in)))) / 4
	var scratch struct{}
	// Rubric: a call per batch of uncached doc checks, and for each section with text, a call
	// per batch of its uncached section checks.
	rubricUnit := func(inputHash string, checks []rubricCheck, extraTokens int64) {
		uncached := 0
		for _, c := range checks {
			k := cacheKey{Step: "rubric:" + c.Slug, InputHash: inputHash, ProfileVer: p.Version, Fingerprint: fp, PromptVersion: PromptRubric}
			if hit, _ := s.cached(ctx, k, &scratch); hit {
				est.CachedHits++
			} else {
				uncached++
			}
		}
		calls := (uncached + rubricBatch - 1) / rubricBatch
		est.Calls += calls
		est.TokensIn += int64(calls) * (bundleTokens + extraTokens + 1500)
		est.TokensOut += int64(calls) * 1500
	}
	var units []scopeUnit
	if stages.has(StageRubric) {
		if units, err = s.unitsWithPrior(ctx, in); err != nil {
			return est, err
		}
	}
	// A unit of checks with reads: [upstream] holds the upstream docs too.
	upTokens := int64(0)
	for _, l := range upstreamDocs(in) {
		upTokens += int64(len(l.main)) / 4
	}
	for _, u := range units {
		extra := int64(0)
		if u.upstream {
			extra = upTokens
		}
		switch {
		case u.named:
			// The call holds the section, its Pointed-to sections and the assets, not the bundle.
			assetTokens := int64(0)
			for _, f := range textAssets(in) {
				assetTokens += int64(len(f.text)) / 4
			}
			for _, p := range u.pointed {
				assetTokens += int64(p.End-p.Start) / 4
			}
			rubricUnit(u.inputHash, u.checks, extra+int64(u.sec.End-u.sec.Start)/4+assetTokens-bundleTokens)
		case u.sec != nil:
			rubricUnit(u.inputHash, u.checks, extra+int64(len(u.sec.Own(in.main)))/4)
		default:
			rubricUnit(u.inputHash, u.checks, extra)
		}
	}
	// Grounding: a claims call per uncached section, and about one label call per two sections.
	sections := 0
	for i := range in.doc.Sections {
		sec := in.doc.Sections[i]
		if !stages.has(StageGrounding) || !stageHasWork(in, StageGrounding) || len(strings.Fields(section.Normalize(sec.Own(in.main)))) < minClaimWords {
			continue
		}
		k := cacheKey{Step: "claims", InputHash: sec.Hash, ProfileVer: p.Version, Fingerprint: fp, PromptVersion: PromptClaims}
		if hit, _ := s.cached(ctx, k, &scratch); hit {
			est.CachedHits++
			continue
		}
		sections++
		est.Calls++
		est.TokensIn += int64(len(sec.Own(in.main)))/4 + 800
		est.TokensOut += 300
	}
	labelCalls := (sections + 1) / 2
	tokens := map[string][2]int64{model.RoleReviewer: {est.TokensIn, est.TokensOut}}
	add := func(role string, calls int, in, out int64) {
		est.Calls += calls
		est.TokensIn += in
		est.TokensOut += out
		tokens[role] = [2]int64{tokens[role][0] + in, tokens[role][1] + out}
	}
	add(model.RoleReviewer, labelCalls, int64(labelCalls)*4000, int64(labelCalls)*1500)
	// One call reads the claims against the files of the bundle and the linked docs.
	if sources, _ := claimSources(in); len(sources) > 0 && sections > 0 {
		var chars int64
		for _, src := range sources {
			chars += int64(len(src.text))
		}
		add(model.RoleReviewer, 1, chars/4+1500, 1500)
	}

	// Divergence: the questions (unless pinned), one call per reader per batch of questions,
	// and about one judge call per question. The reader and judge caches are not counted.
	if stages.has(StageDivergence) {
		pinned, err := s.DB.Queries().ListLiveQuestions(ctx, docID)
		if err != nil {
			return est, err
		}
		nq := len(pinned)
		if nq == 0 {
			nq = questionCap(in, p.Profile.Divergence.Questions.Max)
			add(model.RoleReviewer, 1, bundleTokens+1500, 3000)
		} else {
			est.CachedHits++
		}
		readers := readerRoles(p.Profile.Divergence.Readers)
		batches := (nq + readerBatch - 1) / readerBatch
		for _, r := range readers {
			add(r, batches, int64(batches)*(bundleTokens+1000), int64(nq)*150)
		}
		if len(readers) > 1 {
			add(model.RoleJudge, nq, int64(nq)*800, int64(nq)*150)
		}
	}

	// Coherence: one call per linked doc that the contradiction check reads.
	if stages.has(StageCoherence) && stageHasWork(in, StageCoherence) && !standalone(in.dec) {
		this := contradictionData(in)
		for _, l := range in.linked {
			if !slices.Contains(contradictionKinds, l.kind) {
				continue
			}
			k := contradictionKey(this, l, p.Version, fp)
			if hit, _ := s.cached(ctx, k, &scratch); hit {
				est.CachedHits++
				continue
			}
			add(model.RoleReviewer, 1, bundleTokens+int64(len(l.main))/4+800, 800)
		}
	}

	roles, err := s.DB.Queries().ListAssignments(ctx, s.Workspace)
	if err != nil {
		return est, err
	}
	for _, r := range roles {
		t, used := tokens[r.Role]
		if used && (r.PriceInPerMtok > 0 || r.PriceOutPerMtok > 0) {
			est.Priced = true
			est.CostUSD += float64(t[0])/1e6*r.PriceInPerMtok + float64(t[1])/1e6*r.PriceOutPerMtok
		}
	}
	return est, nil
}

func uuidOf(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// runStages runs lint, then the chosen model stages at the same time (material gate). Each stage
// writes its own evaluation, and one merge joins them in REQ-020 order, so the findings keep
// their order. The coherence steps share the state of the external links, so they run in turn
// in one group. The materiality judge reads the MUST findings of each stage as soon as the stage
// ends, and the questions it finds stream to live views. enter is told of each stage as it
// starts.
func (s *Service) runStages(ctx context.Context, rc *runCtx, in input, stages Stages, fingerprint string, native bool, enter func(string)) (evaluation, error) {
	enter(StageLint)
	rc.enter(StageLint)
	ev := lintStage(in)
	for _, n := range ev.notes {
		rc.note(n)
	}
	type step func(*evaluation) error
	groups := []struct {
		stage string
		steps []step
	}{
		{StageRubric, []step{func(e *evaluation) error { return s.rubricStage(ctx, rc, in, e, fingerprint) }}},
		{StageGrounding, []step{func(e *evaluation) error { return s.groundingStage(ctx, rc, in, e, fingerprint, native) }}},
		{StageDivergence, []step{func(e *evaluation) error { return s.divergenceStage(ctx, rc, in, e, fingerprint) }}},
		{StageCoherence, []step{
			func(e *evaluation) error { return s.contradictionStage(ctx, rc, in, e, fingerprint) },
			func(e *evaluation) error { return s.driftStage(ctx, rc, in, e) },
			func(e *evaluation) error { return s.conflictStage(ctx, rc, in, e, fingerprint) },
		}},
	}
	// The lint stage's findings get their judgement first: a placeholder's build question is
	// on the questions screen before the readers end.
	if err := s.judgeMaterial(ctx, rc, in, &ev, true); err != nil {
		return ev, err
	}
	rc.endAll() // the lint stage; the model stages time themselves below
	parts := make([]evaluation, len(groups))
	errs := make([]error, len(groups))
	var enterMu sync.Mutex
	var wg sync.WaitGroup
	for gi, g := range groups {
		if !stages.has(g.stage) || !stageHasWork(in, g.stage) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			enterMu.Lock()
			enter(g.stage)
			enterMu.Unlock()
			rc.begin(g.stage)
			defer rc.end(g.stage)
			part := evaluation{relaxed: ev.relaxed}
			for _, st := range g.steps {
				if err := st(&part); err != nil {
					errs[gi] = err
					return
				}
			}
			errs[gi] = s.judgeMaterial(ctx, rc, in, &part, true)
			parts[gi] = part
		}()
	}
	wg.Wait()
	for gi, err := range errs {
		if err != nil {
			// The run names the stage that failed: the first one in REQ-020 order.
			enter(groups[gi].stage)
			return ev, err
		}
	}
	for _, part := range parts {
		ev.findings = append(ev.findings, part.findings...)
		ev.items = append(ev.items, part.items...)
		ev.claims = append(ev.claims, part.claims...)
		if part.questions != nil {
			ev.questions = part.questions
		}
		for _, st := range part.external {
			ev.setExternal(st)
		}
		ev.notes = append(ev.notes, part.notes...)
	}
	return ev, nil
}

// stageHasWork reports whether a model stage has something to read in this doc (material gate).
// Grounding runs only when the profile turns it on, and coherence only when the doc has links
// or a linked doc asks it to change. The rubric and the readers always run. A stage with no
// work costs no call, and its findings of an earlier review do not carry.
func stageHasWork(in input, stage string) bool {
	switch stage {
	case StageGrounding:
		return in.profile.Profile.Grounding.Enabled
	case StageCoherence:
		return len(in.links) > 0 || len(in.incoming) > 0
	}
	return true
}

// addUpstreamSources adds a GitHub source for each link of an upstream kind whose target is a
// doc on a GitHub branch that no source holds. A source that does not add leaves the finding
// of links.has-upstream, which says what to do.
func (s *Service) addUpstreamSources(ctx context.Context, b pgdb.SpecDoc, p profile.Profile) {
	if s.AddSource == nil || p.Links.Upstream == nil || !b.CurrentVersionID.Valid {
		return
	}
	files, err := version.Files(ctx, s.DB.Queries(), b.CurrentVersionID.UUID)
	if err != nil {
		return
	}
	var main []byte
	for _, f := range files {
		if f.Path == b.DocPath {
			main = f.Content
		}
	}
	links, _, err := s.resolveLinks(ctx, b, main, nil)
	if err != nil {
		return
	}
	for _, l := range links {
		if l.target != nil || l.external == nil || !slices.Contains(p.Links.Upstream.Kinds, l.kind) {
			continue
		}
		if _, _, _, pinned, ok := l.external.GitHubDoc(); !ok || pinned {
			continue
		}
		if err := s.AddSource(ctx, l.external.URL); err != nil {
			slog.Warn("add the upstream doc as a GitHub source", "url", l.external.URL, "err", err)
		}
	}
}

// sectionNotes are the lines a run adds about the sections of the rubric checks that apply to
// the doc. A check that is about one section of this doc and names none runs again on the
// whole doc after each edit, so the review does not converge, and the author would not know
// why (#108). A check that names a section this doc does not have reads the whole doc (#138).
// A check with "section: none" reads the whole doc on purpose, and gets no note.
func sectionNotes(p profile.Versioned, in input) []string {
	applies := func(slug string) bool {
		c, ok := p.Profile.Check(slug)
		return ok && c.AppliesAt(in.size)
	}
	docs := []section.Doc{in.doc}
	var out []string
	var hinted []string
	for _, h := range profile.SectionHints(p.Loaded, docs) {
		if h.Found > 0 && applies(h.Slug) {
			hinted = append(hinted, h.Slug)
		}
	}
	if len(hinted) > 0 {
		out = append(out, fmt.Sprintf("%d check%s of the %s profile name no section, so each one runs again on the whole doc after each edit: %s. Run \"speccy profile validate\" on the profile file: it names the section to add to each.",
			len(hinted), plural(len(hinted)), p.Profile.Key, quoteList(hinted)))
	}
	var missing []string
	for _, m := range profile.MissingSections(p.Profile, docs) {
		if applies(m.Slug) {
			missing = append(missing, fmt.Sprintf("%s (%q)", m.Slug, m.Section))
		}
	}
	if len(missing) > 0 {
		out = append(out, fmt.Sprintf("%d check%s of the %s profile name a section that this doc does not have, so each one reads the whole doc: %s.",
			len(missing), plural(len(missing)), p.Profile.Key, strings.Join(missing, ", ")))
	}
	return out
}
