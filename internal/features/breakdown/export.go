package breakdown

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/kernel"
)

// Export writes the breakdown as markdown or CSV, for a team with no tracker connection. Each
// item carries the Speccy block a push would write. An export records no handoff, so its blocks
// name none.
func (a *API) Export(ctx context.Context, docID uuid.UUID, format string) ([]byte, error) {
	v, err := a.view(ctx, a.DB.Queries(), docID)
	if err != nil {
		return nil, err
	}
	sum := v.Row.Verdict
	switch format {
	case "markdown":
		var sb strings.Builder
		fmt.Fprintf(&sb, "# Breakdown of %s (version %d)\n\n", v.Bundle.Title, v.VersionNumber)
		for _, e := range v.Body.Epics {
			fmt.Fprintf(&sb, "## %s\n\n", e.Title)
			for _, it := range v.Body.Items {
				if it.Epic != e.ID {
					continue
				}
				fmt.Fprintf(&sb, "### %s\n\n", it.Title)
				if deps := depTitles(v.Body, it); deps != "" {
					fmt.Fprintf(&sb, "Depends on: %s\n\n", deps)
				}
				if as := effective(v.Body, it); as != nil {
					fmt.Fprintf(&sb, "Assignee: %s (%s)\n\n", as.Name, as.Kind)
				}
				// The issue's own headings sit under the item's heading.
				body := issueBody(it, itemAnswers(v.Answers, it), e.Title, a.exportBlock(v, sum, it))
				sb.WriteString(strings.ReplaceAll("\n"+body, "\n## ", "\n#### ")[1:])
				sb.WriteString("\n")
			}
		}
		return []byte(sb.String()), nil
	case "csv":
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)
		_ = w.Write([]string{"epic", "item", "title", "trace_ids", "depends_on", "assignee", "assignee_kind", "repos", "body"})
		for _, it := range v.Body.Items {
			epic := ""
			if e := v.Body.epic(it.Epic); e != nil {
				epic = e.Title
			}
			name, kind := "", ""
			if as := effective(v.Body, it); as != nil {
				name, kind = as.Name, as.Kind
			}
			_ = w.Write([]string{epic, it.ID, it.Title, strings.Join(it.TraceIDs, " "), strings.Join(it.DependsOn, " "),
				name, kind, strings.Join(it.Repos, " "),
				issueBody(it, itemAnswers(v.Answers, it), epic, a.exportBlock(v, sum, it))})
		}
		w.Flush()
		return buf.Bytes(), w.Error()
	}
	return nil, kernel.Invalid("bad_format", "The format must be markdown or csv.")
}

func (a *API) exportBlock(v View, verdict string, it Item) Block {
	blk := a.block(v, uuid.Nil, verdict, it.TraceIDs, it.ID)
	blk.Handoff = ""
	return blk
}

func depTitles(b Body, it Item) string {
	var out []string
	for _, d := range it.DependsOn {
		out = append(out, title(b, d))
	}
	return strings.Join(out, ", ")
}
