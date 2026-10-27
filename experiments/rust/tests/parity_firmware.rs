//! Manifests signed by the last Go implementation must verify in Rust, which
//! proves the canonical signed payload is byte-identical, and re-serialise
//! to the same bytes. Version precedence is compared too. The cases are
//! committed fixtures (`tests/fixtures`); no signing key is stored.

use minimalrouter::firmware::{self, FirmwareManifest};
use minimalrouter::goish::json;
use serde::Deserialize;

#[path = "parity_config.rs"]
#[allow(dead_code)]
mod parity_config;

#[derive(Deserialize)]
struct FirmwareCase {
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
    let mut count = 0;
    for line in parity_config::case_lines("firmware_cases.jsonl") {
        let case: FirmwareCase = serde_json::from_str(&line).unwrap();
        let public = hex::decode(&case.public_key).unwrap();
        let manifest: FirmwareManifest = json::unmarshal(case.manifest.as_bytes()).unwrap();
        firmware::verify_manifest(Some(&manifest), &public).unwrap_or_else(|e| panic!("Go-signed manifest rejected: {e}\n{}", case.manifest));
        assert_eq!(manifest.signature, case.signature, "signature changed while parsing {}", case.manifest);
        assert_eq!(String::from_utf8(json::marshal_indent(&manifest, "  ").unwrap()).unwrap(), case.indented);
        count += 1;
    }
    assert!(count > 0);
}

#[test]
fn version_precedence_matches_go() {
    for line in parity_config::case_lines("version_cases.jsonl") {
        let case: VersionCase = serde_json::from_str(&line).unwrap();
        let got = firmware::compare_release_versions(&case.left, &case.right);
        assert_eq!(got.is_err(), case.error, "{} vs {}", case.left, case.right);
        if let Ok(cmp) = got {
            assert_eq!(cmp, case.compare, "{} vs {}", case.left, case.right);
        }
        assert_eq!(firmware::is_release_version(&case.left), case.valid, "{}", case.left);
    }
}
