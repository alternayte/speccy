// Package tui is the terminal UI (REQ-122): it lists bundles, runs a review, shows the
// verdict and the findings, steps through the tour, and opens a file in $EDITOR. It uses the
// HTTP API, as the browser does.
package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/http/api"
)

// Options are what the TUI needs from the command.
type Options struct {
	Client *api.ClientWithResponses
	// Root is the folder on disk that holds the bundles; files open from it.
	Root string
	// BundleDir returns the folder of a bundle relative to Root.
	BundleDir func(slug string) string
}

// Run shows the TUI until the user quits.
func Run(ctx context.Context, o Options) error {
	m := &model{ctx: ctx, o: o, screen: screenList}
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	// Ctrl+C in the shell ends ctx: that is a normal quit.
	if ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
		err = nil
	}
	return err
}

type screen int

const (
	screenList screen = iota
	screenBundle
	screenTour
)

type model struct {
	ctx    context.Context
	o      Options
	screen screen
	width  int
	height int

	bundles []api.Bundle
	cursor  int

	bundle   *api.Bundle
	findings []api.Finding
	fcursor  int

	tour    []api.TourPoint
	tcursor int

	help    bool   // the key overlay
	running string // the run in progress, or ""
	stage   string
	status  string // one line of feedback
	err     error
}

// Messages from commands.
type (
	bundlesMsg  []api.Bundle
	findingsMsg struct {
		bundle   api.Bundle
		findings []api.Finding
	}
	tourMsg     []api.TourPoint
	startedMsg  string
	progressMsg struct {
		run   api.Run
		stage string
	}
	editorMsg struct{ err error }
	errMsg    struct{ err error }
	tickMsg   struct{}
)

func (m *model) Init() tea.Cmd { return tea.Batch(m.loadBundles(), tick()) }

func tick() tea.Cmd { return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func problem(p *api.Problem, status int) error {
	if p != nil && p.Detail != nil {
		return errors.New(*p.Detail)
	}
	return fmt.Errorf("the API answered with status %d", status)
}

func (m *model) loadBundles() tea.Cmd {
	return func() tea.Msg {
		limit := api.Limit(100)
		var out []api.Bundle
		var cursor *api.Cursor
		for {
			res, err := m.o.Client.ListBundlesWithResponse(m.ctx, &api.ListBundlesParams{Limit: &limit, Cursor: cursor})
			if err != nil {
				return errMsg{err}
			}
			if res.JSON200 == nil {
				return errMsg{problem(res.ApplicationproblemJSONDefault, res.StatusCode())}
			}
			out = append(out, res.JSON200.Items...)
			if res.JSON200.NextCursor == nil || *res.JSON200.NextCursor == "" {
				return bundlesMsg(out)
			}
			next := *res.JSON200.NextCursor
			cursor = &next
		}
	}
}

func (m *model) loadBundle(id uuid.UUID) tea.Cmd {
	return func() tea.Msg {
		res, err := m.o.Client.GetBundleWithResponse(m.ctx, id)
		if err != nil {
			return errMsg{err}
		}
		if res.JSON200 == nil {
			return errMsg{problem(res.ApplicationproblemJSONDefault, res.StatusCode())}
		}
		b := *res.JSON200
		out := findingsMsg{bundle: b}
		if b.Verdict != nil {
			fs, err := m.o.Client.ListFindingsWithResponse(m.ctx, b.Verdict.RunId)
			if err != nil {
				return errMsg{err}
			}
			if fs.JSON200 == nil {
				return errMsg{problem(fs.ApplicationproblemJSONDefault, fs.StatusCode())}
			}
			rank := map[api.FindingLevel]int{api.FindingLevelMUST: 0, api.FindingLevelSHOULD: 1, api.FindingLevelINFO: 2}
			for _, f := range fs.JSON200.Items {
				if !f.Waived {
					out.findings = append(out.findings, f)
				}
			}
			sort.SliceStable(out.findings, func(i, j int) bool { return rank[out.findings[i].Level] < rank[out.findings[j].Level] })
		}
		return out
	}
}

func (m *model) loadTour(id uuid.UUID) tea.Cmd {
	return func() tea.Msg {
		res, err := m.o.Client.GetTourWithResponse(m.ctx, id)
		if err != nil {
			return errMsg{err}
		}
		if res.JSON200 == nil {
			return errMsg{problem(res.ApplicationproblemJSONDefault, res.StatusCode())}
		}
		return tourMsg(res.JSON200.Points)
	}
}

func (m *model) startReview(id uuid.UUID) tea.Cmd {
	return func() tea.Msg {
		res, err := m.o.Client.StartRunWithResponse(m.ctx, id, api.StartRunJSONRequestBody{})
		if err != nil {
			return errMsg{err}
		}
		if res.JSON202 == nil {
			return errMsg{problem(res.ApplicationproblemJSONDefault, res.StatusCode())}
		}
		return startedMsg(res.JSON202.Id.String())
	}
}

func (m *model) pollRun(id string) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		rid, err := uuid.Parse(id)
		if err != nil {
			return errMsg{err}
		}
		res, err := m.o.Client.GetRunWithResponse(m.ctx, rid)
		if err != nil {
			return errMsg{err}
		}
		if res.JSON200 == nil {
			return errMsg{problem(res.ApplicationproblemJSONDefault, res.StatusCode())}
		}
		return progressMsg{run: *res.JSON200, stage: res.JSON200.Stage}
	})
}

