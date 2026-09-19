package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/alternayte/speccy/internal/http/api"
)

var verdictText = map[string]string{
	string(api.BuildReady): "Build Ready", string(api.NotBuildReady): "Not Build Ready", string(api.Stale): "Stale",
}

func verdictLabel(r reviewed) string {
	if r.Error != "" {
		return "Review failed"
	}
	v := verdictText[r.Verdict]
	if r.Waivers > 0 {
		v += fmt.Sprintf(" (%d waiver%s)", r.Waivers, plural(r.Waivers))
	}
	return v
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

var levelOrder = map[api.FindingLevel]int{api.FindingLevelMUST: 0, api.FindingLevelSHOULD: 1, api.FindingLevelINFO: 2}

// open returns the findings that count, MUST first, in document order within a level.
func open(r reviewed) []api.Finding {
	var fs []api.Finding
	for _, f := range r.Findings {
		if !f.Waived {
			fs = append(fs, f)
		}
	}
	sort.SliceStable(fs, func(i, j int) bool { return levelOrder[fs[i].Level] < levelOrder[fs[j].Level] })
	return fs
}

// location is "file:line" for a finding, with the file relative to the root.
func location(r reviewed, f api.Finding) string {
	dir := path.Dir(r.MainDoc)
	file := path.Join(dir, f.Anchor.File)
	if src, ok := r.lines[f.Anchor.File]; ok && f.Anchor.Start <= len(src) {
		return fmt.Sprintf("%s:%d", file, bytes.Count(src[:f.Anchor.Start], []byte("\n"))+1)
	}
	return file
}

// writeResults prints each bundle's verdict and findings (REQ-121).
func writeResults(w io.Writer, format string, rs []reviewed) {
	switch format {
	case "json":
		out := struct {
			Bundles []reviewed `json:"bundles"`
		}{Bundles: nonNilResults(rs)}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	case "md":
		for _, r := range rs {
			fmt.Fprintf(w, "## %s: %s\n\n", r.Path, verdictLabel(r))
			if r.Error != "" {
				fmt.Fprintf(w, "%s\n\n", r.Error)
				continue
			}
			fmt.Fprintf(w, "Profile `%s`. Score %d. %d MUST, %d SHOULD, %d INFO.", r.Profile, r.Score, r.Must, r.Should, r.Info)
			if r.Kind == "lint" {
				fmt.Fprint(w, " Lint checks only.")
			}
			fmt.Fprint(w, "\n\n")
			if r.Relaxed > 0 {
				fmt.Fprintf(w, "Adoption mode: %d check%s relaxed.\n\n", r.Relaxed, plural(r.Relaxed))
			}
			for _, n := range r.Notes {
				fmt.Fprintf(w, "> %s\n\n", n)
			}
			fs := open(r)
			if len(fs) == 0 {
				fmt.Fprint(w, "No findings.\n\n")
				continue
			}
			fmt.Fprint(w, "| Level | Check | Where | Finding |\n|---|---|---|---|\n")
			for _, f := range fs {
				msg := strings.ReplaceAll(f.Message, "|", `\|`)
				if f.Fix != nil {
					msg += " Fix: " + strings.ReplaceAll(*f.Fix, "|", `\|`)
				}
				fmt.Fprintf(w, "| %s | `%s` | `%s` | %s |\n", f.Level, f.CheckSlug, location(r, f), strings.ReplaceAll(msg, "\n", " "))
			}
			fmt.Fprintln(w)
		}
	default:
		for i, r := range rs {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "%s  %s  %s\n", r.Path, strings.ToUpper(r.Profile), verdictLabel(r))
			if r.Error != "" {
				fmt.Fprintf(w, "  %s\n", r.Error)
				continue
			}
			kind := ""
			if r.Kind == "lint" {
				kind = ", lint checks only"
			}
			fmt.Fprintf(w, "  Score %d. %d MUST, %d SHOULD, %d INFO%s.\n", r.Score, r.Must, r.Should, r.Info, kind)
			if r.Relaxed > 0 {
				fmt.Fprintf(w, "  Adoption mode: %d check%s relaxed.\n", r.Relaxed, plural(r.Relaxed))
			}
			for _, n := range r.Notes {
				fmt.Fprintf(w, "  Note: %s\n", n)
			}
			for _, f := range open(r) {
				fmt.Fprintf(w, "  %-6s %s  %s\n         %s\n", f.Level, location(r, f), f.CheckSlug, f.Message)
				if f.Fix != nil {
					fmt.Fprintf(w, "         Fix: %s\n", *f.Fix)
				}
			}
		}
	}
}

