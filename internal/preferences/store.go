package preferences

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath returns the standard location for the preferences file:
// ~/.config/continuum-wm/config.yaml. Deliberately under ~/.config, not
// ~/.local/state where session.yaml lives -- preferences are
// user-authored configuration, session.yaml is machine-generated state
// that happens to be reloaded; the XDG split exists for exactly this
// distinction.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "continuum-wm", "config.yaml"), nil
}

// Load reads preferences from path. If the file doesn't exist yet (the
// normal case before the user has ever opened profile-settings), this
// returns a fresh, empty Preferences and a nil error -- a missing
// preferences file is not an error condition, it just means "use
// defaults for everything," the same way a brand-new install has no
// prior configuration to speak of.
func Load(path string) (*Preferences, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Preferences{SchemaVersion: SchemaVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading preferences file %s: %w", path, err)
	}

	var p Preferences
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing preferences file %s: %w", path, err)
	}
	if p.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("preferences file %s has schema_version %d, this build expects %d -- refusing to guess how to interpret it", path, p.SchemaVersion, SchemaVersion)
	}
	return &p, nil
}

// Save writes preferences to path atomically (temp file + rename), same
// pattern as session.Save and for the same reason -- the file at path is
// always either the complete previous version or the complete new one,
// never a half-write, even if the process is killed mid-write. Creates
// the parent directory if it doesn't exist yet (the common case on first
// save, since ~/.config/continuum-wm/ has no reason to exist before
// this).
func Save(p *Preferences, path string) error {
	p.SchemaVersion = SchemaVersion

	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshaling preferences to YAML: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating preferences directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for atomic write: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp preferences file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp preferences file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp preferences file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return fmt.Errorf("setting permissions on temp preferences file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp preferences file into place: %w", err)
	}
	return nil
}

// DetectDefaultOutput picks a sensible default monitor from a list of
// currently-connected output names, when the user hasn't explicitly set
// one. Heuristic: internal laptop panels are conventionally named eDP-*
// regardless of the trailing number (confirmed in this project's own
// testing: the same physical panel enumerated as both eDP-1 and eDP-2
// across different reboots) -- prefer the first eDP-* match. Falls back
// to the first connected output (alphabetically, for determinism) if no
// eDP-* output exists, e.g. a desktop with no internal panel at all.
//
// Returns ("", false) if connectedOutputs is empty -- genuinely nothing
// to pick from, not something to guess at.
func DetectDefaultOutput(connectedOutputs []string) (string, bool) {
	if len(connectedOutputs) == 0 {
		return "", false
	}

	var edpCandidate string
	var firstAny string
	for _, name := range connectedOutputs {
		if firstAny == "" || name < firstAny {
			firstAny = name
		}
		if strings.HasPrefix(name, "eDP-") {
			if edpCandidate == "" || name < edpCandidate {
				edpCandidate = name
			}
		}
	}
	if edpCandidate != "" {
		return edpCandidate, true
	}
	return firstAny, true
}
