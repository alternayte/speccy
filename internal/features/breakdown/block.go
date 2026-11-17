package breakdown

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Block is the machine-readable Speccy block in a pushed issue. A builder reads it to find the
// spec the issue was cut from.
type Block struct {
	Bundle   string   `yaml:"bundle" json:"bundle"`
	Version  int64    `yaml:"version" json:"version"`
	Handoff  string   `yaml:"handoff" json:"handoff"`
	Verdict  string   `yaml:"verdict" json:"verdict"`
	TraceIDs []string `yaml:"trace_ids" json:"trace_ids"`
	Doc      string   `yaml:"doc" json:"doc"`
	WorkItem string   `yaml:"work_item" json:"work_item"`
}

var blockRe = regexp.MustCompile(`(?s)<!--\s*speccy\s*\n(.*?)-->`)

// render writes the block as an HTML comment, so a person reading the issue does not see it.
func (b Block) render() string {
	raw, _ := yaml.Marshal(b)
	return "<!-- speccy\n" + string(raw) + "-->"
}

// ParseBlock reads the Speccy block of an issue body.
func ParseBlock(body string) (Block, bool) {
	m := blockRe.FindStringSubmatch(body)
	if m == nil {
		return Block{}, false
	}
	var b Block
	if err := yaml.Unmarshal([]byte(m[1]), &b); err != nil || b.Bundle == "" {
		return Block{}, false
	}
	return b, true
}

// issueBody is the text of one pushed work item: what a person reads, then the Speccy block.
func issueBody(it Item, answers []Answer, epic string, blk Block) string {
	var sb strings.Builder
	if epic != "" {
		fmt.Fprintf(&sb, "Part of **%s**.\n\n", epic)
	}
	if len(it.TraceIDs) > 0 {
		fmt.Fprintf(&sb, "Trace IDs: %s\n\n", strings.Join(it.TraceIDs, ", "))
	} else if len(it.HeadingPath) > 0 {
		fmt.Fprintf(&sb, "Section: %s\n\n", strings.Join(it.HeadingPath, " > "))
	}
	sb.WriteString("## Acceptance criteria\n\nEach criterion is quoted from the spec. To change one, change the spec.\n\n")
	for _, c := range it.Criteria {
		where := strings.Join(c.HeadingPath, " > ")
		if c.Doc != "" {
			where = c.Doc + ": " + where
		}
		fmt.Fprintf(&sb, "> %s\n\n— %s\n\n", oneLine(c.Quote), where)
	}
	if len(answers) > 0 {
		sb.WriteString("## Build answers\n\nIndependent readers agreed on these answers.\n\n")
		for _, a := range answers {
			fmt.Fprintf(&sb, "- **%s** %s\n", a.Question, a.Answer)
		}
		sb.WriteString("\n")
	}
	if len(it.Questions) > 0 {
		sb.WriteString("## Open questions\n\n")
		for _, q := range it.Questions {
			fmt.Fprintf(&sb, "- %s\n", q)
		}
		sb.WriteString("\n")
	}
	if it.Note != "" {
		fmt.Fprintf(&sb, "## Note\n\n%s\n\n", it.Note)
	}
	fmt.Fprintf(&sb, "Spec: %s (version %d)\n\n", blk.Doc, blk.Version)
	sb.WriteString(blk.render())
	sb.WriteString("\n")
	return sb.String()
}

// epicBody is the text of one pushed epic.
func epicBody(e Epic, items []Item, blk Block) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Epic from the spec %s (version %d).\n\n", blk.Doc, blk.Version)
	for _, it := range items {
		if it.Epic == e.ID {
			fmt.Fprintf(&sb, "- %s\n", it.Title)
		}
	}
	sb.WriteString("\n")
	sb.WriteString(blk.render())
	sb.WriteString("\n")
	return sb.String()
}
