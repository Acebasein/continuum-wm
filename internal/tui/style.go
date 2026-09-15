package tui

import "github.com/charmbracelet/lipgloss"

// Global style palette for profile-settings, defined once and reused by
// every screen -- deliberately NOT adapting to the terminal's own
// theme/palette (predefined color schemes, matugen-generated dynamic
// colors, etc. vary too much to guess reliable contrast against).
// Instead this owns both a fixed dark canvas background AND its own
// foreground colors, guaranteeing readable contrast regardless of
// whatever theme the user's terminal or WM happens to be running.
//
// Two distinct, both-visible accent families are used deliberately:
// titles/selection (warm amber) vs. footer navigation hints (cool
// blue/turquoise) -- kept in separate color families on purpose, so
// "what you're selecting" and "how to navigate" read as visually
// distinct hierarchy rather than blurring together.
var (
	colorBG      = lipgloss.Color("#1a1b26") // dark canvas background
	colorAccent  = lipgloss.Color("#39BAE6") // sky blue -- titles (kept distinct from the gold row-highlight so the two don't blur together)
	colorFooter  = lipgloss.Color("#DABAFA") // soft lavender -- footer hint lines, kept prominent
	colorMuted   = lipgloss.Color("#565f89") // soft gray -- secondary labels
	colorSuccess = lipgloss.Color("#9ece6a") // green -- confirmations (saved/added/removed)
	colorError   = lipgloss.Color("#f7768e") // red -- errors
	colorWarning = lipgloss.Color("#e0af68") // amber -- destructive-action confirmations
)

var (
	styleCanvas = lipgloss.NewStyle().
			Background(colorBG).
			Padding(1, 2)

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	styleCursor = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleSuccess = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleError = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorError)

	styleWarning = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWarning)

	styleFooter = lipgloss.NewStyle().
			Foreground(colorFooter)
)

// renderCanvas wraps a fully-composed screen body in the shared dark
// canvas background. Applied at the single point every screen's output
// funnels through (View()), not per-screen -- keeps the whole TUI
// visually consistent by construction rather than by convention.
//
// NOTE: this colors the background behind the rendered text block itself
// (lipgloss paints background color per styled line). It does NOT take
// over the full terminal viewport the way an alt-screen application
// (vim, htop) does -- area outside the block still shows the terminal's
// own background. That's a deliberate scope limit for tonight: a true
// full-viewport takeover is a bigger interaction change (tea.WithAltScreen)
// worth its own decision, not bundled into a color palette change.
func renderCanvas(body string) string {
	return styleCanvas.Render(body)
}

// styledStatus renders a status/confirmation message with a checkmark
// icon in the success color -- used consistently everywhere a
// save/add/remove/forget action confirms successfully.
func styledStatus(msg string) string {
	return styleSuccess.Render("✓ " + msg)
}

// styleSelectedRow gives the currently-selected row a full-line
// background highlight, not just a colored cursor arrow -- a clearer,
// more standard selection indicator (matching common patterns like
// lazygit/k9s's row highlighting) than color-only cues.
var styleSelectedRow = lipgloss.NewStyle().
	Background(lipgloss.Color("#E4C44A")). // warm gold highlight, per direct user preference (subtler #283457 wasn't visible enough)
	Foreground(colorBG).                   // dark canvas color reused as foreground, for contrast against the bright background
	Bold(true)

// renderRow renders one selectable list row, with a full-line background
// highlight (not just the cursor arrow) when selected. Highlights the
// row's actual content width, not padded to the full terminal width --
// that would need tracking the terminal size (tea.WindowSizeMsg), a
// separate, larger change deliberately left out of this pass.
func renderRow(text string, selected bool) string {
	if !selected {
		return "  " + text
	}
	return styleSelectedRow.Render("❯ " + text)
}

// cursorPrefix returns the styled selection cursor ("❯ ") when selected
// is true, or two blank spaces (unstyled, for alignment) otherwise. Kept
// for places that need just the arrow without a full-row highlight (none
// currently, but harmless to retain).
func cursorPrefix(selected bool) string {
	if selected {
		return styleCursor.Render("❯ ")
	}
	return "  "
}
