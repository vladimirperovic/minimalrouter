//! A/B release slots. Only content covered by an Ed25519-signed manifest is
//! staged; release-provided scripts are never executed. A journal makes every
//! pointer swap recoverable after an interruption.

use std::io::Write;

use super::fsutil::{self, join};
use super::*;

const OPERATION_JOURNAL_VERSION: i64 = 1;
const OPERATION_JOURNAL_NAME: &str = "operation.json";
const INSTALLATION_JOURNAL: &str = "installation.json";

crate::sentinel!(
    ErrUnknownInstalledVersion,
    "installed bootstrap version is unknown; use a verified full distribution installation to establish the version floor"
);

/// Current, previous and staged versions. The symlinks are authoritative at
/// runtime; state.json mirrors them for diagnostics and the pending marker.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct SlotState {
    pub current: String,
    pub previous: String,
    pub pending: String,
    /// The verified full-install/activation high-water mark; it survives
    /// explicit rollback.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub minimum_version: String,
    #[serde(skip_serializing_if = "crate::goish::json::is_false")]
    pub installation_pending: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
struct SlotOperation {
    version: i64,
    kind: String,
    old: SlotState,
    next: SlotState,
}

fn join_errors(errors: Vec<Error>) -> Option<Error> {
    let messages: Vec<String> = errors.iter().map(|e| e.to_string()).collect();
    if messages.is_empty() { None } else { Some(errorf!("{}", messages.join("\n"))) }
}

impl SlotState {
    /// Separates the monotonic trust policy from the current slot. Legacy
    /// synthetic baselines carry no authenticated version information.
    pub fn upgrade_floor(&self) -> Result<String> {
        let mut floor = self.minimum_version.clone();
        if !floor.is_empty() {
            parse_release_version(&floor)?;
        }
        if self.current.strip_prefix('v').unwrap_or(&self.current).starts_with("0.0.0+bootstrap.") {
            if floor.is_empty() {
                return Err(ErrUnknownInstalledVersion.into());
            }
            return Ok(floor);
        }
        if !self.current.is_empty() {
            parse_release_version(&self.current)?;
            if floor.is_empty() {
                return Ok(self.current.clone());
            }
            if compare_release_versions(&self.current, &floor)? > 0 {
                floor = self.current.clone();
            }
        }
        Ok(floor)
    }
}

#[derive(Clone, Debug, Default)]
pub struct SlotManager {
    pub root: String,
    pub trusted_key: Vec<u8>,
}

impl SlotManager {
    pub fn stage(&self, source_dir: &str, manifest: Option<&FirmwareManifest>) -> Result<()> {
        self.with_lock(|| {
            self.check_installation()?;
            self.recover_operation().map_err(|e| wrapf("recover interrupted slot operation", e))?;
            let Some(manifest) = manifest.filter(|m| RELEASE_VERSION_PATTERN.is_match(&m.version)) else {
                return Err(errorf!("release version is not a safe semantic version"));
            };
            if self.trusted_key.len() != PUBLIC_KEY_SIZE {
                return Err(ErrInvalidPublicKey.into());
            }
            validate_release_candidate(source_dir, Some(manifest), &self.trusted_key).map_err(|e| wrapf("verify release source", e))?;
            let mut state = self.state_without_operation()?;
            let floor = state.upgrade_floor()?;
            validate_forward_upgrade(&manifest.version, &floor)?;
            let slots = join(&self.root, "slots");
            let final_dir = join(&slots, &manifest.version);
            match std::fs::metadata(&final_dir) {
                Ok(_) => return Err(errorf!("release version is already staged")),
                Err(err) if err.kind() != std::io::ErrorKind::NotFound => return Err(err.into()),
                Err(_) => {}
            }
            let temp = TempDir(fsutil::mkdir_temp(&slots, ".staging-")?);
            fsutil::chmod(&temp.0, 0o755)?;
            for relative in manifest.files().keys() {
                let clean = filepath::clean(relative);
                fsutil::copy_regular_file(&join(source_dir, &clean), &join(&temp.0, &clean))?;
                normalize_copied_file(&temp.0, &clean)?;
            }
            validate_release_candidate(&temp.0, Some(manifest), &self.trusted_key).map_err(|e| wrapf("verify copied release slot", e))?;
            sync_payload_tree(&temp.0).map_err(|e| wrapf("sync staged slot", e))?;
            std::fs::rename(&temp.0, &final_dir).map_err(|e| wrapf("commit staged slot", e))?;
            if let Err(err) = fsutil::sync_dir(&slots) {
                let _ = std::fs::remove_dir_all(&final_dir);
                return Err(wrapf("sync committed slot", err));
            }
            state.pending = manifest.version.clone();
            if let Err(err) = self.save_state(&state) {
                let removed = std::fs::remove_dir_all(&final_dir);
                let _ = fsutil::sync_dir(&slots);
                if let Err(remove_err) = removed {
                    return Err(errorf!("{}\nremove uncommitted slot: {}", err, remove_err));
                }
                return Err(err);
            }
            Ok(())
        })
    }

