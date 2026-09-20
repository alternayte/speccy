/* Direction: the web app's frame in a cell grid — Linear density, dim rules instead of boxes, and
   Neon's electric green as the only accent (DEC-029). */

package tui

import "github.com/charmbracelet/lipgloss"

// The tokens. Every colour and glyph in the TUI comes from here, and each pair matches the web
// token of the same name in web/src/index.css, so both surfaces read as one product.
var (
	cInk     = lipgloss.AdaptiveColor{Light: "#1b1b1a", Dark: "#ececea"}
	cInk3    = lipgloss.AdaptiveColor{Light: "#686863", Dark: "#85857f"}
	cLine    = lipgloss.AdaptiveColor{Light: "#d4d4ce", Dark: "#333434"}
	cAccent  = lipgloss.AdaptiveColor{Light: "#00794f", Dark: "#00e599"}
	cSoft    = lipgloss.AdaptiveColor{Light: "#e3f5ec", Dark: "#0e2a1f"}
	cOK      = lipgloss.AdaptiveColor{Light: "#047a4a", Dark: "#34d399"}
	cBad     = lipgloss.AdaptiveColor{Light: "#b42318", Dark: "#f87171"}
	cWarn    = lipgloss.AdaptiveColor{Light: "#8a5a00", Dark: "#fbbf24"}
	cAccInk  = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#04140d"}
	markGlyf = "▌" // the selection marker in the left gutter
	ruleGlyf = "─"
)

var (
	bold      = lipgloss.NewStyle().Bold(true)
	faint     = lipgloss.NewStyle().Foreground(cInk3)
	ok        = lipgloss.NewStyle().Foreground(cOK).Bold(true)
	bad       = lipgloss.NewStyle().Foreground(cBad).Bold(true)
	warn      = lipgloss.NewStyle().Foreground(cWarn).Bold(true)
	accent    = lipgloss.NewStyle().Foreground(cAccent)
	titleBar  = lipgloss.NewStyle().Background(cAccent).Foreground(cAccInk).Bold(true).Padding(0, 1)
	barMeta   = lipgloss.NewStyle().Foreground(cInk3)
	colHead   = lipgloss.NewStyle().Foreground(cInk3).Bold(true)
	rowSel    = lipgloss.NewStyle().Background(cSoft).Foreground(cInk)
	mark      = lipgloss.NewStyle().Background(cSoft).Foreground(cAccent)
	rule      = lipgloss.NewStyle().Foreground(cLine)
	keyGlyph  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	keyLabel  = lipgloss.NewStyle().Foreground(cInk3)
	emptyText = lipgloss.NewStyle().Foreground(cInk3).Italic(true)
)
