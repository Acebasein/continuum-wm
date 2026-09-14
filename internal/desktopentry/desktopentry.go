// Package desktopentry finds the correct launch command for an app_id by
// reading XDG .desktop files -- the same static metadata source app
// launchers like Rofi and Wofi use, rather than inspecting running
// processes.
//
// WHY THIS EXISTS: confirmed that /proc/<pid>/cmdline-based capture is
// fundamentally unreliable for at least two real cases -- NixOS's
// content-addressed store paths (fragile across rebuilds), and X11 apps
// running under XWayland (ONLYOFFICE), where the PID niri reports belongs
// to the xwayland-satellite bridge process, not the app itself, making
// /proc-based capture read the WRONG process entirely. A .desktop lookup
// sidesteps both problems: it never looks at a PID at all, only at the
// app_id string matched against installed application metadata.
package desktopentry

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Entry holds the fields we care about from one .desktop file.
type Entry struct {
	ID             string // filename without ".desktop"
	Exec           string // raw Exec= value, field codes not yet stripped
	StartupWMClass string
	Name           string
}

// Index is a loaded set of desktop entries, keyed for fast app_id lookup.
type Index struct {
	byID      map[string]*Entry
	byWMClass map[string]*Entry
	all       []*Entry // deduplicated, for name-based search (SearchByName)
}

// Load scans the standard XDG application directories and builds an
// Index. Best-effort throughout: unreadable files or directories are
// silently skipped, since this is a convenience lookup that should never
// hard-fail a capture over a missing/malformed .desktop file.
func Load() *Index {
	idx := &Index{byID: map[string]*Entry{}, byWMClass: map[string]*Entry{}}
	seenFile := map[string]bool{}

	for _, dir := range searchDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, de := range entries {
			if de.IsDir() || !strings.HasSuffix(de.Name(), ".desktop") {
				continue
			}
			if seenFile[de.Name()] {
				continue // a higher-priority dir already provided this one
			}
			seenFile[de.Name()] = true

			entry, err := parseFile(filepath.Join(dir, de.Name()))
			if err != nil || entry == nil || entry.Exec == "" {
				continue
			}
			idx.byID[entry.ID] = entry
			if entry.StartupWMClass != "" {
				idx.byWMClass[entry.StartupWMClass] = entry
			}
			idx.all = append(idx.all, entry)
		}
	}
	return idx
}

// Lookup finds the best matching desktop entry for appID: first an exact
// filename match (covers reverse-DNS-style app_ids like
// "org.gnome.Nautilus"), then a StartupWMClass match (covers app_ids that
// don't match their .desktop filename, e.g. ONLYOFFICE). Returns nil if
// nothing matched -- callers should fall back to another strategy, never
// guess.
func (idx *Index) Lookup(appID string) *Entry {
	if e, ok := idx.byID[appID]; ok {
		return e
	}
	if e, ok := idx.byWMClass[appID]; ok {
		return e
	}
	return nil
}

// SearchByName returns every desktop entry whose human-readable Name
// (e.g. "Visual Studio Code") contains query as a case-insensitive
// substring. Unlike Lookup, this searches by the FRIENDLY name, not the
// app_id -- built specifically for suggesting a correct app_id when a
// user types a plausible app name rather than its real identifier (e.g.
// typing "dolphin" or "code" rather than "org.kde.dolphin" or "code").
// Works for apps that aren't even running, since it reads installed
// .desktop metadata directly rather than live window state.
func (idx *Index) SearchByName(query string) []*Entry {
	if query == "" {
		return nil
	}
	lower := strings.ToLower(query)
	var out []*Entry
	for _, e := range idx.all {
		if strings.Contains(strings.ToLower(e.Name), lower) {
			out = append(out, e)
		}
	}
	return out
}

// AppID returns the identifier most likely to match niri's own reported
// app_id for windows of this application: StartupWMClass when present
// (that field exists specifically to record the expected window class),
// falling back to the .desktop filename ID otherwise. This is a BEST
// GUESS, not a guarantee -- confirmed elsewhere in this project that
// app_id and .desktop naming can genuinely diverge (ONLYOFFICE) with no
// fully reliable way to predict it without the app actually running.
func (e *Entry) AppID() string {
	if e.StartupWMClass != "" {
		return e.StartupWMClass
	}
	return e.ID
}

// LaunchCommand returns a cleaned argv for this entry: standard Exec=
// field codes (%f, %F, %u, %U, %i, %c, %k, %%) are stripped, since none
// of them are meaningful for a restore launch with no specific
// file/URL/icon argument to substitute.
func (e *Entry) LaunchCommand() []string {
	fields := strings.Fields(e.Exec)
	var out []string
	for _, f := range fields {
		switch f {
		case "%f", "%F", "%u", "%U", "%i", "%c", "%k":
			continue
		}
		out = append(out, strings.ReplaceAll(f, "%%", "%"))
	}
	return out
}

// searchDirs returns the standard XDG application directories to search,
// honoring XDG_DATA_HOME / XDG_DATA_DIRS when set, in priority order
// (first match wins), with common NixOS-specific paths included as a
// harmless addition even when absent on other distros.
func searchDirs() []string {
	var dirs []string

	if home := os.Getenv("XDG_DATA_HOME"); home != "" {
		dirs = append(dirs, filepath.Join(home, "applications"))
	} else if hd, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(hd, ".local/share/applications"))
	}

	if hd, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(hd, ".nix-profile/share/applications"))
	}
	dirs = append(dirs, "/run/current-system/sw/share/applications")

	if dataDirs := os.Getenv("XDG_DATA_DIRS"); dataDirs != "" {
		for _, d := range strings.Split(dataDirs, ":") {
			if d != "" {
				dirs = append(dirs, filepath.Join(d, "applications"))
			}
		}
	} else {
		dirs = append(dirs, "/usr/local/share/applications", "/usr/share/applications")
	}

	return dirs
}

// parseFile reads a single .desktop file's [Desktop Entry] section only
// (ignoring [Desktop Action ...] and any other sections).
func parseFile(path string) (*Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	e := &Entry{ID: strings.TrimSuffix(filepath.Base(path), ".desktop")}
	inMainSection := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inMainSection = line == "[Desktop Entry]"
			continue
		}
		if !inMainSection {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "Exec":
			e.Exec = val
		case "StartupWMClass":
			e.StartupWMClass = val
		case "Name":
			if e.Name == "" { // keep the first (unlocalized) Name=, skip Name[xx]=
				e.Name = val
			}
		case "NoDisplay":
			if val == "true" {
				return nil, nil // not a real launchable entry, skip it
			}
		case "Hidden":
			if val == "true" {
				return nil, nil
			}
		}
	}
	return e, scanner.Err()
}
