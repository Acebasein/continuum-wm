use std::error::Error;
use std::fs;
use std::process::Command;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::Deserialize;
use serde::de::DeserializeOwned;

use crate::model::{LayoutSnapshot, Snapshot, WindowSnapshot, WorkspaceSnapshot};

#[derive(Debug, Deserialize)]
struct NiriWorkspace {
    id: u64,
    idx: u64,
    name: Option<String>,
    output: Option<String>,
    is_focused: bool,
}

#[derive(Debug, Deserialize)]
struct NiriWindow {
    id: u64,
    title: Option<String>,
    app_id: Option<String>,
    pid: Option<u32>,
    workspace_id: Option<u64>,
    is_focused: bool,
    is_floating: bool,
    layout: NiriLayout,
}

#[derive(Debug, Deserialize)]
struct NiriLayout {
    pos_in_scrolling_layout: Option<[u32; 2]>,
    tile_size: [f64; 2],
    window_size: [i32; 2],
    tile_pos_in_workspace_view: Option<[f64; 2]>,
    window_offset_in_tile: [f64; 2],
}

fn query_niri<T>(query: &str) -> Result<T, Box<dyn Error>>
where
    T: DeserializeOwned,
{
    let output = Command::new("niri")
        .args(["msg", "--json", query])
        .output()?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        return Err(format!("niri query failed: {}", stderr.trim()).into());
    }

    Ok(serde_json::from_slice(&output.stdout)?)
}

fn read_launch_command(pid: u32) -> Option<Vec<String>> {
    let path = format!("/proc/{pid}/cmdline");
    let bytes = fs::read(path).ok()?;

    parse_cmdline(&bytes)
}

fn parse_cmdline(bytes: &[u8]) -> Option<Vec<String>> {
    if bytes.is_empty() {
        return None;
    }

    let mut parts: Vec<&[u8]> = bytes.split(|byte| *byte == b'\0').collect();

    if parts.last().is_some_and(|part| part.is_empty()) {
        parts.pop();
    }

    if parts.is_empty() {
        return None;
    }

    let mut command = Vec::with_capacity(parts.len());

    for part in parts {
        let value = std::str::from_utf8(part).ok()?;
        command.push(value.to_owned());
    }

    if command.first().is_none_or(|program| program.is_empty()) {
        return None;
    }

    Some(command)
}

pub fn capture_focused_workspace() -> Result<Snapshot, Box<dyn Error>> {
    let workspaces: Vec<NiriWorkspace> = query_niri("workspaces")?;

    let focused_workspace = workspaces
        .into_iter()
        .find(|workspace| workspace.is_focused)
        .ok_or("Niri did not report a focused workspace")?;

    let windows: Vec<NiriWindow> = query_niri("windows")?;

    let captured_windows = windows
        .into_iter()
        .filter(|window| window.workspace_id == Some(focused_workspace.id))
        .map(|window| {
            let launch_command = window.pid.and_then(read_launch_command);

            WindowSnapshot {
                runtime_id: window.id,
                app_id: window.app_id,
                pid: window.pid,
                title: window.title,
                workspace_runtime_id: window.workspace_id,
                is_focused: window.is_focused,
                is_floating: window.is_floating,
                launch_command,
                layout: LayoutSnapshot {
                    scrolling_position: window.layout.pos_in_scrolling_layout,
                    tile_size: window.layout.tile_size,
                    window_size: window.layout.window_size,
                    workspace_view_position: window.layout.tile_pos_in_workspace_view,
                    window_offset_in_tile: window.layout.window_offset_in_tile,
                },
            }
        })
        .collect();

    let captured_at_unix_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)?
        .as_millis()
        .try_into()?;

    Ok(Snapshot {
        schema_version: 1,
        captured_at_unix_ms,
        workspace: WorkspaceSnapshot {
            runtime_id: focused_workspace.id,
            index: focused_workspace.idx,
            name: focused_workspace.name,
            output: focused_workspace.output,
            windows: captured_windows,
        },
    })
}

pub fn live_window_ids() -> Result<Vec<u64>, Box<dyn Error>> {
    let windows: Vec<NiriWindow> = query_niri("windows")?;

    Ok(windows.into_iter().map(|window| window.id).collect())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_proc_cmdline() {
        let bytes = b"/usr/bin/example\0--flag\0value\0";

        let command = parse_cmdline(bytes).expect("command should parse");

        assert_eq!(
            command,
            vec![
                "/usr/bin/example".to_string(),
                "--flag".to_string(),
                "value".to_string()
            ]
        );
    }

    #[test]
    fn preserves_empty_arguments() {
        let bytes = b"/usr/bin/example\0\0value\0";

        let command = parse_cmdline(bytes).expect("command should parse");

        assert_eq!(
            command,
            vec![
                "/usr/bin/example".to_string(),
                String::new(),
                "value".to_string()
            ]
        );
    }

    #[test]
    fn rejects_empty_cmdline() {
        assert_eq!(parse_cmdline(b""), None);
    }
}
