// Continuum-WM CLI with mixed-terminal capture/restore test commands
mod matching;
mod model;
mod niri;
mod reconcile;
mod snapshot;
mod terminal_restore;

use std::collections::HashSet;
use std::env;
use std::error::Error;
use std::io::{self, Write};
use std::process::Command;
use std::time::Duration;

use matching::MatchResult;
use reconcile::WindowStatus;

const LAUNCH_MATCH_TIMEOUT: Duration = Duration::from_secs(5);
const MANUAL_OBSERVATION_TIMEOUT: Duration = Duration::from_secs(10);

fn main() -> Result<(), Box<dyn Error>> {
    let mut args = env::args().skip(1);

    match args.next().as_deref() {
        Some("capture") => capture(),
        Some("load") => load(),
        Some("reconcile") => reconcile(),
        Some("launch") => {
            let runtime_id = parse_runtime_id(args.next())?;

            if args.next().is_some() {
                return Err("Usage: continuum-wm launch <saved-runtime-id>".into());
            }

            launch(runtime_id, false)
        }
        Some("launch-match") => {
            let runtime_id = parse_runtime_id(args.next())?;

            if args.next().is_some() {
                return Err("Usage: continuum-wm launch-match <saved-runtime-id>".into());
            }

            launch(runtime_id, true)
        }
        Some("observe-match") => {
            let app_id = args
                .next()
                .ok_or("Usage: continuum-wm observe-match <app-id>")?;

            if args.next().is_some() {
                return Err("Usage: continuum-wm observe-match <app-id>".into());
            }

            observe_match(&app_id)
        }
        Some("terminal-capture-test") => {
            if args.next().is_some() {
                return Err("Usage: continuum-wm terminal-capture-test".into());
            }

            terminal_restore::capture()
        }
        Some("terminal-restore-test") => {
            if args.next().is_some() {
                return Err("Usage: continuum-wm terminal-restore-test".into());
            }

            terminal_restore::run()
        }
        Some("place") => {
            let window_id = parse_required_u64(
                args.next(),
                "Missing Niri window ID",
                "Window ID must be an unsigned integer",
            )?;

            let workspace_index = parse_required_u64(
                args.next(),
                "Missing workspace index",
                "Workspace index must be an unsigned integer",
            )?;

            if args.next().is_some() {
                return Err("Usage: continuum-wm place <window-id> <workspace-index>".into());
            }

            place(window_id, workspace_index)
        }
        Some(command) => Err(format!("Unknown command: {command}").into()),
        None => {
            print_usage();
            Ok(())
        }
    }
}

fn parse_runtime_id(value: Option<String>) -> Result<u64, Box<dyn Error>> {
    parse_required_u64(
        value,
        "Missing saved runtime ID",
        "Saved runtime ID must be an unsigned integer",
    )
}

fn parse_required_u64(
    value: Option<String>,
    missing_message: &'static str,
    invalid_message: &'static str,
) -> Result<u64, Box<dyn Error>> {
    value
        .ok_or_else(|| missing_message.into())
        .and_then(|value| value.parse::<u64>().map_err(|_| invalid_message.into()))
}

