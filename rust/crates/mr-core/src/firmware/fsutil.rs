//! Filesystem primitives with the Go standard library's semantics: Lstat-based
//! walks, mode bits, temp names, directory fsync, symlinks and flock.

use sha2::{Digest, Sha256};
use std::fs::{File, Metadata};
use std::io::{Read, Write};
use std::path::Path;

use crate::errorf;
use crate::goish::errors::Result;

// No individual file may exceed the management plane's expanded release cap.
const MAX_RELEASE_FILE_BYTES: u64 = 256 << 20;

fn checked_length(meta: &Metadata) -> std::io::Result<u64> {
    if meta.len() > MAX_RELEASE_FILE_BYTES {
        return Err(std::io::Error::new(std::io::ErrorKind::InvalidData, "release file too large"));
    }
    Ok(meta.len())
}

pub(crate) fn join(root: &str, relative: &str) -> String {
    Path::new(root).join(relative).to_string_lossy().into_owned()
}

pub(crate) fn sha256_bytes(path: &str) -> std::io::Result<Vec<u8>> {
    let file = open_regular_file(path)?;
    // A writer must not prolong verification indefinitely by appending bytes.
    let length = checked_length(&file.metadata()?)?;
    let mut file = file.take(length + 1);
    let mut total = 0;
    let mut hash = Sha256::new();
    let mut buf = vec![0u8; 64 * 1024];
    loop {
        let n = file.read(&mut buf)?;
        if n == 0 {
            break;
        }
        hash.update(&buf[..n]);
        total += n as u64;
    }
    if total != length {
        return Err(std::io::Error::new(std::io::ErrorKind::InvalidData, "release file changed size"));
    }
    Ok(hash.finalize().to_vec())
}

pub(crate) fn sha256_file(path: &str) -> Result<String> {
    Ok(hex::encode(sha256_bytes(path)?))
}

#[cfg(unix)]
pub(super) fn open_regular_file(path: &str) -> std::io::Result<File> {
    crate::sys::open_regular_nofollow(path)
}

// Developer hosts do not execute privileged appliance updates.
#[cfg(not(unix))]
pub(super) fn open_regular_file(path: &str) -> std::io::Result<File> {
    if !std::fs::symlink_metadata(path)?.is_file() {
        return Err(std::io::Error::new(std::io::ErrorKind::InvalidInput, "not a regular file"));
    }
    let file = File::open(path)?;
    if !file.metadata()?.is_file() {
        return Err(std::io::Error::new(std::io::ErrorKind::InvalidInput, "not a regular file"));
    }
    Ok(file)
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
        let builder = std::fs::DirBuilder::new();
        #[cfg(unix)]
        let builder = {
            use std::os::unix::fs::DirBuilderExt;
            let mut builder = builder;
            builder.mode(0o700);
            builder
        };
        match builder.create(&path) {
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

/// Exclusive advisory lock (`flock(2)` on Linux, so it interoperates with
/// the Go tools) serializing slot mutations across routerd, router-update and
/// router-recovery.
pub(crate) fn lock_exclusive(file: &File) -> Result<()> {
    loop {
        match file.lock() {
            Ok(()) => return Ok(()),
            Err(err) if err.kind() == std::io::ErrorKind::Interrupted => {}
            Err(err) => return Err(err.into()),
        }
    }
}

pub(crate) fn unlock(file: &File) {
    let _ = file.unlock();
}

/// Copies one regular source file to a new destination with its permission
/// bits limited to 0755, fsyncing the result.
pub(crate) fn copy_regular_file(source: &str, destination: &str) -> Result<()> {
    let input = open_regular_file(source)?;
    let meta = input.metadata()?;
    if let Some(parent) = Path::new(destination).parent() {
        std::fs::create_dir_all(parent)?;
    }
    let length = checked_length(&meta)?;
    let mut input = input.take(length + 1);
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
    if std::io::copy(&mut input, &mut output)? != length {
        return Err(errorf!("release file changed size during copy"));
    }
    output.flush()?;
    output.sync_all()?;
    Ok(())
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    struct TempDir(String);
    impl Drop for TempDir {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn reads_reject_symlinks_in_every_position_and_special_files() {
        let root = TempDir(mkdir_temp(std::env::temp_dir().to_str().unwrap(), "mr-source-open-").unwrap());
        let dir = join(&root.0, "dir");
        std::fs::create_dir(&dir).unwrap();
        let file = join(&dir, "file");
        std::fs::write(&file, b"payload").unwrap();
        let leaf = join(&dir, "link");
        std::os::unix::fs::symlink(&file, &leaf).unwrap();
        let parent = join(&root.0, "parent-link");
        std::os::unix::fs::symlink(&dir, &parent).unwrap();
        let fifo = join(&root.0, "fifo");
        assert!(std::process::Command::new("mkfifo").arg(&fifo).status().unwrap().success());
        let socket = join(&root.0, "socket");
        let _listener = std::os::unix::net::UnixListener::bind(&socket).unwrap();
        let destination = join(&root.0, "copy");
        let (send, receive) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            for unsafe_path in [leaf, join(&parent, "file"), fifo, socket, dir] {
                assert!(sha256_bytes(&unsafe_path).is_err());
                assert!(super::super::load_manifest(&unsafe_path).is_err());
                assert!(copy_regular_file(&unsafe_path, &destination).is_err());
                assert!(!Path::new(&destination).exists());
            }
            copy_regular_file(&file, &destination).unwrap();
            assert_eq!(std::fs::read(&destination).unwrap(), b"payload");
            // Existing destinations must never be overwritten.
            assert!(copy_regular_file(&file, &destination).is_err());
            send.send(()).unwrap();
        });
        receive.recv_timeout(std::time::Duration::from_secs(5)).expect("unsafe source must fail without blocking");
    }

    #[test]
    fn oversized_sparse_source_is_rejected_before_copy() {
        let root = mkdir_temp(std::env::temp_dir().to_str().unwrap(), "mr-source-size-").unwrap();
        let source = join(&root, "large");
        File::create(&source).unwrap().set_len(MAX_RELEASE_FILE_BYTES + 1).unwrap();
        assert!(sha256_bytes(&source).is_err());
        assert!(copy_regular_file(&source, &join(&root, "copy")).is_err());
        std::fs::remove_dir_all(root).unwrap();
    }
}
