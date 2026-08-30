mod matching;
mod model;
mod niri;
mod reconcile;
mod snapshot;

use std::collections::HashSet;
use std::env;
use std::error::Error;
use std::io::{self, Write};
use std::process::Command;
use std::time::Duration;

use matching::MatchResult;
use reconcile::WindowStatus;

const MATCH_OBSERVATION_TIMEOUT: Duration = Duration::from_secs(5);

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
        Some(command) => Err(format!("Unknown command: {command}").into()),
        None => {
            print_usage();
            Ok(())
        }
    }
}

fn parse_runtime_id(value: Option<String>) -> Result<u64, Box<dyn Error>> {
    value
        .ok_or_else(|| "Missing saved runtime ID".into())
        .and_then(|value| {
            value
                .parse::<u64>()
                .map_err(|_| "Saved runtime ID must be an unsigned integer".into())
        })
}

fn print_usage() {
    println!("Usage: continuum-wm <command>");
    println!();
    println!("Commands:");
    println!("  capture                     Capture and save the focused workspace");
    println!("  load                        Load and validate the saved snapshot");
    println!("  reconcile                   Compare saved snapshot with live state");
    println!("  launch <runtime-id>         Launch one missing saved window");
    println!("  launch-match <runtime-id>   Launch and observe matching candidates");
    println!("  observe-match <app-id>      Observe matching candidates without launch");
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
        "Continuum-WM MVP 4 — Launch + Match Observation"
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
    println!("This action will only start the captured process.");
    println!("No placement or desktop manipulation will be performed.");

    if observe_match {
        println!(
            "Continuum will observe new Niri windows for {} seconds after launch.",
            MATCH_OBSERVATION_TIMEOUT.as_secs()
        );
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
        println!("Observing matching candidates...");

        let candidates =
            stream.collect_new_windows(baseline_ids, expected_app_id, MATCH_OBSERVATION_TIMEOUT)?;

        println!();

        print_match_result(matching::classify_candidates(candidates), expected_app_id);
    } else {
        println!("No window matching or placement was attempted.");
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
        MATCH_OBSERVATION_TIMEOUT.as_secs()
    );

    let candidates =
        stream.collect_new_windows(&baseline_ids, expected_app_id, MATCH_OBSERVATION_TIMEOUT)?;

    println!();

    print_match_result(matching::classify_candidates(candidates), expected_app_id);

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