fn print_usage() {
    println!("Usage: continuum-wm <command>");
    println!();
    println!("Commands:");
    println!("  capture                     Capture and save the focused workspace");
    println!("  load                        Load and validate the saved snapshot");
    println!("  reconcile                   Compare saved snapshot with live state");
    println!("  launch <runtime-id>         Launch one missing saved window");
    println!("  launch-match <runtime-id>   Launch, match, place, size, and order");
    println!("  observe-match <app-id>      Observe matching candidates without launch");
    println!("  place <window-id> <index>   Move one exact Niri window to a workspace");
    println!("  terminal-capture-test       Auto-capture live terminal continuity state");
    println!("  terminal-restore-test       Restore the captured terminal continuity state");
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

fn launch(runtime_id: u64, observe_match: bool) -> Result<(), Box<dyn Error>> {
    let heading = if observe_match {
        "Continuum-WM MVP 6 — Launch + Match + Layout"
    } else {
        "Continuum-WM MVP 3 — Launch"
    };

    println!("{heading}");
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

    let expected_app_id = saved_window
        .app_id
        .as_deref()
        .ok_or_else(|| format!("Saved window runtime ID {runtime_id} has no app_id"))?;

    let launch_command = saved_window.launch_command.as_ref().ok_or_else(|| {
        format!("Saved window runtime ID {runtime_id} has no captured launch command")
    })?;

    let program = launch_command
        .first()
        .ok_or("Captured launch command is unexpectedly empty")?;

    let arguments = &launch_command[1..];

    println!("Saved window:");
    println!("  app:   {expected_app_id}");
    println!(
        "  title: {}",
        saved_window.title.as_deref().unwrap_or("<untitled>")
    );
    println!("  saved runtime ID: {}", saved_window.runtime_id);
    println!("  target workspace index: {}", saved.workspace.index);

    if let Some([column, row]) = saved_window.layout.scrolling_position {
        println!("  saved scrolling position: [{column},{row}]");
    } else {
        println!("  saved scrolling position: <none>");
    }

    println!(
        "  saved window size: {} × {}",
        saved_window.layout.window_size[0], saved_window.layout.window_size[1]
    );
    println!();

    println!("Captured launch argv:");

    for (index, argument) in launch_command.iter().enumerate() {
        println!("  argv[{index}] = {argument:?}");
    }

    let mut event_stream = if observe_match {
        println!();
        println!("Opening Niri event stream and establishing live-window baseline...");

        let mut stream = niri::EventStream::open()?;
        let baseline_windows = stream.wait_for_initial_windows()?;

        let baseline_ids: HashSet<u64> = baseline_windows.iter().map(|window| window.id).collect();

        println!(
            "Baseline established with {} live window(s).",
            baseline_ids.len()
        );

        Some((stream, baseline_ids))
    } else {
        None
    };

    println!();

    if observe_match {
        println!("This action will:");
        println!("  1. start the exact captured process");
        println!("  2. observe newly created Niri windows");
        println!("  3. classify the result conservatively");
        println!();
        println!("Layout restoration is NOT automatic.");
        println!("A uniquely matched window requires a second confirmation.");
    } else {
        println!("This action will only start the captured process.");
        println!("No matching or layout restoration will be performed.");
    }

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

    if let Some((stream, baseline_ids)) = event_stream.as_mut() {
        println!(
            "Observing matching candidates for {} seconds...",
            LAUNCH_MATCH_TIMEOUT.as_secs()
        );

        let candidates =
            stream.collect_new_windows(baseline_ids, expected_app_id, LAUNCH_MATCH_TIMEOUT)?;

        println!();

        handle_launch_match_result(
            matching::classify_candidates(candidates),
            expected_app_id,
            saved.workspace.index,
            saved_window.layout.window_size,
            saved_window.layout.scrolling_position,
        )?;
    } else {
        println!("No window matching or layout restoration was attempted.");
    }

    Ok(())
}

fn handle_launch_match_result(
    result: MatchResult,
    expected_app_id: &str,
    target_workspace_index: u64,
    saved_window_size: [i32; 2],
    saved_position: Option<[u32; 2]>,
) -> Result<(), Box<dyn Error>> {
    match result {
        MatchResult::Matched(window) => {
            println!("MATCHED");
            println!("  new runtime ID: {}", window.id);
            println!(
                "  app_id: {}",
                window.app_id.as_deref().unwrap_or("<unknown>")
            );
            println!(
                "  title: {}",
                window.title.as_deref().unwrap_or("<untitled>")
            );
            println!(
                "  PID: {}",
                window
                    .pid
                    .map(|pid| pid.to_string())
                    .unwrap_or_else(|| "<unknown>".to_string())
            );
            println!();

            println!("Restoration candidate:");
            println!("  exact matched window ID: {}", window.id);
            println!("  target saved workspace index: {target_workspace_index}");
            println!(
                "  target saved window size: {} × {}",
                saved_window_size[0], saved_window_size[1]
            );

            if let Some([column, row]) = saved_position {
                println!("  target saved scrolling position: [{column},{row}]");
            }

            println!();
            println!("Only this exact newly matched runtime window may be changed.");
            println!("Width and height use exact-window Niri actions.");
            println!("Column restoration is attempted only after safety checks.");
            println!();

            print!("Restore matched window {}? [y/N]: ", window.id);
            io::stdout().flush()?;

            let mut answer = String::new();
            io::stdin().read_line(&mut answer)?;

            if !matches!(answer.trim().to_ascii_lowercase().as_str(), "y" | "yes") {
                println!("Restoration cancelled.");
                println!("The matched window was left untouched.");
                return Ok(());
            }

            niri::move_window_to_workspace(window.id, target_workspace_index)?;

            println!(
                "PLACED: matched Niri window {} moved to workspace index {}.",
                window.id, target_workspace_index
            );

            niri::set_window_width(window.id, saved_window_size[0])?;
            niri::set_window_height(window.id, saved_window_size[1])?;

            println!(
                "SIZE RESTORED: Niri window {} requested at {} × {}.",
                window.id, saved_window_size[0], saved_window_size[1]
            );

            restore_saved_column(window.id, saved_position)?;
        }
        MatchResult::Ambiguous(windows) => {
            println!("AMBIGUOUS");
            println!(
                "{} new windows matched app_id {expected_app_id:?}:",
                windows.len()
            );

            for window in windows {
                println!(
                    "  runtime={} pid={} title={}",
                    window.id,
                    window
                        .pid
                        .map(|pid| pid.to_string())
                        .unwrap_or_else(|| "<unknown>".to_string()),
                    window.title.as_deref().unwrap_or("<untitled>")
                );
            }

            println!();
            println!("Continuum refused to select a window.");
            println!("LAYOUT RESTORATION BLOCKED.");
            println!("No desktop manipulation was attempted.");
        }
        MatchResult::NoMatch => {
            println!("NO MATCH");
            println!(
                "No new window with app_id {expected_app_id:?} appeared during the observation window."
            );
            println!("LAYOUT RESTORATION BLOCKED.");
            println!("No desktop manipulation was attempted.");
        }
    }

    Ok(())
}

fn restore_saved_column(
    window_id: u64,
    saved_position: Option<[u32; 2]>,
) -> Result<(), Box<dyn Error>> {
    let Some([saved_column, saved_row]) = saved_position else {
        println!("COLUMN RESTORE SKIPPED: saved window has no scrolling position.");
        return Ok(());
    };

    if saved_row != 1 {
        println!(
            "COLUMN RESTORE SKIPPED: saved position [{saved_column},{saved_row}] uses a multi-row layout."
        );
        println!("MVP 6.2 restores only single-window-column positions.");
        return Ok(());
    }

    if !niri::window_is_alone_in_column(window_id)? {
        println!(
            "COLUMN RESTORE BLOCKED: Niri window {window_id} currently shares its column with another window."
        );
        println!("Continuum will not move a column containing unrelated live windows.");
        return Ok(());
    }

    let previous_focus = niri::focused_window_id()?;

    println!("COLUMN RESTORE: moving Niri window {window_id} to saved column {saved_column}.");

    niri::focus_window(window_id)?;

    let move_result = niri::move_focused_column_to_index(saved_column);

    if let Some(previous_focus_id) = previous_focus
        && previous_focus_id != window_id
        && let Err(error) = niri::focus_window(previous_focus_id)
    {
        eprintln!(
            "WARNING: failed to restore previous focus to window {previous_focus_id}: {error}"
        );
    }

    move_result?;

    let restored = niri::window_by_id(window_id)?
        .ok_or_else(|| format!("Niri window {window_id} disappeared during column restoration"))?;

    let restored_position = restored.layout.pos_in_scrolling_layout.ok_or_else(|| {
        format!("Niri window {window_id} has no scrolling position after restore")
    })?;

    if restored_position[0] != saved_column {
        return Err(format!(
            "Column verification failed for Niri window {window_id}: expected column {saved_column}, got {}",
            restored_position[0]
        )
        .into());
    }

    println!(
        "COLUMN RESTORED: Niri window {window_id} verified at column {}.",
        restored_position[0]
    );

    if restored_position[1] == saved_row {
        println!(
            "POSITION VERIFIED: Niri reports [{},{}].",
            restored_position[0], restored_position[1]
        );
    }

    Ok(())
}

fn observe_match(expected_app_id: &str) -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM MVP 4 — Match Observation Diagnostic");
    println!();
    println!("Expected app_id: {expected_app_id:?}");
    println!("No process will be launched.");
    println!("No desktop changes will be made.");
    println!();

    println!("Opening Niri event stream and establishing live-window baseline...");

    let mut stream = niri::EventStream::open()?;
    let baseline_windows = stream.wait_for_initial_windows()?;

    let baseline_ids: HashSet<u64> = baseline_windows.iter().map(|window| window.id).collect();

    println!(
        "Baseline established with {} live window(s).",
        baseline_ids.len()
    );

    println!(
        "Observing new Niri windows for {} seconds...",
        MANUAL_OBSERVATION_TIMEOUT.as_secs()
    );

    let candidates =
        stream.collect_new_windows(&baseline_ids, expected_app_id, MANUAL_OBSERVATION_TIMEOUT)?;

    println!();

    print_match_result(matching::classify_candidates(candidates), expected_app_id);

    Ok(())
}

