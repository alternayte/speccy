// Package review runs reviews and stores runs, findings, and verdicts (SDD §8). M3 has the
// lint stage, which runs on every new version (DEC-027), and the lint-only verdict.
package review

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// Stage names (REQ-020).
const (
	StageLint      = "lint"
	StageCoherence = "coherence"
)

// Service runs reviews for one workspace.
type Service struct {
	DB        *store.DB
	Workspace uuid.UUID
	// Profiles returns the current profiles by key. Repo returns .speccy.yaml.
	Profiles func() map[string]profile.Versioned
	Repo     func() source.RepoConfig

	mu sync.Mutex // one lint pass at a time
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

// EnsureLinted lints every bundle whose current version has no run with the current profile
// version. It is safe to call after any change.
func (s *Service) EnsureLinted(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.DB.Queries()
	after := ""
	for {
		page, err := q.ListBundles(ctx, pgdb.ListBundlesParams{WorkspaceID: s.Workspace, AfterSlug: after, PageSize: 100})
		if err != nil {
			return err
		}
		for _, b := range page {
			if err := s.lintIfNeeded(ctx, b); err != nil {
				return fmt.Errorf("lint %s: %w", b.Slug, err)
			}
		}
		if len(page) < 100 {
			return nil
		}
		after = page[len(page)-1].Slug
	}
}

func (s *Service) lintIfNeeded(ctx context.Context, b pgdb.Bundle) error {
	if !b.CurrentVersionID.Valid {
		return nil
	}
	p, ok := s.Profiles()[b.ProfileKey]
	pv := int64(0)
	if ok {
		pv = p.Version
	}
	_, err := s.DB.Queries().LatestRunFor(ctx, pgdb.LatestRunForParams{
		BundleID: b.ID, VersionID: b.CurrentVersionID.UUID, ProfileKey: b.ProfileKey, ProfileVersion: pv,
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.Lint(ctx, b, b.CurrentVersionID.UUID)
	return err
}

// Lint runs the lint stage on version v of b and stores the run, its findings, and its verdict.
func (s *Service) Lint(ctx context.Context, b pgdb.Bundle, versionID uuid.UUID) (pgdb.ReviewRun, error) {
	q := s.DB.Queries()
	now := time.Now().UTC()
	run := pgdb.ReviewRun{
		ID: kernel.NewID(), WorkspaceID: s.Workspace, BundleID: b.ID, VersionID: versionID,
		ProfileKey: b.ProfileKey, Kind: "lint", Stage: StageLint, StartedAt: now,
	}
	p, ok := s.Profiles()[b.ProfileKey]
	if !ok {
		run.Status = "failed"
		run.Error = fmt.Sprintf("No profile has the key %q, so Speccy cannot review this doc. Use a built-in type (%s), or add .speccy/profiles/%s.yaml.",
			b.ProfileKey, strings.Join(profileKeys(s.Profiles()), ", "), b.ProfileKey)
		return run, insertRun(ctx, q, run, now)
	}
	run.ProfileVersion = p.Version

	files, err := version.Files(ctx, q, versionID)
	if err != nil {
		return run, err
	}
	var main []byte
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
		if f.Path == b.MainDoc {
			main = f.Content
		}
	}
	repo := s.Repo()
	relaxed := map[string]bool{}
	for _, slug := range repo.Adoption.Relaxed {
		relaxed[slug] = true
	}
	cfg := lintConfig(p.Profile, p.TemplateText, b.MainDoc, paths, relaxed)
	res := lint.Run(main, cfg)

	type stored struct {
		row     pgdb.InsertFindingParams
		verdict verdict.Finding
	}
	var findings []stored
	add := func(slug string, level kernel.Level, stage string, a anchor.Anchor, msg, fix string) {
		id := kernel.NewID()
		anchorJSON, _ := json.Marshal(a)
		sugg := json.RawMessage(`{}`)
		if fix != "" {
			sugg, _ = json.Marshal(map[string]string{"fix": fix})
		}
		findings = append(findings, stored{
			row: pgdb.InsertFindingParams{ID: id, RunID: run.ID, CheckSlug: slug, Level: string(level), Stage: stage,
				Relaxed: relaxed[slug], Anchor: anchorJSON, Message: msg, Evidence: dbtype.JSON(`{}`), Suggestion: dbtype.JSON(sugg)},
			verdict: verdict.Finding{ID: id.String(), Level: level},
		})
	}
	failed := map[string]bool{}
	for _, f := range res.Findings {
		add(f.Slug, f.Level, StageLint, f.Anchor, f.Message, f.Fix)
		failed[f.Slug] = true
	}
	var items []verdict.Item
	for slug, lvl := range res.Rules {
		items = append(items, verdict.Item{Category: categories[slug], Level: lvl, Passed: !failed[slug], Applicable: true})
	}

	// REQ-057: a required upstream link, or a standalone acknowledgement, in the frontmatter.
	in := verdict.Input{}
	if up := p.Profile.Links.Upstream; up != nil && up.Required {
		level := checkLevel(p.Profile, HasUpstreamSlug, kernel.Must)
		if relaxed[HasUpstreamSlug] {
			level = kernel.Info
		}
		has := hasUpstream(main, up.Kinds)
		in.UpstreamRequired = level == kernel.Must
		in.HasUpstream = has
		items = append(items, verdict.Item{Category: verdict.Coherence, Level: level, Passed: has, Applicable: true})
		if !has {
			// Anchor on the frontmatter, where the link belongs, or on the first line.
			sd := section.Parse(main)
			end := sd.BodyStart
			if end == 0 {
				end = len(main)
				if i := bytes.IndexByte(main, '\n'); i >= 0 {
					end = i
				}
			}
			add(HasUpstreamSlug, level, StageCoherence, anchor.New(b.MainDoc, main, sd, 0, end),
				fmt.Sprintf("This %s has no %s link to a %s, and no standalone acknowledgement.",
					strings.ToUpper(p.Profile.Key), strings.Join(up.Kinds, " or "), strings.ToUpper(strings.Join(up.Types, " or "))),
				"Add a link under links: in the frontmatter, or a standalone: entry with the reason.")
		}
	}
	for _, f := range findings {
		in.Findings = append(in.Findings, f.verdict)
	}
	in.Items = items
	v := verdict.Decide(in)

	relaxedCount := 0
	known := map[string]bool{}
	for _, r := range lint.Rules {
		known[r.Slug] = true
	}
	for _, c := range p.Profile.Checks {
		known[c.Slug] = true
	}
	for slug := range relaxed {
		if known[slug] {
			relaxedCount++
		}
	}

	run.Status = "complete"
	finished := time.Now().UTC()
	err = s.DB.InTx(ctx, func(tx store.Tx) error {
		tq := tx.Queries()
		if err := insertRun(ctx, tq, run, finished); err != nil {
			return err
		}
		for _, f := range findings {
			if err := tq.InsertFinding(ctx, f.row); err != nil {
				return err
			}
		}
		radar, _ := json.Marshal(v.Radar)
		blocking, _ := json.Marshal(nonNil(v.BlockingFindingIDs))
		return tq.InsertVerdict(ctx, pgdb.InsertVerdictParams{
			RunID: run.ID, Result: string(v.Result), Score: int64(v.Score), Radar: dbtype.JSON(radar),
			WaiverCount: int64(v.WaiverCount), RelaxedCount: int64(relaxedCount), BlockingFindingIds: dbtype.JSON(blocking),
		})
	})
	return run, err
}

func insertRun(ctx context.Context, q store.Querier, r pgdb.ReviewRun, finished time.Time) error {
	fin := sql.NullTime{Time: finished, Valid: true}
	return q.InsertRun(ctx, pgdb.InsertRunParams{
		ID: r.ID, WorkspaceID: r.WorkspaceID, BundleID: r.BundleID, VersionID: r.VersionID, ProfileKey: r.ProfileKey,
		ProfileVersion: r.ProfileVersion, Kind: r.Kind, Status: r.Status, Stage: r.Stage, Error: r.Error,
		StartedAt: r.StartedAt, FinishedAt: fin,
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

// hasUpstream reports whether the frontmatter links upstream with one of kinds, or has a
// standalone acknowledgement with a reason.
func hasUpstream(main []byte, kinds []string) bool {
	fm, _, err := source.ReadFrontmatter(main)
	if err != nil {
		return false
	}
	if fm.Standalone != nil && strings.TrimSpace(fm.Standalone.Reason) != "" {
		return true
	}
	for _, l := range fm.Links {
		for _, k := range kinds {
			if l.Kind == k && strings.TrimSpace(l.Target) != "" {
				return true
			}
		}
	}
	return false
}

func profileKeys(ps map[string]profile.Versioned) []string {
	keys := make([]string, 0, len(ps))
	for k := range ps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