// openEditor opens a bundle file at a line in $EDITOR, or else in vi.
func (m *model) openEditor(slug, file string, line int) tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	full := filepath.Join(m.o.Root, filepath.FromSlash(path.Join(m.o.BundleDir(slug), file)))
	parts := strings.Fields(editor)
	args := append([]string{}, parts[1:]...)
	switch base := filepath.Base(parts[0]); {
	case base == "code" || base == "cursor":
		args = append(args, "--wait", "--goto", fmt.Sprintf("%s:%d", full, max(line, 1)))
	case base == "zed" || base == "subl":
		args = append(args, "--wait", fmt.Sprintf("%s:%d", full, max(line, 1)))
	case line > 0:
		args = append(args, fmt.Sprintf("+%d", line), full)
	default:
		args = append(args, full)
	}
	c := exec.Command(parts[0], args...) // #nosec G204 -- the user's own $EDITOR on a file in their folder
	return tea.ExecProcess(c, func(err error) tea.Msg { return editorMsg{err} })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case bundlesMsg:
		m.bundles, m.err = msg, nil
		if m.cursor >= len(m.bundles) {
			m.cursor = max(0, len(m.bundles)-1)
		}
	case findingsMsg:
		b := msg.bundle
		m.bundle, m.findings, m.err = &b, msg.findings, nil
		if m.fcursor >= len(m.findings) {
			m.fcursor = max(0, len(m.findings)-1)
		}
	case tourMsg:
		m.tour, m.tcursor, m.screen, m.err = msg, 0, screenTour, nil
	case startedMsg:
		m.running, m.stage, m.status = string(msg), "queued", ""
		return m, m.pollRun(string(msg))
	case progressMsg:
		m.stage = msg.stage
		switch msg.run.Status {
		case api.RunStatusComplete:
			m.running, m.status = "", "The review finished."
			return m, tea.Batch(m.refresh(), m.loadBundles())
		case api.RunStatusFailed:
			m.running, m.status = "", msg.run.Error
			return m, m.refresh()
		}
		return m, m.pollRun(m.running)
	case editorMsg:
		if msg.err != nil {
			m.status = "The editor did not open: " + msg.err.Error() + ". Set $EDITOR."
		}
		return m, m.refresh()
	case errMsg:
		m.err = msg.err
	case tickMsg:
		// Files change on disk and in the app: keep the lists current.
		return m, tea.Batch(m.refresh(), tick())
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) refresh() tea.Cmd {
	if m.bundle != nil && m.screen != screenList {
		return m.loadBundle(m.bundle.Id)
	}
	return m.loadBundles()
}

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case "n":
		if !m.help {
			return m.doNext()
		}
	}
	if m.help {
		// The overlay takes every other key, so nothing moves behind it.
		if k.String() == "esc" {
			m.help = false
		}
		return m, nil
	}
	switch m.screen {
	case screenList:
		switch k.String() {
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.bundles)-1))
		case "k", "up":
			m.cursor = max(m.cursor-1, 0)
		case "enter", "l", "right":
			if len(m.bundles) > 0 {
				b := m.bundles[m.cursor]
				m.bundle, m.findings, m.fcursor, m.screen = &b, nil, 0, screenBundle
				return m, m.loadBundle(b.Id)
			}
		case "r":
			if len(m.bundles) > 0 && m.running == "" {
				b := m.bundles[m.cursor]
				m.bundle = &b
				return m, m.startReview(b.Id)
			}
		}
	case screenBundle:
		switch k.String() {
		case "esc", "h", "left":
			m.screen, m.status = screenList, ""
			return m, m.loadBundles()
		case "j", "down":
			m.fcursor = min(m.fcursor+1, max(0, len(m.findings)-1))
		case "k", "up":
			m.fcursor = max(m.fcursor-1, 0)
		case "r":
			if m.running == "" {
				return m, m.startReview(m.bundle.Id)
			}
		case "t":
			return m, m.loadTour(m.bundle.Id)
		case "e", "enter":
			if len(m.findings) > 0 {
				f := m.findings[m.fcursor]
				return m, m.openEditor(m.bundle.Slug, f.Anchor.File, m.line(f.Anchor))
			}
			return m, m.openEditor(m.bundle.Slug, m.bundle.MainDoc, 0)
		}
	case screenTour:
		switch k.String() {
		case "esc", "h", "left":
			m.screen = screenBundle
		case "j", "down":
			m.tcursor = min(m.tcursor+1, max(0, len(m.tour)-1))
		case "k", "up":
			m.tcursor = max(m.tcursor-1, 0)
		case "e", "enter":
			if len(m.tour) > 0 && m.tour[m.tcursor].Anchor != nil {
				a := *m.tour[m.tcursor].Anchor
				return m, m.openEditor(m.bundle.Slug, a.File, m.line(a))
			}
		}
	}
	return m, nil
}

