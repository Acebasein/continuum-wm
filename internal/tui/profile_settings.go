// Package tui implements `continuum-cli profile-settings` -- a built-in
// terminal UI, not a system dependency. CONFIRMED DELIBERATE CHOICE
// (design doc): niri's own target audience skews toward minimal,
// keyboard-driven setups that often don't have zenity/yad installed
// (confirmed directly: neither was present on the reference machine this
// project was built against). A bubbletea-based TUI ships compiled into
// the same binary as everything else -- no new runtime dependency for
// the end user at all, unlike a GTK dialog tool would be.
//
// SCOPE NOTE: all three screens (Default Monitor, Ignore Apps, Manage
// Disconnected Outputs) are now implemented. Built and verified one at a
// time, in that order, rather than all at once -- this project's
// standing practice throughout.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"continuum-wm/internal/desktopentry"
	"continuum-wm/internal/niri"
	"continuum-wm/internal/preferences"
	"continuum-wm/internal/session"
)

type screen int

const (
	screenMainMenu screen = iota
	screenDefaultMonitor
	screenIgnoreApps
	screenIgnoreAppsAdd
	screenDisconnectedOutputs
)

// outputSummary is one disconnected output's saved-but-unreachable
// entity count, for the Manage Disconnected Outputs screen.
type outputSummary struct {
	Name        string
	EntityCount int
}

// Model is the top-level bubbletea model for profile-settings.
type Model struct {
	screen    screen
	prefs     *preferences.Preferences
	prefsPath string

	menuItems  []string
	menuCursor int

	connectedOutputs []string
	monitorCursor    int
	autoDetected     string

	ignoreCursor int

	addCandidates      []string
	addCursor          int
	addTextInputActive bool
	addTextBuffer      string
	addConfirming      bool
	addSuggestions     []string
	addSuggestCursor   int

	sessionPath          string
	disconnectedOutputs  []outputSummary
	disconnectedCursor   int
	confirmForget        bool

	statusMsg string
	err       error
	quitting  bool
}

// NewModel loads (or defaults) preferences and returns a ready-to-run
// Model. Does NOT query niri yet -- that only happens when the user
// actually opens the Default Monitor screen, so opening the TUI itself
// never fails just because niri happens to be briefly unreachable.
func NewModel() (Model, error) {
	prefsPath, err := preferences.DefaultPath()
	if err != nil {
		return Model{}, err
	}
	prefs, err := preferences.Load(prefsPath)
	if err != nil {
		return Model{}, err
	}

	return Model{
		screen:    screenMainMenu,
		prefs:     prefs,
		prefsPath: prefsPath,
		menuItems: []string{
			"🖥️  Default Monitor",
			"🚫 Ignore Apps",
			"🔌 Manage Disconnected Outputs",
		},
	}, nil
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.screen {
	case screenDefaultMonitor:
		return m.updateDefaultMonitor(keyMsg)
	case screenIgnoreApps:
		return m.updateIgnoreApps(keyMsg)
	case screenIgnoreAppsAdd:
		if m.addTextInputActive {
			if m.addConfirming {
				return m.updateAddNoMatchConfirm(keyMsg)
			}
			return m.updateAddTextInput(keyMsg)
		}
		return m.updateIgnoreAppsAdd(keyMsg)
	case screenDisconnectedOutputs:
		return m.updateDisconnectedOutputs(keyMsg)
	default:
		return m.updateMainMenu(keyMsg)
	}
}

func (m Model) updateMainMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "down", "j":
		if m.menuCursor < len(m.menuItems)-1 {
			m.menuCursor++
		}
	case "enter":
		switch m.menuCursor {
		case 0:
			outputs, err := liveOutputNames()
			if err != nil {
				m.err = err
				return m, nil
			}
			sort.Strings(outputs)
			m.connectedOutputs = outputs
			m.err = nil

			auto, _ := preferences.DetectDefaultOutput(outputs)
			m.autoDetected = auto

			current := m.prefs.DefaultOutput
			if current == "" {
				current = auto
			}
			m.monitorCursor = 0
			for i, o := range outputs {
				if o == current {
					m.monitorCursor = i
				}
			}
			m.screen = screenDefaultMonitor
			m.statusMsg = ""
		case 1:
			m.ignoreCursor = 0
			m.screen = screenIgnoreApps
			m.statusMsg = ""
			m.err = nil
		case 2:
			summaries, sessPath, err := loadDisconnectedOutputs()
			if err != nil {
				m.err = err
				return m, nil
			}
			m.sessionPath = sessPath
			m.disconnectedOutputs = summaries
			m.disconnectedCursor = 0
			m.confirmForget = false
			m.screen = screenDisconnectedOutputs
			m.statusMsg = ""
			m.err = nil
		default:
			m.statusMsg = "This screen isn't built yet -- coming soon."
		}
	}
	return m, nil
}

