// Package review runs reviews and stores runs, findings, claims, and verdicts (SDD §8). Lint
// runs on every new version (DEC-027); a full run adds the model stages on request.
package review

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// Stage names (REQ-020).
const (
	StageLint       = "lint"
	StageRubric     = "rubric"
	StageGrounding  = "grounding"
	StageDivergence = "divergence"
	StageCoherence  = "coherence"
	StageVerdict    = "verdict"
)

// Searcher is a web search source other than the model's own: an MCP connection marked
// search (REQ-034).
type Searcher interface {
	Name() string
	Search(ctx context.Context, query string) (string, error)
}

// Service runs reviews for one workspace.
type Service struct {
	DB        *store.DB
	Workspace uuid.UUID
	// Profiles returns the current profiles by key. Repo returns .speccy.yaml.
	Profiles func() map[string]profile.Versioned
	Repo     func() source.RepoConfig
	// Gateway calls models. Search returns the MCP search source, or nil when none is set.
	Gateway *model.Gateway
	Search  func(ctx context.Context) (Searcher, error)
	// Progress receives stage events for live views (REQ-026).
	Progress *Broker
	// Parallel bounds the model calls of one run (REQ-105). Zero means 4.
	Parallel int

	mu     sync.Mutex // one lint pass at a time
	wakeMu sync.Mutex
	wake   chan struct{}
}

// categories maps each lint rule to its radar axis (SDD §8.7).
var categories = map[string]verdict.Category{
	lint.Placeholder: verdict.Structure, lint.RequiredHeadings: verdict.Structure, lint.BrokenLink: verdict.Structure,
	lint.DuplicateID: verdict.Structure, lint.DanglingRef: verdict.Structure, lint.ProseLimit: verdict.Structure,
	lint.AssetNudge: verdict.Structure,
	lint.SlopPhrase: verdict.Clarity, lint.SentenceLength: verdict.Clarity, lint.Weasel: verdict.Clarity,
	lint.UndefinedAcronym: verdict.Clarity, lint.RFC2119Case: verdict.Clarity, lint.PassiveVoice: verdict.Clarity,
}

// HasUpstreamSlug is the check for a required upstream link (REQ-057). M3 reads the
// frontmatter; M7 resolves the link target.
const HasUpstreamSlug = "links.has-upstream"

// pending is a finding before it is stored.
type pending struct {
	slug     string
	level    kernel.Level
	stage    string
	anchor   anchor.Anchor
	message  string
	fix      string
	evidence any
}

// pendingClaim is a labelled claim before it is stored (REQ-031).
type pendingClaim struct {
	text    string
	label   string
	reason  string
	sources []string
	anchor  anchor.Anchor
}

// evaluation collects the results of a run's stages.
type evaluation struct {
	findings  []pending
	items     []verdict.Item
	in        verdict.Input
	claims    []pendingClaim
	questions []questionOutcome
	relaxed   map[string]bool
}

// input is what every stage reads: the bundle version, its main doc, and the profile.
type input struct {
	bundle  pgdb.Bundle
	version uuid.UUID
	files   []source.File
	main    []byte
	doc     section.Doc
	profile profile.Versioned
	relaxed map[string]bool
	fm      source.Frontmatter
	// links are the version's links; linked are the bundle targets, at their current version.
	links  []link
	linked []linked
}

func (s *Service) load(ctx context.Context, b pgdb.Bundle, versionID uuid.UUID, p profile.Versioned) (input, error) {
	files, err := version.Files(ctx, s.DB.Queries(), versionID)
	if err != nil {
		return input{}, err
	}
	in := input{bundle: b, version: versionID, files: files, profile: p, relaxed: map[string]bool{}}
	for _, f := range files {
		if f.Path == b.MainDoc {
			in.main = f.Content
		}
	}
	in.doc = section.Parse(in.main)
	in.fm, _, _ = source.ReadFrontmatter(in.main)
	if in.links, err = s.resolveLinks(ctx, b, in.main); err != nil {
		return input{}, err
	}
	if in.linked, err = s.loadLinked(ctx, in.links); err != nil {
		return input{}, err
	}
	if s.Repo != nil {
		for _, slug := range s.Repo().Adoption.Relaxed {
			in.relaxed[slug] = true
		}
	}
	return in, nil
}

