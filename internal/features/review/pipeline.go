package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/store"
)

// RunTimeout bounds a full run (REQ-103).
const RunTimeout = 15 * time.Minute

// jobLock is how long a claimed job stays locked; a worker that dies frees it after this.
const jobLock = RunTimeout + 5*time.Minute

const jobKindRun = "review_run"

// ErrRunActive means the bundle already has a queued or running full run.
var ErrRunActive = kernel.Conflict("run_active", "A review of this bundle is already running. Wait for it to finish.")

// StartRun queues a full review of the bundle's current version (REQ-020). It checks the
// setup first, so a missing profile or model fails at once, not in the worker.
func (s *Service) StartRun(ctx context.Context, b pgdb.Bundle) (pgdb.ReviewRun, error) {
	if !b.CurrentVersionID.Valid {
		return pgdb.ReviewRun{}, kernel.Invalid("no_version", "The bundle has no version to review.")
	}
	p, ok := s.Profiles()[b.ProfileKey]
	if !ok {
		return pgdb.ReviewRun{}, kernel.Invalid("no_profile", "%s", s.noProfile(b.ProfileKey))
	}
	for _, role := range append([]string{model.RoleReviewer}, divergenceRoles(p.Profile)...) {
		if _, err := s.Gateway.Assigned(ctx, role); err != nil {
			return pgdb.ReviewRun{}, err
		}
	}
	q := s.DB.Queries()
	if _, err := q.RunningRunFor(ctx, b.ID); err == nil {
		return pgdb.ReviewRun{}, ErrRunActive
	} else if !errors.Is(err, sql.ErrNoRows) {
		return pgdb.ReviewRun{}, err
	}
	now := time.Now().UTC()
	run := pgdb.ReviewRun{
		ID: kernel.NewID(), WorkspaceID: s.Workspace, BundleID: b.ID, VersionID: b.CurrentVersionID.UUID,
		ProfileKey: b.ProfileKey, ProfileVersion: p.Version, Kind: "full", Status: "queued", Stage: "queued", StartedAt: now,
	}
	payload, _ := json.Marshal(map[string]string{"run_id": run.ID.String()})
	err := s.DB.InTx(ctx, func(tx store.Tx) error {
		tq := tx.Queries()
		if err := tq.InsertRun(ctx, pgdb.InsertRunParams{
			ID: run.ID, WorkspaceID: run.WorkspaceID, BundleID: run.BundleID, VersionID: run.VersionID, ProfileKey: run.ProfileKey,
			ProfileVersion: run.ProfileVersion, Kind: run.Kind, Status: run.Status, Stage: run.Stage, StartedAt: now,
		}); err != nil {
			return err
		}
		return tq.InsertJob(ctx, pgdb.InsertJobParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Kind: jobKindRun, Payload: dbtype.JSON(payload), CreatedAt: now})
	})
	if err != nil {
		return pgdb.ReviewRun{}, err
	}
	s.Progress.Publish(run.ID, Event{Type: "stage", Stage: "queued"})
	s.notify()
	return run, nil
}

func (s *Service) wakeChan() chan struct{} {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	return s.wake
}

func (s *Service) notify() {
	select {
	case s.wakeChan() <- struct{}{}:
	default:
	}
}

// Work runs queued jobs until ctx ends. SDD §7.2: one worker; Postgres claims with
// SKIP LOCKED, so a second process would not take the same job.
func (s *Service) Work(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		ran, err := s.RunNext(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("review job failed", "err", err)
		}
		if ran {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wakeChan():
		case <-time.After(2 * time.Second):
		}
	}
}