func (m Model) updateDefaultMonitor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = screenMainMenu
		return m, nil
	case "up", "k":
		if m.monitorCursor > 0 {
			m.monitorCursor--
		}
	case "down", "j":
		if m.monitorCursor < len(m.connectedOutputs)-1 {
			m.monitorCursor++
		}
	case "enter":
		if len(m.connectedOutputs) == 0 {
			return m, nil
		}
		chosen := m.connectedOutputs[m.monitorCursor]
		m.prefs.DefaultOutput = chosen
		if err := preferences.Save(m.prefs, m.prefsPath); err != nil {
			m.err = err
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("Saved: default monitor set to %q", chosen)
		m.screen = screenMainMenu
	}
	return m, nil
}

func (m Model) updateIgnoreApps(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = screenMainMenu
		m.statusMsg = ""
		return m, nil
	case "up", "k":
		if m.ignoreCursor > 0 {
			m.ignoreCursor--
		}
	case "down", "j":
		if m.ignoreCursor < len(m.prefs.IgnoreApps)-1 {
			m.ignoreCursor++
		}
	case "a":
		candidates, err := liveRunningAppIDs()
		if err != nil {
			m.err = err
			return m, nil
		}
		var filtered []string
		for _, c := range candidates {
			if !m.prefs.IsAppIgnored(c) {
				filtered = append(filtered, c)
			}
		}
		m.addCandidates = filtered
		m.addCursor = 0
		m.addTextInputActive = false
		m.addConfirming = false
		m.addTextBuffer = ""
		m.screen = screenIgnoreAppsAdd
		m.err = nil
		m.statusMsg = ""
	case "d":
		if len(m.prefs.IgnoreApps) == 0 {
			return m, nil
		}
		removed := m.prefs.IgnoreApps[m.ignoreCursor]
		m.prefs.RemoveIgnoredApp(removed)
		if err := preferences.Save(m.prefs, m.prefsPath); err != nil {
			m.err = err
			return m, nil
		}
		if m.ignoreCursor >= len(m.prefs.IgnoreApps) && m.ignoreCursor > 0 {
			m.ignoreCursor--
		}
		m.statusMsg = fmt.Sprintf("Removed %q from ignore list", removed)
		m.err = nil
	}
	return m, nil
}

func (m Model) updateIgnoreAppsAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalItems := len(m.addCandidates) + 1 // +1 for "type a custom app_id"
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = screenIgnoreApps
		return m, nil
	case "up", "k":
		if m.addCursor > 0 {
			m.addCursor--
		}
	case "down", "j":
		if m.addCursor < totalItems-1 {
			m.addCursor++
		}
	case "enter":
		if m.addCursor == len(m.addCandidates) {
			m.addTextInputActive = true
			m.addConfirming = false
			m.addTextBuffer = ""
			return m, nil
		}
		chosen := m.addCandidates[m.addCursor]
		if m.prefs.AddIgnoredApp(chosen) {
			if err := preferences.Save(m.prefs, m.prefsPath); err != nil {
				m.err = err
				return m, nil
			}
			m.statusMsg = fmt.Sprintf("Added %q to ignore list", chosen)
			m.err = nil
		}
		m.screen = screenIgnoreApps
	}
	return m, nil
}

