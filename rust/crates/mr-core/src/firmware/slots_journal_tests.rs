//! Persisted crash boundaries and deterministic journal corruption regressions.
//! These are not a race detector or coverage-guided fuzzing substitute.

use super::*;

struct Fixture {
    _directory: TempDir,
    manager: SlotManager,
    operation: SlotOperation,
}

impl Fixture {
    fn activation() -> Self {
        let directory = TempDir(fsutil::mkdir_temp(std::env::temp_dir().to_str().unwrap(), "mr-slot-journal-").unwrap());
        let manager = SlotManager { root: join(&directory.0, "root"), ..Default::default() };
        manager.ensure_root().unwrap();
        for version in ["0.2.0", "0.3.0", "0.4.0", "0.5.0", "0.0.0+bootstrap.1234"] {
            std::fs::create_dir_all(join(&join(&manager.root, "slots"), version)).unwrap();
        }
        let old = SlotState { current: "0.3.0".into(), pending: "0.4.0".into(), minimum_version: "0.3.0".into(), ..Default::default() };
        let next = SlotState { current: "0.4.0".into(), previous: "0.3.0".into(), minimum_version: "0.4.0".into(), ..Default::default() };
        let operation = SlotOperation { version: OPERATION_JOURNAL_VERSION, kind: "activate".into(), old, next };
        Self { _directory: directory, manager, operation }
    }

    fn rollback() -> Self {
        let mut fixture = Self::activation();
        fixture.operation = SlotOperation {
            kind: "rollback".into(),
            old: fixture.operation.next,
            next: SlotState { current: "0.3.0".into(), previous: "0.4.0".into(), minimum_version: "0.4.0".into(), ..Default::default() },
            ..fixture.operation
        };
        fixture
    }

    fn write_journal(&self, data: &[u8]) {
        std::fs::write(join(&self.manager.root, OPERATION_JOURNAL_NAME), data).unwrap();
    }

    #[cfg(unix)]
    fn persist_old(&self) {
        self.manager.save_state(&self.operation.old).unwrap();
        self.manager.restore_links(&self.operation.old).unwrap();
    }
}

#[test]
fn journal_rejects_invalid_minimum_version() {
    let fixture = Fixture::activation();
    for invalid in ["../0.4.0", "not-a-version", "0.4", "0.4.0\n", "0.4.0+"] {
        for old in [false, true] {
            let mut operation = fixture.operation.clone();
            let state = if old { &mut operation.old } else { &mut operation.next };
            state.minimum_version = invalid.into();
            assert!(fixture.manager.validate_operation(&operation).is_err(), "accepted minimum_version={invalid:?}, old={old}");
        }
    }
}

#[test]
fn journal_rollback_cannot_erase_or_lower_version_floor() {
    let fixture = Fixture::rollback();
    fixture.manager.validate_operation(&fixture.operation).unwrap();
    for floor in ["", "0.2.0", "0.3.0", "0.4.0-rc.1"] {
        let mut operation = fixture.operation.clone();
        operation.next.minimum_version = floor.into();
        assert!(fixture.manager.validate_operation(&operation).is_err(), "rollback journal lowered the floor to {floor:?}");
    }
}

#[test]
fn journal_activation_requires_forward_version_even_when_floor_is_retained() {
    let fixture = Fixture::activation();
    for version in ["0.2.0", "0.3.0"] {
        let mut operation = fixture.operation.clone();
        operation.next.current = version.into();
        // A retained floor must not turn activation into implicit rollback.
        operation.next.minimum_version = "0.4.0".into();
        assert!(fixture.manager.validate_operation(&operation).is_err(), "activation accepted {version}");
    }
    let mut operation = fixture.operation.clone();
    operation.old.minimum_version = "0.4.0".into();
    assert!(fixture.manager.validate_operation(&operation).is_err(), "activation accepted a version equal to the historical floor");
}

