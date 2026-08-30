mod model;
mod niri;
mod snapshot;

use std::env;
use std::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    let command = env::args().nth(1);

    match command.as_deref() {
        Some("capture") => capture(),
        Some("load") => load(),
        Some(command) => Err(format!("Unknown command: {command}").into()),
        None => {
            println!("Usage: continuum-wm <command>");
            println!();
            println!("Commands:");
            println!("  capture    Capture and save the focused workspace");
            println!("  load       Load and validate the saved snapshot");
            Ok(())
        }
    }
}

fn capture() -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM MVP 1 — Capture");

    let captured = niri::capture_focused_workspace()?;

    println!(
        "Captured workspace {} (runtime ID {}) with {} window(s)",
        captured.workspace.index,
        captured.workspace.runtime_id,
        captured.workspace.windows.len()
    );

    let path = snapshot::save(&captured)?;

    println!("Snapshot saved to {}", path.display());

    Ok(())
}

fn load() -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM MVP 1 — Load");

    let loaded = snapshot::load()?;

    println!(
        "Loaded snapshot schema {}: workspace {} with {} window(s)",
        loaded.schema_version,
        loaded.workspace.index,
        loaded.workspace.windows.len()
    );

    Ok(())
}