// updateAddTextInput handles raw character entry for the "type a custom
// app_id" path -- implemented directly against tea.KeyMsg.Type/Runes
// rather than pulling in the separate bubbles/textinput module for one
// field, consistent with keeping new dependencies to what's genuinely
// needed.
func (m Model) updateAddTextInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyEsc:
		m.addTextInputActive = false
		m.addTextBuffer = ""
		return m, nil
	case tea.KeyEnter:
		appID := strings.TrimSpace(m.addTextBuffer)
		if appID == "" {
			m.addTextInputActive = false
			m.addTextBuffer = ""
			return m, nil
		}

		// FRESH check against live state, not the (possibly stale)
		// candidate list gathered when this screen first opened -- the
		// user may have just opened the app they meant to type.
		candidates, err := liveRunningAppIDs()
		if err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		matched := false
		for _, c := range candidates {
			if c == appID {
				matched = true
				break
			}
		}
		if matched {
			if m.prefs.AddIgnoredApp(appID) {
				if err := preferences.Save(m.prefs, m.prefsPath); err != nil {
					m.err = err
					return m, nil
				}
				m.statusMsg = fmt.Sprintf("Added %q to ignore list", appID)
			}
			m.addTextInputActive = false
			m.addTextBuffer = ""
			m.screen = screenIgnoreApps
			return m, nil
		}

		// No live match -- this is very likely a typo (e.g. "dolphin"
		// instead of the real app_id "org.kde.dolphin") rather than a
		// deliberate preemptive entry, so don't add it silently. Look for
		// a close match first (substring, case-insensitive -- app_ids are
		// conventionally "reverse.domain.AppName", so this catches the
		// common "typed the plain app name" pattern directly). Enter a
		// confirmation sub-state either way: offer the suggestion(s) if
		// any were found, always offer refresh-and-recheck (in case the
		// app genuinely isn't open yet) and an explicit, deliberate
		// escape hatch to add the typed value verbatim anyway.
		m.addTextBuffer = appID // normalize (trimmed)
		m.addSuggestions = findAllSuggestions(appID, candidates)
		m.addSuggestCursor = 0
		m.addConfirming = true
		return m, nil
	case tea.KeyBackspace:
		if len(m.addTextBuffer) > 0 {
			r := []rune(m.addTextBuffer)
			m.addTextBuffer = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeySpace:
		m.addTextBuffer += " "
		return m, nil
	case tea.KeyRunes:
		m.addTextBuffer += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// updateAddNoMatchConfirm handles the "typed app_id doesn't match any
// currently running window" confirmation step. Reached only after
// updateAddTextInput's Enter handler found no live match -- see its
// comment for why this exists rather than adding silently.
func (m Model) updateAddNoMatchConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.addTextInputActive = false
		m.addConfirming = false
		m.addTextBuffer = ""
		m.addSuggestions = nil
		return m, nil
	case "up", "k":
		if len(m.addSuggestions) > 0 && m.addSuggestCursor > 0 {
			m.addSuggestCursor--
		}
		return m, nil
	case "down", "j":
		if len(m.addSuggestions) > 0 && m.addSuggestCursor < len(m.addSuggestions)-1 {
			m.addSuggestCursor++
		}
		return m, nil
	case "enter":
		// Accept the currently-highlighted suggestion, if any are shown.
		if len(m.addSuggestions) == 0 {
			return m, nil
		}
		chosen := m.addSuggestions[m.addSuggestCursor]
		if m.prefs.AddIgnoredApp(chosen) {
			if err := preferences.Save(m.prefs, m.prefsPath); err != nil {
				m.err = err
				return m, nil
			}
			m.statusMsg = fmt.Sprintf("Added %q to ignore list", chosen)
		}
		m.addTextInputActive = false
		m.addConfirming = false
		m.addTextBuffer = ""
		m.addSuggestions = nil
		m.screen = screenIgnoreApps
		return m, nil
	case "r":
		typed := m.addTextBuffer
		candidates, err := liveRunningAppIDs()
		if err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil

		for _, c := range candidates {
			if c == typed {
				if m.prefs.AddIgnoredApp(typed) {
					if err := preferences.Save(m.prefs, m.prefsPath); err != nil {
						m.err = err
						return m, nil
					}
					m.statusMsg = fmt.Sprintf("Added %q to ignore list", typed)
				}
				m.addTextInputActive = false
				m.addConfirming = false
				m.addTextBuffer = ""
				m.addSuggestions = nil
				m.screen = screenIgnoreApps
				return m, nil
			}
		}

		// Still no exact match -- refresh suggestions too (something may
		// have opened in the meantime) and stay in the confirm state.
		m.addSuggestions = findAllSuggestions(typed, candidates)
		m.addSuggestCursor = 0

		var filtered []string
		for _, c := range candidates {
			if !m.prefs.IsAppIgnored(c) {
				filtered = append(filtered, c)
			}
		}
		m.addCandidates = filtered
		m.statusMsg = fmt.Sprintf("Still no match for %q after refreshing.", typed)
		return m, nil
	case "y":
		typed := m.addTextBuffer
		if m.prefs.AddIgnoredApp(typed) {
			if err := preferences.Save(m.prefs, m.prefsPath); err != nil {
				m.err = err
				return m, nil
			}
			m.statusMsg = fmt.Sprintf("Added %q to ignore list (not currently running -- added as typed)", typed)
		}
		m.addTextInputActive = false
		m.addConfirming = false
		m.addTextBuffer = ""
		m.addSuggestions = nil
		m.screen = screenIgnoreApps
		return m, nil
	}
	return m, nil
}