// level returns a check's level after adoption mode (REQ-133).
func (in input) level(slug string, l kernel.Level) kernel.Level {
	if in.relaxed[slug] {
		return kernel.Info
	}
	return l
}

// lintStage runs lint and the upstream check (§8.2, REQ-057).
func lintStage(in input) evaluation {
	ev := evaluation{relaxed: in.relaxed}
	paths := make([]string, len(in.files))
	for i, f := range in.files {
		paths[i] = f.Path
	}
	cfg := lintConfig(in.profile.Profile, in.profile.TemplateText, in.bundle.MainDoc, paths, in.relaxed)
	cfg.UpstreamIDs = upstreamIDs(in)
	res := lint.Run(in.main, cfg)
	failed := map[string]bool{}
	for _, f := range res.Findings {
		ev.findings = append(ev.findings, pending{slug: f.Slug, level: f.Level, stage: StageLint, anchor: f.Anchor, message: f.Message, fix: f.Fix})
		failed[f.Slug] = true
	}
	for slug, lvl := range res.Rules {
		ev.items = append(ev.items, verdict.Item{Category: categories[slug], Level: lvl, Passed: !failed[slug], Applicable: true})
	}
	if up := in.profile.Profile.Links.Upstream; up != nil && up.Required {
		level := in.level(HasUpstreamSlug, checkLevel(in.profile.Profile, HasUpstreamSlug, kernel.Must))
		has, missing := hasUpstream(in, up.Kinds, up.Types)
		ev.in.UpstreamRequired = level == kernel.Must
		ev.in.HasUpstream = has
		ev.items = append(ev.items, verdict.Item{Category: verdict.Coherence, Level: level, Passed: has, Applicable: true})
		if !has {
			ev.findings = append(ev.findings, pending{
				slug: HasUpstreamSlug, level: level, stage: StageCoherence, anchor: docAnchor(in),
				message: fmt.Sprintf("This %s has no %s link to a %s, and no standalone acknowledgement.",
					strings.ToUpper(in.profile.Profile.Key), strings.Join(up.Kinds, " or "), strings.ToUpper(strings.Join(up.Types, " or "))),
				fix: "Add a link under links: in the frontmatter, or a standalone: entry with the reason.",
			})
			if missing != "" {
				f := &ev.findings[len(ev.findings)-1]
				f.message = fmt.Sprintf("No bundle matches the link target %q, so this %s has no upstream doc.", missing, strings.ToUpper(in.profile.Profile.Key))
				f.fix = "Use the target bundle's slug, or a path relative to this doc, or add a standalone: entry with the reason."
			}
		}
	}
	coherenceChecks(in, &ev)
	return ev
}

// docAnchor points at the frontmatter, or at the first line when there is none.
func docAnchor(in input) anchor.Anchor {
	end := in.doc.BodyStart
	if end == 0 {
		end = len(in.main)
		if i := bytes.IndexByte(in.main, '\n'); i >= 0 {
			end = i
		}
	}
	return anchor.New(in.bundle.MainDoc, in.main, in.doc, 0, end)
}

// relaxedCount is the number of relaxed slugs that are real checks of the profile.
func relaxedCount(p profile.Profile, relaxed map[string]bool) int {
	known := map[string]bool{GroundingUnverified: true, GroundingContradicted: true, DivergenceAmbiguous: true, DivergenceGap: true,
		RestatementSlug: true, ContradictionSlug: true}
	for _, r := range lint.Rules {
		known[r.Slug] = true
	}
	for _, c := range p.Checks {
		known[c.Slug] = true
	}
	n := 0
	for slug := range relaxed {
		if known[slug] {
			n++
		}
	}
	return n
}

