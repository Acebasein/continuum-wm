use std::error::Error;
use std::fs;
use std::io::{BufRead, BufReader};
use std::os::fd::AsRawFd;
use std::process::{Child, ChildStdout, Command, Stdio};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use serde::Deserialize;
use serde::de::DeserializeOwned;
use serde_json::Value;

use crate::model::{LayoutSnapshot, Snapshot, WindowSnapshot, WorkspaceSnapshot};

#[derive(Debug, Clone, Deserialize)]
pub struct NiriWindow {
    pub id: u64,
    pub title: Option<String>,
    pub app_id: Option<String>,
    pub pid: Option<u32>,
    pub workspace_id: Option<u64>,
    pub is_focused: bool,
    pub is_floating: bool,
    pub layout: NiriLayout,
}

#[derive(Debug, Clone, Deserialize)]
pub struct NiriLayout {
    pub pos_in_scrolling_layout: Option<[u32; 2]>,
    pub tile_size: [f64; 2],
    pub window_size: [i32; 2],
    pub tile_pos_in_workspace_view: Option<[f64; 2]>,
    pub window_offset_in_tile: [f64; 2],
}

#[derive(Debug, Deserialize)]
struct NiriWorkspace {
    id: u64,
    idx: u64,
    name: Option<String>,
    output: Option<String>,
    is_focused: bool,
}

pub struct EventStream {
    child: Child,
    reader: BufReader<ChildStdout>,
}

impl EventStream {
    pub fn open() -> Result<Self, Box<dyn Error>> {
        let mut child = Command::new("niri")
            .args(["msg", "--json", "event-stream"])
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()?;

        let stdout = child
            .stdout
            .take()
            .ok_or("Failed to capture Niri event stream stdout")?;

        Ok(Self {
            child,
            reader: BufReader::new(stdout),
        })
    }

    pub fn wait_for_initial_windows(&mut self) -> Result<Vec<NiriWindow>, Box<dyn Error>> {
        loop {
            let value = self.read_event()?;

            if let Some(windows) = parse_windows_changed(&value)? {
                return Ok(windows);
            }
        }
    }

    pub fn collect_new_windows(
        &mut self,
        baseline_ids: &std::collections::HashSet<u64>,
        expected_app_id: &str,
        timeout: Duration,
    ) -> Result<Vec<NiriWindow>, Box<dyn Error>> {
        let deadline = Instant::now() + timeout;
        let mut matches = Vec::new();
        let mut seen_ids = std::collections::HashSet::new();

        while let Some(value) = self.read_event_until(deadline)? {
            if let Some(window) = parse_window_opened_or_changed(&value)? {
                if baseline_ids.contains(&window.id) {
                    continue;
                }

                if !seen_ids.insert(window.id) {
                    continue;
                }

                if window.app_id.as_deref() == Some(expected_app_id) {
                    matches.push(window);
                }
            }
        }

        Ok(matches)
    }

    fn read_event(&mut self) -> Result<Value, Box<dyn Error>> {
        let mut line = String::new();
        let bytes_read = self.reader.read_line(&mut line)?;

        if bytes_read == 0 {
            return Err("Niri event stream closed unexpectedly".into());
        }

        Ok(serde_json::from_str(line.trim())?)
    }

    fn read_event_until(&mut self, deadline: Instant) -> Result<Option<Value>, Box<dyn Error>> {
        let now = Instant::now();

        if now >= deadline {
            return Ok(None);
        }

        let remaining = deadline.saturating_duration_since(now);

        if !wait_for_readable(self.reader.get_ref(), remaining)? {
            return Ok(None);
        }

        let mut line = String::new();
        let bytes_read = self.reader.read_line(&mut line)?;

        if bytes_read == 0 {
            return Err("Niri event stream closed unexpectedly".into());
        }

        Ok(Some(serde_json::from_str(line.trim())?))
    }
}

