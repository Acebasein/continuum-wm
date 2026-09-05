package procinfo

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Children returns the PIDs of all processes whose parent is parentPID,
// at this instant.
//
// Why this exists: terminal emulators like Ghostty and Kitty run the
// user's shell as a CHILD process, not as themselves. The terminal
// emulator's own /proc/<pid>/cwd reflects wherever IT was launched from
// (typically the user's home directory) and does not update when the user
// `cd`s around inside the shell. Confirmed experimentally: Kitty's own PID
// reported a stale home-directory cwd even after the user had `cd`'d
// elsewhere. To get the directory that actually matters, we need the
// child shell's /proc/<child-pid>/cwd, not the parent's.
func Children(parentPID int32) ([]int32, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("reading /proc: %w", err)
	}

	var children []int32
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a PID directory (e.g. "self", "net", "1234-extra")
		}
		ppid, err := readPPID(int32(pid))
		if err != nil {
			continue // process likely exited between listing and reading; skip it
		}
		if ppid == parentPID {
			children = append(children, int32(pid))
		}
	}
	return children, nil
}

func readPPID(pid int32) (int32, error) {
	path := fmt.Sprintf("/proc/%d/status", pid)
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "PPid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return 0, fmt.Errorf("unexpected PPid line format in %s: %q", path, line)
		}
		v, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, fmt.Errorf("parsing PPid in %s: %w", path, err)
		}
		return int32(v), nil
	}
	return 0, fmt.Errorf("no PPid line found in %s", path)
}

// Comm reads /proc/<pid>/comm -- the kernel's short name for the process
// (e.g. "bash", "kitten"), trimmed of the trailing newline it always has.
func Comm(pid int32) (string, error) {
	path := fmt.Sprintf("/proc/%d/comm", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}
