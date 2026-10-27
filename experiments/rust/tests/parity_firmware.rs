//! Go-signed manifests must verify in Rust, and Rust must reproduce Go's
//! deterministic Ed25519 signature from the same key: proof that the canonical
//! signed payload is byte-identical. Version precedence is compared too.

use std::io::BufRead;

use minimalrouter::firmware::{self, FirmwareManifest};
use minimalrouter::goish::json;
use serde::Deserialize;

#[path = "parity_config.rs"]
#[allow(dead_code)]
mod parity_config;

#[derive(Deserialize)]
struct FirmwareCase {
    private_key: String,
    public_key: String,
    manifest: String,
    indented: String,
    signature: String,
}

#[derive(Deserialize)]
struct VersionCase {
    left: String,
    right: String,
    compare: i32,
    error: bool,
    valid: bool,
}

#[test]
fn firmware_signatures_match_go() {
    let Some(path) = parity_config::case_path("firmware_cases.jsonl") else {
        eprintln!("skipping: run `go run ./experiments/rust/_goparity` first");
        return;
    };
    let mut count = 0;
    for line in std::io::BufReader::new(std::fs::File::open(path).unwrap()).lines() {
        let case: FirmwareCase = serde_json::from_str(&line.unwrap()).unwrap();
        let public = hex::decode(&case.public_key).unwrap();
        let private = hex::decode(&case.private_key).unwrap();
        let manifest: FirmwareManifest = json::unmarshal(case.manifest.as_bytes()).unwrap();
        firmware::verify_manifest(Some(&manifest), &public).unwrap_or_else(|e| panic!("Go-signed manifest rejected: {e}\n{}", case.manifest));
        let mut resigned = manifest.clone();
        firmware::sign_manifest(&mut resigned, &private).unwrap();
        assert_eq!(resigned.signature, case.signature, "payload differs for {}", case.manifest);
        assert_eq!(String::from_utf8(json::marshal_indent(&manifest, "  ").unwrap()).unwrap(), case.indented);
        count += 1;
    }
    assert!(count > 0);
}

#[test]
fn version_precedence_matches_go() {
    let Some(path) = parity_config::case_path("version_cases.jsonl") else {
        return;
    };
    for line in std::io::BufReader::new(std::fs::File::open(path).unwrap()).lines() {
        let case: VersionCase = serde_json::from_str(&line.unwrap()).unwrap();
        let got = firmware::compare_release_versions(&case.left, &case.right);
        assert_eq!(got.is_err(), case.error, "{} vs {}", case.left, case.right);
        if let Ok(cmp) = got {
            assert_eq!(cmp, case.compare, "{} vs {}", case.left, case.right);
        }
        assert_eq!(firmware::is_release_version(&case.left), case.valid, "{}", case.left);
    }
}