func (m Model) updateDisconnectedOutputs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmForget {
		return m.updateConfirmForget(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = screenMainMenu
		m.statusMsg = ""
		return m, nil
	case "up", "k":
		if m.disconnectedCursor > 0 {
			m.disconnectedCursor--
		}
	case "down", "j":
		if m.disconnectedCursor < len(m.disconnectedOutputs)-1 {
			m.disconnectedCursor++
		}
	case "f":
		if len(m.disconnectedOutputs) == 0 {
			return m, nil
		}
		m.confirmForget = true
	}
	return m, nil
}

// updateConfirmForget handles the "really forget this output?"
// confirmation. Forgetting is a genuinely destructive, irreversible
// action (permanently deletes saved workspace data), so unlike most
// other actions in this TUI it requires an explicit extra confirmation
// step rather than acting immediately on a single keypress.
func (m Model) updateConfirmForget(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc", "n":
		m.confirmForget = false
		return m, nil
	case "y":
		target := m.disconnectedOutputs[m.disconnectedCursor]
		if err := forgetOutput(m.sessionPath, target.Name); err != nil {
			m.err = err
			m.confirmForget = false
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("Forgot output %q (%d entit(y/ies) removed)", target.Name, target.EntityCount)
		m.err = nil
		m.confirmForget = false

		summaries, _, err := loadDisconnectedOutputs()
		if err != nil {
			m.err = err
			return m, nil
		}
		m.disconnectedOutputs = summaries
		if m.disconnectedCursor >= len(m.disconnectedOutputs) && m.disconnectedCursor > 0 {
			m.disconnectedCursor--
		}
	}
	return m, nil
}

// findSuggestions returns every candidate app_id that contains typed as a
// case-insensitive substring -- catches the common pattern of a user
// typing an app's plain name (e.g. "dolphin") instead of its real,
// reverse-domain app_id (e.g. "org.kde.dolphin"). Simple substring
// matching, not true fuzzy/edit-distance matching -- deliberately, since
// this only needs to catch the one common pattern, not every possible typo.
func findSuggestions(typed string, candidates []string) []string {
	var out []string
	lowerTyped := strings.ToLower(typed)
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c), lowerTyped) {
			out = append(out, c)
		}
	}
	return out
}

// findAllSuggestions combines live-window substring matches (findSuggestions)
// with installed .desktop Name matches (deduplicated) -- so a suggestion
// can come from either "this app is currently running with a similar
// app_id" or "this app is installed, matched by its human-readable name"
// (e.g. typing "code" or "dolphin" -- the plain, friendly name a user
// actually knows, not the reverse-domain app_id). The .desktop path
// catches apps that aren't even open right now, which live-window
// matching alone never could.
func findAllSuggestions(typed string, liveCandidates []string) []string {
	seen := make(map[string]bool)
	var out []string

	for _, s := range findSuggestions(typed, liveCandidates) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	idx := desktopentry.Load()
	for _, e := range idx.SearchByName(typed) {
		id := e.AppID()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}

	return out
}

