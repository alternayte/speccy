package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

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

// aiStages are the stages that call a model, in run order.
var aiStages = []string{StageRubric, StageGrounding, StageDivergence, StageCoherence}

// itemStage is the AI stage that scored a verdict item, or "" for an item that every run scores
// again on the current text.
func itemStage(it verdict.Item) string {
	switch {
	case it.Category == verdict.Completeness:
		return StageRubric
	case it.Category == verdict.Evidence:
		return StageGrounding
	case it.Category == verdict.Precision:
		return StageDivergence
	case it.Slug == ContradictionSlug || it.Slug == ExternalConflictSlug:
		return StageCoherence
	}
	return ""
}

// carry adds to the evaluation the AI findings that this run does not judge again: those of
// the bundle's last full review, with the ones that review carried itself. ran reports the
// stages this run does; a lint run does none, and a run with a stage subset does some.
//
// A finding stays while the section of its anchor is the text its review read: the model never
// read a changed section, so its findings there drop out. A whole-doc finding stays whatever
// the text is, until a review judges its check again: it is a finding of a whole-doc check, or
// a finding with no quote, which says that content is missing. It would otherwise leave the
// verdict on any save, and the doc could read Build Ready by mistake.
//
// The AI items carry too, so the score counts them; a failed item whose every finding dropped
// is no longer known.
func (s *Service) carry(ctx context.Context, b pgdb.SpecDoc, in input, ev *evaluation, ran func(stage string) bool) error {
	q := s.DB.Queries()
	full, err := q.LatestFullReview(ctx, b.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	vd, err := q.GetVerdict(ctx, full.ID)
	if err != nil {
		return nil //nolint:nilerr // a full run with no verdict has nothing to carry
	}
	findings, err := q.ListFindings(ctx, full.ID)
	if err != nil {
		return err
	}
	older, err := carriedRows(ctx, q, full.ID)
	if err != nil {
		return err
	}
	findings = append(findings, older...)

	// read is the doc as the run of a finding read it.
	type read struct {
		main []byte
		doc  section.Doc
	}
	reads := map[uuid.UUID]read{}
	readOf := func(runID uuid.UUID) (read, error) {
		if r, ok := reads[runID]; ok {
			return r, nil
		}
		versionID := full.VersionID
		if runID != full.ID {
			run, err := q.GetRunByID(ctx, runID)
			if err != nil {
				return read{}, err
			}
			versionID = run.VersionID
		}
		files, err := version.Files(ctx, q, versionID)
		if err != nil {
			return read{}, err
		}
		var r read
		for _, f := range files {
			if f.Path == b.DocPath {
				r.main = f.Content
			}
		}
		r.doc = section.Parse(r.main)
		reads[runID] = r
		return r, nil
	}
	then, err := readOf(full.ID)
	if err != nil {
		return err
	}

	kept, dropped := map[string]bool{}, map[string]bool{}
	for _, f := range findings {
		if !aiFinding(f.Stage, f.CheckSlug) || ran(f.Stage) {
			continue
		}
		var an anchor.Anchor
		_ = json.Unmarshal(f.Anchor, &an)
		was, err := readOf(f.RunID)
		if err != nil {
			return err
		}
		if !wholeDocFinding(in, was.doc, f) {
			before, ok1 := section.HashAt(was.doc, was.main, an.HeadingPath)
			now, ok2 := section.HashAt(in.doc, in.main, an.HeadingPath)
			if !ok1 || !ok2 || before != now {
				dropped[f.CheckSlug] = true
				continue
			}
		}
		kept[f.CheckSlug] = true
		ev.findings = append(ev.findings, pending{slug: f.CheckSlug, level: kernel.Level(f.Level), stage: f.Stage,
			anchor: an, message: f.Message, carried: f.ID})
	}
	var items []verdict.Item
	_ = json.Unmarshal(vd.Items, &items)
	scored := map[string]bool{}
	for _, it := range ev.items {
		scored[it.Slug] = true
	}
	for _, it := range items {
		stage := itemStage(it)
		if stage != "" && ran(stage) {
			continue // this run scored the stage again
		}
		if it.Slug != "" && scored[it.Slug] {
			continue // this run scored the check again on the current text
		}
		if !it.Passed && dropped[it.Slug] && !kept[it.Slug] {
			continue // every finding of this check was in a changed section
		}
		it.Waived = false // the waivers are applied again to the current version
		ev.items = append(ev.items, it)
	}
	ev.carriedRun = full.ID
	ev.sectionsChanged = changedSections(then.doc, then.main, in.doc, in.main)
	return nil
}

// wholeDocFinding reports whether a finding is about the doc and not about one section's
// text: a finding of a whole-doc check, or a rubric finding with no quote in the doc. was is
// the doc as the finding's review read it.
func wholeDocFinding(in input, was section.Doc, f pgdb.Finding) bool {
	if f.Stage != StageRubric {
		return false
	}
	if c, ok := in.profile.Profile.Check(f.CheckSlug); ok && c.Scope != "section" && c.Named(was) == nil {
		return true
	}
	var ev struct {
		Quotes []quoteEvidence `json:"quotes"`
	}
	_ = json.Unmarshal(f.Evidence, &ev)
	for _, q := range ev.Quotes {
		if q.Found {
			return false
		}
	}
	return true
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
