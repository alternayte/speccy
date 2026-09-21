package review_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/store/storetest"
)

const upstreamPRD = `---
type: prd
title: Refunds
---

# Refunds

## Requirements

- **REQ-001:** The system MUST refund a card payment within 5 working days of the request.
- **REQ-002:** The system MUST email the customer when the refund is paid.
`

// traceAck is the sidecar of refunds-sdd: REQ-002 is acknowledged as out of scope (DEC-009).
const traceAck = "trace:\n  - id: REQ-002\n    status: out_of_scope\n    reason: The mail service sends it.\n"

// ackPath is the sidecar of the doc at docPath.
func ackPath(docPath string) string { return ".speccy/decisions/" + docPath + ".yaml" }

// sdd returns an SDD that implements the refunds PRD, with extra frontmatter and body text.
func sdd(frontmatter, body string) string {
	return "---\ntype: sdd\ntitle: Refunds design\nlinks:\n  - kind: implements\n    target: refunds-prd\n" + frontmatter +
		"---\n\n# Refunds design\n\n## Design\n\nThe refund worker covers REQ-001 with a daily job.\n" + body
}

func findingsOf(fs []pgdb.Finding, slug string) []pgdb.Finding {
	var out []pgdb.Finding
	for _, f := range fs {
		if f.CheckSlug == slug {
			out = append(out, f)
		}
	}
	return out
}

// T-070
func TestCoherence_UncoveredReqIsMust(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			en := newEnv(t, e, map[string]string{"refunds-prd/PRD.md": upstreamPRD, "refunds-sdd/SPEC.md": sdd("", "")})
			_, fs, v := en.latest(t, "refunds-sdd")
			gaps := findingsOf(fs, review.CoverageSlug)
			if len(gaps) != 1 || gaps[0].Level != "MUST" || !strings.Contains(gaps[0].Message, "REQ-002") {
				t.Fatalf("coverage findings %+v, want one MUST for REQ-002", gaps)
			}
			if !strings.Contains(string(v.BlockingFindingIds), gaps[0].ID.String()) {
				t.Errorf("the coverage gap does not block: %s", v.BlockingFindingIds)
			}
			// An acknowledgement with a reason covers it.
			writeFile(t, en.dir, ackPath("refunds-sdd/SPEC.md"), traceAck)
			if err := en.bundles.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, fs, _ := en.latest(t, "refunds-sdd"); len(findingsOf(fs, review.CoverageSlug)) != 0 {
				t.Errorf("an acknowledged ID is still a gap: %+v", findingsOf(fs, review.CoverageSlug))
			}
		})
	}
}

// T-071
func TestCoherence_StandaloneAck(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			// A link rule would link the SDD to the PRD, but the SDD says it stands alone.
			standaloneSDD := "---\ntype: sdd\ntitle: Refunds design\n---\n\n# Refunds design\n\n" +
				"## Design\n\nThe system MUST refund a card payment within 5 working days of the request.\n"
			en := newEnv(t, e, map[string]string{
				".speccy.yaml":   "map:\n  - glob: \"*.md\"\n    profile: sdd\nlink_rules:\n  - \"sdd-{name}.md implements prd-{name}.md\"\n",
				"prd-refunds.md": upstreamPRD, "sdd-refunds.md": standaloneSDD,
				ackPath("sdd-refunds.md"): "standalone:\n  reason: An internal change.\n  acknowledged_by: nathan\n",
			})
			_, fs, v := en.latest(t, "sdd-refunds")
			for _, slug := range []string{review.CoverageSlug, review.RestatementSlug, review.HasUpstreamSlug} {
				if got := findingsOf(fs, slug); len(got) != 0 {
					t.Errorf("a standalone doc has %s findings: %+v", slug, got)
				}
			}
			var radar map[string]int
			_ = json.Unmarshal(v.Radar, &radar)
			if radar["coherence"] != 100 {
				t.Errorf("coherence radar %d, want 100 (the standalone acknowledgement only)", radar["coherence"])
			}
		})
	}
}

// T-072
func TestCoherence_UpstreamEditStales(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			ack := ""
			pe := newPipeline(t, e, map[string]string{"refunds-prd/PRD.md": upstreamPRD, "refunds-sdd/SPEC.md": sdd(ack, ""), ackPath("refunds-sdd/SPEC.md"): traceAck}, "fake-1")
			q := pe.bundles.DB.Queries()
			summary := func(slug string) *string {
				b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: pe.bundles.Workspace, Slug: slug})
				if err != nil {
					t.Fatal(err)
				}
				v, _, err := review.Summary(ctx, q, b)
				if err != nil || v == nil {
					t.Fatalf("summary: %v, %v", v, err)
				}
				s := string(v.Result)
				if v.StaleReason != nil {
					s += "/" + string(*v.StaleReason)
				}
				return &s
			}
			pe.run(t, "refunds-sdd")
			if got := *summary("refunds-sdd"); strings.HasPrefix(got, "stale") {
				t.Fatalf("a fresh full run reads %s", got)
			}
			pe.write(t, "refunds-prd/PRD.md", strings.Replace(upstreamPRD, "5 working days", "3 working days", 1))
			if got := *summary("refunds-sdd"); got != "stale/upstream_changed" {
				t.Errorf("after an upstream edit the full verdict reads %s, want stale/upstream_changed", got)
			}

			// A lint-only verdict is linted again instead.
			en := newEnv(t, e, map[string]string{"refunds-prd/PRD.md": upstreamPRD, "refunds-sdd/SPEC.md": sdd(ack, ""), ackPath("refunds-sdd/SPEC.md"): traceAck})
			first, _, _ := en.latest(t, "refunds-sdd")
			writeFile(t, en.dir, "refunds-prd/PRD.md", strings.Replace(upstreamPRD, "5 working days", "3 working days", 1))
			if err := en.bundles.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			again, _, _ := en.latest(t, "refunds-sdd")
			if again.ID == first.ID {
				t.Error("the lint verdict was not linted again after the upstream edit")
			}
		})
	}
}

