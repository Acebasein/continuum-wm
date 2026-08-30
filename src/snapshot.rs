use std::env;
use std::error::Error;
use std::fs;
use std::path::PathBuf;

use crate::model::Snapshot;

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

pub fn save(snapshot: &Snapshot) -> Result<PathBuf, Box<dyn Error>> {
    let directory = state_directory()?;

    fs::create_dir_all(&directory)?;

    let path = directory.join("snapshot.json");

    let mut json = serde_json::to_string_pretty(snapshot)?;
    json.push('\n');

    fs::write(&path, json)?;

    Ok(path)
}
