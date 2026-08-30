use std::error::Error;
use std::process::Command;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::Deserialize;

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
    layout: Option<NiriLayout>,
}

#[derive(Debug, Deserialize)]
struct NiriLayout {
    pos_in_scrolling_layout: Option<[i64; 2]>,
    tile_size: Option<[f64; 2]>,
    window_size: Option<[i64; 2]>,
    tile_pos_in_workspace_view: Option<[f64; 2]>,
    window_offset_in_tile: Option<[f64; 2]>,
}

fn query_niri<T>(query: &str) -> Result<T, Box<dyn Error>>
where
    T: for<'de> Deserialize<'de>,
{
    let output = Command::new("niri")
        .args(["msg", "--json", query])
        .output()?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);

        return Err(format!("niri query '{query}' failed: {}", stderr.trim()).into());
    }

    Ok(serde_json::from_slice(&output.stdout)?)
}

pub fn capture_focused_workspace() -> Result<Snapshot, Box<dyn Error>> {
    let workspaces: Vec<NiriWorkspace> = query_niri("workspaces")?;

    let focused_workspace = workspaces
        .into_iter()
        .find(|workspace| workspace.is_focused)
        .ok_or("Niri reported no focused workspace")?;

    let windows: Vec<NiriWindow> = query_niri("windows")?;

    let normalized_windows = windows
        .into_iter()
        .filter(|window| window.workspace_id == Some(focused_workspace.id))
        .map(|window| WindowSnapshot {
            runtime_id: window.id,
            app_id: window.app_id,
            pid: window.pid,
            title: window.title,
            workspace_runtime_id: window.workspace_id,
            is_focused: window.is_focused,
            is_floating: window.is_floating,
            layout: window.layout.map(|layout| LayoutSnapshot {
                scrolling_position: layout.pos_in_scrolling_layout,
                tile_size: layout.tile_size,
                window_size: layout.window_size,
                workspace_view_position: layout.tile_pos_in_workspace_view,
                window_offset_in_tile: layout.window_offset_in_tile,
            }),
        })
        .collect();

    let captured_at_unix_ms = SystemTime::now().duration_since(UNIX_EPOCH)?.as_millis() as u64;

    Ok(Snapshot {
        schema_version: 1,
        captured_at_unix_ms,
        workspace: WorkspaceSnapshot {
            runtime_id: focused_workspace.id,
            index: focused_workspace.idx,
            name: focused_workspace.name,
            output: focused_workspace.output,
            windows: normalized_windows,
        },
    })
}
