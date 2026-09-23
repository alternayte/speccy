package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/alternayte/speccy/internal/http/api"
)

// The frame: a title bar, one body that fills the terminal, a status line that never moves, and
// a key bar. Every screen uses it, so nothing shifts when data arrives.
const (
	minWidth  = 80 // the floor: the body drops columns below it, it never scrolls sideways
	minHeight = 10
)

func (m *model) View() string {
	w, h := m.width, m.height
	if w < minWidth {
		w = minWidth
	}
	if h < minHeight {
		h = minHeight
	}
	title, meta, body, keys := m.screenParts(w, h-5)
	if m.help {
		body, keys = helpBody(m.screen), [][2]string{{"?", "close"}, {"q", "quit"}}
	}
	var b strings.Builder
	// The bar is cut to the width before it is styled: a wrapped bar would push every row down.
	title = truncate(title, max(6, w/3))
	meta = truncate(meta, max(0, w-lipgloss.Width(title)-11))
	b.WriteString(pad(titleBar.Render("Speccy "+title)+" "+barMeta.Render(meta), w) + "\n")
	b.WriteString(strings.Repeat(" ", w) + "\n")
	for _, line := range fit(body, h-5) {
		b.WriteString(pad(line, w) + "\n")
	}
	b.WriteString(pad(m.statusLine(w), w) + "\n")
	b.WriteString(rule.Render(strings.Repeat(ruleGlyf, w)) + "\n")
	b.WriteString(pad(keyBar(keys, w), w))
	return b.String()
}

// screenParts gives the title, the meta text of the bar, the body lines, and the key hints.
func (m *model) screenParts(w, rows int) (string, string, []string, [][2]string) {
	switch m.screen {
	case screenBundle:
		return m.bundleBody(w, rows)
	case screenTour:
		return m.tourBody(w, rows)
	}
	return m.listBody(w, rows)
}

func (m *model) listBody(w, rows int) (string, string, []string, [][2]string) {
	keys := m.keysWithNext([][2]string{{"j/k", "move"}, {"enter", "open"}, {"r", "review"}, {"?", "keys"}, {"q", "quit"}})
	meta := fmt.Sprintf("%d bundle%s", len(m.bundles), plural(len(m.bundles)))
	if len(m.bundles) == 0 {
		return "bundles", meta, center(rows, w, emptyText.Render("No bundles here."),
			faint.Render("A bundle is a folder with one markdown file that has a type in its frontmatter.")), keys
	}
	nameW := clamp(w/3, 16, 44)
	wide := w >= 96
	head := "  " + padRight("BUNDLE", nameW+2)
	if wide {
		head += padRight("PROFILE", 9) + padRight("SCORE", 7)
	}
	head += "VERDICT"
	preview := m.bundlePreview(w)
	out := []string{colHead.Render(head)}
	start, window := window(m.cursor, len(m.bundles), max(1, rows-1-len(preview)))
	for i := start; i < len(m.bundles) && i < start+window; i++ {
		bu := m.bundles[i]
		score := "  –"
		if bu.Verdict != nil {
			score = fmt.Sprintf("%3d", bu.Verdict.Score)
		}
		line := "  " + padRight(truncate(bu.Slug, nameW), nameW+2)
		if wide {
			line += padRight(strings.ToUpper(bu.ProfileKey), 9) + padRight(score, 7)
		}
		line += verdictStyled(bu.Verdict)
		if bu.Verdict != nil && bu.Verdict.Must > 0 {
			line += bad.Render(fmt.Sprintf("  %d MUST", bu.Verdict.Must))
		}
		out = append(out, selectable(line, w, i == m.cursor))
	}
	if more := scrollNote(start, window, len(m.bundles)); more != "" {
		meta += " · " + more
	}
	return "bundles", meta, bottom(out, preview, rows), keys
}

// bundlePreview describes the bundle under the cursor, so the list screen has no empty band and
// the reader sees what enter would open.
func (m *model) bundlePreview(w int) []string {
	out := []string{"", rule.Render(strings.Repeat(ruleGlyf, w)), "", "", "", ""}
	if len(m.bundles) == 0 {
		return out
	}
	b := m.bundles[m.cursor]
	out[2] = "  " + bold.Render(truncate(b.Title, max(10, w-30))) + faint.Render("  "+b.Path)
	out[3] = "  " + verdictStyled(b.Verdict) + countsText(b.Verdict)
	status := "draft"
	if b.Status != nil {
		status = strings.ReplaceAll(string(*b.Status), "_", " ")
	}
	out[4] = "  " + faint.Render(fmt.Sprintf("v%d · %s · %s · changed %s", b.CurrentVersion.Number, b.SourceKind, status, ago(b.UpdatedAt)))
	if b.RunError != nil {
		out[5] = "  " + bad.Render("The last review failed: ") + truncate(*b.RunError, max(10, w-30))
	} else if b.NextAction != nil {
		// The server names the next thing, so the preview and the status line agree.
		out[5] = "  " + keyGlyph.Render("n") + " " + faint.Render(truncate(b.NextAction.Sentence, max(10, w-30)))
	}
	return out
}

