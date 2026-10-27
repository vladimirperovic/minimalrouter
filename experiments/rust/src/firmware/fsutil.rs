//! Filesystem primitives with the Go standard library's semantics: Lstat-based
//! walks, mode bits, temp names, directory fsync, symlinks and flock.

use sha2::{Digest, Sha256};
use std::fs::{File, Metadata};
use std::io::{Read, Write};
use std::path::Path;

use crate::errorf;
use crate::goish::errors::Result;

pub(crate) fn join(root: &str, relative: &str) -> String {
    Path::new(root).join(relative).to_string_lossy().into_owned()
}

pub(crate) fn sha256_bytes(path: &str) -> std::io::Result<Vec<u8>> {
    let mut file = File::open(path)?;
    let mut hash = Sha256::new();
    let mut buf = vec![0u8; 64 * 1024];
    loop {
        let n = file.read(&mut buf)?;
        if n == 0 {
            break;
        }
        hash.update(&buf[..n]);
    }
    Ok(hash.finalize().to_vec())
}

pub(crate) fn sha256_file(path: &str) -> Result<String> {
    Ok(hex::encode(sha256_bytes(path)?))
}

/// Lexical, Lstat-based walk (filepath.Walk/WalkDir): `visit(path, relative,
/// is_regular)` for every non-directory entry. Symlinks are never followed.
pub(crate) fn walk_files(root: &str, visit: &mut dyn FnMut(&str, &str, bool) -> Result<()>) -> Result<()> {
    fn walk(dir: &Path, prefix: &str, visit: &mut dyn FnMut(&str, &str, bool) -> Result<()>) -> Result<()> {
        let mut entries: Vec<_> = std::fs::read_dir(dir)?.collect::<std::io::Result<_>>()?;
        entries.sort_by_key(|e| e.file_name());
        for entry in entries {
            let name = entry.file_name().to_string_lossy().into_owned();
            let relative = if prefix.is_empty() { name } else { format!("{}/{}", prefix, name) };
            let path = entry.path();
            let file_type = std::fs::symlink_metadata(&path)?.file_type();
            if file_type.is_dir() {
                walk(&path, &relative, visit)?;
            } else {
                visit(&path.to_string_lossy(), &relative, file_type.is_file())?;
            }
        }
        Ok(())
    }
    walk(Path::new(root), "", visit)
}

/// Directories in walk order (parents before children), for bottom-up sync.
pub(crate) fn walk_dirs(root: &str) -> Result<Vec<String>> {
    let mut out = vec![root.to_string()];
    let mut index = 0;
    while index < out.len() {
        let mut entries: Vec<_> = std::fs::read_dir(&out[index])?.collect::<std::io::Result<_>>()?;
        entries.sort_by_key(|e| e.file_name());
        for entry in entries {
            if std::fs::symlink_metadata(entry.path())?.file_type().is_dir() {
                out.push(entry.path().to_string_lossy().into_owned());
            }
        }
        index += 1;
    }
    Ok(out)
}

#[cfg(unix)]
pub(crate) fn mode_bits(meta: &Metadata) -> u32 {
    use std::os::unix::fs::PermissionsExt;
    meta.permissions().mode() & 0o7777
}

/// Developer hosts have no Unix mode bits; report the conservative file mode.
#[cfg(not(unix))]
pub(crate) fn mode_bits(meta: &Metadata) -> u32 {
    if meta.permissions().readonly() { 0o555 } else { 0o755 }
}

#[cfg(unix)]
pub(crate) fn chmod(path: &str, mode: u32) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode))?;
    Ok(())
}

#[cfg(not(unix))]
pub(crate) fn chmod(_path: &str, _mode: u32) -> Result<()> {
    Ok(())
}

pub(crate) fn write_file(path: &str, data: &[u8], mode: u32) -> Result<()> {
    std::fs::write(path, data)?;
    chmod(path, mode)
}

fn random_suffix() -> String {
    let mut bytes = [0u8; 4];
    let _ = crate::goish::rand::fill(&mut bytes);
    u32::from_le_bytes(bytes).to_string()
}

/// os.MkdirTemp: a new 0700 directory named `pattern` + random digits.
pub(crate) fn mkdir_temp(dir: &str, pattern: &str) -> Result<String> {
    for _ in 0..10000 {
        let path = join(dir, &format!("{}{}", pattern, random_suffix()));
        match std::fs::create_dir(&path) {
            Ok(()) => {
                chmod(&path, 0o700)?;
                return Ok(path);
            }
            Err(err) if err.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(err) => return Err(err.into()),
        }
    }
    Err(errorf!("mkdirtemp: too many collisions"))
}

/// os.CreateTemp: a new 0600 file named `pattern` + random digits.
pub(crate) fn create_temp(dir: &str, pattern: &str) -> Result<(File, String)> {
    for _ in 0..10000 {
        let path = join(dir, &format!("{}{}", pattern, random_suffix()));
        let mut options = std::fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        match options.open(&path) {
            Ok(file) => return Ok((file, path)),
            Err(err) if err.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(err) => return Err(err.into()),
        }
    }
    Err(errorf!("createtemp: too many collisions"))
}

#[cfg(unix)]
pub(crate) fn sync_dir(path: &str) -> Result<()> {
    File::open(path)?.sync_all()?;
    Ok(())
}

/// Directory handles cannot be fsynced on developer hosts.
#[cfg(not(unix))]
pub(crate) fn sync_dir(path: &str) -> Result<()> {
    std::fs::metadata(path)?;
    Ok(())
}

#[cfg(unix)]
pub(crate) fn symlink(target: &str, link: &str) -> Result<()> {
    std::os::unix::fs::symlink(target, link)?;
    Ok(())
}

#[cfg(not(unix))]
pub(crate) fn symlink(_target: &str, _link: &str) -> Result<()> {
    Err(errorf!("slot pointers require a Unix filesystem"))
}

/// Exclusive advisory lock serializing slot mutations across routerd,
/// router-update and router-recovery.
#[cfg(unix)]
pub(crate) fn lock_exclusive(file: &File) -> Result<()> {
    use std::os::unix::io::AsRawFd;
    loop {
        if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX) } == 0 {
            return Ok(());
        }
        let err = std::io::Error::last_os_error();
        if err.kind() != std::io::ErrorKind::Interrupted {
            return Err(err.into());
        }
    }
}

/// Developer hosts build only; no cross-process serialization is claimed.
#[cfg(not(unix))]
pub(crate) fn lock_exclusive(_file: &File) -> Result<()> {
    Ok(())
}

#[cfg(unix)]
pub(crate) fn unlock(file: &File) {
    use std::os::unix::io::AsRawFd;
    unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_UN) };
}

#[cfg(not(unix))]
pub(crate) fn unlock(_file: &File) {}

/// Copies one regular source file to a new destination with its permission
/// bits limited to 0755, fsyncing the result.
pub(crate) fn copy_regular_file(source: &str, destination: &str) -> Result<()> {
    let meta = std::fs::symlink_metadata(source).ok().filter(|m| m.file_type().is_file()).ok_or_else(|| errorf!("release file is missing or unsafe: {}", source))?;
    if let Some(parent) = Path::new(destination).parent() {
        std::fs::create_dir_all(parent)?;
    }
    let mut input = File::open(source)?;
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(mode_bits(&meta) & 0o755);
    }
    #[cfg(not(unix))]
    let _ = &meta;
    let mut output = options.open(destination)?;
    std::io::copy(&mut input, &mut output)?;
    output.flush()?;
    output.sync_all()?;
    Ok(())
}
