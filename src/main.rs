mod model;
mod niri;
mod reconcile;
mod snapshot;

use std::env;
use std::error::Error;
use std::io::{self, Write};
use std::process::Command;

use reconcile::WindowStatus;

fn main() -> Result<(), Box<dyn Error>> {
    let mut args = env::args().skip(1);

    match args.next().as_deref() {
        Some("capture") => capture(),
        Some("load") => load(),
        Some("reconcile") => reconcile(),
        Some("launch") => {
            let runtime_id = args
                .next()
                .ok_or("Usage: continuum-wm launch <saved-runtime-id>")?
                .parse::<u64>()
                .map_err(|_| "Saved runtime ID must be an unsigned integer")?;

            if args.next().is_some() {
                return Err("Usage: continuum-wm launch <saved-runtime-id>".into());
            }

            launch(runtime_id)
        }
        Some(command) => Err(format!("Unknown command: {command}").into()),
        None => {
            print_usage();
            Ok(())
        }
    }
}

fn print_usage() {
    println!("Usage: continuum-wm <command>");
    println!();
    println!("Commands:");
    println!("  capture                 Capture and save the focused workspace");
    println!("  load                    Load and validate the saved snapshot");
    println!("  reconcile               Compare saved snapshot with live state");
    println!("  launch <runtime-id>     Launch one missing saved window");
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

fn launch(runtime_id: u64) -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM MVP 3 — Launch");
    println!();

    let saved = snapshot::load()?;
    let plan = reconcile::reconcile(&saved)?;

    let planned_window = plan
        .windows
        .iter()
        .find(|window| window.runtime_id == runtime_id)
        .ok_or_else(|| format!("Saved window runtime ID {runtime_id} was not found"))?;

    if planned_window.status == WindowStatus::Present {
        return Err(format!(
            "Saved window runtime ID {runtime_id} is already present; refusing to launch a duplicate"
        )
        .into());
    }

    let saved_window = saved
        .workspace
        .windows
        .iter()
        .find(|window| window.runtime_id == runtime_id)
        .ok_or_else(|| format!("Saved window runtime ID {runtime_id} was not found"))?;

    let launch_command = saved_window.launch_command.as_ref().ok_or_else(|| {
        format!("Saved window runtime ID {runtime_id} has no captured launch command")
    })?;

    let program = launch_command
        .first()
        .ok_or("Captured launch command is unexpectedly empty")?;

    let arguments = &launch_command[1..];

    println!("Saved window:");
    println!(
        "  app:   {}",
        saved_window.app_id.as_deref().unwrap_or("<unknown>")
    );
    println!(
        "  title: {}",
        saved_window.title.as_deref().unwrap_or("<untitled>")
    );
    println!("  saved runtime ID: {}", saved_window.runtime_id);
    println!();

    println!("Captured launch argv:");

    for (index, argument) in launch_command.iter().enumerate() {
        println!("  argv[{index}] = {argument:?}");
    }

    println!();
    println!("Continuum has not matched any future window to this saved window.");
    println!("This action will only start the captured process.");
    println!();

    print!("Launch this process? [y/N]: ");
    io::stdout().flush()?;

    let mut answer = String::new();
    io::stdin().read_line(&mut answer)?;

    if !matches!(answer.trim().to_ascii_lowercase().as_str(), "y" | "yes") {
        println!("Launch cancelled.");
        return Ok(());
    }

    let child = Command::new(program).args(arguments).spawn()?;

    println!();
    println!("Process started with PID {}.", child.id());
    println!("No window matching or placement was attempted.");

    Ok(())
}
