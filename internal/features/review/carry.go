package review

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/section"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
)

// carriedFinding is one finding of a full run that a later run's verdict counts: its row stays
// in the full run, and the verdict records whether a waiver covers it now.
type carriedFinding struct {
	ID     uuid.UUID `json:"id"`
	Waived bool      `json:"waived"`
}

// aiFinding reports whether a finding came from a stage that calls a model. A lint run makes
// the rest again on the current text, so only these carry.
func aiFinding(stage, slug string) bool {
	switch stage {
	case StageRubric, StageGrounding, StageDivergence:
		return true
	case StageCoherence:
		return slug == ContradictionSlug || slug == ExternalConflictSlug
	}
	return false
}

// carry adds the AI findings of the bundle's last full review to a lint evaluation, for every
// finding whose anchored section is unchanged since that review read it. A finding with no
// section is about the whole doc, so it carries only while the doc is unchanged. The model
// never read a changed section, so its findings there drop out. The run's AI items carry too,
// so the score counts them; a failed item whose every finding dropped is no longer known.
func (s *Service) carry(ctx context.Context, b pgdb.SpecDoc, in input, ev *evaluation) error {
	q := s.DB.Queries()
	runs, err := q.ListRuns(ctx, pgdb.ListRunsParams{SpecDocID: b.ID, Before: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), PageSize: 50})
	if err != nil {
		return err
	}
	var full *pgdb.ReviewRun
	for i := range runs {
		if runs[i].Kind == "full" && runs[i].Status == "complete" {
			full = &runs[i]
			break
		}
	}
	if full == nil {
		return nil
	}
	vd, err := q.GetVerdict(ctx, full.ID)
	if err != nil {
		return nil //nolint:nilerr // a full run with no verdict has nothing to carry
	}
	findings, err := q.ListFindings(ctx, full.ID)
	if err != nil {
		return err
	}
	files, err := version.Files(ctx, q, full.VersionID)
	if err != nil {
		return err
	}
	var oldMain []byte
	for _, f := range files {
		if f.Path == b.DocPath {
			oldMain = f.Content
		}
	}
	oldDoc := section.Parse(oldMain)
	unchanged := func(path []string) bool {
		if len(path) == 0 {
			return section.Normalize(oldMain) == section.Normalize(in.main)
		}
		was, ok1 := section.HashAt(oldDoc, oldMain, path)
		now, ok2 := section.HashAt(in.doc, in.main, path)
		return ok1 && ok2 && was == now
	}

	ev.carriedRun = full.ID
	ev.sectionsChanged = changedSections(oldDoc, oldMain, in.doc, in.main)
	kept, dropped := map[string]bool{}, map[string]bool{}
	for _, f := range findings {
		if !aiFinding(f.Stage, f.CheckSlug) {
			continue
		}
		var an anchor.Anchor
		_ = json.Unmarshal(f.Anchor, &an)
		if !unchanged(an.HeadingPath) {
			dropped[f.CheckSlug] = true
			continue
		}
		kept[f.CheckSlug] = true
		ev.findings = append(ev.findings, pending{slug: f.CheckSlug, level: kernel.Level(f.Level), stage: f.Stage,
			anchor: an, message: f.Message, carried: f.ID})
	}
	var items []verdict.Item
	_ = json.Unmarshal(vd.Items, &items)
	lintSlugs := map[string]bool{}
	for _, it := range ev.items {
		lintSlugs[it.Slug] = true
	}
	for _, it := range items {
		if it.Slug != "" && lintSlugs[it.Slug] {
			continue // the lint run scored this check again on the current text
		}
		if !it.Passed && dropped[it.Slug] && !kept[it.Slug] {
			continue // every finding of this check was in a changed section
		}
		it.Waived = false // the waivers are applied again to the current version
		ev.items = append(ev.items, it)
	}
	return nil
}

// changedSections counts the sections of the current doc that are new or whose text changed
// since the old doc.
func changedSections(oldDoc section.Doc, oldMain []byte, doc section.Doc, main []byte) int {
	n := 0
	for _, sec := range doc.Sections {
		was, ok := section.HashAt(oldDoc, oldMain, sec.Path)
		if !ok || was != sec.Hash {
			n++
		}
	}
	for _, sec := range oldDoc.Sections {
		if !slices.ContainsFunc(doc.Sections, func(x section.Section) bool { return slices.Equal(x.Path, sec.Path) }) {
			n++
		}
	}
	return n
}
