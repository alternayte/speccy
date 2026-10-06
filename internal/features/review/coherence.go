package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/engine/anchor"
	"github.com/alternayte/speccy/internal/engine/coherence"
	"github.com/alternayte/speccy/internal/engine/lint"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/model"
	"github.com/alternayte/speccy/internal/source"
)

// Coherence finding slugs. trace.coverage is the Appendix A check (REQ-053).
const (
	CoverageSlug      = "trace.coverage"
	RestatementSlug   = "coherence.restatement"
	ContradictionSlug = source.ContradictionCheck
)

// Link kinds that each coherence check reads.
var (
	coverageKinds      = []string{"implements"}                          // REQ-053
	restatementKinds   = []string{"implements", "refines"}               // REQ-055: downstream docs
	contradictionKinds = []string{"implements", "refines", "references"} // REQ-054
)

// standalone reports whether the doc has a standalone acknowledgement with a reason (REQ-057).
func standalone(d source.Decisions) bool {
	return d.Standalone != nil && strings.TrimSpace(d.Standalone.Reason) != ""
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
	if standalone(in.dec) {
		return
	}
	covered := in.profile.Profile.Trace.Cover
	referenced := map[string]bool{}
	for _, d := range append(lint.References(in.main, covered), lint.Definitions(in.main, covered)...) {
		referenced[d.ID] = true
	}
	acks := map[string]coherence.Ack{}
	for _, a := range in.dec.Trace {
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
			// A doc often defines one ID in a summary table and again in a heading: coverage
			// checks each ID once, at its first definition (#130).
			defined := definedIDs(lint.Definitions(l.main, covered))
			// An upstream doc with no ID gives coverage nothing to check: the check does not
			// apply, and it never passes on nothing.
			if len(defined) == 0 {
				ev.items = append(ev.items, verdict.Item{Slug: CoverageSlug, Category: verdict.Coherence, Level: coverLevel, Applicable: false})
			}
			ids := make([]string, len(defined))
			for i, d := range defined {
				ids[i] = d.id
			}
			var gaps []string
			for i, c := range coherence.Coverage(ids, referenced, acks) {
				ev.items = append(ev.items, verdict.Item{Slug: CoverageSlug, Category: verdict.Coherence, Level: coverLevel, Passed: c.State != "gap", Applicable: true})
				if c.State != "gap" {
					continue
				}
				gaps = append(gaps, c.ID)
				sites := defined[i].sites
				d := sites[0]
				up := anchor.New(l.target.DocPath, l.main, l.doc, d.Start, d.End)
				msg := fmt.Sprintf("%s of %s is not referenced in this doc, and not acknowledged.", c.ID, l.target.Slug)
				if a, ok := acks[c.ID]; ok && !a.Valid() {
					msg = fmt.Sprintf("%s of %s is not referenced, and its acknowledgement needs a status, a reason, and a target for covered_by.", c.ID, l.target.Slug)
				}
				evidence := map[string]any{"id": c.ID, "text": strings.TrimSpace(d.Text), "upstream": l.target.Slug,
					"upstream_bundle_id": l.target.ID, "upstream_anchor": up}
				if len(sites) > 1 {
					anchors := make([]anchor.Anchor, len(sites))
					places := make([]string, len(sites))
					for j, s := range sites {
						anchors[j] = anchor.New(l.target.DocPath, l.main, l.doc, s.Start, s.End)
						places[j] = placeOf(l.main, anchors[j])
					}
					evidence["upstream_definitions"] = anchors
					msg += fmt.Sprintf(" %s defines it in %d places: %s.", l.target.Slug, len(sites), strings.Join(places, "; "))
				}
				ev.findings = append(ev.findings, pending{
					slug: CoverageSlug, level: coverLevel, stage: StageCoherence, anchor: docAnchor(in), message: msg,
					fix:      fmt.Sprintf("Answer the gap: name %s in the section that covers it, name the doc that covers it, or mark it out of scope with a reason.", c.ID),
					evidence: evidence,
				})
			}
			if len(gaps) > 1 {
				ev.notes = append(ev.notes, fmt.Sprintf("%d IDs of %s have no reference and no acknowledgement in this doc: %s. One coverage table that names each ID answers all of them.",
					len(gaps), l.target.Slug, strings.Join(gaps, ", ")))
			}
		}
		if slices.Contains(restatementKinds, l.kind) {
			upParas := lint.Paragraphs(l.main)
			upText := make([]string, len(upParas))
			for i, p := range upParas {
				upText[i] = p.Text
			}
			found := coherence.Restated(downText, upText)
			ev.items = append(ev.items, verdict.Item{Slug: RestatementSlug, Category: verdict.Coherence, Level: restateLevel, Passed: len(found) == 0, Applicable: true})
			for _, r := range found {
				dp, up := downParas[r.Down], upParas[r.Up]
				ev.findings = append(ev.findings, pending{
					slug: RestatementSlug, level: restateLevel, stage: StageCoherence,
					anchor:  anchor.New(in.bundle.DocPath, in.main, in.doc, dp.Start, dp.End),
					message: fmt.Sprintf("This paragraph repeats %s (%d%% of its 8-word runs). Link, do not repeat.", l.target.Slug, int(r.Overlap*100)),
					fix:     fmt.Sprintf("Replace the paragraph with a reference to %s, and keep only what this doc adds.", l.target.Slug),
					evidence: map[string]any{"upstream": l.target.Slug, "upstream_bundle_id": l.target.ID, "overlap": r.Overlap,
						"upstream_anchor": anchor.New(l.target.DocPath, l.main, l.doc, up.Start, up.End)},
				})
			}
		}
	}
}

