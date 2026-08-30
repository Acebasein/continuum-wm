mod model;
mod niri;
mod snapshot;

use std::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    println!("Continuum-WM MVP 0");

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