    pub fn activate(&self, version: &str) -> Result<()> {
        self.with_lock(|| {
            self.check_installation()?;
            self.recover_operation().map_err(|e| wrapf("recover interrupted slot operation", e))?;
            if !RELEASE_VERSION_PATTERN.is_match(version) {
                return Err(errorf!("invalid release version"));
            }
            self.require_slot(version)?;
            let mut state = self.state_without_operation()?;
            if state.current == version {
                state.pending.clear();
                return self.save_state(&state);
            }
            let floor = state.upgrade_floor()?;
            validate_forward_upgrade(version, &floor)?;
            let old = state.clone();
            let next = SlotState { previous: old.current.clone(), current: version.into(), pending: String::new(), minimum_version: version.into(), ..state };
            self.commit_operation("activate", &old, &next, || {
                if !old.current.is_empty() {
                    self.swap_link("previous", &old.current).map_err(|e| wrapf("prepare previous slot pointer", e))?;
                } else {
                    self.remove_link("previous")?;
                }
                self.swap_link("current", version).map_err(|e| wrapf("activate slot pointer", e))
            })
        })
    }

    pub fn rollback(&self) -> Result<()> {
        self.with_lock(|| {
            self.check_installation()?;
            self.recover_operation().map_err(|e| wrapf("recover interrupted slot operation", e))?;
            let state = self.state_without_operation()?;
            if state.previous.is_empty() {
                return Err(errorf!("no previous release slot is available"));
            }
            if self.require_slot(&state.previous).is_err() {
                return Err(errorf!("previous release slot is missing"));
            }
            let old = state.clone();
            let floor = state.upgrade_floor()?;
            let next = SlotState { current: old.previous.clone(), previous: old.current.clone(), pending: String::new(), minimum_version: floor, ..state };
            self.commit_operation("rollback", &old, &next, || {
                self.swap_link("current", &next.current).map_err(|e| wrapf("restore current slot pointer", e))?;
                if !next.previous.is_empty() {
                    self.swap_link("previous", &next.previous).map_err(|e| wrapf("preserve rollback slot pointer", e))
                } else {
                    self.remove_link("previous")
                }
            })
        })
    }

    pub fn state(&self) -> Result<SlotState> {
        match self.read_operation()? {
            Some(operation) => {
                let mut state = self.project_operation_state(&operation)?;
                self.apply_installation_floor(&mut state)?;
                Ok(state)
            }
            None => self.state_without_operation(),
        }
    }

