use serde::{Deserialize, Serialize};

#[derive(Debug, Deserialize, Serialize)]
pub struct Snapshot {
    pub schema_version: u32,
    pub captured_at_unix_ms: u64,
    pub workspace: WorkspaceSnapshot,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct WorkspaceSnapshot {
    pub runtime_id: u64,
    pub index: u64,
    pub name: Option<String>,
    pub output: Option<String>,
    pub windows: Vec<WindowSnapshot>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct WindowSnapshot {
    pub runtime_id: u64,
    pub app_id: Option<String>,
    pub pid: Option<u32>,
    pub title: Option<String>,
    pub workspace_runtime_id: Option<u64>,
    pub is_focused: bool,
    pub is_floating: bool,
    pub layout: Option<LayoutSnapshot>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct LayoutSnapshot {
    pub scrolling_position: Option<[i64; 2]>,
    pub tile_size: Option<[f64; 2]>,
    pub window_size: Option<[i64; 2]>,
    pub workspace_view_position: Option<[f64; 2]>,
    pub window_offset_in_tile: Option<[f64; 2]>,
}