func (m Model) View() string {
	if m.quitting {
		return "\n"
	}
	var body string
	switch m.screen {
	case screenDefaultMonitor:
		body = m.viewDefaultMonitor()
	case screenIgnoreApps:
		body = m.viewIgnoreApps()
	case screenIgnoreAppsAdd:
		body = m.viewIgnoreAppsAdd()
	case screenDisconnectedOutputs:
		body = m.viewDisconnectedOutputs()
	default:
		body = m.viewMainMenu()
	}
	if m.err != nil {
		body += "\n" + styleError.Render(fmt.Sprintf("✗ error: %v", m.err)) + "\n"
	}
	return renderCanvas(body)
}

func (m Model) viewMainMenu() string {
	s := styleTitle.Render("Continuum-WM Profile Settings") + "\n\n"
	for i, item := range m.menuItems {
		s += renderRow(item, i == m.menuCursor) + "\n"
	}
	if m.statusMsg != "" {
		s += "\n" + styledStatus(m.statusMsg) + "\n"
	}
	s += "\n" + styleFooter.Render("(up/down to move, enter to select, q to quit)") + "\n"
	return s
}

func (m Model) viewDefaultMonitor() string {
	s := styleTitle.Render("🖥️  Default Monitor") + "\n\n"
	if len(m.connectedOutputs) == 0 {
		s += styleMuted.Render("  (no outputs detected -- is niri running?)") + "\n"
	}
	for i, o := range m.connectedOutputs {
		label := o
		switch {
		case o == m.prefs.DefaultOutput:
			label += styleMuted.Render("  [currently set]")
		case o == m.autoDetected:
			label += styleMuted.Render("  (auto-detected default)")
		}
		s += renderRow(label, i == m.monitorCursor) + "\n"
	}
	s += "\n" + styleFooter.Render("(up/down to move, enter to save selection, esc to go back)") + "\n"
	return s
}

func (m Model) viewIgnoreApps() string {
	s := styleTitle.Render("🚫 Ignore Apps") + "\n\n"
	if len(m.prefs.IgnoreApps) == 0 {
		s += styleMuted.Render("  (none ignored yet)") + "\n"
	}
	for i, a := range m.prefs.IgnoreApps {
		s += renderRow(a, i == m.ignoreCursor) + "\n"
	}
	if m.statusMsg != "" {
		s += "\n" + styledStatus(m.statusMsg) + "\n"
	}
	s += "\n" + styleFooter.Render("(up/down to move, 'a' to add, 'd' to remove selected, esc to go back)") + "\n"
	return s
}

func (m Model) viewIgnoreAppsAdd() string {
	if m.addTextInputActive {
		if m.addConfirming {
			s := styleTitle.Render(fmt.Sprintf("No exact match for %q", m.addTextBuffer)) + "\n\n"
			if len(m.addSuggestions) > 0 {
				s += "Did you mean one of these?\n\n"
				for i, sug := range m.addSuggestions {
					s += renderRow(sug, i == m.addSuggestCursor) + "\n"
				}
				s += "\n" + styleFooter.Render("(up/down to move, enter to add selected)") + "\n"
			}
			s += styleWarning.Render(fmt.Sprintf("\n  ⚠ [r] open the app, then refresh and check again\n  [y] add %q anyway (not currently running)\n  [esc] cancel\n", m.addTextBuffer))
			if m.statusMsg != "" {
				s += "\n" + styledStatus(m.statusMsg) + "\n"
			}
			return s
		}
		return fmt.Sprintf("%s\n\n> %s\u2588\n\n%s\n",
			styleTitle.Render("➕ Add Ignored App -- type an app_id"),
			m.addTextBuffer,
			styleFooter.Render("(enter to confirm, esc to cancel)"))
	}

	s := styleTitle.Render("➕ Add Ignored App") + "\n\n"
	if len(m.addCandidates) == 0 {
		s += styleMuted.Render("  (no other running apps found)") + "\n"
	}
	for i, c := range m.addCandidates {
		s += renderRow(c, i == m.addCursor) + "\n"
	}
	s += renderRow("Type a custom app_id...", m.addCursor == len(m.addCandidates)) + "\n"
	s += "\n" + styleFooter.Render("(up/down to move, enter to select, esc to go back)") + "\n"
	return s
}