// RunNext claims and runs one job. ran is false when the queue is empty.
func (s *Service) RunNext(ctx context.Context) (ran bool, err error) {
	now := time.Now().UTC()
	job, err := s.DB.Queries().ClaimJob(ctx, pgdb.ClaimJobParams{LockUntil: sql.NullTime{Time: now.Add(jobLock), Valid: true}, Now: sql.NullTime{Time: now, Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var p struct {
		RunID string `json:"run_id"`
	}
	runErr := json.Unmarshal(job.Payload, &p)
	if runErr == nil {
		runErr = s.execute(ctx, p.RunID)
	}
	status, msg := "done", ""
	if runErr != nil {
		status, msg = "failed", runErr.Error()
	}
	// A job's own failure is stored on its run; the job only records that it ended.
	if err := s.DB.Queries().FinishJob(context.WithoutCancel(ctx), pgdb.FinishJobParams{ID: job.ID, Status: status, LastError: msg}); err != nil {
		return true, err
	}
	return true, runErr
}

// execute runs one full review (SDD §7.3): the stages in REQ-020 order, then the verdict.
func (s *Service) execute(parent context.Context, runIDText string) error {
	q := s.DB.Queries()
	run, err := q.GetRunByID(parent, uuidOf(runIDText))
	if err != nil {
		return err
	}
	b, err := q.GetBundle(parent, pgdb.GetBundleParams{WorkspaceID: s.Workspace, ID: run.BundleID})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, RunTimeout)
	defer cancel()
	rc := &runCtx{
		id: run.ID, progress: s.Progress, roles: map[string]string{}, prompts: map[string]string{},
		prices: map[string][2]float64{}, sem: make(chan struct{}, s.parallel()),
	}
	stage := StageLint
	fail := func(cause error) error {
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
	assigned, err := s.Gateway.Assigned(ctx, model.RoleReviewer)
	if err != nil {
		return fail(err)
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

	in, err := s.load(ctx, b, run.VersionID, p)
	if err != nil {
		return fail(err)
	}

	rc.publish(Event{Type: "stage", Stage: StageLint})
	ev := lintStage(in)

	stage = StageRubric
	s.setStage(ctx, run, stage)
	rc.publish(Event{Type: "stage", Stage: stage})
	if err := s.rubricStage(ctx, rc, in, &ev, fingerprint); err != nil {
		return fail(err)
	}

	stage = StageGrounding
	s.setStage(ctx, run, stage)
	rc.publish(Event{Type: "stage", Stage: stage})
	if err := s.groundingStage(ctx, rc, in, &ev, fingerprint, native); err != nil {
		return fail(err)
	}

	stage = StageDivergence
	s.setStage(ctx, run, stage)
	rc.publish(Event{Type: "stage", Stage: stage})
	if err := s.divergenceStage(ctx, rc, in, &ev, fingerprint); err != nil {
		return fail(err)
	}

	stage = StageVerdict
	rc.publish(Event{Type: "stage", Stage: stage})
	run.Status, run.Stage = "complete", StageVerdict
	s.fillRun(&run, rc)
	if err := s.save(ctx, run, ev, p.Profile, true); err != nil {
		return fail(err)
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
	notes := rc.notes
	if notes == nil {
		notes = []string{}
	}
	run.Notes, _ = json.Marshal(notes)
}

func (s *Service) parallel() int {
	if s.Parallel > 0 {
		return s.Parallel
	}
	return 4
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
func (s *Service) EstimateRun(ctx context.Context, b pgdb.Bundle) (Estimate, error) {
	var est Estimate
	p, ok := s.Profiles()[b.ProfileKey]
	if !ok {
		return est, kernel.Invalid("no_profile", "%s", s.noProfile(b.ProfileKey))
	}
	assigned, err := s.Gateway.Assigned(ctx, model.RoleReviewer)
	if err != nil {
		return est, err
	}
	for _, role := range divergenceRoles(p.Profile) {
		if _, err := s.Gateway.Assigned(ctx, role); err != nil {
			return est, err
		}
	}
	in, err := s.load(ctx, b, b.CurrentVersionID.UUID, p)
	if err != nil {
		return est, err
	}
	fp := assigned.Backend.Kind + ":" + assigned.Model
	bundleTokens := int64(len(bundleData(in.bundle.MainDoc, in.main, textAssets(in)))) / 4
	var scratch struct{}
	// Rubric: a call per batch of uncached doc checks.
	docUncached := 0
	for _, c := range p.Profile.Checks {
		if c.Stage != StageRubric || c.Scope == "section" {
			continue
		}
		k := cacheKey{Step: "rubric:" + c.Slug, InputHash: bundleHash(in), ProfileVer: p.Version, Fingerprint: fp, PromptVersion: PromptRubric}
		if hit, _ := s.cached(ctx, k, &scratch); hit {
			est.CachedHits++
		} else {
			docUncached++
		}
	}
	rubricCalls := (docUncached + rubricBatch - 1) / rubricBatch
	est.Calls += rubricCalls
	est.TokensIn += int64(rubricCalls) * (bundleTokens + 1500)
	est.TokensOut += int64(rubricCalls) * 1500
	// Grounding: a claims call per uncached section, and about one label call per two sections.
	sections := 0
	for i := range in.doc.Sections {
		sec := in.doc.Sections[i]
		if len(strings.Fields(section.Normalize(sec.Own(in.main)))) < minClaimWords {
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

	// Divergence: the questions (unless pinned), one call per reader per batch of questions,
	// and about one judge call per question. The reader and judge caches are not counted.
	pinned, err := s.DB.Queries().ListQuestions(ctx, b.CurrentVersionID.UUID)
	if err != nil {
		return est, err
	}
	nq := len(pinned)
	if nq == 0 {
		nq = p.Profile.Divergence.Questions.Max
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