    fn state_without_operation(&self) -> Result<SlotState> {
        let mut state = match std::fs::read(join(&self.root, "state.json")) {
            Ok(data) => crate::goish::json::unmarshal::<SlotState>(&data).map_err(|e| wrapf("decode update state", e))?,
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => SlotState::default(),
            Err(err) => return Err(err.into()),
        };
        if let Some(current) = self.link_version("current")? {
            if state.pending == current {
                state.pending.clear();
            }
            state.current = current;
        }
        if let Some(previous) = self.link_version("previous")? {
            state.previous = previous;
        }
        self.apply_installation_floor(&mut state)?;
        Ok(state)
    }

    fn commit_operation(&self, kind: &str, old: &SlotState, next: &SlotState, mutate: impl FnOnce() -> Result<()>) -> Result<()> {
        let operation = SlotOperation { version: OPERATION_JOURNAL_VERSION, kind: kind.into(), old: old.clone(), next: next.clone() };
        self.begin_operation(&operation).map_err(|e| wrapf("persist slot operation journal", e))?;
        if let Err(err) = mutate() {
            return Err(self.abort_operation(old, err));
        }
        if let Err(err) = self.save_state(next) {
            return Err(self.abort_operation(old, wrapf("persist slot state", err)));
        }
        self.clear_operation().map_err(|e| wrapf("clear completed slot operation journal", e))
    }

    fn abort_operation(&self, old: &SlotState, primary: Error) -> Error {
        if let Err(err) = self.restore_links(old) {
            return errorf!("{}\nrestore slot pointers: {}", primary, err);
        }
        if let Err(err) = self.save_state(old) {
            return errorf!("{}\nrestore slot state: {}", primary, err);
        }
        if let Err(err) = self.clear_operation() {
            return errorf!("{}\nclear aborted slot operation journal: {}", primary, err);
        }
        primary
    }

    fn recover_operation(&self) -> Result<()> {
        let Some(operation) = self.read_operation()? else { return Ok(()) };
        let target = self.project_operation_state(&operation)?;
        self.restore_links(&target).map_err(|e| wrapf("recover slot pointers", e))?;
        self.save_state(&target).map_err(|e| wrapf("recover slot state", e))?;
        self.clear_operation().map_err(|e| wrapf("clear recovered slot operation journal", e))
    }

    fn project_operation_state(&self, operation: &SlotOperation) -> Result<SlotState> {
        self.validate_operation(operation)?;
        let current = self.link_version("current")?;
        match current.as_deref() {
            Some(c) if c == operation.next.current => Ok(operation.next.clone()),
            Some(c) if c == operation.old.current => Ok(operation.old.clone()),
            None if operation.old.current.is_empty() => Ok(operation.old.clone()),
            None if operation.next.current.is_empty() => Ok(operation.next.clone()),
            _ => Err(errorf!("slot operation journal does not match the current runtime pointer")),
        }
    }

    fn begin_operation(&self, operation: &SlotOperation) -> Result<()> {
        self.validate_operation(operation)?;
        self.write_atomic_json(OPERATION_JOURNAL_NAME, ".operation-", 0o600, operation)
    }

    fn read_operation(&self) -> Result<Option<SlotOperation>> {
        let data = match std::fs::read(join(&self.root, OPERATION_JOURNAL_NAME)) {
            Ok(data) => data,
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(err) => return Err(err.into()),
        };
        let operation: SlotOperation = crate::goish::json::unmarshal(&data).map_err(|e| wrapf("decode slot operation journal", e))?;
        self.validate_operation(&operation)?;
        Ok(Some(operation))
    }

