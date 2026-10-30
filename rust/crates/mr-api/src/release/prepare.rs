use std::fs::OpenOptions;
use std::io::{Read, Write};
use std::path::Path;

use super::*;
use crate::goish::filepath;

pub(crate) const RELEASE_DOWNLOAD_BASE_URL: &str = "https://github.com/vladimirperovic/minimalrouter/releases/download/";
const MAX_RELEASE_MANIFEST: u64 = 1 << 20;
const MAX_RELEASE_ARCHIVE: u64 = 128 << 20;
const MAX_EXPANDED_RELEASE: u64 = 256 << 20;

fn join(root: &str, relative: &str) -> String {
    Path::new(root).join(relative).to_string_lossy().into_owned()
}

fn create_private(path: &str) -> std::io::Result<std::fs::File> {
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    options.open(path)
}

#[cfg(unix)]
fn chmod(path: &str, mode: u32) -> std::io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode))
}

#[cfg(not(unix))]
fn chmod(_path: &str, _mode: u32) -> std::io::Result<()> {
    Ok(())
}

/// Copies at most `maximum` bytes; more means the source exceeds the limit.
fn copy_limited(input: impl Read, destination: &str, maximum: u64, too_large: &str) -> Result<()> {
    let mut output = create_private(destination)?;
    let written = std::io::copy(&mut input.take(maximum + 1), &mut output)?;
    output.flush()?;
    drop(output);
    if written > maximum {
        return Err(errorf!("{}", too_large));
    }
    Ok(())
}

fn download_release_file(asset_url: &str, destination: &str, maximum: u64) -> Result<()> {
    if !asset_url.starts_with("https://github.com/vladimirperovic/minimalrouter/releases/download/") {
        return Err(errorf!("release asset URL is outside the canonical Minimal Router GitHub release path"));
    }
    // Redirects stay on HTTPS (GitHub hands assets to its object storage).
    let agent = ureq::AgentBuilder::new().timeout(StdDuration::from_secs(120)).redirects(5).https_only(true).build();
    let response = match agent.get(asset_url).set("User-Agent", USER_AGENT).call() {
        Ok(response) => response,
        Err(ureq::Error::Status(code, _)) => return Err(errorf!("release asset returned HTTP {}", code)),
        Err(err) => return Err(errorf!("{}", err)),
    };
    if response.status() != 200 {
        return Err(errorf!("release asset returned HTTP {}", response.status()));
    }
    if response.header("content-length").and_then(|v| v.trim().parse::<u64>().ok()).is_some_and(|n| n > maximum) {
        return Err(errorf!("release asset exceeds size limit"));
    }
    copy_limited(response.into_reader(), destination, maximum, "release asset exceeds size limit")
}

fn safe_archive_path(name: &str, prefix: &str) -> Result<String> {
    let clean = filepath::clean(name.trim());
    if clean == "." || clean.is_empty() || filepath::is_abs(&clean) || clean == ".." || clean.starts_with("../") {
        return Err(errorf!("unsafe release archive path {:?}", name));
    }
    if clean != prefix && !clean.starts_with(&format!("{}/", prefix)) {
        return Err(errorf!("release archive contains unexpected top-level path {:?}", name));
    }
    Ok(clean)
}

