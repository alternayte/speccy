// Package insights computes the metrics of SDD §8.9 for each profile (REQ-092), from the runs,
// verdicts, findings, waivers, and bundle status that the store holds.
package insights

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/verify"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// API serves insights.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Profiles  func() map[string]profile.Versioned
	// Decisions returns a bundle's sidecar (DEC-009).
	Decisions func(ctx context.Context, b pgdb.SpecDoc) (source.Decisions, error)
}

type bundleStats struct {
	runsToReady int           // full runs up to and including the first build_ready
	toReady     time.Duration // from the first run to the first build_ready
	ready       bool          // a first build_ready exists
}

// GetInsights returns the metrics per profile.
func (a *API) GetInsights(ctx context.Context, _ api.GetInsightsRequestObject) (api.GetInsightsResponseObject, error) {
	q := a.DB.Queries()
	var bundles []pgdb.SpecDoc
	after := ""
	for {
		page, err := q.ListSpecDocs(ctx, pgdb.ListSpecDocsParams{WorkspaceID: a.Workspace, AfterSlug: after, PageSize: 200})
		if err != nil {
			return nil, err
		}
		for _, b := range page {
			if !b.ArchivedAt.Valid {
				bundles = append(bundles, b)
			}
		}
		if len(page) < 200 {
			break
		}
		after = page[len(page)-1].Slug
	}
	runs, err := q.ListAllRuns(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	stats := map[uuid.UUID]*bundleStats{}
	first := map[uuid.UUID]time.Time{}
	current := map[uuid.UUID]string{} // the latest full verdict of each bundle, with its version
	for _, r := range runs {
		// §8.9 counts review runs: full runs. A lint run happens on every save, and its
		// verdict has no model stages.
		if r.Kind != "full" {
			continue
		}
		st := stats[r.SpecDocID]
		if st == nil {
			st = &bundleStats{}
			stats[r.SpecDocID] = st
			first[r.SpecDocID] = r.StartedAt
		}
		if !st.ready {
			st.runsToReady++
			if r.VerdictResult.Valid && r.VerdictResult.String == "build_ready" {
				st.ready = true
				st.toReady = r.StartedAt.Sub(first[r.SpecDocID])
			}
		}
		if r.VerdictResult.Valid {
			current[r.SpecDocID] = r.VerdictResult.String + "|" + r.VersionID.String()
		}
	}
	waivers, err := q.ListWorkspaceWaivers(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	// REQ-137: a Build Ready handoff that came back blocked says the verdict was wrong. It is
	// the only measure of the review against reality, so it is counted per profile.
	verifications, err := q.ListWorkspaceVerificationRuns(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	handoffs, err := q.ListWorkspaceHandoffs(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	blocked, err := q.ListBuildThreads(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}
	blockedByHandoff := map[uuid.UUID][]pgdb.ThreadView{}
	for _, t := range blocked {
		if t.Blocking && t.HandoffID.Valid {
			blockedByHandoff[t.HandoffID.UUID] = append(blockedByHandoff[t.HandoffID.UUID], t)
		}
	}

	byProfile := map[string][]pgdb.SpecDoc{}
	for _, b := range bundles {
		byProfile[b.ProfileKey] = append(byProfile[b.ProfileKey], b)
	}
	out := api.Insights{Profiles: []api.ProfileInsights{}}
	keys := make([]string, 0)
	for k := range a.Profiles() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := a.Profiles()[key]
		pi := api.ProfileInsights{Key: key, Name: p.Profile.Name, Bundles: len(byProfile[key]),
			TopFailing: []struct {
				CheckSlug string `json:"check_slug"`
				Count     int    `json:"count"`
			}{},
			BlockedSections: []struct {
				Count   int    `json:"count"`
				Section string `json:"section"`
			}{},
			WaiverRate: []struct {
				CheckSlug string `json:"check_slug"`
				Requested int    `json:"requested"`
				Waived    int    `json:"waived"`
			}{},
		}
		var runsTo, hoursTo, hoursApproval []float64
		ids := map[uuid.UUID]bool{}
		for _, b := range byProfile[key] {
			ids[b.ID] = true
			if st := stats[b.ID]; st != nil && st.ready {
				runsTo = append(runsTo, float64(st.runsToReady))
				hoursTo = append(hoursTo, st.toReady.Hours())
			}
			if v, ok := current[b.ID]; ok && b.CurrentVersionID.Valid && v == "build_ready|"+b.CurrentVersionID.UUID.String() {
				pi.BuildReady++
			}
			if sv, err := q.GetSpecDocStatusView(ctx, b.ID); err == nil && sv.ReviewRequestedAt.Valid && sv.ApprovedAt.Valid {
				hoursApproval = append(hoursApproval, sv.ApprovedAt.Time.Sub(sv.ReviewRequestedAt.Time).Hours())
			}
			if a.standalone(ctx, b) {
				pi.Standalone++
			}
		}
		// The rate counts the handoffs taken at Build Ready only: a handoff taken with
		// acknowledged is no evidence against the profile.
		ready, cameBack := 0, 0
		sections := map[string]int{}
		for _, h := range handoffs {
			if !ids[h.SpecDocID] || h.Acknowledged || h.Verdict != "build_ready" {
				continue
			}
			ready++
			ts := blockedByHandoff[h.ID]
			if len(ts) > 0 {
				cameBack++
			}
			for _, t := range ts {
				sections[sectionOf(t.Anchor)]++
			}
		}
		if ready > 0 {
			pi.FalseReadyRate = float32(cameBack) / float32(ready)
		}
		// The breach rate is the share of verified trace IDs that came back breached or
		// missing. A profile whose requirements are built wrong has a weak rubric.
		verified, wrong := 0, 0
		for _, v := range verifications {
			if !ids[v.SpecDocID] {
				continue
			}
			var c verify.Counts
			if err := json.Unmarshal(v.Counts, &c); err != nil {
				continue
			}
			verified += c.Implemented + c.Untested + c.Unproven + c.Missing + c.Breached
			wrong += c.Missing + c.Breached
		}
		pi.VerifiedTraceIds = verified
		if verified > 0 {
			pi.BreachRate = float32(wrong) / float32(verified)
		}
		for name, n := range sections {
			pi.BlockedSections = append(pi.BlockedSections, struct {
				Count   int    `json:"count"`
				Section string `json:"section"`
			}{Count: n, Section: name})
		}
		sort.Slice(pi.BlockedSections, func(i, j int) bool {
			if pi.BlockedSections[i].Count != pi.BlockedSections[j].Count {
				return pi.BlockedSections[i].Count > pi.BlockedSections[j].Count
			}
			return pi.BlockedSections[i].Section < pi.BlockedSections[j].Section
		})
		pi.RunsToBuildReady = float32(median(runsTo))
		pi.HoursToBuildReady = float32(median(hoursTo))
		pi.HoursToApproval = float32(median(hoursApproval))
		failing, err := q.CountFindingsByCheck(ctx, pgdb.CountFindingsByCheckParams{WorkspaceID: a.Workspace, ProfileKey: key})
		if err != nil {
			return nil, err
		}
		for _, f := range failing {
			pi.TopFailing = append(pi.TopFailing, struct {
				CheckSlug string `json:"check_slug"`
				Count     int    `json:"count"`
			}{CheckSlug: f.CheckSlug, Count: int(f.N)})
		}
		rate := map[string][2]int{}
		for _, w := range waivers {
			if !ids[w.SpecDocID] {
				continue
			}
			r := rate[w.CheckSlug]
			r[1]++
			if w.Status == "approved" || w.Status == "invalidated" {
				r[0]++
			}
			rate[w.CheckSlug] = r
		}
		for slug, r := range rate {
			pi.WaiverRate = append(pi.WaiverRate, struct {
				CheckSlug string `json:"check_slug"`
				Requested int    `json:"requested"`
				Waived    int    `json:"waived"`
			}{CheckSlug: slug, Requested: r[1], Waived: r[0]})
		}
		sort.Slice(pi.WaiverRate, func(i, j int) bool { return pi.WaiverRate[i].Requested > pi.WaiverRate[j].Requested })
		out.Profiles = append(out.Profiles, pi)
	}
	return api.GetInsights200JSONResponse(out), nil
}

// standalone reports whether the current main doc acknowledges that it stands alone.
// sectionOf names the section a build report points at, from the thread's anchor.
func sectionOf(raw []byte) string {
	var an struct {
		HeadingPath []string `json:"heading_path"`
	}
	_ = json.Unmarshal(raw, &an)
	if len(an.HeadingPath) == 0 {
		return "the doc"
	}
	return strings.Join(an.HeadingPath, " › ")
}

func (a *API) standalone(ctx context.Context, b pgdb.SpecDoc) bool {
	if a.Decisions == nil {
		return false
	}
	dec, err := a.Decisions(ctx, b)
	if err != nil {
		return false
	}
	return dec.Standalone != nil && strings.TrimSpace(dec.Standalone.Reason) != ""
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}
