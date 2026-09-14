// Package preferences holds user-configured settings -- deliberately
// separate from internal/session, which is auto-capture's territory and
// gets freely rewritten every 5 minutes. Preferences are only ever
// written by explicit user action (the profile-settings TUI), never by
// any background process, which is exactly the property session.yaml
// couldn't guarantee (recall: we had to specifically defend the
// `favorite` flag from being clobbered by auto-capture -- preferences
// avoids that whole class of problem by living in a completely separate
// file no automated process ever touches).
package preferences

// SchemaVersion is written into every saved preferences file, same
// discipline as session.SchemaVersion -- bump and add migration logic if
// this shape ever needs to change, so an old file is never silently
// misread under a new version's assumptions.
const SchemaVersion = 1

// Preferences is the full set of user-configured settings.
type Preferences struct {
	SchemaVersion int `yaml:"schema_version"`

	// DefaultOutput is the user's explicitly chosen default monitor.
	// Empty means "not explicitly set" -- callers should fall back to the
	// eDP-* auto-detection heuristic (see DetectDefaultOutput) rather than
	// treating an empty string as "no monitor at all."
	DefaultOutput string `yaml:"default_output,omitempty"`

	// IgnoreApps is a list of app_id values that capture should skip
	// entirely -- they're never saved into session.yaml at all, and so
	// never restored either. Matches the `nirinit` precedent noted
	// earlier in this project (its skip.apps setting) -- a proven,
	// simple pattern, not a new invention.
	IgnoreApps []string `yaml:"ignore_apps,omitempty"`
}

// IsAppIgnored reports whether appID is in the ignore list.
func (p *Preferences) IsAppIgnored(appID string) bool {
	if p == nil {
		return false
	}
	for _, a := range p.IgnoreApps {
		if a == appID {
			return true
		}
	}
	return false
}

// AddIgnoredApp adds appID to the ignore list if it isn't already present.
// Returns false if it was already there (nothing changed).
func (p *Preferences) AddIgnoredApp(appID string) bool {
	if p.IsAppIgnored(appID) {
		return false
	}
	p.IgnoreApps = append(p.IgnoreApps, appID)
	return true
}

// RemoveIgnoredApp removes appID from the ignore list. Returns false if it
// wasn't there (nothing changed).
func (p *Preferences) RemoveIgnoredApp(appID string) bool {
	for i, a := range p.IgnoreApps {
		if a == appID {
			p.IgnoreApps = append(p.IgnoreApps[:i], p.IgnoreApps[i+1:]...)
			return true
		}
	}
	return false
}