/// Extracts directories and regular files only, bounded in expanded size;
/// links, devices and every other entry type are refused.
fn extract_release_archive(archive_path: &str, destination: &str, arch: &str) -> Result<String> {
    if arch != "amd64" && arch != "arm64" {
        return Err(errorf!("unsupported update architecture {:?}", arch));
    }
    let prefix = format!("minimalrouter-linux-{}", arch);
    let file = std::fs::File::open(archive_path)?;
    let gz = flate2::read::MultiGzDecoder::new(std::io::BufReader::new(file));
    std::fs::create_dir_all(destination)?;
    let mut archive = tar::Archive::new(gz);
    let (mut expanded, mut seen_root) = (0u64, false);
    for entry in archive.entries().map_err(|e| wrapf("read release archive", e))? {
        let mut entry = entry.map_err(|e| wrapf("read release archive", e))?;
        let name = String::from_utf8_lossy(&entry.path_bytes()).into_owned();
        let clean = safe_archive_path(&name, &prefix)?;
        let mode = entry.header().mode().unwrap_or(0) & 0o777;
        let target = join(destination, &clean);
        match entry.header().entry_type() {
            tar::EntryType::Directory => {
                std::fs::create_dir_all(&target)?;
                chmod(&target, mode)?;
                if clean == prefix {
                    seen_root = true;
                }
            }
            tar::EntryType::Regular => {
                let size = entry.header().size().unwrap_or(u64::MAX);
                if size > MAX_RELEASE_ARCHIVE || expanded > MAX_EXPANDED_RELEASE.saturating_sub(size) {
                    return Err(errorf!("expanded release exceeds size limit"));
                }
                expanded += size;
                let parent = filepath::dir(&clean);
                if parent != "." {
                    std::fs::create_dir_all(join(destination, &parent))?;
                }
                let mut output = create_private(&target)?;
                let written = std::io::copy(&mut (&mut entry).take(size), &mut output)?;
                drop(output);
                if written != size {
                    return Err(errorf!("short release archive entry"));
                }
                chmod(&target, mode)?;
                seen_root = true;
            }
            _ => return Err(errorf!("release archive contains unsupported entry type at {:?}", name)),
        }
    }
    if !seen_root {
        return Err(errorf!("release archive is empty"));
    }
    let root = join(destination, &prefix);
    if !std::fs::symlink_metadata(&root).is_ok_and(|m| m.is_dir()) {
        return Err(errorf!("release archive payload root is missing"));
    }
    Ok(root)
}

fn prepare_inbox(destination: &str) -> Result<()> {
    match std::fs::remove_dir_all(destination) {
        Err(err) if err.kind() != std::io::ErrorKind::NotFound => return Err(err.into()),
        _ => {}
    }
    std::fs::create_dir_all(destination)?;
    chmod(destination, 0o700)?;
    Ok(())
}

fn finish_archive(archive: &str, destination: &str, arch: &str) -> Result<String> {
    let extract_root = join(destination, "release");
    std::fs::create_dir_all(&extract_root)?;
    let payload_root = extract_release_archive(archive, &extract_root, arch)?;
    std::fs::remove_file(archive)?;
    Ok(payload_root)
}

/// Downloads the signed manifest and archive into a private inbox and
/// extracts regular files only. The root updater performs the authoritative
/// signature, hash, mode, architecture, version and slot checks afterwards.
pub fn prepare_published(published: &Release, arch: &str, destination: &str) -> Result<(String, String)> {
    if arch != "amd64" && arch != "arm64" {
        return Err(errorf!("unsupported update architecture {:?}", arch));
    }
    if !firmware::is_release_version(&published.version) || !firmware::is_release_version(&published.tag) {
        return Err(errorf!("invalid published release version"));
    }
    let (archive_name, manifest_name) = payload_asset_names(arch);
    let archive_url = published.assets.get(&archive_name).cloned().unwrap_or_default();
    let manifest_url = published.assets.get(&manifest_name).cloned().unwrap_or_default();
    if archive_url.is_empty() || manifest_url.is_empty() {
        return Err(errorf!("release {} does not contain {} and {}", published.tag, archive_name, manifest_name));
    }
    prepare_inbox(destination)?;
    let manifest_path = join(destination, "manifest.json");
    let archive_path = join(destination, "release.tar.gz");
    download_release_file(&manifest_url, &manifest_path, MAX_RELEASE_MANIFEST).map_err(|e| wrapf("download signed manifest", e))?;
    let manifest = firmware::load_manifest(&manifest_path).map_err(|e| wrapf("read signed manifest", e))?;
    if manifest.version != published.version {
        return Err(errorf!("release tag {} does not match manifest version {}", published.tag, manifest.version));
    }
    download_release_file(&archive_url, &archive_path, MAX_RELEASE_ARCHIVE).map_err(|e| wrapf("download release archive", e))?;
    let payload_root = finish_archive(&archive_path, destination, arch)?;
    Ok((payload_root, manifest_path))
}

