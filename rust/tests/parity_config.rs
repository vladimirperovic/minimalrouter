//! Differential tests against the Go implementation.
//!
//! Cases are produced by `go run ./rust/_goparity` (see
//! `tools/parity.sh`). The test is skipped when the case file is absent so a
//! plain `cargo test` still works without a Go toolchain.

use std::collections::BTreeMap;

use minimalrouter::config::{self, SystemConfig, ValidationError};
use minimalrouter::goish::json;
use serde::Deserialize;

#[derive(Deserialize)]
struct Outcome {
    validate: Option<Vec<ValidationError>>,
    scenario: Option<Vec<ValidationError>>,
}

#[derive(Deserialize)]
struct Case {
    name: String,
    input: String,
    unmarshal_ok: bool,
    #[serde(default)]
    marshal: String,
    outcome: Outcome,
    changes_from_base: Option<Vec<ValidationError>>,
    requires_confirmation: bool,
    #[serde(default)]
    migrated: String,
    #[serde(default)]
    trusted: BTreeMap<String, bool>,
}

fn errors_of(result: Result<(), config::ValidationErrors>) -> Vec<ValidationError> {
    result.err().map(|e| e.0).unwrap_or_default()
}

/// Go iterates `day_windows` in random map order; compare those messages as a set.
fn normalise(mut errs: Vec<ValidationError>) -> Vec<ValidationError> {
    let (mut days, rest): (Vec<_>, Vec<_>) = errs.drain(..).partition(|e| e.field.contains(".day_windows."));
    days.sort_by(|a, b| (&a.field, &a.message).cmp(&(&b.field, &b.message)));
    let mut out = rest;
    out.extend(days);
    out
}

/// The lines of a committed fixture, `tests/fixtures/<name>.gz`. The fixtures
/// were generated once from the last Go implementation (see
/// `tests/fixtures/README.md`). A missing fixture fails the test: skipping
/// would let a broken checkout pass silently.
pub fn case_lines(name: &str) -> Vec<String> {
    use std::io::Read;
    let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures").join(format!("{name}.gz"));
    let file = std::fs::File::open(&path).unwrap_or_else(|e| panic!("fixture {} is missing: {e}", path.display()));
    let mut text = String::new();
    flate2::read::GzDecoder::new(file).read_to_string(&mut text).unwrap_or_else(|e| panic!("fixture {} is corrupt: {e}", path.display()));
    text.lines().map(str::to_owned).collect()
}

#[test]
fn config_matches_go() {
    let bases: BTreeMap<&str, SystemConfig> = BTreeMap::new();
    let mut bases = bases;
    let mut failures = Vec::new();
    let mut total = 0;
    for line in case_lines("config_cases.jsonl") {
        let case: Case = serde_json::from_str(&line).unwrap();
        total += 1;
        let parsed: Result<SystemConfig, _> = json::unmarshal(case.input.as_bytes());
        if parsed.is_ok() != case.unmarshal_ok {
            failures.push(format!("{}: unmarshal ok rust={} go={}", case.name, parsed.is_ok(), case.unmarshal_ok));
            continue;
        }
        let Ok(cfg) = parsed else { continue };
        if !case.name.contains('-') {
            bases.insert(if case.name == "default" { "default" } else { "full" }, cfg.clone());
        }
        let base_name = if case.name.starts_with("default") { "default" } else { "full" };
        let base = bases.get(base_name).cloned().expect("base cases come first");

        let marshal = json::marshal_string(&cfg).unwrap();
        if marshal != case.marshal {
            failures.push(format!("{}: marshal differs\n  rust: {}\n  go:   {}", case.name, marshal, case.marshal));
        }
        let validate = normalise(errors_of(cfg.validate()));
        let expected = normalise(case.outcome.validate.unwrap_or_default());
        if validate != expected {
            failures.push(format!("{}: validate differs\n  rust: {:?}\n  go:   {:?}", case.name, validate, expected));
        }
        let scenario = errors_of(cfg.validate_scenario_safety());
        if scenario != case.outcome.scenario.unwrap_or_default() {
            failures.push(format!("{}: scenario safety differs: {:?}", case.name, scenario));
        }
        let changes = normalise(errors_of(cfg.validate_changes_from(Some(&base))));
        let expected_changes = normalise(case.changes_from_base.unwrap_or_default());
        if changes != expected_changes {
            failures.push(format!("{}: changes_from differs\n  rust: {:?}\n  go:   {:?}", case.name, changes, expected_changes));
        }
        if config::requires_confirmation(&base, &cfg) != case.requires_confirmation {
            failures.push(format!("{}: requires_confirmation differs", case.name));
        }
        let mut migrated = cfg.clone();
        migrated.migrate_legacy_fields();
        let migrated = json::marshal_string(&migrated).unwrap();
        if migrated != case.migrated {
            failures.push(format!("{}: migrate_legacy_fields differs", case.name));
        }
        for (probe, expected) in &case.trusted {
            if cfg.is_trusted_client_address(probe) != *expected {
                failures.push(format!("{}: is_trusted_client_address({}) differs", case.name, probe));
            }
        }
    }
    if !failures.is_empty() {
        let shown: Vec<_> = failures.iter().take(15).collect();
        panic!("{} of {} config cases differ from Go:\n{}", failures.len(), total, shown.iter().map(|s| s.as_str()).collect::<Vec<_>>().join("\n"));
    }
    eprintln!("{} config cases identical to Go", total);
}
