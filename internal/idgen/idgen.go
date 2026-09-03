// Package idgen generates persistent identifiers for Continuum-WM session
// entities (workspaces, windows).
//
// IMPORTANT: these IDs are generated ONCE, the first time we capture a
// workspace/window, and then stored forever in the session file. They are
// never derived from anything niri gives us (niri's window/workspace IDs
// are runtime-only and do not survive a restart -- see the project design
// doc, "Runtime IDs and PIDs Must Not Be Persistent Identity").
package idgen

import (
	"crypto/rand"
	"fmt"
)

// New generates a random 128-bit ID, formatted like a UUID (e.g.
// "a1b2c3d4-e5f6-...") purely for readability in the YAML file. It carries
// no special UUID-version semantics -- it's just a convenient, effectively-
// unique-forever random string, which is all persistent identity requires
// here. We avoid pulling in a UUID library for one function this simple.
func New(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is extremely unusual (would indicate a
		// broken system entropy source). Panicking here is deliberate:
		// silently returning a weak/predictable ID would be worse than
		// crashing, since it would quietly break the identity guarantees
		// the whole project depends on.
		panic(fmt.Sprintf("idgen: crypto/rand failed: %v", err))
	}
	raw := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	if prefix == "" {
		return raw
	}
	return prefix + "-" + raw
}