fn copy_uploaded_release_file(source: &str, destination: &str, maximum: u64) -> Result<()> {
    let meta =
        std::fs::symlink_metadata(source).ok().filter(|m| m.file_type().is_file()).ok_or_else(|| errorf!("uploaded release file is missing or unsafe"))?;
    if meta.len() > maximum {
        return Err(errorf!("uploaded release file exceeds size limit"));
    }
    copy_limited(std::fs::File::open(source)?, destination, maximum, "uploaded release file exceeds size limit")
}

/// Moves a browser-uploaded signed manifest and archive into the fixed
/// private inbox, then performs the same safe extraction. Cryptographic
/// verification remains the root updater's responsibility.
pub fn prepare_local_release(manifest_source: &str, archive_source: &str, arch: &str, destination: &str) -> Result<(String, String, String)> {
    if arch != "amd64" && arch != "arm64" {
        return Err(errorf!("unsupported update architecture {:?}", arch));
    }
    prepare_inbox(destination)?;
    let manifest_path = join(destination, "manifest.json");
    let archive_path = join(destination, "release.tar.gz");
    copy_uploaded_release_file(manifest_source, &manifest_path, MAX_RELEASE_MANIFEST).map_err(|e| wrapf("copy signed manifest", e))?;
    let manifest = firmware::load_manifest(&manifest_path).map_err(|e| wrapf("read signed manifest", e))?;
    if !firmware::is_release_version(&manifest.version) {
        return Err(errorf!("uploaded manifest has an invalid release version"));
    }
    copy_uploaded_release_file(archive_source, &archive_path, MAX_RELEASE_ARCHIVE).map_err(|e| wrapf("copy release archive", e))?;
    let payload_root = finish_archive(&archive_path, destination, arch)?;
    Ok((payload_root, manifest_path, manifest.version))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn archive(entries: &[(&str, tar::EntryType, &[u8])]) -> Vec<u8> {
        let mut builder = tar::Builder::new(flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::fast()));
        for (name, kind, data) in entries {
            let mut header = tar::Header::new_gnu();
            header.set_entry_type(*kind);
            header.set_size(data.len() as u64);
            header.set_mode(0o755);
            if *kind == tar::EntryType::Symlink {
                header.set_link_name("/etc/passwd").unwrap();
            }
            // Raw name bytes: the builder itself refuses `..` components,
            // which is exactly what a hostile archive would contain.
            header.as_old_mut().name[..name.len()].copy_from_slice(name.as_bytes());
            header.set_cksum();
            builder.append(&header, *data).unwrap();
        }
        builder.into_inner().unwrap().finish().unwrap()
    }

    #[test]
    fn extraction_accepts_payload_and_refuses_escapes() {
        let base = std::env::temp_dir().join(format!("mr-release-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        std::fs::create_dir_all(&base).unwrap();
        let source = base.join("upload.tar.gz");
        let good = archive(&[
            ("minimalrouter-linux-amd64/", tar::EntryType::Directory, b""),
            ("minimalrouter-linux-amd64/bin/routerd-amd64", tar::EntryType::Regular, b"ELF"),
        ]);
        std::fs::write(&source, good).unwrap();
        let manifest = base.join("upload.json");
        std::fs::write(&manifest, br#"{"version":"0.3.0","files":{}}"#).unwrap();
        let inbox = base.join("inbox").to_string_lossy().into_owned();
        let (root, _, version) = prepare_local_release(manifest.to_str().unwrap(), source.to_str().unwrap(), "amd64", &inbox).unwrap();
        assert_eq!(version, "0.3.0");
        assert_eq!(std::fs::read(Path::new(&root).join("bin/routerd-amd64")).unwrap(), b"ELF");

        for (entries, want) in [
            (vec![("../x", tar::EntryType::Regular, &b"x"[..])], "unsafe release archive path"),
            (vec![("other/x", tar::EntryType::Regular, &b"x"[..])], "unexpected top-level path"),
            (vec![("minimalrouter-linux-amd64/link", tar::EntryType::Symlink, &b""[..])], "unsupported entry type"),
        ] {
            std::fs::write(&source, archive(&entries)).unwrap();
            let err = prepare_local_release(manifest.to_str().unwrap(), source.to_str().unwrap(), "amd64", &inbox).unwrap_err();
            assert!(format!("{:#}", err).contains(want), "{err:#}");
        }
        std::fs::remove_dir_all(base).ok();
    }
}