// save stores a finished run with its findings, claims, and verdict, in one transaction.
// An existing row (a queued full run) is finished; a new one (a lint run) is inserted.
func (s *Service) save(ctx context.Context, run pgdb.ReviewRun, in input, ev evaluation, p profile.Profile, existing bool) error {
	var rows []pgdb.InsertFindingParams
	vin := ev.in
	for _, f := range ev.findings {
		id := kernel.NewID()
		anchorJSON, _ := json.Marshal(f.anchor)
		evidence := dbtype.JSON(`{}`)
		if f.evidence != nil {
			e, _ := json.Marshal(f.evidence)
			evidence = e
		}
		sugg := dbtype.JSON(`{}`)
		if f.fix != "" {
			sugg, _ = json.Marshal(map[string]string{"fix": f.fix})
		}
		rows = append(rows, pgdb.InsertFindingParams{
			ID: id, RunID: run.ID, CheckSlug: f.slug, Level: string(f.level), Stage: f.stage, Relaxed: ev.relaxed[f.slug],
			Anchor: anchorJSON, Message: f.message, Evidence: evidence, Suggestion: sugg,
		})
		vin.Findings = append(vin.Findings, verdict.Finding{ID: id.String(), Level: f.level})
	}
	vin.Items = ev.items
	v := verdict.Decide(vin)
	finished := time.Now().UTC()
	return s.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		if existing {
			if err := finishRun(ctx, q, run, finished); err != nil {
				return err
			}
		} else if err := insertRun(ctx, q, run, finished); err != nil {
			return err
		}
		for _, r := range rows {
			if err := q.InsertFinding(ctx, r); err != nil {
				return err
			}
		}
		for _, c := range ev.claims {
			sources, _ := json.Marshal(nonNil(c.sources))
			an, _ := json.Marshal(c.anchor)
			if err := q.InsertClaim(ctx, pgdb.InsertClaimParams{ID: kernel.NewID(), RunID: run.ID, Text: c.text, Label: c.label,
				Reason: c.reason, Sources: sources, Anchor: an}); err != nil {
				return err
			}
		}
		// REQ-056: the linked versions this run used.
		for _, l := range in.linked {
			if err := q.InsertRunLink(ctx, pgdb.InsertRunLinkParams{RunID: run.ID, BundleID: l.target.ID, VersionID: l.version}); err != nil {
				return err
			}
		}
		if in.bundle.CurrentVersionID.Valid && in.bundle.CurrentVersionID.UUID == run.VersionID {
			if err := storeLinks(ctx, q, s.Workspace, in.bundle, in.links); err != nil {
				return err
			}
		}
		for _, o := range ev.questions {
			for _, a := range o.answers {
				quotes, _ := json.Marshal(nonNilQuotes(a.quotes))
				if err := q.InsertAnswer(ctx, pgdb.InsertAnswerParams{QuestionID: o.q.id, RunID: run.ID, ReaderRole: a.role,
					ModelFingerprint: a.fingerprint, Answer: a.answer, Quotes: quotes, QuotesFound: a.quotesFound}); err != nil {
					return err
				}
			}
			groups, _ := json.Marshal(o.groups)
			if err := q.InsertQuestionResult(ctx, pgdb.InsertQuestionResultParams{RunID: run.ID, QuestionID: o.q.id, Result: string(o.result), Groups: groups}); err != nil {
				return err
			}
		}
		radar, _ := json.Marshal(v.Radar)
		blocking, _ := json.Marshal(nonNil(v.BlockingFindingIDs))
		return q.InsertVerdict(ctx, pgdb.InsertVerdictParams{
			RunID: run.ID, Result: string(v.Result), Score: int64(v.Score), Radar: dbtype.JSON(radar),
			WaiverCount: int64(v.WaiverCount), RelaxedCount: int64(relaxedCount(p, ev.relaxed)), BlockingFindingIds: dbtype.JSON(blocking),
		})
	})
}

// EnsureLinted lints every bundle whose current version has no run with the current profile
// version. It is safe to call after any change.
func (s *Service) EnsureLinted(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.allBundles(ctx)
	if err != nil {
		return err
	}
	for _, b := range all {
		if err := s.lintIfNeeded(ctx, b, all); err != nil {
			return fmt.Errorf("lint %s: %w", b.Slug, err)
		}
	}
	return nil
}

