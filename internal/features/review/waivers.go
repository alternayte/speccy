package review

import (
	"slices"
	"strings"

	"github.com/alternayte/speccy/internal/source"
)

// waiverKey is a check in a section.
func waiverKey(check string, path []string) string {
	return check + "\x00" + strings.Join(path, "\x00")
}

// waiverHash is the hash a sidecar waiver recorded: of the check for a waiver of a whole-doc
// check, or of its section.
func waiverHash(w source.Waiver) string {
	if w.CheckHash != "" {
		return w.CheckHash
	}
	return w.SectionHash
}

// validWaivers returns the sidecar waivers that still hold: a reason, and a hash equal to the
// hash now (REQ-074). A waiver of a section holds while the section is the same. A waiver of a
// whole-doc check holds while the check asks the same thing, so an edit does not end it.
// DEC-009: a waiver written into the sidecar by hand is honoured the same way.
func validWaivers(in input) map[string]bool {
	out := map[string]bool{}
	for _, w := range in.dec.Waivers {
		if strings.TrimSpace(w.Reason) == "" || w.Check == "" {
			continue
		}
		if in.profile.Profile.Holds(w.Check, w.Section, waiverHash(w), in.doc, in.main) {
			out[waiverKey(w.Check, w.Section)] = true
		}
	}
	return out
}

// applyWaivers marks the findings that a valid waiver covers, and the items whose every
// finding is waived (§8.7: a waived item counts as passed). One waiver covers every shortfall
// of its check in its section.
func applyWaivers(in input, ev *evaluation) []bool {
	valid := validWaivers(in)
	waived := make([]bool, len(ev.findings))
	open := map[string]bool{}
	for i, f := range ev.findings {
		if b, ok := in.profile.Profile.Bind(f.slug, in.doc, in.main, f.anchor.HeadingPath); ok {
			// A waiver of the whole doc text, from before the check named a section, still
			// covers the check while the doc is the text it was approved for.
			waived[i] = valid[waiverKey(f.slug, b.Path)] || valid[waiverKey(f.slug, nil)]
		}
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
