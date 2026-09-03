// Continuum-WM mixed-terminal auto-capture/restore feasibility test
use std::collections::{HashMap, HashSet, VecDeque};
use std::env;
use std::error::Error;
use std::fs;
use std::path::PathBuf;
use std::process::Command;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

use crate::niri;

const TERMINAL_SESSION_SCHEMA_VERSION: u32 = 1;
const TERMINAL_SESSION_FILE: &str = "terminal-session.json";

#[derive(Debug, Serialize, Deserialize)]
struct TerminalSession {
    schema_version: u32,
    captured_at_unix_ms: u64,
    terminals: Vec<TerminalRestoreRecord>,
}

#[derive(Debug, Serialize, Deserialize)]
struct TerminalRestoreRecord {
    label: String,
    app_id: String,
    program: PathBuf,
    cwd: PathBuf,
    workspace_index: u64,
    window_size: [i32; 2],
    scrolling_position: [u32; 2],
}

#[derive(Debug)]
struct RestoredTerminal<'a> {
    record: &'a TerminalRestoreRecord,
    window_id: u64,
}

#[derive(Debug, Deserialize)]
struct RawWindow {
    id: u64,
    title: Option<String>,
    app_id: Option<String>,
    pid: Option<u32>,
    workspace_id: Option<u64>,
    layout: RawLayout,
}

#[derive(Debug, Deserialize)]
struct RawLayout {
    pos_in_scrolling_layout: Option<[u32; 2]>,
    window_size: [i32; 2],
}

#[derive(Debug, Deserialize)]
struct RawWorkspace {
    id: u64,
    idx: u64,
}

pub fn capture() -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM — Automatic Terminal Capture Test");
    println!();
    println!("Capture policy:");
    println!("  - discover terminal-like windows from the live Niri desktop");
    println!("  - derive current CWD from the window process tree");
    println!("  - save relative workspace order, position, and size");
    println!("  - runtime Niri IDs and PIDs are capture-time evidence only");
    println!();

    let windows: Vec<RawWindow> = niri_json("windows")?;
    let workspaces: Vec<RawWorkspace> = niri_json("workspaces")?;

    let workspace_indices: HashMap<u64, u64> = workspaces
        .into_iter()
        .map(|workspace| (workspace.id, workspace.idx))
        .collect();

    let mut records = Vec::new();
    let mut skipped_ambiguous = Vec::new();

    for window in windows {
        let Some(pid) = window.pid else {
            continue;
        };

        let Some(app_id) = window.app_id.as_deref() else {
            continue;
        };

        let Some(workspace_runtime_id) = window.workspace_id else {
            continue;
        };

        let Some(workspace_index) = workspace_indices.get(&workspace_runtime_id).copied() else {
            continue;
        };

        let Some(scrolling_position) = window.layout.pos_in_scrolling_layout else {
            continue;
        };

        let shell = match discover_terminal_shell(pid)? {
            ShellDiscovery::NotTerminal => continue,
            ShellDiscovery::Ambiguous(candidates) => {
                skipped_ambiguous.push(format!(
                    "window {} app_id {:?}: {} equally-near PTY descendants",
                    window.id,
                    app_id,
                    candidates.len()
                ));
                continue;
            }
            ShellDiscovery::Found(shell) => shell,
        };

        let program = fs::read_link(format!("/proc/{pid}/exe"))?;
        let cwd = fs::read_link(format!("/proc/{}/cwd", shell.pid))?;

        let title = window.title.as_deref().unwrap_or("<untitled>");
        let label = format!("{} — {}", app_id, title);

        records.push(TerminalRestoreRecord {
            label,
            app_id: app_id.to_string(),
            program,
            cwd,
            workspace_index,
            window_size: window.layout.window_size,
            scrolling_position,
        });
    }

    records.sort_by_key(|record| {
        (
            record.workspace_index,
            record.scrolling_position[0],
            record.scrolling_position[1],
        )
    });

    if records.is_empty() {
        return Err("No unambiguous terminal windows were discovered; nothing was saved".into());
    }

    validate_continuity_records(&records)?;

    println!(
        "Automatically discovered {} terminal instance(s):",
        records.len()
    );
    println!();

    for (index, record) in records.iter().enumerate() {
        println!(
            "  {:>2}. WS{} [{},{}] app_id={} cwd={} size={}×{}",
            index + 1,
            record.workspace_index,
            record.scrolling_position[0],
            record.scrolling_position[1],
            record.app_id,
            record.cwd.display(),
            record.window_size[0],
            record.window_size[1]
        );
        println!("      program={}", record.program.display());
    }

    if !skipped_ambiguous.is_empty() {
        println!();
        println!("Skipped ambiguous terminal-like windows:");
        for message in &skipped_ambiguous {
            println!("  - {message}");
        }
        println!("Continuum did not guess.");
    }

    let session = TerminalSession {
        schema_version: TERMINAL_SESSION_SCHEMA_VERSION,
        captured_at_unix_ms: now_unix_ms()?,
        terminals: records,
    };

    let path = terminal_session_path()?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }

    let json = serde_json::to_string_pretty(&session)?;
    fs::write(&path, json)?;

    println!();
    println!("CAPTURE COMPLETE");
    println!("Saved terminal session to {}", path.display());
    println!("PIDs and Niri runtime IDs were not persisted as restore identity.");

    Ok(())
}

