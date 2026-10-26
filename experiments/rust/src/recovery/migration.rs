//! Explicit local-root, offline configuration replacement. An interruption
//! leaves a durable fence; rerunning with the identical input finishes
//! forward. SQLite is only ever opened by an unprivileged worker process.

use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::io::{Read, Write};

use crate::config::{OfflineConfig, SQLiteStore};
use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json;

pub const MIGRATION_DB_WORKER_ARGUMENT: &str = "--internal-migration-db-worker";
#[cfg_attr(not(unix), allow(dead_code))]
const MIGRATION_LIMIT: usize = 4 << 20;
const WORKER_FRAME_LIMIT: usize = 3 * (4 << 20);

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct WorkerRequest {
    operation: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    directory: String,
    original: OfflineConfig,
    #[serde(with = "opt_bytes", skip_serializing_if = "Vec::is_empty")]
    target: Vec<u8>,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct WorkerResponse {
    state: OfflineConfig,
    #[serde(skip_serializing_if = "String::is_empty")]
    error: String,
}

mod opt_bytes {
    use base64::Engine;
    use serde::{Deserialize, Deserializer, Serializer};
    pub fn serialize<S: Serializer>(v: &[u8], s: S) -> Result<S::Ok, S::Error> {
        s.serialize_str(&base64::engine::general_purpose::STANDARD.encode(v))
    }
    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<Vec<u8>, D::Error> {
        let text = Option::<String>::deserialize(d)?.unwrap_or_default();
        crate::goish::base64::std_decode(&text).ok_or_else(|| serde::de::Error::custom("illegal base64 data"))
    }
}

fn write_worker_frame<T: Serialize>(w: &mut impl Write, value: &T) -> Result<()> {
    let data = json::marshal(value)?;
    if data.len() > WORKER_FRAME_LIMIT {
        return Err(errorf!("migration worker frame exceeds limit"));
    }
    w.write_all(&(data.len() as u32).to_be_bytes())?;
    w.write_all(&data)?;
    w.flush()?;
    Ok(())
}

fn read_worker_frame<T: serde::de::DeserializeOwned>(r: &mut impl Read) -> Result<T> {
    let mut header = [0u8; 4];
    r.read_exact(&mut header)?;
    let size = u32::from_be_bytes(header) as usize;
    if size == 0 || size > WORKER_FRAME_LIMIT {
        return Err(errorf!("invalid migration worker frame size"));
    }
    let mut data = vec![0u8; size];
    r.read_exact(&mut data)?;
    Ok(json::unmarshal(&data)?)
}

/// Serves only after platform checks establish permanent, unprivileged
/// credentials. It never opens the private root journal or helper state.
pub fn run_migration_db_worker() -> Result<()> {
    harden_migration_db_worker()?;
    serve_migration_db_worker(&mut std::io::stdin().lock(), &mut std::io::stdout().lock())
}

fn serve_migration_db_worker(input: &mut impl Read, output: &mut impl Write) -> Result<()> {
    let mut store: Option<SQLiteStore> = None;
    loop {
        let req: WorkerRequest = read_worker_frame(input)?;
        let mut response = WorkerResponse::default();
        let result: Result<()> = match req.operation.as_str() {
            "open" if store.is_some() => Err(errorf!("database already open")),
            "open" => SQLiteStore::open_offline_migration_store(&req.directory).map(|s| store = Some(s)),
            "read" => match &store {
                None => Err(errorf!("database is not open")),
                Some(s) => s.read_offline_config().map(|state| response.state = state),
            },
            "commit" => match &store {
                None => Err(errorf!("database is not open")),
                Some(s) => s.commit_offline_migration(&req.original, &req.target),
            },
            "close" => {
                store = None;
                Ok(())
            }
            _ => Err(errorf!("unknown migration database operation")),
        };
        if let Err(err) = result {
            response.error = err.to_string();
        }
        write_worker_frame(output, &response)?;
        if req.operation == "close" {
            return Ok(());
        }
    }
}

#[cfg(target_os = "linux")]
fn harden_migration_db_worker() -> Result<()> {
    let (uid, gid) = linux::migration_worker_identity()?;
    linux::restrict_migration_db_worker(uid, gid)
}

#[cfg(not(target_os = "linux"))]
fn harden_migration_db_worker() -> Result<()> {
    Err(errorf!("migration database worker requires Linux"))
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct MigrationBackup {
    present: bool,
    #[serde(with = "opt_bytes")]
    bytes: Vec<u8>,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct MigrationJournal {
    version: i64,
    data_dir: String,
    input_hash: String,
    original: OfflineConfig,
    #[serde(with = "opt_bytes")]
    target: Vec<u8>,
    helper: BTreeMap<String, MigrationBackup>,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct MigrationMarker {
    attempt: String,
    checksum: String,
}

/// Runs the migration; returns the attempt directory on success.
#[cfg(target_os = "linux")]
pub fn migrate_config(data_dir: &str, file: &str) -> Result<String> {
    // SAFETY: geteuid has no preconditions.
    if unsafe { libc::geteuid() } != 0 {
        return Err(errorf!("configuration migration requires root on the Linux appliance"));
    }
    linux::migrate(data_dir, file)
}

#[cfg(not(target_os = "linux"))]
pub fn migrate_config(_data_dir: &str, _file: &str) -> Result<String> {
    Err(errorf!("configuration migration requires root on the Linux appliance"))
}

#[cfg(target_os = "linux")]
mod linux {
    use super::*;
    use crate::config::{validate_offline_candidate, SystemConfig};
    use crate::recovery::guard::{acquire_offline_lock, require_daemons_stopped, secure_directory, MIGRATION_DIRECTORY, OFFLINE_LOCK_PATH};
    use sha2::{Digest, Sha256};
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
    use std::os::unix::process::CommandExt;
    use std::path::{Path, PathBuf};
    use std::process::{Child, ChildStdin, ChildStdout, Command, Stdio};
    use std::time::Duration;

    const HELPER_MIGRATION_FILES: [&str; 3] = ["last-good.json", "pending-confirmation.json", "last-transaction.json"];
    const HELPER_DIR: &str = "/var/lib/minimalrouter-applyd";

    pub(super) fn migration_worker_identity() -> Result<(u32, u32)> {
        let passwd = std::fs::read_to_string("/etc/passwd")?;
        for line in passwd.lines() {
            let fields: Vec<&str> = line.split(':').collect();
            if fields.len() >= 4 && fields[0] == "routerd" {
                let uid: u32 = fields[2].parse().map_err(|_| errorf!("migration worker requires a non-root routerd UID"))?;
                let gid: u32 = fields[3].parse().map_err(|_| errorf!("migration worker requires a non-root routerd GID"))?;
                if uid == 0 {
                    return Err(errorf!("migration worker requires a non-root routerd UID"));
                }
                if gid == 0 {
                    return Err(errorf!("migration worker requires a non-root routerd GID"));
                }
                return Ok((uid, gid));
            }
        }
        Err(errorf!("resolve fixed routerd worker account: user: unknown user routerd"))
    }

    pub(super) fn restrict_migration_db_worker(uid: u32, gid: u32) -> Result<()> {
        let (mut r, mut e, mut s) = (0, 0, 0);
        let (mut gr, mut ge, mut gs) = (0, 0, 0);
        // SAFETY: out pointers are valid.
        unsafe {
            libc::getresuid(&mut r, &mut e, &mut s);
            libc::getresgid(&mut gr, &mut ge, &mut gs);
        }
        if uid == 0 || gid == 0 || [r, e, s] != [uid; 3] || [gr, ge, gs] != [gid; 3] {
            return Err(errorf!("migration database worker must have permanently dropped all user/group IDs"));
        }
        // SAFETY: a zero-length query returns the group count.
        if unsafe { libc::getgroups(0, std::ptr::null_mut()) } != 0 {
            return Err(errorf!("migration database worker must have no supplementary groups"));
        }
        // Refuse unexpected inherited capabilities.
        #[repr(C)]
        struct CapHeader {
            version: u32,
            pid: i32,
        }
        #[repr(C)]
        #[derive(Default, Clone, Copy, PartialEq)]
        struct CapData {
            effective: u32,
            permitted: u32,
            inheritable: u32,
        }
        let mut header = CapHeader { version: 0x2008_0522, pid: 0 };
        let mut caps = [CapData::default(); 2];
        // SAFETY: header and data buffers match the kernel ABI for version 3.
        if unsafe { libc::syscall(libc::SYS_capget, &mut header, caps.as_mut_ptr()) } != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        if caps != [CapData::default(); 2] {
            return Err(errorf!("migration database worker inherited capabilities"));
        }
        // SAFETY: plain prctl/setrlimit/umask calls with constant arguments.
        unsafe {
            if libc::syscall(libc::SYS_capset, &mut header, caps.as_ptr()) != 0
                || libc::prctl(libc::PR_CAP_AMBIENT, libc::PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0) != 0
                || libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0
                || libc::prctl(libc::PR_SET_DUMPABLE, 0, 0, 0, 0) != 0
            {
                return Err(std::io::Error::last_os_error().into());
            }
            let zero = libc::rlimit { rlim_cur: 0, rlim_max: 0 };
            if libc::setrlimit(libc::RLIMIT_CORE, &zero) != 0 {
                return Err(std::io::Error::last_os_error().into());
            }
            libc::umask(0o077);
        }
        Ok(())
    }

    /// The root-side handle to the unprivileged SQLite worker.
    struct Worker {
        child: Child,
        input: Option<ChildStdin>,
        output: Option<ChildStdout>,
        closed: bool,
    }

    impl Worker {
        fn open(dir: &str) -> Result<Worker> {
            let (uid, gid) = migration_worker_identity()?;
            let executable = std::env::current_exe()?;
            let meta = std::fs::symlink_metadata(&executable)?;
            if meta.uid() != 0 || !meta.file_type().is_file() || meta.mode() & 0o022 != 0 || meta.mode() & 0o6000 != 0 {
                return Err(errorf!("migration worker executable must be root-owned, non-writable, and without set-ID bits"));
            }
            let mut command = Command::new(executable);
            command
                .arg(MIGRATION_DB_WORKER_ARGUMENT)
                .env_clear()
                .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
                .current_dir("/")
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::null())
                .uid(uid)
                .gid(gid);
            // SAFETY: setgroups is async-signal-safe and runs before exec.
            unsafe {
                command.pre_exec(|| {
                    if libc::setgroups(0, std::ptr::null()) != 0 {
                        return Err(std::io::Error::last_os_error());
                    }
                    Ok(())
                });
            }
            let mut child = command.spawn()?;
            let mut worker = Worker { input: child.stdin.take(), output: child.stdout.take(), child, closed: false };
            if let Err(err) = worker.call(&WorkerRequest { operation: "open".into(), directory: dir.into(), ..Default::default() }) {
                worker.abort();
                return Err(err);
            }
            Ok(worker)
        }

        fn call(&mut self, request: &WorkerRequest) -> Result<WorkerResponse> {
            let (Some(input), Some(output)) = (self.input.as_mut(), self.output.as_mut()) else {
                return Err(errorf!("migration database worker is closed"));
            };
            // A blocked worker cannot leave the root coordinator waiting forever.
            let pid = self.child.id() as i32;
            let done = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
            let flag = done.clone();
            std::thread::spawn(move || {
                std::thread::sleep(Duration::from_secs(30));
                if !flag.load(std::sync::atomic::Ordering::SeqCst) {
                    // SAFETY: killing our own child by PID.
                    unsafe { libc::kill(pid, libc::SIGKILL) };
                }
            });
            let result = write_worker_frame(input, request)
                .map_err(|e| errorf!("write migration worker request: {}", e))
                .and_then(|_| read_worker_frame::<WorkerResponse>(output).map_err(|e| errorf!("read migration worker response: {}", e)));
            done.store(true, std::sync::atomic::Ordering::SeqCst);
            let response = match result {
                Ok(r) => r,
                Err(e) => {
                    self.abort();
                    return Err(e);
                }
            };
            if !response.error.is_empty() {
                return Err(errorf!("migration database worker: {}", response.error));
            }
            Ok(response)
        }

        fn read(&mut self) -> Result<OfflineConfig> {
            Ok(self.call(&WorkerRequest { operation: "read".into(), ..Default::default() })?.state)
        }

        fn commit(&mut self, original: &OfflineConfig, target: &[u8]) -> Result<()> {
            self.call(&WorkerRequest { operation: "commit".into(), original: original.clone(), target: target.to_vec(), ..Default::default() }).map(|_| ())
        }

        fn close(&mut self) -> Result<()> {
            if self.closed {
                return Ok(());
            }
            let result = self.call(&WorkerRequest { operation: "close".into(), ..Default::default() });
            self.closed = true;
            self.input = None;
            if result.is_err() {
                let _ = self.child.kill();
            }
            let status = self.child.wait();
            self.output = None;
            result?;
            let status = status?;
            if !status.success() {
                return Err(errorf!("{}", status));
            }
            Ok(())
        }

        fn abort(&mut self) {
            self.closed = true;
            self.input = None;
            self.output = None;
            let _ = self.child.kill();
            let _ = self.child.wait();
        }
    }

    impl Drop for Worker {
        fn drop(&mut self) {
            if !self.closed {
                let _ = self.close();
            }
        }
    }

    fn migration_hash(data: &[u8]) -> String {
        hex::encode(Sha256::digest(data))
    }

    fn read_checked(path: &Path, limit: usize, owner: Option<(u32, bool)>) -> std::io::Result<Vec<u8>> {
        let mut file = std::fs::OpenOptions::new().read(true).custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC | libc::O_NONBLOCK).open(path)?;
        let meta = file.metadata()?;
        if let Some((uid, private)) = owner {
            let mask = if private { 0o077 } else { 0o022 };
            if meta.uid() != uid || meta.mode() & mask != 0 || meta.nlink() != 1 {
                return Err(std::io::Error::other("unsafe migration journal file ownership or permissions"));
            }
        }
        if !meta.file_type().is_file() || meta.len() as usize > limit {
            return Err(std::io::Error::other("migration file must be a bounded regular file"));
        }
        let mut data = Vec::new();
        (&mut file).take(limit as u64 + 1).read_to_end(&mut data)?;
        if data.len() > limit {
            return Err(std::io::Error::other("migration file exceeds size limit"));
        }
        Ok(data)
    }

    fn sync_dir(path: &Path) -> std::io::Result<()> {
        std::fs::File::open(path)?.sync_all()
    }

    fn write_atomic(path: &Path, data: &[u8], mode: u32) -> std::io::Result<()> {
        let dir = path.parent().unwrap_or(Path::new("/"));
        let tmp = dir.join(format!(".migration-{}", crate::auth::generate_random_hex(8).unwrap_or_default()));
        let result = (|| {
            let mut file = std::fs::OpenOptions::new().write(true).create_new(true).mode(mode).open(&tmp)?;
            file.set_permissions(std::fs::Permissions::from_mode(mode))?;
            file.write_all(data)?;
            file.sync_all()?;
            drop(file);
            std::fs::rename(&tmp, path)?;
            sync_dir(dir)
        })();
        let _ = std::fs::remove_file(&tmp);
        result
    }

    pub(super) fn migrate(data_dir: &str, file: &str) -> Result<String> {
        let _lock = acquire_offline_lock(OFFLINE_LOCK_PATH, true, 0)?;
        require_daemons_stopped("/proc")?;
        for name in ["routerd", "router-applyd"] {
            if std::fs::symlink_metadata(Path::new("/run/openrc/started").join(name)).is_ok() {
                return Err(errorf!("stop OpenRC service {} before migration", name));
            }
        }
        let input = read_checked(Path::new(file), MIGRATION_LIMIT, None)?;
        let candidate_value: serde_json::Value = match crate::apply::decode_single::<serde_json::Value>(&input) {
            Ok(v) => v,
            Err(crate::apply::SingleDecodeError::Trailing) => return Err(errorf!("migration file must contain one JSON configuration")),
            Err(crate::apply::SingleDecodeError::Invalid(e)) => return Err(errorf!("decode full migration configuration: {}", e)),
        };
        let mut candidate: SystemConfig = json::from_value(&candidate_value).map_err(|e| errorf!("decode full migration configuration: {}", e))?;
        candidate.migrate_legacy_fields();
        validate_offline_candidate(&candidate).map_err(|e| errorf!("migration candidate: {}", e))?;
        let data = std::path::absolute(data_dir)?;
        let data_str = data.to_string_lossy().into_owned();
        let db = data.join("minimalrouter.db");
        if !std::fs::symlink_metadata(&db)?.file_type().is_file() {
            return Err(errorf!("migration requires regular file: {}", db.display()));
        }
        let journal_dir = Path::new(MIGRATION_DIRECTORY);
        for dir in [Path::new(HELPER_DIR), journal_dir] {
            let parent = dir.parent().unwrap_or(Path::new("/")).to_string_lossy().into_owned();
            secure_directory(&parent, 0, false)?;
            match std::fs::create_dir(dir) {
                Ok(()) => std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700))?,
                Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {}
                Err(e) => return Err(e.into()),
            }
            secure_directory(&dir.to_string_lossy(), 0, false)?;
        }
        // The fence directory must be searchable by the unprivileged daemon.
        std::fs::set_permissions(journal_dir, std::fs::Permissions::from_mode(0o755))?;
        sync_dir(journal_dir)?;
        sync_dir(journal_dir.parent().unwrap_or(Path::new("/")))?;

        let mut store = Worker::open(&data_str)?;
        let marker_path = journal_dir.join("pending.json");
        let journal: MigrationJournal;
        let attempt: PathBuf;
        match read_checked(&marker_path, 4096, Some((0, false))) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                let original = store.read()?;
                if original.revision < 1 || original.revision == i64::MAX {
                    return Err(errorf!("canonical revision cannot advance"));
                }
                if original.json.len() > MIGRATION_LIMIT {
                    return Err(errorf!("original configuration exceeds migration backup limit"));
                }
                candidate.revision = (original.revision + 1) as u64;
                candidate.updated_at = crate::goish::time::Time::now_utc();
                let target = json::marshal(&candidate)?;
                if target.len() > MIGRATION_LIMIT {
                    return Err(errorf!("replacement configuration exceeds migration limit"));
                }
                let mut helper = BTreeMap::new();
                for name in HELPER_MIGRATION_FILES {
                    match read_checked(&Path::new(HELPER_DIR).join(name), MIGRATION_LIMIT, None) {
                        Ok(bytes) => helper.insert(name.to_string(), MigrationBackup { present: true, bytes }),
                        Err(e) if e.kind() == std::io::ErrorKind::NotFound => helper.insert(name.to_string(), MigrationBackup::default()),
                        Err(e) => return Err(e.into()),
                    };
                }
                let built = MigrationJournal {
                    version: 1,
                    data_dir: data_str.clone(),
                    input_hash: migration_hash(&input),
                    original,
                    target,
                    helper,
                };
                let dir = journal_dir.join(format!("attempt-{}", crate::auth::generate_random_hex(6)?));
                std::fs::create_dir(&dir)?;
                std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700))?;
                let data = json::marshal(&built)?;
                write_atomic(&dir.join("backup.json"), &data, 0o600)?;
                sync_dir(journal_dir)?;
                let marker = json::marshal(&MigrationMarker {
                    attempt: dir.file_name().unwrap().to_string_lossy().into_owned(),
                    checksum: migration_hash(&data),
                })?;
                write_atomic(&marker_path, &marker, 0o644)?;
                journal = built;
                attempt = dir;
            }
            Err(e) => return Err(e.into()),
            Ok(marker_bytes) => {
                let marker: MigrationMarker = json::unmarshal(&marker_bytes).map_err(|e| errorf!("invalid migration fence: {}", e))?;
                if marker.attempt.is_empty() || marker.attempt == "." || marker.attempt.contains('/') {
                    return Err(errorf!("invalid migration backup path"));
                }
                let dir = journal_dir.join(&marker.attempt);
                secure_directory(&dir.to_string_lossy(), 0, true)?;
                let data = read_checked(&dir.join("backup.json"), 8 * MIGRATION_LIMIT, Some((0, true)))?;
                if migration_hash(&data) != marker.checksum {
                    return Err(errorf!("migration backup checksum mismatch; fence retained"));
                }
                let loaded: MigrationJournal = json::unmarshal(&data)?;
                if loaded.version != 1 || loaded.data_dir != data_str || loaded.input_hash != migration_hash(&input) {
                    return Err(errorf!("resume requires the original migration input and data directory; fence retained"));
                }
                journal = loaded;
                attempt = dir;
            }
        }
        // Idempotent CAS recognises an already committed target after power loss.
        store.commit(&journal.original, &journal.target)?;
        write_atomic(&Path::new(HELPER_DIR).join("last-good.json"), &journal.target, 0o600)?;
        for name in &HELPER_MIGRATION_FILES[1..] {
            match std::fs::remove_file(Path::new(HELPER_DIR).join(name)) {
                Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e.into()),
                _ => {}
            }
        }
        sync_dir(Path::new(HELPER_DIR))?;
        let current = store.read()?;
        let helper = read_checked(&Path::new(HELPER_DIR).join("last-good.json"), MIGRATION_LIMIT, None)?;
        if current.revision != journal.original.revision + 1 || current.json != journal.target || helper != journal.target {
            return Err(errorf!("migration state verification failed; fence retained"));
        }
        // Close flushes SQLite before daemon users may reopen it.
        store.close()?;
        sync_dir(&data)?;
        std::fs::remove_file(&marker_path)?;
        sync_dir(journal_dir)?;
        Ok(attempt.to_string_lossy().into_owned())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn worker_frames_round_trip() {
        let mut buf = Vec::new();
        let req = WorkerRequest { operation: "commit".into(), target: b"{\"revision\":2}".to_vec(), ..Default::default() };
        write_worker_frame(&mut buf, &req).unwrap();
        let back: WorkerRequest = read_worker_frame(&mut buf.as_slice()).unwrap();
        assert_eq!(back.operation, "commit");
        assert_eq!(back.target, req.target);
        assert!(read_worker_frame::<WorkerRequest>(&mut [0u8, 0, 0, 0].as_slice()).is_err());
    }
}