#[test]
fn journal_installation_cannot_erase_or_lower_version_floor() {
    let fixture = Fixture::activation();
    for floor in ["", "0.2.0", "0.3.0", "0.5.0"] {
        let operation = SlotOperation {
            kind: "install".into(),
            next: SlotState { current: "0.0.0+bootstrap.1234".into(), minimum_version: floor.into(), ..Default::default() },
            ..fixture.operation.clone()
        };
        assert_eq!(fixture.manager.validate_operation(&operation).is_ok(), matches!(floor, "0.3.0" | "0.5.0"), "installation floor {floor:?}");
    }
    // An initial development bootstrap with no authenticated floor remains
    // representable, but cannot authorize an ordinary firmware activation.
    let operation = SlotOperation {
        kind: "install".into(),
        old: SlotState::default(),
        next: SlotState { current: "0.0.0+bootstrap.1234".into(), ..Default::default() },
        ..fixture.operation.clone()
    };
    fixture.manager.validate_operation(&operation).unwrap();
    let mut repair = operation.clone();
    repair.old = operation.next.clone();
    repair.next.minimum_version = "0.5.0".into();
    fixture.manager.validate_operation(&repair).unwrap();
    let mut activation = SlotOperation { old: operation.next, next: fixture.operation.next, kind: "activate".into(), ..operation };
    activation.next.previous = activation.old.current.clone();
    let error = fixture.manager.validate_operation(&activation).unwrap_err();
    assert!(crate::goish::errors::is::<ErrUnknownInstalledVersion>(&error));
}

#[test]
fn malformed_journal_corpus_is_rejected_without_rewriting_evidence() {
    let fixture = Fixture::activation();
    let valid = serde_json::to_vec(&fixture.operation).unwrap();
    let mut cases: Vec<Vec<u8>> = (0..valid.len()).map(|end| valid[..end].to_vec()).collect();
    cases.extend([b"{}".to_vec(), b"null".to_vec(), b"[]".to_vec(), vec![0xff, 0x00], b"{\"version\":999999999999999999999999}".to_vec()]);
    let value = serde_json::to_value(&fixture.operation).unwrap();
    for (path, replacement) in [
        (vec!["version"], serde_json::json!(2)),
        (vec!["kind"], serde_json::json!("execute")),
        (vec!["old", "current"], serde_json::json!("../../etc")),
        (vec!["next", "current"], serde_json::json!("0.9.0")),
        (vec!["next", "current"], serde_json::json!(null)),
        (vec!["next", "previous"], serde_json::json!("0.2.0")),
        (vec!["next", "pending"], serde_json::json!("0.4.0")),
        (vec!["old", "previous"], serde_json::json!("0.3.0")),
    ] {
        let mut malformed = value.clone();
        let mut field = &mut malformed;
        for key in path {
            field = &mut field[key];
        }
        *field = replacement;
        cases.push(serde_json::to_vec(&malformed).unwrap());
    }
    for (index, data) in cases.iter().enumerate() {
        fixture.write_journal(data);
        assert!(fixture.manager.read_operation().is_err(), "accepted malformed journal case {index}");
        assert_eq!(std::fs::read(join(&fixture.manager.root, OPERATION_JOURNAL_NAME)).unwrap(), *data, "rewrote malformed journal case {index}");
    }
    fixture.write_journal(&valid);
    assert_eq!(fixture.manager.read_operation().unwrap().unwrap(), fixture.operation);
}

#[cfg(unix)]
mod unix {
    use super::*;

    #[derive(Debug, PartialEq)]
    struct Snapshot {
        current: Option<std::path::PathBuf>,
        previous: Option<std::path::PathBuf>,
        state: Vec<u8>,
        journal: Vec<u8>,
    }

    fn snapshot(manager: &SlotManager) -> Snapshot {
        Snapshot {
            current: std::fs::read_link(join(&manager.root, "current")).ok(),
            previous: std::fs::read_link(join(&manager.root, "previous")).ok(),
            state: std::fs::read(join(&manager.root, "state.json")).unwrap(),
            journal: std::fs::read(join(&manager.root, OPERATION_JOURNAL_NAME)).unwrap(),
        }
    }

    fn assert_recovered(manager: &SlotManager, expected: &SlotState) {
        assert_eq!(manager.state().unwrap(), *expected);
        let persisted: SlotState = serde_json::from_slice(&std::fs::read(join(&manager.root, "state.json")).unwrap()).unwrap();
        assert_eq!(persisted, *expected);
        for (name, version) in [("current", &expected.current), ("previous", &expected.previous)] {
            assert_eq!(manager.link_version(name).unwrap().as_deref(), if version.is_empty() { None } else { Some(version.as_str()) });
        }
        assert!(!std::path::Path::new(&join(&manager.root, OPERATION_JOURNAL_NAME)).exists());
        manager.recover_operation().unwrap();
        assert_eq!(manager.state().unwrap(), *expected, "recovery must be idempotent");
    }