    fn validate_operation(&self, operation: &SlotOperation) -> Result<()> {
        if operation.version != OPERATION_JOURNAL_VERSION {
            return Err(errorf!("unsupported slot operation journal"));
        }
        self.validate_journal_state(&operation.old).map_err(|e| wrapf("invalid old slot operation state", e))?;
        self.validate_journal_state(&operation.next).map_err(|e| wrapf("invalid next slot operation state", e))?;
        if !operation.next.pending.is_empty() {
            return Err(errorf!("completed slot operation state cannot remain pending"));
        }
        let (old, next) = (&operation.old, &operation.next);
        match operation.kind.as_str() {
            "install" if next.current.is_empty() || !next.previous.is_empty() => Err(errorf!("invalid full installation journal")),
            "activate" if next.current.is_empty() || next.current == old.current || next.previous != old.current => {
                Err(errorf!("invalid activation slot operation journal"))
            }
            "rollback" if old.previous.is_empty() || next.current != old.previous || next.previous != old.current => {
                Err(errorf!("invalid rollback slot operation journal"))
            }
            "install" | "activate" | "rollback" => Ok(()),
            _ => Err(errorf!("unknown slot operation journal kind")),
        }?;
        // Recovery trusts this persisted transition just as the original
        // operation did. A corrupt but parseable journal must not erase the
        // high-water mark or turn activation into an implicit downgrade.
        let floor = match old.upgrade_floor() {
            Err(err) if operation.kind == "install" && crate::goish::errors::is::<ErrUnknownInstalledVersion>(&err) => String::new(),
            result => result?,
        };
        if !floor.is_empty() && (next.minimum_version.is_empty() || compare_release_versions(&next.minimum_version, &floor)? < 0) {
            return Err(errorf!("slot operation journal cannot lower or erase the recorded version floor"));
        }
        if operation.kind == "activate" {
            validate_forward_upgrade(&next.current, &floor)?;
        }
        Ok(())
    }

    fn validate_journal_state(&self, state: &SlotState) -> Result<()> {
        if !state.minimum_version.is_empty() {
            parse_release_version(&state.minimum_version)?;
        }
        for version in [&state.current, &state.previous, &state.pending] {
            if version.is_empty() {
                continue;
            }
            if !RELEASE_VERSION_PATTERN.is_match(version) {
                return Err(errorf!("journal contains an invalid release version"));
            }
            if self.require_slot(version).is_err() {
                return Err(errorf!("journal references an unavailable slot {:?}", version));
            }
        }
        if !state.current.is_empty() && state.current == state.previous {
            return Err(errorf!("current and previous journal slots must differ"));
        }
        Ok(())
    }

    fn clear_operation(&self) -> Result<()> {
        remove_if_exists(&join(&self.root, OPERATION_JOURNAL_NAME))?;
        fsutil::sync_dir(&self.root)
    }

    fn ensure_root(&self) -> Result<()> {
        if self.root.trim().is_empty() {
            return Err(errorf!("update root is required"));
        }
        let slots = join(&self.root, "slots");
        std::fs::create_dir_all(&slots)?;
        fsutil::chmod(&self.root, 0o755)?;
        fsutil::chmod(&slots, 0o755)
    }

    fn with_lock<T>(&self, run: impl FnOnce() -> Result<T>) -> Result<T> {
        self.ensure_root()?;
        let mut options = std::fs::OpenOptions::new();
        options.read(true).write(true).create(true).truncate(false);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let lock = options.open(join(&self.root, ".update.lock"))?;
        fsutil::lock_exclusive(&lock)?;
        let result = run();
        fsutil::unlock(&lock);
        result
    }

    fn save_state(&self, state: &SlotState) -> Result<()> {
        self.write_atomic_json("state.json", ".state-", 0o644, state)
    }

    fn write_atomic_json<T: Serialize>(&self, name: &str, pattern: &str, mode: u32, value: &T) -> Result<()> {
        self.ensure_root()?;
        let mut data = crate::goish::json::marshal_indent(value, "  ")?;
        data.push(b'\n');
        let (mut temp, temp_name) = fsutil::create_temp(&self.root, pattern)?;
        let guard = RemoveOnDrop(temp_name.clone());
        fsutil::chmod(&temp_name, mode)?;
        temp.write_all(&data)?;
        temp.sync_all()?;
        drop(temp);
        std::fs::rename(&temp_name, join(&self.root, name))?;
        drop(guard);
        fsutil::sync_dir(&self.root)
    }