// definedID is one trace ID and each place that defines it, in doc order.
type definedID struct {
	id    string
	sites []lint.Definition
}

// definedIDs groups definitions by ID, in the order of each ID's first definition.
func definedIDs(defs []lint.Definition) []definedID {
	at := map[string]int{}
	var out []definedID
	for _, d := range defs {
		if i, ok := at[d.ID]; ok {
			out[i].sites = append(out[i].sites, d)
			continue
		}
		at[d.ID] = len(out)
		out = append(out, definedID{id: d.ID, sites: []lint.Definition{d}})
	}
	return out
}

// placeOf names where an anchor is in src, for a message: the line and the heading path.
func placeOf(src []byte, a anchor.Anchor) string {
	line := bytes.Count(src[:min(a.Start, len(src))], []byte("\n")) + 1
	if len(a.HeadingPath) == 0 {
		return fmt.Sprintf("line %d", line)
	}
	return fmt.Sprintf("line %d, under %s", line, strings.Join(a.HeadingPath, " / "))
}

type conflict struct {
	Analysis    string `json:"analysis"`
	BothCanHold bool   `json:"both_can_hold"`
	ThisQuote   string `json:"this_quote"`
	OtherQuote  string `json:"other_quote"`
	Explanation string `json:"explanation"`
}

// contradictionData is this doc as the contradiction call reads it: the main doc and its text
// assets.
func contradictionData(in input) string {
	return bundleData(in.bundle.DocPath, in.main, textAssets(in))
}

// contradictionKey is the cache key of the contradiction call with one linked doc (#132). It
// holds only what the model reads: this doc as data, and the other doc's main doc. An edit to a
// binary asset, or a new version of the other doc that changes only its sidecar or its assets,
// such as a waiver approved on it, keeps the answer.
func contradictionKey(this string, l linked, profileVer int64, fingerprint string) cacheKey {
	return cacheKey{Step: "contradiction", InputHash: hashOf(this, l.target.ID.String(), version.Hash(l.main)),
		ProfileVer: profileVer, Fingerprint: fingerprint, PromptVersion: PromptContradiction, Extra: l.kind}
}

// contradictionStage asks the reviewer for conflicts between the doc and each linked doc
// (REQ-054). A conflict is kept only when both quotes are in their docs; it becomes a MUST
// finding anchored in this doc, with the other doc's anchor in its evidence.
func (s *Service) contradictionStage(ctx context.Context, rc *runCtx, in input, ev *evaluation, fingerprint string) error {
	if standalone(in.dec) {
		return nil
	}
	var targets []linked
	for _, l := range in.linked {
		if slices.Contains(contradictionKinds, l.kind) {
			targets = append(targets, l)
		}
	}
	lvl := in.level(ContradictionSlug, kernel.Must)
	this := contradictionData(in)
	for i, l := range targets {
		rc.publish(Event{Type: "progress", Stage: StageCoherence, Message: "Comparing with " + l.target.Slug, Done: i, Total: len(targets)})
		key := contradictionKey(this, l, in.profile.Version, fingerprint)
		var out struct {
			Conflicts []conflict `json:"conflicts"`
		}
		ok, err := s.cached(ctx, key, &out)
		if err != nil {
			return err
		}
		if ok {
			rc.hit()
		} else {
			res, err := rc.call(ctx, s.Gateway, model.Call{
				Role: model.RoleReviewer, PromptVersion: PromptContradiction, System: systemPrompt,
				Prompt: contradictionPrompt(l.kind, this, data("Other doc "+l.target.Slug, string(l.main))),
				Schema: contradictionSchema, MaxTokens: 4000,
			})
			if err != nil {
				return err
			}
			if err := json.Unmarshal(res.JSON, &out); err != nil {
				return err
			}
			if err := s.putCache(ctx, key, out); err != nil {
				return err
			}
		}
		kept, dropped := 0, 0
		for _, c := range out.Conflicts {
			if c.BothCanHold {
				continue // the reviewer found that a builder can follow both
			}
			ts, te, ok1 := anchor.Find(in.main, c.ThisQuote)
			os, oe, ok2 := anchor.Find(l.main, c.OtherQuote)
			if !ok1 || !ok2 {
				dropped++ // the REQ-043 rule: a quote that is not in the doc is not evidence
				continue
			}
			kept++
			ev.findings = append(ev.findings, pending{
				slug: ContradictionSlug, level: lvl, stage: StageCoherence,
				anchor:  anchor.New(in.bundle.DocPath, in.main, in.doc, ts, te),
				message: fmt.Sprintf("This conflicts with %s: %s", l.target.Slug, sentence(c.Explanation)),
				fix:     fmt.Sprintf("Change this doc or %s so that both say the same thing.", l.target.Slug),
				evidence: map[string]any{"upstream": l.target.Slug, "upstream_bundle_id": l.target.ID, "explanation": c.Explanation,
					"quote": c.ThisQuote, "upstream_quote": c.OtherQuote,
					"upstream_anchor": anchor.New(l.target.DocPath, l.main, l.doc, os, oe)},
			})
		}
		if dropped > 0 {
			rc.note(fmt.Sprintf("%d possible conflicts with %s quoted text that is not in the docs, so they were dropped.", dropped, l.target.Slug))
		}
		ev.items = append(ev.items, verdict.Item{Slug: ContradictionSlug, Category: verdict.Coherence, Level: lvl, Passed: kept == 0, Applicable: true})
	}
	return nil
}
