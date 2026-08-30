use std::env;
use std::error::Error;
use std::fs;
use std::path::PathBuf;

use crate::model::Snapshot;

const SUPPORTED_SCHEMA_VERSION: u32 = 1;

fn state_directory() -> Result<PathBuf, Box<dyn Error>> {
    if let Some(path) = env::var_os("XDG_STATE_HOME") {
        return Ok(PathBuf::from(path).join("continuum-wm"));
    }

    let home = env::var_os("HOME").ok_or("Neither XDG_STATE_HOME nor HOME is set")?;

    Ok(PathBuf::from(home)
        .join(".local")
        .join("state")
        .join("continuum-wm"))
}

fn snapshot_path() -> Result<PathBuf, Box<dyn Error>> {
    Ok(state_directory()?.join("snapshot.json"))
}

pub fn save(snapshot: &Snapshot) -> Result<PathBuf, Box<dyn Error>> {
    let directory = state_directory()?;

    fs::create_dir_all(&directory)?;

    let path = directory.join("snapshot.json");

    let mut json = serde_json::to_string_pretty(snapshot)?;
    json.push('\n');

    fs::write(&path, json)?;

    Ok(path)
}

pub fn load() -> Result<Snapshot, Box<dyn Error>> {
    let path = snapshot_path()?;

    let contents = fs::read_to_string(&path)?;

    let snapshot: Snapshot = serde_json::from_str(&contents)?;

    validate(&snapshot)?;

    Ok(snapshot)
}

fn validate(snapshot: &Snapshot) -> Result<(), Box<dyn Error>> {
    if snapshot.schema_version != SUPPORTED_SCHEMA_VERSION {
        return Err(format!(
            "Unsupported snapshot schema version: {} (supported: {})",
            snapshot.schema_version, SUPPORTED_SCHEMA_VERSION
        )
        .into());
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn valid_snapshot() -> Snapshot {
        Snapshot {
            schema_version: SUPPORTED_SCHEMA_VERSION,
            captured_at_unix_ms: 1,
            workspace: crate::model::WorkspaceSnapshot {
                runtime_id: 1,
                index: 1,
                name: None,
                output: Some("test-output".to_string()),
                windows: Vec::new(),
            },
        }
    }

    #[test]
    fn accepts_supported_schema_version() {
        let snapshot = valid_snapshot();

        assert!(validate(&snapshot).is_ok());
    }

    #[test]
    fn rejects_unsupported_schema_version() {
        let mut snapshot = valid_snapshot();
        snapshot.schema_version = SUPPORTED_SCHEMA_VERSION + 1;

        let error = validate(&snapshot).expect_err("unsupported schema version should fail");

        assert!(
            error
                .to_string()
                .contains("Unsupported snapshot schema version")
        );
    }
}