// ago is a short age, such as "4m" or "2d".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func (m *model) bundleBody(w, rows int) (string, string, []string, [][2]string) {
	keys := m.keysWithNext([][2]string{{"j/k", "move"}, {"e", "edit"}, {"r", "review"}, {"t", "tour"}, {"esc", "back"}, {"?", "keys"}})
	bu := m.bundle
	meta := fmt.Sprintf("%s · %s · v%d", truncate(bu.Title, 44), strings.ToUpper(bu.ProfileKey), bu.CurrentVersion.Number)
	head := []string{"  " + verdictStyled(bu.Verdict) + countsText(bu.Verdict), ""}
	if bu.RunError != nil {
		head = []string{"  " + bad.Render("The last review failed: ") + truncate(*bu.RunError, w-30), ""}
	}
	detail := m.findingDetail(w)
	listRows := max(3, rows-len(head)-len(detail))
	if len(m.findings) == 0 {
		body := append(head, center(listRows, w, emptyText.Render("No open findings."),
			faint.Render("Press r to review this bundle again."))...)
		return truncate(bu.Slug, 32), meta, bottom(body, detail, rows), keys
	}
	out := append([]string{}, head...)
	out = append(out, colHead.Render("  "+padRight("LEVEL", 8)+padRight("WHERE", 26)+"FINDING"))
	start, win := window(m.fcursor, len(m.findings), listRows-1)
	for i := start; i < len(m.findings) && i < start+win; i++ {
		f := m.findings[i]
		where := f.Anchor.File
		if l := m.line(f.Anchor); l > 0 {
			where = fmt.Sprintf("%s:%d", f.Anchor.File, l)
		}
		line := "  " + levelStyled(f.Level) + "  " + padRight(truncate(where, 24), 26) + truncate(f.Message, max(10, w-40))
		out = append(out, selectable(line, w, i == m.fcursor))
	}
	meta += fmt.Sprintf(" · finding %d of %d", m.fcursor+1, len(m.findings))
	return truncate(bu.Slug, 32), meta, bottom(out, detail, rows), keys
}

// findingDetail is the block under the list: it keeps its height whatever the finding holds, so
// the list above never jumps.
func (m *model) findingDetail(w int) []string {
	out := []string{"", rule.Render(strings.Repeat(ruleGlyf, w)), "", "", ""}
	if len(m.findings) == 0 {
		return out
	}
	f := m.findings[m.fcursor]
	out[2] = "  " + accent.Render(f.CheckSlug) + faint.Render("  "+f.Stage)
	out[3] = "  " + truncate(f.Message, max(10, w-4))
	if f.Fix != nil {
		out[4] = "  " + faint.Render("Fix: "+truncate(*f.Fix, max(10, w-10)))
	} else if q := strings.TrimSpace(f.Anchor.Quote); q != "" {
		out[4] = "  " + faint.Render("│ "+truncate(q, max(10, w-8)))
	}
	return out
}

func (m *model) tourBody(w, rows int) (string, string, []string, [][2]string) {
	keys := m.keysWithNext([][2]string{{"j/k", "next and previous"}, {"e", "edit"}, {"esc", "back"}, {"?", "keys"}})
	if len(m.tour) == 0 {
		return "tour", m.bundle.Slug, center(rows, w, emptyText.Render("Nothing needs a decision."),
			faint.Render("Every point of this bundle is answered.")), keys
	}
	p := m.tour[m.tcursor]
	meta := fmt.Sprintf("%s · %d of %d", m.bundle.Slug, m.tcursor+1, len(m.tour))
	text := lipgloss.NewStyle().Width(max(20, min(w-6, 76)))
	out := []string{
		"  " + progressBar(m.tcursor+1, len(m.tour), min(40, w-20)) +
			faint.Render(fmt.Sprintf("  %d of %d · %s", m.tcursor+1, len(m.tour), strings.ReplaceAll(string(p.Kind), "_", " "))),
		"",
	}
	for _, l := range strings.Split(text.Render(p.Ask), "\n") {
		out = append(out, "  "+bold.Render(l))
	}
	if p.Context != "" {
		out = append(out, "")
		for _, l := range strings.Split(text.Render(p.Context), "\n") {
			out = append(out, "  "+l)
		}
	}
	if p.Anchor != nil && strings.TrimSpace(p.Anchor.Quote) != "" {
		out = append(out, "")
		for _, l := range strings.Split(text.Render(p.Anchor.Quote), "\n") {
			out = append(out, "  "+faint.Render("│ "+l))
		}
	}
	out = append(out, "", faint.Render("  Decide in the app, or with the MCP tool post_message."))
	return "tour", meta, out, keys
}

