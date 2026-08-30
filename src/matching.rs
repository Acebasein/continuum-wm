use crate::niri::NiriWindow;

#[derive(Debug)]
pub enum MatchResult {
    Matched(NiriWindow),
    Ambiguous(Vec<NiriWindow>),
    NoMatch,
}

pub fn classify_candidates(candidates: Vec<NiriWindow>) -> MatchResult {
    match candidates.len() {
        0 => MatchResult::NoMatch,
        1 => MatchResult::Matched(
            candidates
                .into_iter()
                .next()
                .expect("one candidate must exist"),
        ),
        _ => MatchResult::Ambiguous(candidates),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::niri::NiriLayout;

    fn test_window(id: u64) -> NiriWindow {
        NiriWindow {
            id,
            title: Some("Test".to_string()),
            app_id: Some("example".to_string()),
            pid: Some(1000),
            workspace_id: Some(1),
            is_focused: false,
            is_floating: false,
            layout: NiriLayout {
                pos_in_scrolling_layout: Some([1, 1]),
                tile_size: [100.0, 100.0],
                window_size: [100, 100],
                tile_pos_in_workspace_view: None,
                window_offset_in_tile: [0.0, 0.0],
            },
        }
    }

    #[test]
    fn zero_candidates_is_no_match() {
        assert!(matches!(
            classify_candidates(Vec::new()),
            MatchResult::NoMatch
        ));
    }

    #[test]
    fn one_candidate_is_match() {
        let result = classify_candidates(vec![test_window(10)]);

        match result {
            MatchResult::Matched(window) => assert_eq!(window.id, 10),
            _ => panic!("expected matched result"),
        }
    }

    #[test]
    fn multiple_candidates_are_ambiguous() {
        let result = classify_candidates(vec![test_window(10), test_window(20)]);

        match result {
            MatchResult::Ambiguous(windows) => assert_eq!(windows.len(), 2),
            _ => panic!("expected ambiguous result"),
        }
    }
}