impl Drop for EventStream {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

fn wait_for_readable(stdout: &ChildStdout, timeout: Duration) -> Result<bool, Box<dyn Error>> {
    let timeout_ms = timeout.as_millis().min(i32::MAX as u128) as i32;

    let mut poll_fd = libc::pollfd {
        fd: stdout.as_raw_fd(),
        events: libc::POLLIN,
        revents: 0,
    };

    loop {
        // SAFETY:
        // poll_fd points to one valid pollfd value for the duration of the call.
        // The file descriptor comes from the live ChildStdout owned by EventStream.
        let result = unsafe { libc::poll(&mut poll_fd, 1, timeout_ms) };

        if result > 0 {
            if poll_fd.revents & libc::POLLIN != 0 {
                return Ok(true);
            }

            if poll_fd.revents & (libc::POLLERR | libc::POLLHUP | libc::POLLNVAL) != 0 {
                return Err("Niri event stream became unavailable".into());
            }

            continue;
        }

        if result == 0 {
            return Ok(false);
        }

        let error = std::io::Error::last_os_error();

        if error.kind() == std::io::ErrorKind::Interrupted {
            continue;
        }

        return Err(error.into());
    }
}

fn parse_windows_changed(value: &Value) -> Result<Option<Vec<NiriWindow>>, Box<dyn Error>> {
    let Some(payload) = value.get("WindowsChanged") else {
        return Ok(None);
    };

    let Some(windows_value) = payload.get("windows") else {
        return Ok(None);
    };

    let windows = serde_json::from_value(windows_value.clone())?;
    Ok(Some(windows))
}

fn parse_window_opened_or_changed(value: &Value) -> Result<Option<NiriWindow>, Box<dyn Error>> {
    let Some(payload) = value.get("WindowOpenedOrChanged") else {
        return Ok(None);
    };

    let Some(window_value) = payload.get("window") else {
        return Ok(None);
    };

    let window = serde_json::from_value(window_value.clone())?;
    Ok(Some(window))
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
pub fn move_window_to_workspace(
    window_id: u64,
    workspace_index: u64,
) -> Result<(), Box<dyn Error>> {
    let output = Command::new("niri")
        .args([
            "msg",
            "action",
            "move-window-to-workspace",
            "--window-id",
            &window_id.to_string(),
            "--focus",
            "false",
            &workspace_index.to_string(),
        ])
        .output()?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);

        return Err(format!(
            "Failed to move Niri window {window_id} to workspace {workspace_index}: {}",
            stderr.trim()
        )
        .into());
    }

    Ok(())
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

    #[test]
    fn parses_initial_windows_event() {
        let value: Value = serde_json::from_str(
            r#"{
                "WindowsChanged": {
                    "windows": [{
                        "id": 54,
                        "title": "Mozilla Firefox",
                        "app_id": "firefox",
                        "pid": 2725,
                        "workspace_id": 1,
                        "is_focused": true,
                        "is_floating": false,
                        "layout": {
                            "pos_in_scrolling_layout": [2, 1],
                            "tile_size": [762.4, 826.4],
                            "window_size": [761, 825],
                            "tile_pos_in_workspace_view": null,
                            "window_offset_in_tile": [0.8, 0.8]
                        }
                    }]
                }
            }"#,
        )
        .expect("event JSON should parse");

        let windows = parse_windows_changed(&value)
            .expect("event should deserialize")
            .expect("event should contain windows");

        assert_eq!(windows.len(), 1);
        assert_eq!(windows[0].id, 54);
        assert_eq!(windows[0].app_id.as_deref(), Some("firefox"));
    }

    #[test]
    fn parses_window_opened_event() {
        let value: Value = serde_json::from_str(
            r#"{
                "WindowOpenedOrChanged": {
                    "window": {
                        "id": 54,
                        "title": "Mozilla Firefox",
                        "app_id": "firefox",
                        "pid": 2725,
                        "workspace_id": 1,
                        "is_focused": true,
                        "is_floating": false,
                        "layout": {
                            "pos_in_scrolling_layout": [2, 1],
                            "tile_size": [762.4, 826.4],
                            "window_size": [761, 825],
                            "tile_pos_in_workspace_view": null,
                            "window_offset_in_tile": [0.8, 0.8]
                        }
                    }
                }
            }"#,
        )
        .expect("event JSON should parse");

        let window = parse_window_opened_or_changed(&value)
            .expect("event should deserialize")
            .expect("event should contain a window");

        assert_eq!(window.id, 54);
        assert_eq!(window.app_id.as_deref(), Some("firefox"));
    }
}