    #[test]
    fn interrupted_activation_recovers_at_every_persisted_boundary() {
        // Journal persisted; previous changed; current changed; state saved.
        // The current pointer alone selects which complete state survives.
        for boundary in 0..=3 {
            let fixture = Fixture::activation();
            let manager = &fixture.manager;
            let operation = &fixture.operation;
            fixture.persist_old();
            manager.begin_operation(operation).unwrap();
            if boundary >= 1 {
                manager.swap_link("previous", &operation.old.current).unwrap();
            }
            if boundary >= 2 {
                manager.swap_link("current", &operation.next.current).unwrap();
            }
            if boundary >= 3 {
                manager.save_state(&operation.next).unwrap();
            }
            let expected = if boundary < 2 { &operation.old } else { &operation.next };
            let before = snapshot(manager);
            assert_eq!(manager.state().unwrap(), *expected, "read-only projection at boundary {boundary}");
            assert_eq!(snapshot(manager), before, "status must not repair disk state");
            manager.recover_operation().unwrap();
            assert_recovered(manager, expected);
        }
    }

    #[test]
    fn interrupted_rollback_recovers_at_every_persisted_boundary_and_preserves_floor() {
        for boundary in 0..=3 {
            let fixture = Fixture::rollback();
            let manager = &fixture.manager;
            let operation = &fixture.operation;
            fixture.persist_old();
            manager.begin_operation(operation).unwrap();
            if boundary >= 1 {
                manager.swap_link("current", &operation.next.current).unwrap();
            }
            if boundary >= 2 {
                manager.swap_link("previous", &operation.next.previous).unwrap();
            }
            if boundary >= 3 {
                manager.save_state(&operation.next).unwrap();
            }
            let expected = if boundary == 0 { &operation.old } else { &operation.next };
            let before = snapshot(manager);
            assert_eq!(manager.state().unwrap(), *expected);
            assert_eq!(snapshot(manager), before);
            manager.recover_operation().unwrap();
            assert_recovered(manager, expected);
            assert_eq!(manager.state().unwrap().upgrade_floor().unwrap(), "0.4.0");
            assert!(manager.activate("0.2.0").is_err());
            if boundary > 0 {
                assert!(manager.activate("0.4.0").is_err(), "a recovered rollback must retain the historical floor above current");
            }
            assert_recovered(manager, expected);
        }
    }

    #[test]
    fn interrupted_first_activation_with_no_current_pointer_restores_empty_state() {
        let mut fixture = Fixture::activation();
        fixture.operation.old = SlotState { pending: "0.4.0".into(), ..Default::default() };
        fixture.operation.next.previous.clear();
        fixture.persist_old();
        fixture.manager.begin_operation(&fixture.operation).unwrap();
        fixture.manager.recover_operation().unwrap();
        assert_recovered(&fixture.manager, &fixture.operation.old);
    }

    #[test]
    fn interrupted_installation_keeps_updates_fenced_and_the_authenticated_floor() {
        for switched in [false, true] {
            let mut fixture = Fixture::rollback();
            fixture.persist_old();
            fixture.manager.begin_installation("0.5.0").unwrap();
            fixture.operation = SlotOperation {
                kind: "install".into(),
                old: fixture.manager.state().unwrap(),
                next: SlotState { current: "0.0.0+bootstrap.1234".into(), minimum_version: "0.5.0".into(), ..Default::default() },
                ..fixture.operation
            };
            fixture.manager.begin_operation(&fixture.operation).unwrap();
            fixture.manager.remove_link("previous").unwrap();
            if switched {
                fixture.manager.swap_link("current", &fixture.operation.next.current).unwrap();
            }
            fixture.manager.recover_operation().unwrap();
            let state = fixture.manager.state().unwrap();
            assert_eq!(state.current, if switched { &fixture.operation.next.current } else { &fixture.operation.old.current }.as_str());
            assert_eq!(state.minimum_version, "0.5.0");
            assert!(state.installation_pending);
            assert!(fixture.manager.activate("0.5.0").unwrap_err().to_string().contains("full installation is incomplete"));
            assert!(fixture.manager.rollback().unwrap_err().to_string().contains("full installation is incomplete"));
            assert!(fixture.manager.stage("unused", None).unwrap_err().to_string().contains("full installation is incomplete"));
            assert!(fixture.manager.begin_installation("0.4.0").is_err());
            assert!(fixture.manager.begin_installation("").is_err());
            assert!(std::path::Path::new(&join(&fixture.manager.root, INSTALLATION_JOURNAL)).exists());
        }
    }

