package provider

import "continuum-wm/internal/session"

func init() {
	register(Provider{
		AppID:              "com.mitchellh.ghostty",
		BuildLaunchCommand: ghosttyBuildLaunchCommand,
	})
}

// ghosttyBuildLaunchCommand is Phase 8's reference provider implementation
// -- migrated from a hardcoded special case that used to live directly in
// internal/restore, kept here unchanged in behavior, just relocated.
//
// CONFIRMED, THROUGH REAL TESTING (see design doc's Ghostty findings):
//
//  1. Ghostty defaults to GTK/D-Bus single-instance activation, meaning a
//     plain `ghostty` launch can silently join an already-running
//     instance rather than starting a genuinely separate process -- this
//     is what makes per-window CWD unresolvable at capture time for
//     shared-instance windows.
//
//  2. When we DO have a high-confidence saved CWD, Ghostty's own
//     `+new-window --working-directory=<path>` action reliably places
//     the new window in the correct directory EVERY TIME, regardless of
//     whether it joins an existing instance or starts a fresh one --
//     confirmed against the real /proc/<pid>/cwd of the resulting shell,
//     not just window title text (which can lag). This sidesteps the
//     single-instance problem entirely for the placement problem, though
//     NOT for future re-capturability -- see the design doc's "known
//     limitation" write-up for why restoring 2+ Ghostty entities in one
//     run can still cause them to re-share a pid afterward.
//
//  3. When we DON'T have a saved CWD, we still force a genuinely separate
//     process by appending --gtk-single-instance=false, overriding
//     Ghostty's own .desktop file (which explicitly sets
//     --gtk-single-instance=true). This doesn't recover the CWD for THIS
//     restore, but means a FUTURE capture of this window can resolve it
//     correctly, where it couldn't before -- a self-correcting fallback,
//     though only reliably so when a single Ghostty entity is restored
//     per run (see the same design doc write-up).
func ghosttyBuildLaunchCommand(ent session.Entity) []string {
	if ent.ProviderMetadata.CWDConfidence == session.CWDHigh && ent.ProviderMetadata.CWD != "" {
		return []string{"ghostty", "+new-window", "--working-directory=" + ent.ProviderMetadata.CWD}
	}

	cmd := make([]string, len(ent.Launch.Command), len(ent.Launch.Command)+1)
	copy(cmd, ent.Launch.Command)
	return append(cmd, "--gtk-single-instance=false")
}
