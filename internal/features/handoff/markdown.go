package handoff

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alternayte/speccy/internal/http/api"
)

// HandoffMarkdown writes the re-entry prompt: what to build, what to read, what is done, and
// what is next (REQ-136). The agent owns this file after the handoff and rewrites it as it
// works, so a session that lost its context resumes from it. Speccy never reads it back.
func HandoffMarkdown(p api.BuildPacket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Build %s\n\n", p.Title)
	fmt.Fprintf(&b, "You are building the design in `%s`. Read it first, in full.\n\n", p.MainDoc)
	b.WriteString("This file is yours. Tick a line when its work is done and the tests for it pass. ")
	b.WriteString("Read this file first when you resume, so you continue instead of starting again.\n\n")

	b.WriteString("## Read\n\n")
	fmt.Fprintf(&b, "- `%s` — the design, version %d.\n", p.MainDoc, p.VersionNumber)
	for _, f := range p.Files {
		if f.Path != p.MainDoc {
			fmt.Fprintf(&b, "- `%s` — an asset of the design.\n", f.Path)
		}
	}
	for _, l := range p.Links {
		fmt.Fprintf(&b, "- `%s` — the %s doc this design %s.\n", l.Path, l.Title, l.Kind)
	}
	b.WriteString("\n")

	if len(p.Questions) > 0 {
		b.WriteString("## Answers\n\n")
		b.WriteString("Independent readers answered these questions from the doc alone. Build to these answers.\n\n")
		for _, q := range p.Questions {
			switch {
			case q.Answer != nil:
				fmt.Fprintf(&b, "- **%s** %s\n", q.Text, *q.Answer)
			case q.Result == api.PacketQuestionResultGap:
				fmt.Fprintf(&b, "- **%s** The doc does not answer this. Ask the author before you build it.\n", q.Text)
			default:
				fmt.Fprintf(&b, "- **%s** The readers read this differently. Ask the author before you build it.\n", q.Text)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Work\n\n")
	if len(p.TraceIds) == 0 {
		b.WriteString("The doc defines no trace IDs, so this list is yours to write. ")
		b.WriteString("Split the design into units of work, one line each, before you start.\n\n")
		b.WriteString("- [ ] \n")
	} else {
		b.WriteString("One line per trace ID in the doc.\n\n")
		for _, t := range p.TraceIds {
			fmt.Fprintf(&b, "- [ ] **%s** %s\n", t.Id, oneLine(t.Text, 160))
		}
	}
	b.WriteString("\n## Done when\n\n")
	b.WriteString("- Every line above is ticked.\n")
	b.WriteString("- The project's own checks pass.\n")
	b.WriteString("- Nothing in the design is left unbuilt without a line here that says why.\n")
	return b.String()
}

// oneLine flattens a definition to one line for the checklist.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max])) + "…"
}

// isText reports whether content is UTF-8 text, so the packet carries it as it is.
func isText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return true
}

func base64Of(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
