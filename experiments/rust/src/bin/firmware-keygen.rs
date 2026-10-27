//! firmware-keygen: creates the release-signing Ed25519 key pair. The raw
//! private key never overwrites an existing file.

use std::io::Write;
use std::path::Path;

use minimalrouter::firmware;
use minimalrouter::goish::flag::{ErrorHandling, FlagSet};

fn fatal(message: impl std::fmt::Display) -> ! {
    eprintln!("ERROR: {}", message);
    std::process::exit(1);
}

/// O_EXCL create, write, fsync and chmod; a partial file is removed.
fn write_file(path: &str, data: &[u8], mode: u32) -> std::io::Result<()> {
    if let Some(parent) = Path::new(path).parent().filter(|p| !p.as_os_str().is_empty()) {
        std::fs::create_dir_all(parent)?;
    }
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(mode);
    }
    let mut file = options.open(path)?;
    let written = file.write_all(data).and_then(|_| file.sync_all());
    drop(file);
    if let Err(err) = written {
        let _ = std::fs::remove_file(path);
        return Err(err);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode))?;
    }
    #[cfg(not(unix))]
    let _ = mode;
    Ok(())
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let mut flags = FlagSet::new(args.first().map(String::as_str).unwrap_or("firmware-keygen"), ErrorHandling::Exit);
    flags.string("private-key", "", "output path for the raw Ed25519 private key");
    flags.string("public-key", "", "output path for the hex-encoded Ed25519 public key");
    let _ = flags.parse(&args[1..]);
    let (private_path, public_path) = (flags.get("private-key"), flags.get("public-key"));
    if private_path.is_empty() || public_path.is_empty() {
        fatal("--private-key and --public-key are required");
    }
    let (public_key, private_key) = firmware::generate_key_pair().unwrap_or_else(|e| fatal(e));
    if let Err(err) = write_file(&private_path, &private_key, 0o600) {
        fatal(err);
    }
    if let Err(err) = write_file(&public_path, format!("{}\n", hex::encode(public_key)).as_bytes(), 0o644) {
        let mut message = err.to_string();
        if let Err(remove) = std::fs::remove_file(&private_path) {
            if remove.kind() != std::io::ErrorKind::NotFound {
                message = format!("{}\nremove incomplete private key: {}", message, remove);
            }
        }
        fatal(message);
    }
}