fn place(window_id: u64, workspace_index: u64) -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM MVP 5 — Placement Diagnostic");
    println!();
    println!("Window runtime ID: {window_id}");
    println!("Target workspace index: {workspace_index}");
    println!();
    println!("This action WILL move exactly one Niri window.");
    println!("Window focus will not follow the move.");
    println!("No other placement or layout changes will be made.");
    println!();

    print!("Move this window? [y/N]: ");
    io::stdout().flush()?;

    let mut answer = String::new();
    io::stdin().read_line(&mut answer)?;

    if !matches!(answer.trim().to_ascii_lowercase().as_str(), "y" | "yes") {
        println!("Placement cancelled.");
        return Ok(());
    }

    niri::move_window_to_workspace(window_id, workspace_index)?;

    println!();
    println!("Requested move of Niri window {window_id} to workspace index {workspace_index}.");

    Ok(())
}

fn print_match_result(result: MatchResult, expected_app_id: &str) {
    match result {
        MatchResult::Matched(window) => {
            println!("MATCHED");
            println!("  new runtime ID: {}", window.id);
            println!(
                "  app_id: {}",
                window.app_id.as_deref().unwrap_or("<unknown>")
            );
            println!(
                "  title: {}",
                window.title.as_deref().unwrap_or("<untitled>")
            );
            println!(
                "  PID: {}",
                window
                    .pid
                    .map(|pid| pid.to_string())
                    .unwrap_or_else(|| "<unknown>".to_string())
            );
            println!();
            println!("Diagnostic only.");
            println!("No placement or desktop manipulation was attempted.");
        }
        MatchResult::Ambiguous(windows) => {
            println!("AMBIGUOUS");
            println!(
                "{} new windows matched app_id {expected_app_id:?}:",
                windows.len()
            );

            for window in windows {
                println!(
                    "  runtime={} pid={} title={}",
                    window.id,
                    window
                        .pid
                        .map(|pid| pid.to_string())
                        .unwrap_or_else(|| "<unknown>".to_string()),
                    window.title.as_deref().unwrap_or("<untitled>")
                );
            }

            println!();
            println!("Continuum refused to select a window.");
            println!("No placement or desktop manipulation was attempted.");
        }
        MatchResult::NoMatch => {
            println!("NO MATCH");
            println!(
                "No new window with app_id {expected_app_id:?} appeared during the observation window."
            );
            println!("No placement or desktop manipulation was attempted.");
        }
    }
}