    fn swap_link(&self, name: &str, version: &str) -> Result<()> {
        self.require_slot(version)?;
        let temp_link = join(&self.root, &format!(".{}-new-{}", name, std::process::id()));
        let _ = std::fs::remove_file(&temp_link);
        fsutil::symlink(&format!("slots/{}", version), &temp_link)?;
        if let Err(err) = std::fs::rename(&temp_link, join(&self.root, name)) {
            let _ = std::fs::remove_file(&temp_link);
            return Err(err.into());
        }
        fsutil::sync_dir(&self.root)
    }

    fn remove_link(&self, name: &str) -> Result<()> {
        remove_if_exists(&join(&self.root, name))?;
        fsutil::sync_dir(&self.root)
    }

    fn restore_links(&self, state: &SlotState) -> Result<()> {
        let mut errors = Vec::new();
        for (name, version) in [("current", &state.current), ("previous", &state.previous)] {
            let result = if version.is_empty() { self.remove_link(name) } else { self.swap_link(name, version) };
            if let Err(err) = result {
                errors.push(err);
            }
        }
        join_errors(errors).map_or(Ok(()), Err)
    }

    fn require_slot(&self, version: &str) -> Result<()> {
        if !RELEASE_VERSION_PATTERN.is_match(version) {
            return Err(errorf!("invalid release version"));
        }
        if !std::fs::metadata(join(&join(&self.root, "slots"), version)).is_ok_and(|m| m.is_dir()) {
            return Err(errorf!("release slot is not staged"));
        }
        Ok(())
    }

    fn link_version(&self, name: &str) -> Result<Option<String>> {
        let target = match std::fs::read_link(join(&self.root, name)) {
            Ok(target) => target.to_string_lossy().into_owned(),
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            // A regular file or directory where a pointer belongs.
            Err(err) if err.kind() == std::io::ErrorKind::InvalidInput => return Err(errorf!("readlink {}: invalid argument", join(&self.root, name))),
            Err(err) => return Err(err.into()),
        };
        let clean = filepath::clean(&target);
        if filepath::is_abs(&clean) || filepath::dir(&clean) != "slots" {
            return Err(errorf!("unsafe {} slot pointer", name));
        }
        let version = filepath::base(&clean);
        if !RELEASE_VERSION_PATTERN.is_match(&version) {
            return Err(errorf!("invalid {} slot pointer", name));
        }
        self.require_slot(&version).map_err(|e| errorf!("{} slot pointer is broken: {}", name, e))?;
        Ok(Some(version))
    }

    fn check_installation(&self) -> Result<()> {
        match std::fs::metadata(join(&self.root, INSTALLATION_JOURNAL)) {
            Ok(_) => Err(errorf!("full installation is incomplete; finish the full installer before update or rollback")),
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(err) => Err(err.into()),
        }
    }

    /// Fences ordinary stage/activate/rollback before the full installer
    /// makes its first change; interrupted installs remain fail-closed.
    pub fn begin_installation(&self, minimum: &str) -> Result<()> {
        self.with_lock(|| {
            self.recover_operation()?;
            let state = self.state_without_operation()?;
            let minimum = installation_minimum(&state, minimum)?;
            self.write_atomic_json(INSTALLATION_JOURNAL, ".install-intent-", 0o600, &SlotState { minimum_version: minimum, ..Default::default() })
        })
    }