pub fn run() -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM — Captured Terminal Continuity Restore Test");
    println!();

    let session = load_terminal_session()?;

    if session.schema_version != TERMINAL_SESSION_SCHEMA_VERSION {
        return Err(format!(
            "Unsupported terminal session schema version {}; expected {}",
            session.schema_version, TERMINAL_SESSION_SCHEMA_VERSION
        )
        .into());
    }

    let records = session.terminals;
    validate_continuity_records(&records)?;

    println!("Loaded {} captured terminal instance(s).", records.len());
    println!("Restore order follows saved relative workspace order.");
    println!();
    println!("Restore priority:");
    println!("  Phase 1: recreate every terminal and place it on the correct relative workspace");
    println!("  Phase 2: restore saved column positions");
    println!("  Phase 3: restore saved window sizes");
    println!();

    for record in &records {
        println!(
            "  WS{} [{},{}] {} -> {} -> {} × {}",
            record.workspace_index,
            record.scrolling_position[0],
            record.scrolling_position[1],
            record.app_id,
            record.cwd.display(),
            record.window_size[0],
            record.window_size[1]
        );
    }

    let total = records.len();
    let mut restored = Vec::with_capacity(total);

    println!();
    println!("============================================================");
    println!("PHASE 1 — TERMINAL CONTINUITY");
    println!("============================================================");
    println!();

    for (index, record) in records.iter().enumerate() {
        let window_id = restore_continuity(record, index + 1, total)?;
        restored.push(RestoredTerminal { record, window_id });
    }

    println!();
    println!(
        "CONTINUITY COMPLETE — {total}/{total} captured terminal instances are alive and on their target workspaces."
    );
    println!();

    println!("============================================================");
    println!("PHASE 2 — POSITION RESTORATION");
    println!("============================================================");
    println!();

    for restored_terminal in &restored {
        restore_position_best_effort(restored_terminal);
    }

    println!();
    println!("POSITION PHASE COMPLETE.");
    println!();

    println!("============================================================");
    println!("PHASE 3 — SIZE RESTORATION");
    println!("============================================================");
    println!();

    for restored_terminal in &restored {
        restore_size_best_effort(restored_terminal);
    }

    println!();
    println!("SIZE PHASE COMPLETE.");
    println!();

    println!("============================================================");
    println!("RESTORE COMPLETE");
    println!("============================================================");
    println!(
        "{total}/{total} captured terminal instances restored with strict workspace continuity."
    );
    println!("Position and size restoration were attempted as best-effort enhancements.");

    Ok(())
}

#[derive(Debug)]
struct ShellCandidate {
    pid: u32,
}

enum ShellDiscovery {
    NotTerminal,
    Found(ShellCandidate),
    Ambiguous(Vec<ShellCandidate>),
}

fn discover_terminal_shell(terminal_pid: u32) -> Result<ShellDiscovery, Box<dyn Error>> {
    let mut queue = VecDeque::new();
    let mut visited = HashSet::new();

    queue.push_back((terminal_pid, 0usize));
    visited.insert(terminal_pid);

    let mut candidates = Vec::new();
    let mut best_depth: Option<usize> = None;

    while let Some((pid, depth)) = queue.pop_front() {
        if let Some(best) = best_depth
            && depth > best
        {
            break;
        }

        if pid != terminal_pid && process_stdin_is_pty(pid) {
            best_depth.get_or_insert(depth);
            candidates.push(ShellCandidate { pid });
            continue;
        }

        for child in process_children(pid)? {
            if visited.insert(child) {
                queue.push_back((child, depth + 1));
            }
        }
    }

    match candidates.len() {
        0 => Ok(ShellDiscovery::NotTerminal),
        1 => Ok(ShellDiscovery::Found(candidates.remove(0))),
        _ => Ok(ShellDiscovery::Ambiguous(candidates)),
    }
}

