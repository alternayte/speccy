package review

import (
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/engine/section"
)

// waiverKey is a check in a section.
func waiverKey(check string, path []string) string {
	return check + "\x00" + strings.Join(path, "\x00")
}

// validWaivers returns the sidecar waivers that still hold: a reason, and a section hash equal
// to the section's hash now (REQ-074). DEC-009: a waiver written into the sidecar by hand is
// honoured the same way.
func validWaivers(in input) map[string]bool {
	out := map[string]bool{}
	for _, w := range in.dec.Waivers {
		if strings.TrimSpace(w.Reason) == "" || w.Check == "" {
			continue
		}
		if h, ok := section.HashAt(in.doc, in.main, w.Section); ok && h == w.SectionHash {
			out[waiverKey(w.Check, w.Section)] = true
		}
	}
	return out
}

// applyWaivers marks the findings that a valid waiver covers, and the items whose every
// finding is waived (§8.7: a waived item counts as passed).
func applyWaivers(in input, ev *evaluation) []bool {
	valid := validWaivers(in)
	waived := make([]bool, len(ev.findings))
	open := map[string]bool{}
	for i, f := range ev.findings {
		path := f.anchor.HeadingPath
		if path == nil || in.profile.Profile.DocScope(f.slug) {
			path = []string{} // a doc-scope check is waived for the whole doc
		}
		waived[i] = valid[waiverKey(f.slug, path)]
		if !waived[i] {
			open[f.slug] = true
		}
	}
	for i, it := range ev.items {
		if it.Passed || it.Slug == "" || open[it.Slug] {
			continue
		}
		if slices.ContainsFunc(ev.findings, func(f pending) bool { return f.slug == it.Slug }) {
			ev.items[i].Waived = true
		}
	}
	return waived
}
