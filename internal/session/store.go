package session

import (
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"continuum-wm/internal/idgen"
	"continuum-wm/internal/niri"
)

// Save writes the session to path as YAML. It always updates UpdatedAt
// before writing.
func Save(s *Session, path string) error {
	s.UpdatedAt = time.Now().UTC()

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshaling session to YAML: %w", err)
	}

	// 0600: session files can reveal working directories, open documents,
	// etc. -- treat them as user-private by default.
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing session file %s: %w", path, err)
	}
	return nil
}

// Load reads and parses a session file. It rejects files with a schema
// version it doesn't understand rather than guessing -- silently
// misinterpreting an incompatible schema is exactly the kind of "confident
// but wrong" behavior the project is designed to avoid.
func Load(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading session file %s: %w", path, err)
	}

	var s Session
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing session YAML: %w", err)
	}

	if s.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf(
			"session file has schema_version %d, this build of continuum-wm understands version %d -- refusing to load rather than guess",
			s.SchemaVersion, SchemaVersion,
		)
	}

	return &s, nil
}

// CaptureLive builds a brand-new Session from the current live Niri state.
//
// Phase 2 note: every workspace and window gets a FRESH persistent_id every
// time this runs. There is deliberately no "is this the same window as
// last capture" logic here yet -- that matching problem belongs to Phase 4
// / Part 7 of the design doc, and doing it half-correctly now would be
// worse than not doing it at all. Each capture is an honest, independent
// snapshot.
//
// Per niri's own IPC documentation, a separate Workspaces() call and a
// separate Windows() call are not guaranteed to be perfectly consistent
// with each other (state can change in between). CaptureLive does its best
// to associate windows with the workspaces list it just read, but treat
// this capture as best-effort, matching the same caveat noted in
// internal/niri/client.go.
func CaptureLive(ctx context.Context, client *niri.Client) (*Session, error) {
	niriWorkspaces, err := client.Workspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading workspaces: %w", err)
	}
	niriWindows, err := client.Windows(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading windows: %w", err)
	}

	now := time.Now().UTC()

	s := &Session{
		SchemaVersion: SchemaVersion,
		SessionID:     idgen.New("session"),
		CreatedAt:     now,
		UpdatedAt:     now,
		Compositor:    "niri",
	}

	// Index windows by their niri workspace_id so we can group them.
	windowsByWorkspace := make(map[uint64][]niri.Window)
	for _, w := range niriWindows {
		if w.WorkspaceID == nil {
			continue // a window with no workspace isn't something we can place; skip rather than guess
		}
		windowsByWorkspace[*w.WorkspaceID] = append(windowsByWorkspace[*w.WorkspaceID], w)
	}

	for _, nw := range niriWorkspaces {
		ws := Workspace{
			PersistentID: idgen.New("ws"),
			IsFavorite:   false, // Phase 2 doesn't set this yet; Phase 6 will
		}
		if nw.Output != nil {
			ws.OutputHint = *nw.Output
		}

		for _, win := range windowsByWorkspace[nw.ID] {
			// Skip windows with no app_id at all -- Phase 1 showed us these
			// are typically transient system popups, not real restorable
			// application windows (see design doc discussion of the "som"
			// / "Mount" windows observed during testing).
			if win.AppID == nil || *win.AppID == "" {
				continue
			}

			ent := Entity{
				PersistentID: idgen.New("ent"),
				AppID:        *win.AppID,
				LastSeen: LastSeen{
					NiriWindowID: win.ID,
					CapturedAt:   now,
				},
			}
			if win.Title != nil {
				ent.Title = *win.Title
			}
			ws.Entities = append(ws.Entities, ent)
		}

		s.Workspaces = append(s.Workspaces, ws)
	}

	return s, nil
}