fn process_children(pid: u32) -> Result<Vec<u32>, Box<dyn Error>> {
    let task_dir = format!("/proc/{pid}/task");
    let entries = match fs::read_dir(&task_dir) {
        Ok(entries) => entries,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
        Err(error) => return Err(error.into()),
    };

    let mut children = HashSet::new();

    for entry in entries {
        let entry = entry?;

        let Some(tid) = entry.file_name().to_str().map(str::to_owned) else {
            continue;
        };

        if tid.parse::<u32>().is_err() {
            continue;
        }

        let path = entry.path().join("children");

        let contents = match fs::read_to_string(path) {
            Ok(contents) => contents,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
            Err(error) => return Err(error.into()),
        };

        for value in contents.split_whitespace() {
            if let Ok(child_pid) = value.parse::<u32>() {
                children.insert(child_pid);
            }
        }
    }

    let mut children: Vec<u32> = children.into_iter().collect();
    children.sort_unstable();

    Ok(children)
}

fn process_stdin_is_pty(pid: u32) -> bool {
    fs::read_link(format!("/proc/{pid}/fd/0"))
        .ok()
        .and_then(|path| path.to_str().map(str::to_owned))
        .is_some_and(|path| path.starts_with("/dev/pts/"))
}

fn niri_json<T>(request: &str) -> Result<T, Box<dyn Error>>
where
    T: for<'de> Deserialize<'de>,
{
    let output = Command::new("niri")
        .args(["msg", "--json", request])
        .output()?;

    if !output.status.success() {
        return Err(format!(
            "niri msg --json {request} failed: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        )
        .into());
    }

    Ok(serde_json::from_slice(&output.stdout)?)
}

fn terminal_session_path() -> Result<PathBuf, Box<dyn Error>> {
    if let Some(state_home) = env::var_os("XDG_STATE_HOME") {
        return Ok(PathBuf::from(state_home)
            .join("continuum-wm")
            .join(TERMINAL_SESSION_FILE));
    }

    let home = env::var_os("HOME").ok_or("HOME is not set")?;
    Ok(PathBuf::from(home)
        .join(".local/state/continuum-wm")
        .join(TERMINAL_SESSION_FILE))
}

fn load_terminal_session() -> Result<TerminalSession, Box<dyn Error>> {
    let path = terminal_session_path()?;
    let contents = fs::read_to_string(&path).map_err(|error| {
        format!(
            "Failed to load captured terminal session {}: {error}",
            path.display()
        )
    })?;

    Ok(serde_json::from_str(&contents)?)
}

fn now_unix_ms() -> Result<u64, Box<dyn Error>> {
    Ok(SystemTime::now()
        .duration_since(UNIX_EPOCH)?
        .as_millis()
        .try_into()?)
}

fn validate_continuity_records(records: &[TerminalRestoreRecord]) -> Result<(), Box<dyn Error>> {
    if records.is_empty() {
        return Err("Captured terminal session contains no terminal records".into());
    }

    for record in records {
        if !record.cwd.is_dir() {
            return Err(format!(
                "{} restore directory does not exist or is not a directory: {}",
                record.label,
                record.cwd.display()
            )
            .into());
        }

        if !record.program.is_file() {
            return Err(format!(
                "{} captured terminal executable does not exist: {}",
                record.label,
                record.program.display()
            )
            .into());
        }

        if record.workspace_index == 0 {
            return Err(format!("{} has invalid workspace index 0", record.label).into());
        }
    }

    Ok(())
}

fn restore_continuity(
    record: &TerminalRestoreRecord,
    sequence_number: usize,
    total: usize,
) -> Result<u64, Box<dyn Error>> {
    println!("[{sequence_number}/{total}] {}", record.label);
    println!("  app_id: {}", record.app_id);
    println!("  program: {}", record.program.display());
    println!("  cwd: {}", record.cwd.display());
    println!("  target workspace: {}", record.workspace_index);
    println!("  establishing Niri baseline...");

    let mut event_stream = niri::EventStream::open()?;
    let baseline_windows = event_stream.wait_for_initial_windows()?;
    let baseline_ids: HashSet<u64> = baseline_windows.iter().map(|window| window.id).collect();

    println!("  baseline: {} live window(s)", baseline_ids.len());
    println!("  launching captured terminal executable from saved CWD...");

    let child = Command::new(&record.program)
        .current_dir(&record.cwd)
        .spawn()?;

    println!("  launcher PID: {}", child.id());
    println!("  waiting for the new {:?} window...", record.app_id);

    let window = event_stream.wait_for_first_new_window(&baseline_ids, &record.app_id)?;

    println!("  observed new Niri window ID: {}", window.id);
    println!(
        "  moving exact window {} -> workspace {}...",
        window.id, record.workspace_index
    );

    niri::move_window_to_workspace(window.id, record.workspace_index)?;

    if !niri::window_is_on_workspace_index(window.id, record.workspace_index)? {
        return Err(format!(
            "{} workspace verification failed: Niri window {} is not on workspace {}",
            record.label, window.id, record.workspace_index
        )
        .into());
    }

    println!("  workspace verified.");
    println!(
        "  CONTINUITY COMPLETE: {} -> window {} -> workspace {}",
        record.label, window.id, record.workspace_index
    );
    println!();

    Ok(window.id)
}

fn restore_position_best_effort(restored_terminal: &RestoredTerminal<'_>) {
    let record = restored_terminal.record;
    let window_id = restored_terminal.window_id;

    println!(
        "{} — window {} -> target position [{},{}]",
        record.label, window_id, record.scrolling_position[0], record.scrolling_position[1]
    );

    match restore_position(window_id, record) {
        Ok(()) => {
            println!(
                "  position restored: [{},{}]",
                record.scrolling_position[0], record.scrolling_position[1]
            );
        }
        Err(error) => {
            eprintln!(
                "  WARNING: {} position restoration failed: {error}",
                record.label
            );
            eprintln!("  continuing restore.");
        }
    }

    println!();
}

fn restore_position(window_id: u64, record: &TerminalRestoreRecord) -> Result<(), Box<dyn Error>> {
    let [saved_column, saved_row] = record.scrolling_position;

    if saved_column == 0 || saved_row == 0 {
        return Err(
            format!("invalid saved scrolling position [{saved_column},{saved_row}]").into(),
        );
    }

    if saved_row != 1 {
        return Err(format!(
            "unsupported multi-row scrolling position [{saved_column},{saved_row}]"
        )
        .into());
    }

    if !niri::window_is_alone_in_column(window_id)? {
        return Err(
            format!("Niri window {window_id} shares its column with another window").into(),
        );
    }

    let previous_focus = niri::focused_window_id()?;

    println!(
        "  moving window {} column -> saved column {}...",
        window_id, saved_column
    );

    niri::focus_window(window_id)?;

    let move_result = niri::move_focused_column_to_index(saved_column);

    if let Some(previous_focus_id) = previous_focus
        && previous_focus_id != window_id
        && let Err(error) = niri::focus_window(previous_focus_id)
    {
        eprintln!(
            "  WARNING: failed to restore previous focus to window {previous_focus_id}: {error}"
        );
    }

    move_result?;
    verify_position(window_id, record)?;

    Ok(())
}

fn verify_position(window_id: u64, record: &TerminalRestoreRecord) -> Result<(), Box<dyn Error>> {
    let window = niri::window_by_id(window_id)?.ok_or_else(|| {
        format!("Niri window {window_id} disappeared during position verification")
    })?;

    let actual_position = window
        .layout
        .pos_in_scrolling_layout
        .ok_or_else(|| format!("Niri window {window_id} has no scrolling-layout position"))?;

    if actual_position != record.scrolling_position {
        return Err(format!(
            "verification mismatch: expected [{},{}], got [{},{}]",
            record.scrolling_position[0],
            record.scrolling_position[1],
            actual_position[0],
            actual_position[1]
        )
        .into());
    }

    Ok(())
}

fn restore_size_best_effort(restored_terminal: &RestoredTerminal<'_>) {
    let record = restored_terminal.record;
    let window_id = restored_terminal.window_id;

    println!(
        "{} — window {} -> target size {} × {}",
        record.label, window_id, record.window_size[0], record.window_size[1]
    );

    match restore_size(window_id, record) {
        Ok(()) => {
            println!(
                "  size restored: {} × {}",
                record.window_size[0], record.window_size[1]
            );
        }
        Err(error) => {
            eprintln!(
                "  WARNING: {} size restoration failed: {error}",
                record.label
            );
            eprintln!("  continuing restore.");
        }
    }

    println!();
}

fn restore_size(window_id: u64, record: &TerminalRestoreRecord) -> Result<(), Box<dyn Error>> {
    if record.window_size[0] <= 0 || record.window_size[1] <= 0 {
        return Err(format!(
            "invalid saved window size {} × {}",
            record.window_size[0], record.window_size[1]
        )
        .into());
    }

    println!(
        "  restoring window {} -> {} × {}...",
        window_id, record.window_size[0], record.window_size[1]
    );

    niri::set_window_width(window_id, record.window_size[0])?;
    niri::set_window_height(window_id, record.window_size[1])?;

    verify_size(window_id, record)?;

    Ok(())
}

fn verify_size(window_id: u64, record: &TerminalRestoreRecord) -> Result<(), Box<dyn Error>> {
    let window = niri::window_by_id(window_id)?
        .ok_or_else(|| format!("Niri window {window_id} disappeared during size verification"))?;

    if window.layout.window_size != record.window_size {
        return Err(format!(
            "verification mismatch: expected {} × {}, got {} × {}",
            record.window_size[0],
            record.window_size[1],
            window.layout.window_size[0],
            window.layout.window_size[1]
        )
        .into());
    }

    Ok(())
}