// lintIfNeeded lints b when its current version has no run with the current profile version.
// Otherwise it refreshes b's stored links, which change without a new version when a link
// rule or a target bundle changes, and lints again when a lint verdict read other linked
// versions than the current ones (REQ-056). A full verdict stays stale until the next full
// run, so its model results are not hidden.
func (s *Service) lintIfNeeded(ctx context.Context, b pgdb.Bundle, all []pgdb.Bundle) error {
	if !b.CurrentVersionID.Valid {
		return nil
	}
	p, ok := s.Profiles()[b.ProfileKey]
	pv := int64(0)
	if ok {
		pv = p.Version
	}
	q := s.DB.Queries()
	latest, err := q.LatestRunFor(ctx, pgdb.LatestRunForParams{
		BundleID: b.ID, VersionID: b.CurrentVersionID.UUID, ProfileKey: b.ProfileKey, ProfileVersion: pv,
	})
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.Lint(ctx, b, b.CurrentVersionID.UUID)
		return err
	}
	if err != nil {
		return err
	}
	files, err := version.Files(ctx, q, b.CurrentVersionID.UUID)
	if err != nil {
		return err
	}
	var main []byte
	for _, f := range files {
		if f.Path == b.MainDoc {
			main = f.Content
		}
	}
	links := resolveLinksIn(all, b, main, s.linkRules())
	stored, err := q.ListLinksFrom(ctx, b.ID)
	if err != nil {
		return err
	}
	if !sameLinks(stored, links) {
		if err := s.DB.InTx(ctx, func(tx store.Tx) error { return storeLinks(ctx, tx.Queries(), s.Workspace, b, links) }); err != nil {
			return err
		}
	}
	if latest.Kind != "lint" || latest.Status != "complete" {
		return nil
	}
	used, err := q.ListRunLinks(ctx, latest.ID)
	if err != nil {
		return err
	}
	if sameVersions(used, links) {
		return nil
	}
	_, err = s.Lint(ctx, b, b.CurrentVersionID.UUID)
	return err
}

// sameLinks reports whether the stored links equal links.
func sameLinks(stored []pgdb.Link, links []link) bool {
	key := func(kind, ref, origin, targetKind string, target uuid.NullUUID) string {
		return strings.Join([]string{kind, ref, origin, targetKind, target.UUID.String(), fmt.Sprint(target.Valid)}, "|")
	}
	a := make([]string, 0, len(stored))
	for _, l := range stored {
		a = append(a, key(l.Kind, l.TargetRef, l.Origin, l.TargetKind, l.TargetBundleID))
	}
	b := make([]string, 0, len(links))
	for _, l := range links {
		var t uuid.NullUUID
		if l.target != nil {
			t = uuid.NullUUID{UUID: l.target.ID, Valid: true}
		}
		b = append(b, key(l.kind, l.ref, l.origin, l.targetKind, t))
	}
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}

// sameVersions reports whether a run read the current version of each linked bundle.
func sameVersions(used []pgdb.RunLink, links []link) bool {
	now := map[uuid.UUID]uuid.UUID{}
	for _, l := range links {
		if l.target != nil && l.target.CurrentVersionID.Valid {
			now[l.target.ID] = l.target.CurrentVersionID.UUID
		}
	}
	if len(used) != len(now) {
		return false
	}
	for _, u := range used {
		if now[u.BundleID] != u.VersionID {
			return false
		}
	}
	return true
}

// noProfile is the error of a run on a doc whose type has no profile.
func (s *Service) noProfile(key string) string {
	return fmt.Sprintf("No profile has the key %q, so Speccy cannot review this doc. Use a built-in type (%s), or add .speccy/profiles/%s.yaml.",
		key, strings.Join(profileKeys(s.Profiles()), ", "), key)
}

// Lint runs the lint stage on version v of b and stores the run, its findings, and its verdict.
func (s *Service) Lint(ctx context.Context, b pgdb.Bundle, versionID uuid.UUID) (pgdb.ReviewRun, error) {
	now := time.Now().UTC()
	run := pgdb.ReviewRun{
		ID: kernel.NewID(), WorkspaceID: s.Workspace, BundleID: b.ID, VersionID: versionID,
		ProfileKey: b.ProfileKey, Kind: "lint", Stage: StageLint, StartedAt: now,
		Roles: dbtype.JSON(`{}`), PromptVersions: dbtype.JSON(`{}`),
	}
	p, ok := s.Profiles()[b.ProfileKey]
	if !ok {
		run.Status = "failed"
		run.Error = s.noProfile(b.ProfileKey)
		return run, insertRun(ctx, s.DB.Queries(), run, now)
	}
	run.ProfileVersion = p.Version
	in, err := s.load(ctx, b, versionID, p)
	if err != nil {
		return run, err
	}
	run.Status = "complete"
	return run, s.save(ctx, run, in, lintStage(in), p.Profile, false)
}