// statusLine holds one line for progress, feedback, or an error. It is always there, so the body
// above it keeps its height.
func (m *model) statusLine(w int) string {
	switch {
	case m.err != nil:
		return "  " + bad.Render("Error: ") + truncate(m.err.Error(), max(10, w-12))
	case m.running != "":
		return "  " + warn.Render("Reviewing") + faint.Render(": "+m.stage+" — this takes a few minutes")
	case m.status != "":
		return "  " + faint.Render(truncate(m.status, max(10, w-4)))
	}
	if n := m.next(); n != nil {
		return "  " + keyGlyph.Render("n") + " " + truncate(n.Sentence, max(10, w-8))
	}
	return ""
}

// keysWithNext puts the next action first in the key bar. A hidden panel is acceptable, a
// hidden key is not (SDD §13.4).
func (m *model) keysWithNext(keys [][2]string) [][2]string {
	if m.next() == nil {
		return keys
	}
	return append([][2]string{{"n", "do the next thing"}}, keys...)
}

func helpBody(s screen) []string {
	rows := [][2]string{
		{"n", "do the next thing the status line names"},
		{"j / k, ↓ / ↑", "move"},
		{"enter", "open the bundle, or the finding in $EDITOR"},
		{"e", "open the file in $EDITOR at the finding"},
		{"r", "run a full review"},
		{"t", "open the tour"},
		{"esc / h", "go back"},
		{"?", "keys"},
		{"q / ctrl+c", "quit"},
	}
	out := []string{colHead.Render("  KEYS"), ""}
	for _, r := range rows {
		out = append(out, "  "+keyGlyph.Render(padRight(r[0], 14))+r[1])
	}
	out = append(out, "", faint.Render("  A key that a screen does not use does nothing."))
	return out
}

// selectable draws one row: the accent marker and the tinted row of the web app's selection.
func selectable(line string, w int, on bool) string {
	if !on {
		return line
	}
	body := strings.TrimPrefix(line, "  ")
	body = pad(body, max(1, w-2))
	return mark.Render(markGlyf) + rowSel.Render(" "+body)
}

// keyBar renders the key hints. A bar that does not fit drops its middle hints, never its
// first, which is the next action, and never its last, which is the help key.
func keyBar(keys [][2]string, w int) string {
	render := func(ks [][2]string) string {
		parts := make([]string, 0, len(ks))
		for _, k := range ks {
			parts = append(parts, keyGlyph.Render(k[0])+" "+keyLabel.Render(k[1]))
		}
		return "  " + strings.Join(parts, keyLabel.Render(" · "))
	}
	out := render(keys)
	for len(keys) > 2 && lipgloss.Width(out) > w {
		keys = append(keys[:len(keys)-2:len(keys)-2], keys[len(keys)-1])
		out = render(keys)
	}
	return out
}

func progressBar(at, of, w int) string {
	if of <= 0 || w <= 0 {
		return ""
	}
	full := at * w / of
	return accent.Render(strings.Repeat("━", full)) + rule.Render(strings.Repeat("━", w-full))
}

func countsText(v *api.BundleVerdict) string {
	if v == nil {
		return ""
	}
	kind := ""
	if v.Kind == api.BundleVerdictKindLint {
		kind = " · lint checks only"
	}
	return faint.Render(fmt.Sprintf("  %d MUST · %d SHOULD · %d INFO · score %d%s", v.Must, v.Should, v.Info, v.Score, kind))
}

// window is the slice of a list to show, with the cursor kept inside it.
func window(cursor, total, rows int) (int, int) {
	rows = max(1, rows)
	if total <= rows {
		return 0, rows
	}
	start := max(0, min(cursor-rows/2, total-rows))
	return start, rows
}

func scrollNote(start, window, total int) string {
	if total <= window {
		return ""
	}
	return fmt.Sprintf("showing %d–%d", start+1, min(start+window, total))
}

// center puts the lines in the middle of the rows, for an empty state.
func center(rows, w int, lines ...string) []string {
	out := make([]string, 0, rows)
	top := max(0, (rows-len(lines))/2)
	for range top {
		out = append(out, "")
	}
	for _, l := range lines {
		out = append(out, lipgloss.NewStyle().Width(w).Align(lipgloss.Center).Render(l))
	}
	return out
}

// bottom puts the detail block at the foot of the body, so the rule above it always sits in the
// same place and the list does not float in an empty screen.
func bottom(body, detail []string, rows int) []string {
	for len(body) < rows-len(detail) {
		body = append(body, "")
	}
	return append(body, detail...)
}

// fit pads or cuts the body, so the frame has the same height on every screen.
func fit(lines []string, rows int) []string {
	rows = max(1, rows)
	if len(lines) > rows {
		return lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines
}

// pad fills a line to the width, counting cells and not bytes.
func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func padRight(s string, w int) string { return pad(s, w) }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
