mod model;
mod niri;
mod reconcile;
mod snapshot;

use std::env;
use std::error::Error;

use reconcile::WindowStatus;

fn main() -> Result<(), Box<dyn Error>> {
    let command = env::args().nth(1);

    match command.as_deref() {
        Some("capture") => capture(),
        Some("load") => load(),
        Some("reconcile") => reconcile(),
        Some(command) => Err(format!("Unknown command: {command}").into()),
        None => {
            println!("Usage: continuum-wm <command>");
            println!();
            println!("Commands:");
            println!("  capture      Capture and save the focused workspace");
            println!("  load         Load and validate the saved snapshot");
            println!("  reconcile    Compare the saved snapshot with live state");
            Ok(())
        }
    }
}

fn capture() -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM — Capture");

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
    println!("Continuum-WM — Load");

    let loaded = snapshot::load()?;

    println!(
        "Loaded snapshot schema {}: workspace {} with {} window(s)",
        loaded.schema_version,
        loaded.workspace.index,
        loaded.workspace.windows.len()
    );

    Ok(())
}

fn reconcile() -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM MVP 2 — Reconciliation");
    println!();

    let saved = snapshot::load()?;
    let plan = reconcile::reconcile(&saved)?;

    println!(
        "Saved workspace {} contains {} window(s):",
        saved.workspace.index,
        plan.windows.len()
    );

    println!();

    for window in &plan.windows {
        let status = match window.status {
            WindowStatus::Present => "PRESENT",
            WindowStatus::Missing => "MISSING",
        };

        let app_id = window.app_id.as_deref().unwrap_or("<unknown>");
        let title = window.title.as_deref().unwrap_or("<untitled>");

        println!(
            "[{status}] runtime={} app={} title={}",
            window.runtime_id, app_id, title
        );
    }

    let present = plan
        .windows
        .iter()
        .filter(|window| window.status == WindowStatus::Present)
        .count();

    let missing = plan.windows.len() - present;

    println!();
    println!("Plan: {present} present, {missing} missing");
    println!("No desktop changes were made.");

    Ok(())
}
