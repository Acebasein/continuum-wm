// Package provider implements Continuum-WM's Application Provider
// registry (design doc Part 11 / Phase 8).
//
// SCOPE, DELIBERATE: this is NOT an attempt to support every application
// in existence -- not every AUR package, not every Flatpak, not every
// Electron app. It holds only the specific, per-app overrides
// Continuum-WM has actually proven it needs, through real testing on
// real hardware. Adding a new provider should require the same standard
// every existing one met: a confirmed, tested reason the generic
// mechanism (plain $PATH launch command / .desktop-file lookup / the
// CWD-allowlist resolution in internal/session) isn't sufficient for
// that specific app -- not a guess that it might need special handling.
//
// Most apps need ZERO overrides. A missing entry here isn't a gap to
// fill preemptively; it means the generic mechanism already works.
package provider

import "continuum-wm/internal/session"

// Provider holds optional per-app overrides. A nil field means "use
// Continuum-WM's generic mechanism for this app" -- which is the default,
// correct behavior for the overwhelming majority of apps.
type Provider struct {
	AppID string

	// BuildLaunchCommand, if set, overrides the generic launch command
	// (see session.resolveLaunchCommand's layered $PATH-name / .desktop /
	// raw-cmdline strategy) for this specific app_id. Return nil or an
	// empty slice to fall back to the generic command for this entity.
	BuildLaunchCommand func(ent session.Entity) []string
}

var registry = map[string]Provider{}

// register adds p to the registry, keyed by p.AppID. Called from this
// package's own provider definition files (see ghostty.go) via init() --
// not intended to be called from outside the package.
func register(p Provider) {
	registry[p.AppID] = p
}

// Lookup returns the registered Provider for appID, if any. Callers
// should treat a missing entry as "use the generic mechanism," never as
// an error.
func Lookup(appID string) (Provider, bool) {
	p, ok := registry[appID]
	return p, ok
}
