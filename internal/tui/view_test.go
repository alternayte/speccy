package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/http/api"
)

// The frame keeps its shape: every line is exactly as wide as the terminal, and the frame is
// exactly as tall. A row that overflows wraps in a real terminal and breaks every row under it,
// which no reader of the code can see.
func TestView_FrameFitsTheTerminal(t *testing.T) {
	st := api.ReviewStatus("in_review")
	v := &api.BundleVerdict{Result: api.NotBuildReady, Score: 45, Must: 2, Should: 111, Info: 51, WaiverCount: 2,
		Kind: api.BundleVerdictKindFull, RunId: uuid.New(), VersionNumber: 7}
	b := api.Bundle{Title: "Speccy — Software Design Document", Slug: "docs/specs/a-rather-long-bundle-slug",
		ProfileKey: "sdd", MainDoc: "SDD.md", CurrentVersion: api.Version{Number: 7}, SourceKind: "local",
		Status: &st, UpdatedAt: time.Now().Add(-70 * time.Minute), Verdict: v}
	fix := strings.Repeat("a long fix that must be cut ", 8)
	fs := []api.Finding{
		{Level: api.FindingLevelMUST, CheckSlug: "sdd.data.model", Stage: "rubric", Fix: &fix,
			Message: strings.Repeat("a long message that must be cut ", 8), Anchor: api.Anchor{File: "SDD.md", Quote: strings.Repeat("quoted ", 40)}},
		{Level: api.FindingLevelSHOULD, CheckSlug: "lint.prose-limit", Stage: "lint", Message: "The doc has 9503 words."},
	}
	tour := []api.TourPoint{{Kind: "divergence", Ask: strings.Repeat("a long question ", 12), Context: strings.Repeat("context ", 30),
		Anchor: &api.Anchor{File: "SDD.md", Quote: strings.Repeat("quoted ", 30)}}}

	models := map[string]*model{
		"list":     {screen: screenList, bundles: []api.Bundle{b, b}},
		"empty":    {screen: screenList},
		"bundle":   {screen: screenBundle, bundle: &b, findings: fs, fcursor: 1},
		"running":  {screen: screenBundle, bundle: &b, findings: fs, running: "x", stage: "divergence"},
		"nofinds":  {screen: screenBundle, bundle: &b},
		"tour":     {screen: screenTour, bundle: &b, tour: tour},
		"tourless": {screen: screenTour, bundle: &b},
		"help":     {screen: screenBundle, bundle: &b, findings: fs, help: true},
	}
	sizes := [][2]int{{80, 24}, {100, 30}, {160, 45}, {60, 12}}
	for name, m := range models {
		for _, s := range sizes {
			m.width, m.height = s[0], s[1]
			w, h := max(s[0], minWidth), max(s[1], minHeight)
			lines := strings.Split(m.View(), "\n")
			if len(lines) != h {
				t.Errorf("%s at %dx%d: %d lines, want %d", name, s[0], s[1], len(lines), h)
			}
			for i, l := range lines {
				if got := lipgloss.Width(l); got != w {
					t.Errorf("%s at %dx%d: line %d is %d cells wide, want %d: %q", name, s[0], s[1], i+1, got, w, l)
				}
			}
		}
	}
}
