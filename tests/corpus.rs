//! Opt-in local corpus smoke tests.

use std::{fs, path::Path};

use pagemark::{extract, Options};

fn html_files(root: &Path, output: &mut Vec<std::path::PathBuf>) {
    let Ok(entries) = fs::read_dir(root) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_dir() {
            html_files(&path, output);
        } else if path
            .extension()
            .is_some_and(|extension| extension == "html")
        {
            output.push(path);
        }
    }
}

#[test]
#[ignore = "run explicitly after initializing optional corpora"]
fn corpus_does_not_panic() {
    let mut fixtures = Vec::new();
    html_files(Path::new("testdata"), &mut fixtures);
    assert!(!fixtures.is_empty());
    for fixture in fixtures {
        let Ok(html) = fs::read_to_string(&fixture) else {
            continue;
        };
        let result = std::panic::catch_unwind(|| extract(&html, None, &Options::default()));
        assert!(result.is_ok(), "panic for {}", fixture.display());
    }
}