    #[test]
    fn runtime_pointer_mismatch_is_rejected_without_recovery_mutation() {
        let fixture = Fixture::activation();
        fixture.persist_old();
        fixture.manager.begin_operation(&fixture.operation).unwrap();
        fixture.manager.swap_link("current", "0.5.0").unwrap();
        let before = snapshot(&fixture.manager);
        assert!(fixture.manager.state().is_err());
        assert!(fixture.manager.recover_operation().is_err());
        assert_eq!(snapshot(&fixture.manager), before);
    }

    #[test]
    fn failed_pointer_repair_retains_journal_for_retry() {
        let fixture = Fixture::activation();
        fixture.persist_old();
        fixture.manager.begin_operation(&fixture.operation).unwrap();
        fixture.manager.swap_link("current", &fixture.operation.next.current).unwrap();
        std::fs::create_dir(join(&fixture.manager.root, "previous")).unwrap();
        let before = snapshot(&fixture.manager);
        assert!(fixture.manager.recover_operation().is_err());
        assert_eq!(snapshot(&fixture.manager), before);
        std::fs::remove_dir(join(&fixture.manager.root, "previous")).unwrap();
        fixture.manager.recover_operation().unwrap();
        assert_recovered(&fixture.manager, &fixture.operation.next);
    }

    #[test]
    fn corrupt_journal_cannot_change_runtime_pointers_or_erase_evidence() {
        let fixture = Fixture::rollback();
        fixture.persist_old();
        let mut operation = fixture.operation.clone();
        operation.next.minimum_version.clear();
        let mut cases = vec![Vec::new(), b"{broken".to_vec(), serde_json::to_vec(&operation).unwrap()];
        operation = fixture.operation.clone();
        operation.next.current = "../../etc".into();
        cases.push(serde_json::to_vec(&operation).unwrap());
        operation = fixture.operation.clone();
        operation.next.current = "0.9.0".into();
        cases.push(serde_json::to_vec(&operation).unwrap());
        for data in cases {
            fixture.write_journal(&data);
            let before = snapshot(&fixture.manager);
            assert!(fixture.manager.state().is_err());
            assert!(fixture.manager.recover_operation().is_err());
            assert!(fixture.manager.activate("0.5.0").is_err());
            assert!(fixture.manager.rollback().is_err());
            assert!(fixture.manager.stage("unused", None).is_err());
            assert_eq!(snapshot(&fixture.manager), before);
        }
    }

    #[test]
    fn deterministic_journal_bytes_never_make_status_mutate_disk() {
        let fixture = Fixture::activation();
        fixture.persist_old();
        let valid = serde_json::to_vec(&fixture.operation).unwrap();
        let mut cases = vec![valid.clone()];
        for index in 0..valid.len() {
            let mut bytes = valid.clone();
            bytes[index] ^= 0xff;
            cases.push(bytes);
        }
        let mut seed = 0x4d52534cu32;
        for length in 0..512 {
            let bytes: Vec<_> = (0..length)
                .map(|_| {
                    seed ^= seed << 13;
                    seed ^= seed >> 17;
                    seed ^= seed << 5;
                    seed as u8
                })
                .collect();
            cases.push(bytes);
        }
        for (index, bytes) in cases.iter().enumerate() {
            fixture.write_journal(bytes);
            let before = snapshot(&fixture.manager);
            let _ = fixture.manager.state();
            assert_eq!(snapshot(&fixture.manager), before, "status mutated persisted state for corpus case {index}");
        }
    }

    #[test]
    fn operation_journal_is_private() {
        let fixture = Fixture::activation();
        fixture.persist_old();
        fixture.manager.begin_operation(&fixture.operation).unwrap();
        assert_eq!(fsutil::mode_bits(&std::fs::metadata(join(&fixture.manager.root, OPERATION_JOURNAL_NAME)).unwrap()), 0o600);
    }
}
