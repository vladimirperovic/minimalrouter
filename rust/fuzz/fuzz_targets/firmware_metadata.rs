#![no_main]
use libfuzzer_sys::fuzz_target;
use mr_core::firmware::{self, FirmwareManifest};
fuzz_target!(|data: &[u8]| {
    if data.len() > 64 * 1024 {
        return;
    }
    if let Ok(manifest) = serde_json::from_slice::<FirmwareManifest>(data) {
        let _ = firmware::validate_appliance_payload(Some(&manifest));
        for arch in ["amd64", "arm64", "invalid"] {
            let _ = firmware::validate_appliance_architecture(Some(&manifest), arch);
        }
        let _ = firmware::verify_manifest(Some(&manifest), &[7; 32]);
        let _ = firmware::compare_release_versions(&manifest.version, "0.4.1");
    }
    if let Ok(name) = std::str::from_utf8(data) {
        if let Ok(path) = mr_api::release::fuzz_archive_path(name) {
            assert!(path == "minimalrouter-linux-amd64" || path.starts_with("minimalrouter-linux-amd64/"));
            assert!(path.split('/').all(|part| part != ".."));
            assert!(!std::path::Path::new(&path).is_absolute());
        }
    }
});