    /// Only the root full installer uses this. Every full install starts a
    /// new rollback generation: an application-only rollback cannot undo
    /// replaced OS integration.
    pub fn install_baseline(&self, source: &str, inventory: &FirmwareManifest, minimum: &str) -> Result<()> {
        self.with_lock(|| {
            self.recover_operation()?;
            let installed = self.state_without_operation()?;
            let minimum = installation_minimum(&installed, minimum)?;
            let sum = Sha256::digest(signed_payload(inventory)?);
            let version = format!("0.0.0+bootstrap.{}", hex::encode(&sum[..16]));
            let slots = join(&self.root, "slots");
            let temp = TempDir(fsutil::mkdir_temp(&slots, ".install-")?);
            fsutil::chmod(&temp.0, 0o755)?;
            for relative in inventory.files().keys() {
                if !filepath::is_local(relative) || &filepath::clean(relative) != relative {
                    return Err(errorf!("unsafe installer path: {}", relative));
                }
                fsutil::copy_regular_file(&join(source, relative), &join(&temp.0, relative))?;
                normalize_copied_file(&temp.0, relative)?;
            }
            let copied = inspect_local_payload(&temp.0)?;
            for (path, hash) in inventory.files() {
                if copied.files().get(path) != Some(hash) {
                    return Err(errorf!("installer payload changed during copy: {}", path));
                }
            }
            sync_payload_tree(&temp.0).map_err(|e| wrapf("persist complete baseline", e))?;
            let final_dir = join(&slots, &version);
            match std::fs::metadata(&final_dir) {
                Err(err) if err.kind() == std::io::ErrorKind::NotFound => std::fs::rename(&temp.0, &final_dir)?,
                Err(err) => return Err(err.into()),
                Ok(_) => {
                    let existing = inspect_local_payload(&final_dir)?;
                    if existing.files().len() != inventory.files().len() {
                        return Err(errorf!("existing baseline contains a different file inventory"));
                    }
                    if inventory.files().iter().any(|(path, hash)| existing.files().get(path) != Some(hash)) {
                        return Err(errorf!("existing baseline contents differ"));
                    }
                    sync_payload_tree(&final_dir)?;
                }
            }
            fsutil::sync_dir(&slots)?;
            let old = self.state_without_operation()?;
            let next = SlotState { current: version.clone(), minimum_version: minimum, ..Default::default() };
            self.commit_operation("install", &old, &next, || {
                self.remove_link("previous")?;
                self.swap_link("current", &version)
            })?;
            remove_if_exists(&join(&self.root, INSTALLATION_JOURNAL))?;
            fsutil::sync_dir(&self.root)
        })
    }

    fn apply_installation_floor(&self, state: &mut SlotState) -> Result<()> {
        state.installation_pending = false;
        let data = match std::fs::read(join(&self.root, INSTALLATION_JOURNAL)) {
            Ok(data) => data,
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(err) => return Err(err.into()),
        };
        let intent: SlotState = crate::goish::json::unmarshal(&data)?;
        state.installation_pending = true;
        if !intent.minimum_version.is_empty() {
            parse_release_version(&intent.minimum_version)?;
            if state.minimum_version.is_empty() || compare_release_versions(&intent.minimum_version, &state.minimum_version)? > 0 {
                state.minimum_version = intent.minimum_version;
            }
        }
        Ok(())
    }
}

/// Removes a staging directory unless it was renamed into place.
struct TempDir(String);

impl Drop for TempDir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

struct RemoveOnDrop(String);

impl Drop for RemoveOnDrop {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}

fn remove_if_exists(path: &str) -> Result<()> {
    match std::fs::remove_file(path) {
        Err(err) if err.kind() != std::io::ErrorKind::NotFound => Err(err.into()),
        _ => Ok(()),
    }
}

fn normalize_copied_file(root: &str, relative: &str) -> Result<()> {
    fsutil::chmod(&join(root, relative), appliance_file_mode(relative))?;
    let mut dir = filepath::dir(relative);
    while dir != "." {
        fsutil::chmod(&join(root, &dir), 0o755)?;
        dir = filepath::dir(&dir);
    }
    Ok(())
}

