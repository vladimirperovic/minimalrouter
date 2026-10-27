//! firmware-sign: hashes every file of a release directory and writes the
//! Ed25519-signed manifest the appliance verifies against its pinned key.

use std::path::Path;

use minimalrouter::firmware::{self, PRIVATE_KEY_SIZE, PUBLIC_KEY_SIZE};
use minimalrouter::goish::flag::{ErrorHandling, FlagSet};
use minimalrouter::goish::time::Time;

fn fatal(message: impl std::fmt::Display) -> ! {
    eprintln!("ERROR: {}", message);
    std::process::exit(1);
}

/// 64 raw bytes, or base64 of them. A raw key is arbitrary binary and is
/// never trimmed: it may begin or end with whitespace bytes.
fn load_private_key(path: &str) -> Result<Vec<u8>, String> {
    let data = std::fs::read(path).map_err(|e| e.to_string())?;
    if data.len() == PRIVATE_KEY_SIZE {
        return Ok(data);
    }
    let text = String::from_utf8_lossy(&data);
    minimalrouter::goish::base64::std_decode(text.trim()).filter(|k| k.len() == PRIVATE_KEY_SIZE).ok_or_else(|| format!("private key must be {} raw bytes or base64 thereof", PRIVATE_KEY_SIZE))
}

fn write_public_key(path: &str, key: &[u8]) -> Result<(), String> {
    if key.len() != PUBLIC_KEY_SIZE {
        return Err(firmware::ErrInvalidPublicKey.to_string());
    }
    if let Some(parent) = Path::new(path).parent().filter(|p| !p.as_os_str().is_empty()) {
        std::fs::create_dir_all(parent).map_err(|e| format!("create public key directory: {}", e))?;
    }
    std::fs::write(path, format!("{}\n", hex::encode(key))).map_err(|e| format!("write pinned public key: {}", e))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o644)).map_err(|e| e.to_string())?;
    }
    Ok(())
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let mut flags = FlagSet::new(args.first().map(String::as_str).unwrap_or("firmware-sign"), ErrorHandling::Exit);
    flags.string("dir", "", "directory containing release artifacts");
    flags.string("key", "", "file containing a raw or base64-encoded Ed25519 private key");
    flags.string("version", "", "release version");
    flags.string("commit", "", "source commit");
    flags.string("output", "release-manifest.json", "manifest output path");
    flags.string("public-key-output", "", "optional path for the hex-encoded pinned public key; write this inside --dir so it is signed");
    let _ = flags.parse(&args[1..]);
    let (directory, key_path, version, commit) = (flags.get("dir"), flags.get("key"), flags.get("version"), flags.get("commit"));
    if directory.is_empty() || key_path.is_empty() || version.is_empty() || commit.is_empty() {
        fatal("--dir, --key, --version, and --commit are required");
    }
    let private_key = load_private_key(&key_path).unwrap_or_else(|e| fatal(e));
    let public_output = flags.get("public-key-output");
    if !public_output.is_empty() {
        // Go's PrivateKey.Public returns the stored public half.
        if let Err(err) = write_public_key(&public_output, &private_key[32..]) {
            fatal(err);
        }
    }
    let mut manifest = firmware::sign_firmware(&directory, &private_key).unwrap_or_else(|e| fatal(e));
    manifest.version = version;
    manifest.build_date = Time::now_utc().rfc3339();
    manifest.git_commit = commit;
    if let Err(err) = firmware::sign_manifest(&mut manifest, &private_key) {
        fatal(err);
    }
    if let Err(err) = firmware::save_manifest(&manifest, &flags.get("output")) {
        fatal(err);
    }
}
