// Package insights computes the metrics of SDD §8.9 for each profile (REQ-092), from the runs,
// verdicts, findings, waivers, and bundle status that the store holds.
package insights

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/store"
)

// API serves insights.
type API struct {
	DB        *store.DB
	Workspace uuid.UUID
	Profiles  func() map[string]profile.Versioned
}

type bundleStats struct {
	runsToReady int           // full runs up to and including the first build_ready
	toReady     time.Duration // from the first run to the first build_ready
	ready       bool          // a first build_ready exists
}

// GetInsights returns the metrics per profile.
func (a *API) GetInsights(ctx context.Context, _ api.GetInsightsRequestObject) (api.GetInsightsResponseObject, error) {
	q := a.DB.Queries()
	var bundles []pgdb.Bundle
	after := ""
	for {
		page, err := q.ListBundles(ctx, pgdb.ListBundlesParams{WorkspaceID: a.Workspace, AfterSlug: after, PageSize: 200})
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
		st := stats[r.BundleID]
		if st == nil {
			st = &bundleStats{}
			stats[r.BundleID] = st
			first[r.BundleID] = r.StartedAt
		}
		if !st.ready {
			st.runsToReady++
			if r.VerdictResult.Valid && r.VerdictResult.String == "build_ready" {
				st.ready = true
				st.toReady = r.StartedAt.Sub(first[r.BundleID])
			}
		}
		if r.VerdictResult.Valid {
			current[r.BundleID] = r.VerdictResult.String + "|" + r.VersionID.String()
		}
	}
	waivers, err := q.ListWorkspaceWaivers(ctx, a.Workspace)
	if err != nil {
		return nil, err
	}

	byProfile := map[string][]pgdb.Bundle{}
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
			if sv, err := q.GetBundleStatusView(ctx, b.ID); err == nil && sv.ReviewRequestedAt.Valid && sv.ApprovedAt.Valid {
				hoursApproval = append(hoursApproval, sv.ApprovedAt.Time.Sub(sv.ReviewRequestedAt.Time).Hours())
			}
			if standalone(ctx, q, b) {
				pi.Standalone++
			}
		}
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
			if !ids[w.BundleID] {
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
func standalone(ctx context.Context, q store.Querier, b pgdb.Bundle) bool {
	if !b.CurrentVersionID.Valid {
		return false
	}
	files, err := version.Files(ctx, q, b.CurrentVersionID.UUID)
	if err != nil {
		return false
	}
	for _, f := range files {
		if f.Path == b.MainDoc {
			fm, _, _ := source.ReadFrontmatter(f.Content)
			return fm.Standalone != nil && strings.TrimSpace(fm.Standalone.Reason) != ""
		}
	}
	return false
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
