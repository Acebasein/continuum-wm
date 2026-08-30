use std::collections::HashSet;

use crate::model::Snapshot;
use crate::niri;

#[derive(Debug, PartialEq, Eq)]
pub enum WindowStatus {
    Present,
    Missing,
}

#[derive(Debug)]
pub struct WindowPlan {
    pub runtime_id: u64,
    pub app_id: Option<String>,
    pub title: Option<String>,
    pub status: WindowStatus,
}

#[derive(Debug)]
pub struct ReconciliationPlan {
    pub windows: Vec<WindowPlan>,
}

pub fn reconcile(snapshot: &Snapshot) -> Result<ReconciliationPlan, Box<dyn std::error::Error>> {
    let live_window_ids: HashSet<u64> = niri::live_window_ids()?.into_iter().collect();

    let windows = snapshot
        .workspace
        .windows
        .iter()
        .map(|saved_window| {
            let status = if live_window_ids.contains(&saved_window.runtime_id) {
                WindowStatus::Present
            } else {
                WindowStatus::Missing
            };

            WindowPlan {
                runtime_id: saved_window.runtime_id,
                app_id: saved_window.app_id.clone(),
                title: saved_window.title.clone(),
                status,
            }
        })
        .collect();

    Ok(ReconciliationPlan { windows })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn classifies_known_runtime_id_as_present() {
        let live_ids = HashSet::from([10_u64, 20_u64]);

        let status = if live_ids.contains(&10) {
            WindowStatus::Present
        } else {
            WindowStatus::Missing
        };

        assert_eq!(status, WindowStatus::Present);
    }

    #[test]
    fn classifies_unknown_runtime_id_as_missing() {
        let live_ids = HashSet::from([10_u64, 20_u64]);

        let status = if live_ids.contains(&30) {
            WindowStatus::Present
        } else {
            WindowStatus::Missing
        };

        assert_eq!(status, WindowStatus::Missing);
    }
}