// line is the 1-based line of an anchor in its file on disk.
func (m *model) line(a api.Anchor) int {
	if m.o.BundleDir == nil || m.bundle == nil {
		return 0
	}
	if a.Detached != nil && *a.Detached {
		return 0
	}
	src, err := os.ReadFile(filepath.Join(m.o.Root, filepath.FromSlash(path.Join(m.o.BundleDir(m.bundle.Slug), a.File))))
	if err != nil || a.Start > len(src) {
		return 0
	}
	return bytes.Count(src[:a.Start], []byte("\n")) + 1
}

func verdictStyled(v *api.BundleVerdict) string {
	if v == nil {
		return faint.Render("Not reviewed")
	}
	label := map[api.VerdictResult]string{api.BuildReady: "Build Ready", api.NotBuildReady: "Not Build Ready", api.Stale: "Stale"}[v.Result]
	if v.WaiverCount > 0 {
		label += fmt.Sprintf(" (%d waiver%s)", v.WaiverCount, map[bool]string{true: "", false: "s"}[v.WaiverCount == 1])
	}
	switch v.Result {
	case api.BuildReady:
		return ok.Render(label)
	case api.NotBuildReady:
		return bad.Render(label)
	}
	return warn.Render(label)
}

func levelStyled(l api.FindingLevel) string {
	switch l {
	case api.FindingLevelMUST:
		return bad.Render("MUST  ")
	case api.FindingLevelSHOULD:
		return warn.Render("SHOULD")
	}
	return faint.Render("INFO  ")
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n <= 1 || len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// next is the next action of the bundle in hand: the one under the cursor on the list, or the
// open one. The server names it, so the TUI says what the web app says (SDD §13.4).
func (m *model) next() *api.NextAction {
	if m.screen == screenList {
		if len(m.bundles) == 0 {
			return nil
		}
		return m.bundles[m.cursor].NextAction
	}
	if m.bundle == nil {
		return nil
	}
	return m.bundle.NextAction
}

// doNext does the named thing, as far as a terminal can: it runs a review, opens the tour, or
// opens the file at the finding. The rest needs the app, and the status line says so.
func (m *model) doNext() (tea.Model, tea.Cmd) {
	n := m.next()
	if n == nil {
		return m, nil
	}
	b := m.bundle
	if m.screen == screenList && len(m.bundles) > 0 {
		x := m.bundles[m.cursor]
		b = &x
	}
	if b == nil {
		return m, nil
	}
	switch n.Kind {
	case api.NextActionKindReview:
		if m.running != "" {
			return m, nil
		}
		m.bundle = b
		return m, m.startReview(b.Id)
	case api.NextActionKindDecide:
		m.bundle = b
		return m, m.loadTour(b.Id)
	case api.NextActionKindFix:
		if m.screen != screenBundle {
			m.bundle, m.findings, m.fcursor, m.screen = b, nil, 0, screenBundle
			return m, m.loadBundle(b.Id)
		}
		if len(m.findings) > 0 {
			f := m.findings[m.fcursor]
			return m, m.openEditor(b.Slug, f.Anchor.File, m.line(f.Anchor))
		}
		return m, m.openEditor(b.Slug, b.MainDoc, 0)
	default:
		m.status = n.Sentence + ": do this in the app."
		return m, nil
	}
}