func (m Model) viewDisconnectedOutputs() string {
	if m.confirmForget {
		target := m.disconnectedOutputs[m.disconnectedCursor]
		return styleWarning.Render(fmt.Sprintf(
			"⚠ Forget output %q?\n\nThis permanently deletes %d saved entit(y/ies) for this output.\nThis cannot be undone.\n\n  [y] confirm\n  [n/esc] cancel\n",
			target.Name, target.EntityCount,
		))
	}

	s := styleTitle.Render("🔌 Manage Disconnected Outputs") + "\n\n"
	if len(m.disconnectedOutputs) == 0 {
		s += styleMuted.Render("  (nothing to manage -- no saved entities reference a currently-disconnected output)") + "\n"
	}
	for i, o := range m.disconnectedOutputs {
		s += renderRow(fmt.Sprintf("%s: %d entit(y/ies)", o.Name, o.EntityCount), i == m.disconnectedCursor) + "\n"
	}
	if m.statusMsg != "" {
		s += "\n" + styledStatus(m.statusMsg) + "\n"
	}
	s += "\n" + styleFooter.Render("(up/down to move, 'f' to forget selected, esc to go back)") + "\n"
	return s
}

// defaultSessionPath returns the same session file location the systemd
// daemon uses by default (~/.local/state/continuum-wm/session.yaml) --
// profile-settings is meant as a companion to that day-to-day workflow,
// so this screen manages that same file rather than requiring the user
// to specify a path.
func defaultSessionPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "continuum-wm", "session.yaml"), nil
}

// loadDisconnectedOutputs computes, for the default session file, every
// output referenced by saved entities that isn't currently connected --
// the same check already built into lazy-restore's startup warning,
// reused here rather than reimplemented. A missing session file (e.g.
// the daemon has never run yet) is treated as "nothing to manage," not
// an error.
func loadDisconnectedOutputs() ([]outputSummary, string, error) {
	sessPath, err := defaultSessionPath()
	if err != nil {
		return nil, "", err
	}

	s, err := session.Load(sessPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, sessPath, nil
		}
		return nil, sessPath, err
	}

	client := &niri.Client{}
	liveOutputs, err := client.Outputs(context.Background())
	if err != nil {
		return nil, sessPath, err
	}
	connected := make(map[string]bool, len(liveOutputs))
	for name := range liveOutputs {
		connected[name] = true
	}

	counts := make(map[string]int)
	for _, ws := range s.Workspaces {
		n := len(ws.AllEntities())
		if n == 0 {
			continue
		}
		if ws.OutputHint != "" && !connected[ws.OutputHint] {
			counts[ws.OutputHint] += n
		}
	}

	var out []outputSummary
	for name, n := range counts {
		out = append(out, outputSummary{Name: name, EntityCount: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, sessPath, nil
}

// forgetOutput permanently removes every saved workspace whose
// OutputHint matches outputName from the session file at sessionPath.
// Irreversible -- callers must confirm with the user before calling this
// (see updateConfirmForget).
func forgetOutput(sessionPath, outputName string) error {
	s, err := session.Load(sessionPath)
	if err != nil {
		return err
	}
	var kept []session.Workspace
	for _, ws := range s.Workspaces {
		if ws.OutputHint == outputName {
			continue
		}
		kept = append(kept, ws)
	}
	s.Workspaces = kept
	return session.Save(s, sessionPath)
}

// liveOutputNames queries niri for currently connected output names,
// reusing the same niri.Client.Outputs() built and tested during
// Phase 7 -- deliberately not reimplementing this IPC call here.
func liveOutputNames() ([]string, error) {
	client := &niri.Client{}
	outputs, err := client.Outputs(context.Background())
	if err != nil {
		return nil, fmt.Errorf("querying niri outputs: %w", err)
	}
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	return names, nil
}

// liveRunningAppIDs queries niri for every currently-running window's
// app_id, deduplicated and sorted -- used to suggest apps the user can
// ignore without needing to know niri's exact app_id naming from memory.
func liveRunningAppIDs() ([]string, error) {
	client := &niri.Client{}
	wins, err := client.Windows(context.Background())
	if err != nil {
		return nil, fmt.Errorf("querying niri windows: %w", err)
	}
	seen := make(map[string]bool)
	var ids []string
	for _, w := range wins {
		if w.AppID == nil || *w.AppID == "" {
			continue
		}
		if !seen[*w.AppID] {
			seen[*w.AppID] = true
			ids = append(ids, *w.AppID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