func insertRun(ctx context.Context, q store.Querier, r pgdb.ReviewRun, finished time.Time) error {
	return q.InsertRun(ctx, pgdb.InsertRunParams{
		ID: r.ID, WorkspaceID: r.WorkspaceID, BundleID: r.BundleID, VersionID: r.VersionID, ProfileKey: r.ProfileKey,
		ProfileVersion: r.ProfileVersion, Kind: r.Kind, Status: r.Status, Stage: r.Stage, Error: r.Error,
		StartedAt: r.StartedAt, FinishedAt: sql.NullTime{Time: finished, Valid: true},
	})
}

func finishRun(ctx context.Context, q store.Querier, r pgdb.ReviewRun, finished time.Time) error {
	roles, prompts, notes := r.Roles, r.PromptVersions, r.Notes
	if len(roles) == 0 {
		roles = dbtype.JSON(`{}`)
	}
	if len(prompts) == 0 {
		prompts = dbtype.JSON(`{}`)
	}
	if len(notes) == 0 {
		notes = dbtype.JSON(`[]`)
	}
	return q.FinishRun(ctx, pgdb.FinishRunParams{
		ID: r.ID, Status: r.Status, Stage: r.Stage, Error: r.Error, Roles: roles, PromptVersions: prompts, Notes: notes,
		TokensIn: r.TokensIn, TokensOut: r.TokensOut, CostEstimate: r.CostEstimate, CacheHits: r.CacheHits,
		FinishedAt: sql.NullTime{Time: finished, Valid: true},
	})
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// lintConfig builds the lint configuration from a profile. Relaxed checks report at INFO
// (REQ-133); a profile override of "off" stays off.
func lintConfig(p profile.Profile, template []byte, mainDoc string, files []string, relaxed map[string]bool) lint.Config {
	levels := map[string]string{}
	for slug, o := range p.Lint.Overrides {
		levels[slug] = o.Level
	}
	for slug := range relaxed {
		if levels[slug] != "off" {
			levels[slug] = string(kernel.Info)
		}
	}
	var required []lint.Heading
	for _, h := range profile.RequiredHeadings(template) {
		required = append(required, lint.Heading{Level: h.Level, Title: h.Title})
	}
	return lint.Config{
		Path: mainDoc, Files: files,
		MaxWords: p.Limits.MaxWords, MaxSectionWords: p.Limits.MaxSectionWords, MaxSentenceWords: p.Limits.MaxSentenceWords,
		MaxCodeBlockLines: p.Limits.MaxCodeBlockLines, MaxTableRows: p.Limits.MaxTableRows,
		Prefixes: p.Trace.Prefixes, UpstreamPrefixes: p.Trace.Cover,
		Required: required, SlopExtra: p.Lint.SlopExtra, Levels: levels,
	}
}

func checkLevel(p profile.Profile, slug string, fallback kernel.Level) kernel.Level {
	for _, c := range p.Checks {
		if c.Slug == slug {
			return kernel.Level(c.Level)
		}
	}
	return fallback
}

// hasUpstream reports whether the doc links to a bundle of one of types with one of kinds, or
// has a standalone acknowledgement with a reason (REQ-057). missing is the first link target
// of a right kind that no bundle matches.
func hasUpstream(in input, kinds, types []string) (has bool, missing string) {
	if standalone(in.fm) {
		return true, ""
	}
	for _, l := range in.links {
		if !slices.Contains(kinds, l.kind) {
			continue
		}
		if l.target == nil {
			if l.targetKind == "bundle" && missing == "" {
				missing = l.ref
			}
			continue
		}
		if len(types) == 0 || slices.Contains(types, l.target.ProfileKey) {
			return true, ""
		}
	}
	return false, missing
}

func profileKeys(ps map[string]profile.Versioned) []string {
	keys := make([]string, 0, len(ps))
	for k := range ps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// upstreamMoved reports whether a bundle that the run read has a newer version now (REQ-056).
func upstreamMoved(ctx context.Context, q store.Querier, workspace, runID uuid.UUID) (bool, error) {
	links, err := q.ListRunLinks(ctx, runID)
	if err != nil {
		return false, err
	}
	for _, l := range links {
		b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: workspace, ID: l.BundleID})
		if err != nil {
			return false, err
		}
		if !b.CurrentVersionID.Valid || b.CurrentVersionID.UUID != l.VersionID {
			return true, nil
		}
	}
	return false, nil
}