// summaryRow is one bundle in the --summary table (REQ-134).
type summaryRow struct {
	Path    string   `json:"path"`
	Profile string   `json:"profile"`
	Verdict string   `json:"verdict"`
	Score   int      `json:"score"`
	Top     []string `json:"top_failing_checks"`
	Error   string   `json:"error,omitempty"`
}

// topChecks returns the slugs of the failing checks, the most severe first, then by count.
func topChecks(r reviewed, n int) []string {
	count := map[string]int{}
	level := map[string]int{}
	for _, f := range open(r) {
		if f.Level == api.FindingLevelINFO {
			continue // INFO is for information; it never fails a check that blocks or advises
		}
		count[f.CheckSlug]++
		if l, ok := level[f.CheckSlug]; !ok || levelOrder[f.Level] < l {
			level[f.CheckSlug] = levelOrder[f.Level]
		}
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if level[a] != level[b] {
			return level[a] < level[b]
		}
		if count[a] != count[b] {
			return count[a] > count[b]
		}
		return a < b
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// writeSummary prints one table for all bundles, then the totals (REQ-134).
func writeSummary(w io.Writer, format string, rs []reviewed) {
	rows := make([]summaryRow, 0, len(rs))
	common := map[string]int{}
	ready := 0
	for _, r := range rs {
		row := summaryRow{Path: r.Path, Profile: r.Profile, Verdict: r.Verdict, Score: r.Score, Top: topChecks(r, 3), Error: r.Error}
		if row.Top == nil {
			row.Top = []string{}
		}
		rows = append(rows, row)
		if r.Verdict == string(api.BuildReady) && r.Error == "" {
			ready++
		}
		seen := map[string]bool{}
		for _, f := range open(r) {
			if !seen[f.CheckSlug] && f.Level != api.FindingLevelINFO {
				seen[f.CheckSlug] = true
				common[f.CheckSlug]++
			}
		}
	}
	top := sortedCounts(common)
	if len(top) > 5 {
		top = top[:5]
	}
	type commonCheck struct {
		Check   string `json:"check"`
		Bundles int    `json:"bundles"`
	}
	commons := make([]commonCheck, 0, len(top))
	for _, c := range top {
		commons = append(commons, commonCheck{Check: c, Bundles: common[c]})
	}
	switch format {
	case "json":
		out := struct {
			Bundles []summaryRow `json:"bundles"`
			Totals  struct {
				Bundles     int           `json:"bundles"`
				BuildReady  int           `json:"build_ready"`
				MostFailing []commonCheck `json:"most_common_failing_checks"`
			} `json:"totals"`
		}{Bundles: rows}
		out.Totals.Bundles, out.Totals.BuildReady, out.Totals.MostFailing = len(rows), ready, commons
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	case "md":
		fmt.Fprint(w, "| Path | Profile | Verdict | Score | Top failing checks |\n|---|---|---|---|---|\n")
		for i, r := range rows {
			fmt.Fprintf(w, "| `%s` | %s | %s | %d | %s |\n", r.Path, r.Profile, verdictLabel(rs[i]), r.Score, codeList(r.Top))
		}
		fmt.Fprintf(w, "\n%d bundle%s. %d Build Ready.", len(rows), plural(len(rows)), ready)
		if len(commons) > 0 {
			fmt.Fprint(w, " Most common failing checks:")
			for i, c := range commons {
				sep := ","
				if i == len(commons)-1 {
					sep = "."
				}
				fmt.Fprintf(w, " `%s` (%d)%s", c.Check, c.Bundles, sep)
			}
		}
		fmt.Fprintln(w)
	default:
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PATH\tPROFILE\tVERDICT\tSCORE\tTOP FAILING CHECKS")
		for i, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", r.Path, r.Profile, verdictLabel(rs[i]), r.Score, strings.Join(r.Top, ", "))
		}
		_ = tw.Flush()
		fmt.Fprintf(w, "\n%d bundle%s, %d Build Ready.\n", len(rows), plural(len(rows)), ready)
		if len(commons) > 0 {
			fmt.Fprintln(w, "Most common failing checks:")
			for _, c := range commons {
				fmt.Fprintf(w, "  %s  in %d bundle%s\n", c.Check, c.Bundles, plural(c.Bundles))
			}
		}
	}
}

func codeList(xs []string) string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "`" + x + "`"
	}
	return strings.Join(out, ", ")
}

func nonNilResults(rs []reviewed) []reviewed {
	out := make([]reviewed, len(rs))
	for i, r := range rs {
		if r.Findings == nil {
			r.Findings = []api.Finding{}
		}
		if r.Notes == nil {
			r.Notes = []string{}
		}
		out[i] = r
	}
	return out
}