// T-073
func TestCoherence_RestatementShingles(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ack := ""
			restated := "\n## Scope\n\nThe system must refund a card payment within 5 working days of the request, as the PRD says.\n\n" +
				"## Storage\n\nThe refund worker writes each refund to the refunds table with the order ID and the amount.\n"
			en := newEnv(t, e, map[string]string{"refunds-prd/PRD.md": upstreamPRD, "refunds-sdd/SPEC.md": sdd(ack, restated), ackPath("refunds-sdd/SPEC.md"): traceAck})
			_, fs, v := en.latest(t, "refunds-sdd")
			got := findingsOf(fs, review.RestatementSlug)
			if len(got) != 1 || got[0].Level != "SHOULD" || !strings.Contains(got[0].Message, "Link, do not repeat") {
				t.Fatalf("restatement findings %+v, want one SHOULD", got)
			}
			if !strings.Contains(string(got[0].Anchor), "within 5 working days") {
				t.Errorf("the finding anchors on %s, want the restated paragraph", got[0].Anchor)
			}
			if strings.Contains(string(v.BlockingFindingIds), got[0].ID.String()) {
				t.Error("a restatement blocks the verdict")
			}
		})
	}
}

// T-094
func TestConfig_LinkRules(t *testing.T) {
	for _, e := range storetest.Engines() {
		t.Run(e.Name, func(t *testing.T) {
			ctx := context.Background()
			body := "\n# Design\n\nThe refund worker covers REQ-001 and REQ-002 with a daily job.\n"
			en := newEnv(t, e, map[string]string{
				".speccy.yaml":        "map:\n  - glob: docs/prd-*.md\n    profile: prd\n  - glob: docs/sdd-*.md\n    profile: sdd\nlink_rules:\n  - \"docs/sdd-{name}.md implements docs/prd-{name}.md\"\n",
				"docs/prd-refunds.md": upstreamPRD,
				"docs/sdd-refunds.md": body,
				"docs/sdd-orphan.md":  body,
			})
			q := en.bundles.DB.Queries()
			links := func(slug string) []pgdb.Link {
				b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: en.bundles.Workspace, Slug: slug})
				if err != nil {
					t.Fatal(err)
				}
				ls, err := q.ListLinksFrom(ctx, b.ID)
				if err != nil {
					t.Fatal(err)
				}
				return ls
			}
			prd, _ := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: en.bundles.Workspace, Slug: "docs/prd-refunds"})
			got := links("docs/sdd-refunds")
			if len(got) != 1 || got[0].Kind != "implements" || got[0].Origin != "rule" || got[0].TargetBundleID.UUID != prd.ID {
				t.Errorf("sdd-refunds links %+v, want one implements rule link to the PRD", got)
			}
			if got := links("docs/sdd-orphan"); len(got) != 0 {
				t.Errorf("sdd-orphan has links %+v, but docs/prd-orphan.md does not exist", got)
			}
			_, fs, _ := en.latest(t, "docs/sdd-refunds")
			if len(findingsOf(fs, review.HasUpstreamSlug)) != 0 {
				t.Error("the rule link does not satisfy links.has-upstream")
			}
			if _, fs, _ := en.latest(t, "docs/sdd-orphan"); len(findingsOf(fs, review.HasUpstreamSlug)) != 1 {
				t.Error("sdd-orphan has no links.has-upstream finding")
			}
		})
	}
}

// REQ-054: a conflict with a linked doc is a MUST finding anchored in both docs. A conflict
// whose quotes are not in the docs is dropped.
func TestCoherence_Contradiction(t *testing.T) {
	ack := ""
	body := "\n## Timing\n\nThe worker pays each refund within 10 working days. The invented case is handled.\n"
	pe := newPipeline(t, storetest.Engines()[0], map[string]string{"refunds-prd/PRD.md": upstreamPRD, "refunds-sdd/SPEC.md": sdd(ack, body), ackPath("refunds-sdd/SPEC.md"): traceAck}, "fake-1")
	run, fs, v := pe.run(t, "refunds-sdd")
	got := findingsOf(fs, review.ContradictionSlug)
	if len(got) != 1 || got[0].Level != "MUST" {
		t.Fatalf("contradiction findings %+v, want one MUST", got)
	}
	var ev struct {
		Upstream       string
		UpstreamAnchor struct{ Quote string } `json:"upstream_anchor"`
	}
	_ = json.Unmarshal(got[0].Evidence, &ev)
	if !strings.Contains(string(got[0].Anchor), "10 working days") || ev.Upstream != "refunds-prd" || !strings.Contains(ev.UpstreamAnchor.Quote, "5 working days") {
		t.Errorf("anchors: this %s; upstream %+v", got[0].Anchor, ev)
	}
	if !strings.Contains(string(v.BlockingFindingIds), got[0].ID.String()) {
		t.Error("the contradiction does not block")
	}
	if !strings.Contains(string(run.Notes), "were dropped") {
		t.Errorf("the invented conflict is not noted: %s", run.Notes)
	}
}
