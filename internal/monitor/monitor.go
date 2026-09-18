// Package monitor implements persistent-monitor-identity resolution:
// matching a SAVED monitor's hardware identity against the CURRENTLY
// LIVE set of niri outputs, to find its current (possibly different)
// connector name.
//
// A separate package from internal/niri (the adapter) and
// internal/session (the schema) specifically to avoid an import cycle --
// this needs both niri.Output (live data) and session.MonitorIdentity
// (persisted data) as inputs, and neither of those packages should
// depend on the other just to support this.
//
// CORE PRINCIPLE, confirmed necessary via direct testing: a connector
// name (e.g. "eDP-1") is NOT durable identity -- it can change even
// WITHIN a single boot, not just across separate reboots. This package
// never treats the connector name as identity; only hardware evidence
// (serial, or make+model+physical-size) counts.
package monitor

import (
	"continuum-wm/internal/niri"
	"continuum-wm/internal/session"
)

// MatchStatus is an explicit result, never a bare connector string --
// per the design principle "do not guess": ambiguous or missing cases
// must be handled explicitly by the caller, not silently papered over.
type MatchStatus int

const (
	Matched MatchStatus = iota
	Missing
	Ambiguous
)

// Match is the result of resolving one saved monitor against the live
// output set.
type Match struct {
	Status MatchStatus

	// Connector is the CURRENT connector name for the matched monitor --
	// only meaningful when Status == Matched. Callers should resolve this
	// fresh, right before acting, rather than caching it across any
	// meaningful span of time, since it's exactly the kind of value this
	// whole package exists because it can't be trusted to stay put.
	Connector string
}

// Resolve finds saved's current connector among the live outputs, using
// an evidence-based priority order:
//
//  1. Serial number, when saved has one -- the strongest signal
//     available. If saved has a serial, ONLY serial matching is
//     attempted; a saved serial with zero live matches is reported
//     Missing, never falling back to a fuzzier match. Falling back here
//     would risk matching a genuinely DIFFERENT physical monitor that
//     happens to share make/model with the one we have strong specific
//     evidence about -- worse than reporting Missing honestly.
//  2. Make + Model + physical size, when no usable serial exists (e.g.
//     most internal laptop panels, confirmed directly: this project's
//     own test hardware reports no serial for its built-in display).
//
// Connector class (the "eDP"/"HDMI" prefix) is deliberately NOT used as
// a tiebreaker in this initial implementation -- the design doc treats it
// as weak supporting evidence only, and composite matching without it is
// sufficient for every case observed so far. Add it later only if real
// ambiguity actually requires the extra signal, not preemptively.
//
// Two physically identical monitors with no serial number will correctly
// produce Ambiguous for each, rather than one arbitrarily "winning" --
// this is the intended behavior (see the design doc's Section 9), not a
// gap to close.
func Resolve(saved session.MonitorIdentity, live map[string]niri.Output) Match {
	if saved.Serial != nil && *saved.Serial != "" {
		var candidates []string
		for name, o := range live {
			if o.Serial != nil && *o.Serial == *saved.Serial {
				candidates = append(candidates, name)
			}
		}
		return matchFromCandidates(candidates)
	}

	var candidates []string
	for name, o := range live {
		if o.Make == saved.Make &&
			o.Model == saved.Model &&
			o.PhysicalSizeMM[0] == saved.PhysicalWidthMM &&
			o.PhysicalSizeMM[1] == saved.PhysicalHeightMM {
			candidates = append(candidates, name)
		}
	}
	return matchFromCandidates(candidates)
}

func matchFromCandidates(candidates []string) Match {
	switch len(candidates) {
	case 1:
		return Match{Status: Matched, Connector: candidates[0]}
	case 0:
		return Match{Status: Missing}
	default:
		return Match{Status: Ambiguous}
	}
}
