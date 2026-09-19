package review

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/coherence"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// Coherence finding slugs. trace.coverage is the Appendix A check (REQ-053).
const (
	CoverageSlug      = "trace.coverage"
	RestatementSlug   = "coherence.restatement"
	ContradictionSlug = "coherence.contradiction"
)

// Link kinds that each coherence check reads.
var (
	coverageKinds      = []string{"implements"}                          // REQ-053
	restatementKinds   = []string{"implements", "refines"}               // REQ-055: downstream docs
	contradictionKinds = []string{"implements", "refines", "references"} // REQ-054
)

// standalone reports whether the doc has a standalone acknowledgement with a reason (REQ-057).
func standalone(fm source.Frontmatter) bool {
	return fm.Standalone != nil && strings.TrimSpace(fm.Standalone.Reason) != ""
}

// upstreamIDs returns the IDs that the implements and refines targets define, or nil when
// the doc has no such target. Lint checks references with an upstream prefix against it.
func upstreamIDs(in input) map[string]bool {
	var ids map[string]bool
	for _, l := range in.linked {
		if !slices.Contains(restatementKinds, l.kind) {
			continue
		}
		if ids == nil {
			ids = map[string]bool{}
		}
		for _, d := range lint.Definitions(l.main, in.profile.Profile.Trace.Cover) {
			ids[d.ID] = true
		}
	}
	return ids
}

// coherenceChecks runs the deterministic coherence checks: coverage (REQ-053) and
// restatement (REQ-055). They run on every save, like lint. A standalone doc has no
// coherence checks (REQ-057: not applicable).
func coherenceChecks(in input, ev *evaluation) {
	if standalone(in.fm) {
		return
	}
	covered := in.profile.Profile.Trace.Cover
	referenced := map[string]bool{}
	for _, d := range append(lint.References(in.main, covered), lint.Definitions(in.main, covered)...) {
		referenced[d.ID] = true
	}
	acks := map[string]coherence.Ack{}
	for _, a := range in.fm.Trace {
		acks[a.ID] = coherence.Ack{Status: a.Status, Target: a.Target, Reason: a.Reason}
	}
	coverLevel := in.level(CoverageSlug, checkLevel(in.profile.Profile, CoverageSlug, kernel.Must))
	restateLevel := in.level(RestatementSlug, kernel.Should)
	downParas := lint.Paragraphs(in.main)
	downText := make([]string, len(downParas))
	for i, p := range downParas {
		downText[i] = p.Text
	}

	for _, l := range in.linked {
		if slices.Contains(coverageKinds, l.kind) && len(covered) > 0 {
			defs := lint.Definitions(l.main, covered)
			ids := make([]string, len(defs))
			byID := map[string]lint.Definition{}
			for i, d := range defs {
				ids[i] = d.ID
				byID[d.ID] = d
			}
			for _, c := range coherence.Coverage(ids, referenced, acks) {
				ev.items = append(ev.items, verdict.Item{Category: verdict.Coherence, Level: coverLevel, Passed: c.State != "gap", Applicable: true})
				if c.State != "gap" {
					continue
				}
				d := byID[c.ID]
				up := anchor.New(l.target.MainDoc, l.main, l.doc, d.Start, d.End)
				msg := fmt.Sprintf("%s of %s is not referenced in this doc, and not acknowledged.", c.ID, l.target.Slug)
				if a, ok := acks[c.ID]; ok && !a.Valid() {
					msg = fmt.Sprintf("%s of %s is not referenced, and its acknowledgement needs a status, a reason, and a target for covered_by.", c.ID, l.target.Slug)
				}
				ev.findings = append(ev.findings, pending{
					slug: CoverageSlug, level: coverLevel, stage: StageCoherence, anchor: docAnchor(in), message: msg,
					fix: fmt.Sprintf("Reference %s where the design covers it, or add it under trace: in the frontmatter as covered_by or out_of_scope, with a reason.", c.ID),
					evidence: map[string]any{"id": c.ID, "text": strings.TrimSpace(d.Text), "upstream": l.target.Slug,
						"upstream_bundle_id": l.target.ID, "upstream_anchor": up},
				})
			}
		}
		if slices.Contains(restatementKinds, l.kind) {
			upParas := lint.Paragraphs(l.main)
			upText := make([]string, len(upParas))
			for i, p := range upParas {
				upText[i] = p.Text
			}
			found := coherence.Restated(downText, upText)
			ev.items = append(ev.items, verdict.Item{Category: verdict.Coherence, Level: restateLevel, Passed: len(found) == 0, Applicable: true})
			for _, r := range found {
				dp, up := downParas[r.Down], upParas[r.Up]
				ev.findings = append(ev.findings, pending{
					slug: RestatementSlug, level: restateLevel, stage: StageCoherence,
					anchor:  anchor.New(in.bundle.MainDoc, in.main, in.doc, dp.Start, dp.End),
					message: fmt.Sprintf("This paragraph repeats %s (%d%% of its 8-word runs). Link, do not repeat.", l.target.Slug, int(r.Overlap*100)),
					fix:     fmt.Sprintf("Replace the paragraph with a reference to %s, and keep only what this doc adds.", l.target.Slug),
					evidence: map[string]any{"upstream": l.target.Slug, "upstream_bundle_id": l.target.ID, "overlap": r.Overlap,
						"upstream_anchor": anchor.New(l.target.MainDoc, l.main, l.doc, up.Start, up.End)},
				})
			}
		}
	}
}
