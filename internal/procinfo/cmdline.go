// Package procinfo reads information from Linux's /proc filesystem.
//
// CRITICAL DESIGN RULE: everything in this package takes a PID as INPUT and
// is used at a single instant in time (e.g. "right now, at capture time,
// what command launched this process?"). The PID itself is NEVER persisted
// as identity -- only the resulting data (a command line, a working
// directory) is saved. This matches the project's core rule that runtime
// identifiers like PIDs must never be treated as permanent session
// identity; they're a legitimate, transient lookup key and nothing more.
package procinfo

import (
	"fmt"
	"os"
	"strings"
)

// ReadCmdline reads /proc/<pid>/cmdline and returns it as a slice of
// arguments (argv[0], argv[1], ...), suitable for later use as a launch
// command.
//
// This is best-effort by nature:
//   - the process may have exited already (race between observing the PID
//     and reading /proc)
//   - permissions may prevent reading another user's /proc entry
//   - some processes rewrite their own argv (e.g. some daemons), so this
//     is not a 100% reliable "how do I relaunch this" answer for every
//     app -- it's a reasonable generic default, to be refined later by
//     application-specific providers (see design doc, Part 11).
//
// A returned error here should be treated as "we don't know how to
// relaunch this one," never papered over with a guess.
func ReadCmdline(pid int32) ([]string, error) {
	path := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s was empty", path)
	}

	// /proc/<pid>/cmdline separates arguments with NUL bytes, and
	// typically has a trailing NUL. Split and drop any empty trailing
	// entries that produces.
	parts := strings.Split(string(data), "\x00")
	var args []string
	for _, p := range parts {
		if p != "" {
			args = append(args, p)
		}
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("%s contained no usable arguments", path)
	}
	return args, nil
}

// ReadCwd reads /proc/<pid>/cwd, a symlink to the process's current
// working directory, and returns the resolved path.
//
// Same transient-use rule as ReadCmdline: pid is used once, right now, to
// resolve a path string -- the pid itself is never persisted.
func ReadCwd(pid int32) (string, error) {
	path := fmt.Sprintf("/proc/%d/cwd", pid)
	target, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return target, nil
}