/// Persists file bytes/modes and every directory entry, bottom-up, before a
/// slot pointer or completion state can outlive any copied subtree.
fn sync_payload_tree(root: &str) -> Result<()> {
    fsutil::walk_files(root, &mut |path, _, regular| {
        if !regular {
            return Err(errorf!("unsafe baseline entry: {}", path));
        }
        std::fs::File::open(path)?.sync_all()?;
        Ok(())
    })?;
    for dir in fsutil::walk_dirs(root)?.iter().rev() {
        fsutil::sync_dir(dir)?;
    }
    Ok(())
}

/// Inventories an explicitly trusted root-operated full installation. It
/// does not turn an unsigned development payload into firmware.
pub fn inspect_local_payload(root: &str) -> Result<FirmwareManifest> {
    let mut manifest = FirmwareManifest { files: Some(BTreeMap::new()), ..Default::default() };
    fsutil::walk_files(root, &mut |path, relative, regular| {
        if !regular {
            return Err(errorf!("unsafe installer payload: {}", path));
        }
        let hash = fsutil::sha256_file(path)?;
        manifest.files_mut().insert(relative.to_string(), hash);
        Ok(())
    })?;
    validate_appliance_file_modes(root, Some(&manifest))?;
    Ok(manifest)
}

fn installation_minimum(state: &SlotState, proposed: &str) -> Result<String> {
    let floor = match state.upgrade_floor() {
        Ok(floor) => floor,
        Err(err) if crate::goish::errors::is::<ErrUnknownInstalledVersion>(&err) => String::new(),
        Err(err) => return Err(err),
    };
    if proposed.is_empty() {
        if !floor.is_empty() {
            return Err(errorf!("full installation cannot erase a recorded version floor; repeat preflight"));
        }
        return Ok(String::new());
    }
    parse_release_version(proposed)?;
    if !floor.is_empty() && compare_release_versions(proposed, &floor)? < 0 {
        return Err(errorf!("full installation cannot lower the recorded version floor"));
    }
    Ok(proposed.into())
}

#[cfg(test)]
#[path = "slots_journal_tests.rs"]
mod journal_tests;

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    fn payload(dir: &str) -> FirmwareManifest {
        let mut manifest = FirmwareManifest::default();
        for role in appliance_file_roles(host_arch()).unwrap() {
            let path = join(dir, &role.path);
            std::fs::create_dir_all(std::path::Path::new(&path).parent().unwrap()).unwrap();
            std::fs::write(&path, role.path.as_bytes()).unwrap();
            fsutil::chmod(&path, role.mode).unwrap();
            manifest.files_mut().insert(role.path.clone(), fsutil::sha256_file(&path).unwrap());
        }
        manifest
    }

    #[test]
    fn stage_activate_and_rollback() {
        let base = std::env::temp_dir().join(format!("mr-slots-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        let source = base.join("src").to_string_lossy().into_owned();
        let (public, private) = generate_key_pair().unwrap();
        let manager = SlotManager { root: base.join("root").to_string_lossy().into_owned(), trusted_key: public };
        for version in ["0.3.0", "0.3.1"] {
            let mut manifest = payload(&source);
            manifest.version = version.into();
            sign_manifest(&mut manifest, &private).unwrap();
            manager.stage(&source, Some(&manifest)).unwrap();
            manager.activate(version).unwrap();
        }
        let state = manager.state().unwrap();
        assert_eq!((state.current.as_str(), state.previous.as_str(), state.minimum_version.as_str()), ("0.3.1", "0.3.0", "0.3.1"));
        manager.rollback().unwrap();
        let state = manager.state().unwrap();
        assert_eq!((state.current.as_str(), state.previous.as_str(), state.minimum_version.as_str()), ("0.3.0", "0.3.1", "0.3.1"));
        // The floor survives rollback: re-activating 0.3.0's predecessor or
        // staging an older release is refused.
        let mut old = payload(&source);
        old.version = "0.2.9".into();
        sign_manifest(&mut old, &private).unwrap();
        assert!(manager.stage(&source, Some(&old)).unwrap_err().to_string().contains("refusing non-forward update"));
        std::fs::remove_dir_all(base).ok();
    }
}
