//! Signed firmware updates: the cached release check, the durable operation
//! record, published-release and offline-upload installation through the
//! privileged helper, and reconciliation after the activation restart.

use serde::{Deserialize, Serialize};
use std::io::Write;
use std::sync::{Mutex as StdMutex, RwLock as StdRwLock};
use std::time::Duration as StdDuration;

use super::*;
use crate::firmware::{SlotManager, SlotState};
use crate::release::{self, Channel, Checker};

const UPDATE_ROOT: &str = "/var/lib/minimalrouter-update";
const UPDATE_INBOX: &str = "/var/lib/minimalrouter/update-inbox";
const UPDATE_PRIVILEGE_HELPER: &str = "/usr/libexec/minimalrouter/routerd-update";
const UPDATE_ACTIVATION_LOG: &str = "/var/lib/minimalrouter/update.log";
const UPDATE_OPERATION_PATH: &str = "/var/lib/minimalrouter/update-operation.json";
const UPDATE_PREFERENCES_PATH: &str = "/var/lib/minimalrouter/update-preferences.json";
const FIRMWARE_STAGE_TIMEOUT: StdDuration = StdDuration::from_secs(180);
const MAX_FIRMWARE_UPLOAD_BODY: u64 = 131 << 20;
const MAX_FIRMWARE_MANIFEST_UPLOAD: u64 = 1 << 20;
const MAX_FIRMWARE_ARCHIVE_UPLOAD: u64 = 128 << 20;
/// 131 MiB still arrives at about 1 Mbit/s.
const FIRMWARE_UPLOAD_READ_TIMEOUT: StdDuration = StdDuration::from_secs(20 * 60);
/// Download, expanded payload and new slot while current and previous stay.
const UPDATE_FREE_SPACE_BYTES: u64 = 700 << 20;

static FIRMWARE_UPDATE: StdMutex<()> = StdMutex::new(());

// Stable codes the dashboard maps to explanations.
const BLOCK_MISSING_TRUST_KEY: &str = "missing_trust_key";
const BLOCK_MISSING_HELPER: &str = "missing_update_helper";
const BLOCK_MISSING_BASELINE: &str = "missing_baseline";
const BLOCK_UNKNOWN_INSTALLED_VERSION: &str = "unknown_installed_version";
const BLOCK_BELOW_INSTALLED_VERSION: &str = "below_installed_version";
const BLOCK_PENDING_ACTIVATION: &str = "pending_activation";
const BLOCK_UNSUPPORTED_ARCH: &str = "unsupported_architecture";
const BLOCK_INSUFFICIENT_SPACE: &str = "insufficient_space";
const BLOCK_CONFIGURATION_BUSY: &str = "configuration_pending";
const BLOCK_RECOVERY_REQUIRED: &str = "recovery_required";
const BLOCK_UPDATE_IN_PROGRESS: &str = "update_in_progress";
const BLOCK_CHECK_UNAVAILABLE: &str = "check_unavailable";
const BLOCK_NO_CANDIDATE: &str = "no_candidate";
const BLOCK_ALREADY_CURRENT: &str = "already_current";
const BLOCK_LOCAL_STATE_UNKNOWN: &str = "local_state_unavailable";
const BLOCK_CANDIDATE_SUPERSEDED: &str = "candidate_superseded";

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "snake_case")]
pub enum UpdateState {
    #[default]
    Queued,
    Downloading,
    Verifying,
    Staging,
    Activating,
    CheckingHealth,
    Succeeded,
    Failed,
    RollingBack,
    RolledBack,
    RecoveryRequired,
}

impl UpdateState {
    pub fn terminal(&self) -> bool {
        matches!(self, UpdateState::Succeeded | UpdateState::Failed | UpdateState::RolledBack | UpdateState::RecoveryRequired)
    }

    /// Whether the slot pointer may already have moved.
    fn past_activation(&self) -> bool {
        matches!(self, UpdateState::Activating | UpdateState::CheckingHealth | UpdateState::RollingBack)
    }
}

/// The durable record of one accepted update; it survives closing the tab,
/// losing the response and restarting routerd.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UpdateOperation {
    pub id: String,
    pub state: UpdateState,
    pub from_version: String,
    pub target_version: String,
    pub candidate_id: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub idempotency_key: String,
    pub source: String,
    pub started_at: Time,
    pub updated_at: Time,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub completed_at: Option<Time>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error_code: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

crate::sentinel!(ErrUpdateInProgress, "another update is already running");

fn write_private_atomic(path: &str, pattern: &str, data: &[u8], sync: bool) -> Result<()> {
    let dir = std::path::Path::new(path).parent().map(|p| p.to_string_lossy().into_owned()).unwrap_or_else(|| ".".into());
    std::fs::create_dir_all(&dir)?;
    let mut suffix = [0u8; 4];
    let _ = crate::goish::rand::fill(&mut suffix);
    let temp = format!("{}/{}{}", dir, pattern, u32::from_le_bytes(suffix));
    let result = (|| -> Result<()> {
        let mut options = std::fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&temp)?;
        file.write_all(data)?;
        if sync {
            file.sync_all()?;
        }
        drop(file);
        std::fs::rename(&temp, path)?;
        Ok(())
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&temp);
    }
    result
}

/// Exactly one record: two concurrent updates are never valid, so the file
/// is the mutual exclusion that survives a restart.
pub(crate) struct UpdateOperationStore {
    path: String,
    state: StdMutex<(bool, Option<UpdateOperation>)>,
}

impl UpdateOperationStore {
    pub(crate) fn new(path: &str) -> UpdateOperationStore {
        UpdateOperationStore { path: path.into(), state: StdMutex::new((false, None)) }
    }

    fn with<T>(&self, f: impl FnOnce(&mut Option<UpdateOperation>, &dyn Fn(&Option<UpdateOperation>) -> Result<()>) -> Result<T>) -> Result<T> {
        let mut guard = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if !guard.0 {
            match std::fs::read(&self.path) {
                Ok(data) => {
                    // An unreadable record must not silently unlock an update.
                    let op: UpdateOperation = crate::goish::json::unmarshal(&data).map_err(|e| crate::errorf!("update operation state is unreadable: {}", e))?;
                    guard.1 = Some(op);
                }
                Err(err) if err.kind() == std::io::ErrorKind::NotFound => {}
                Err(err) => return Err(err.into()),
            }
            guard.0 = true;
        }
        let path = self.path.clone();
        let save = move |current: &Option<UpdateOperation>| -> Result<()> {
            match current {
                None => match std::fs::remove_file(&path) {
                    Err(err) if err.kind() != std::io::ErrorKind::NotFound => Err(err.into()),
                    _ => Ok(()),
                },
                Some(op) => {
                    let mut data = crate::goish::json::marshal_indent(op, "  ")?;
                    data.push(b'\n');
                    write_private_atomic(&path, ".update-operation-", &data, true)
                }
            }
        };
        f(&mut guard.1, &save)
    }

    pub(crate) fn current(&self) -> Result<Option<UpdateOperation>> {
        self.with(|current, _| Ok(current.clone()))
    }

    /// An identical idempotency key returns the existing operation, so a
    /// retried request cannot install twice.
    pub(crate) fn begin(&self, mut operation: UpdateOperation) -> Result<UpdateOperation> {
        self.with(|current, save| {
            if let Some(existing) = current.as_ref() {
                let same_key = !operation.idempotency_key.is_empty() && operation.idempotency_key == existing.idempotency_key;
                if same_key {
                    return Ok(existing.clone());
                }
                if !existing.state.terminal() {
                    return Err(ErrUpdateInProgress.into());
                }
            }
            operation.updated_at = operation.started_at;
            let previous = current.replace(operation.clone());
            if let Err(err) = save(current) {
                *current = previous;
                return Err(err);
            }
            Ok(operation)
        })
    }

    fn update(&self, id: &str, now: Time, mutate: impl FnOnce(&mut UpdateOperation)) -> Result<()> {
        self.with(|current, save| {
            let Some(op) = current.as_mut().filter(|op| op.id == id) else {
                return Err(crate::errorf!("update operation {:?} is not the recorded operation", id));
            };
            mutate(op);
            op.updated_at = now;
            save(current)
        })
    }

    pub(crate) fn advance(&self, id: &str, state: UpdateState, now: Time) -> Result<()> {
        self.update(id, now, |op| op.state = state)
    }

    pub(crate) fn finish(&self, id: &str, state: UpdateState, code: &str, message: &str, now: Time) -> Result<()> {
        self.update(id, now, |op| {
            op.state = state;
            op.error_code = code.into();
            op.error = message.into();
            op.completed_at = Some(now);
        })
    }

    /// A restart means this process no longer runs the operation. Before
    /// activation nothing was installed; after it the updater's journal is
    /// authoritative and the operator verifies the running version.
    pub(crate) fn recover_interrupted(&self, now: Time) -> Result<Option<UpdateOperation>> {
        self.with(|current, save| {
            let Some(op) = current.as_mut().filter(|op| !op.state.terminal()) else { return Ok(None) };
            if op.state.past_activation() {
                op.state = UpdateState::RecoveryRequired;
                op.error_code = "interrupted_during_activation".into();
                op.error = "The management service restarted while the new release was being activated. The privileged updater's slot journal holds the authoritative result; verify the running version before retrying.".into();
            } else {
                op.state = UpdateState::Failed;
                op.error_code = "interrupted".into();
                op.error = "The management service restarted before the release was activated. Nothing was installed; the update can be started again.".into();
            }
            op.completed_at = Some(now);
            op.updated_at = now;
            let copy = op.clone();
            save(current)?;
            Ok(Some(copy))
        })
    }
}

/// The channel is an update preference, never part of the canonical config.
pub(crate) struct UpdatePreferences {
    path: String,
    channel: StdMutex<Option<Channel>>,
}

impl UpdatePreferences {
    fn new(path: &str) -> UpdatePreferences {
        UpdatePreferences { path: path.into(), channel: StdMutex::new(None) }
    }

    /// Existing installations have always been offered pre-releases, so Beta
    /// stays the default.
    pub(crate) fn channel(&self) -> Channel {
        let mut guard = self.channel.lock().unwrap_or_else(|e| e.into_inner());
        *guard.get_or_insert_with(|| {
            #[derive(Deserialize, Default)]
            #[serde(default)]
            struct Stored {
                channel: String,
            }
            std::fs::read(&self.path).ok().and_then(|d| crate::goish::json::unmarshal::<Stored>(&d).ok()).and_then(|s| release::parse_channel(&s.channel)).unwrap_or(Channel::Beta)
        })
    }

    pub(crate) fn set_channel(&self, channel: Channel) -> Result<()> {
        let _ = self.channel();
        let mut data = crate::goish::json::marshal_indent(&json!({"channel": channel.as_str()}), "  ")?;
        data.push(b'\n');
        write_private_atomic(&self.path, ".update-preferences-", &data, false)?;
        *self.channel.lock().unwrap_or_else(|e| e.into_inner()) = Some(channel);
        Ok(())
    }
}

#[derive(Default)]
pub(crate) struct UpdateService {
    checker: Option<Arc<Checker>>,
    operations: Option<Arc<UpdateOperationStore>>,
    preferences: Option<Arc<UpdatePreferences>>,
}

pub(crate) type UpdateServiceSlot = StdRwLock<UpdateService>;

#[derive(Serialize, Default)]
struct FirmwareStatusResponse {
    enabled: bool,
    current_version: String,
    running_version: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    previous_version: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pending_version: String,
    channel: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    latest_version: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    target_version: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    candidate_id: String,
    update_available: bool,
    can_install: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    blocked_reason: String,
    #[serde(skip_serializing_if = "crate::goish::json::is_false")]
    prerelease: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    published_at: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    release_url: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    release_notes: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    checked_at: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    last_successful_check_at: String,
    stale: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    check_error: String,
    #[serde(skip_serializing_if = "crate::goish::json::is_false")]
    rate_limited: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    next_check_not_before: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    manual_check_not_before: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    operation: Option<UpdateOperation>,
}

fn appliance_update_state() -> Result<SlotState> {
    SlotManager { root: UPDATE_ROOT.into(), trusted_key: Vec::new() }.state()
}

#[cfg(unix)]
fn update_helper_installed() -> bool {
    use std::os::unix::fs::PermissionsExt;
    std::fs::metadata(UPDATE_PRIVILEGE_HELPER).is_ok_and(|m| m.is_file() && m.permissions().mode() & 0o777 == 0o755)
}

#[cfg(not(unix))]
fn update_helper_installed() -> bool {
    false
}

/// Space a normal process may still use on the update filesystem.
#[cfg(unix)]
fn available_update_bytes() -> Result<u64> {
    let path = std::ffi::CString::new(UPDATE_ROOT)?;
    let mut stat: libc::statvfs = unsafe { std::mem::zeroed() };
    if unsafe { libc::statvfs(path.as_ptr(), &mut stat) } != 0 {
        return Err(std::io::Error::last_os_error().into());
    }
    Ok(stat.f_bavail as u64 * stat.f_frsize as u64)
}

#[cfg(not(unix))]
fn available_update_bytes() -> Result<u64> {
    Err(crate::errorf!("statfs unsupported"))
}

/// A bootstrap directory is a content identity, not the running release.
fn current_appliance_version(state: &SlotState) -> String {
    if !state.current.is_empty() && !state.current.strip_prefix('v').unwrap_or(&state.current).starts_with("0.0.0+bootstrap.") {
        return state.current.clone();
    }
    crate::buildinfo::display_version()
}

fn format_optional_time(value: Time) -> String {
    if value.is_zero() { String::new() } else { value.utc().rfc3339() }
}

fn bounded_output(output: &crate::goish::exec::Output) -> String {
    let mut combined = output.combined();
    combined.truncate(32 << 10);
    String::from_utf8_lossy(&combined).trim().to_string()
}

fn run_update_stage() -> std::result::Result<String, (String, String)> {
    let output = crate::goish::exec::run("/usr/bin/doas", &[UPDATE_PRIVILEGE_HELPER, "stage"], FIRMWARE_STAGE_TIMEOUT);
    let text = bounded_output(&output);
    if output.timed_out {
        return Err((text, "signed update staging timed out".into()));
    }
    match output.error() {
        Some(err) => Err((text, format!("signed update staging failed: {}", err))),
        None => Ok(text),
    }
}

/// Activation restarts routerd, so it runs in its own session and must not
/// die with the process that started it.
fn activate_pending_update_detached() -> Result<()> {
    let log_file = std::fs::OpenOptions::new().create(true).truncate(true).write(true).open(UPDATE_ACTIVATION_LOG)?;
    let mut command = std::process::Command::new("/usr/bin/doas");
    command.args([UPDATE_PRIVILEGE_HELPER, "activate"]).stdin(std::process::Stdio::null()).stdout(log_file.try_clone()?).stderr(log_file);
    #[cfg(unix)]
    unsafe {
        use std::os::unix::process::CommandExt;
        command.pre_exec(|| {
            libc::setsid();
            Ok(())
        });
    }
    command.spawn()?;
    Ok(())
}

fn expected_update_paths() -> (String, String) {
    (format!("{}/release/minimalrouter-linux-{}", UPDATE_INBOX, crate::firmware::host_arch()), format!("{}/manifest.json", UPDATE_INBOX))
}

fn save_firmware_upload(reader: &mut crate::httpd::MultipartReader<'_, '_>, destination: &str, maximum: u64) -> std::result::Result<(), String> {
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(destination).map_err(|e| e.to_string())?;
    let (mut written, mut buf) = (0u64, vec![0u8; 64 * 1024]);
    loop {
        let n = reader.read_part(&mut buf).map_err(|e| e.to_string())?;
        if n == 0 {
            return Ok(());
        }
        written += n as u64;
        if written > maximum {
            return Err("uploaded firmware file exceeds size limit".into());
        }
        file.write_all(&buf[..n]).map_err(|e| e.to_string())?;
    }
}

impl Server {
    fn updates(&self) -> std::sync::RwLockReadGuard<'_, UpdateService> {
        self.ext.updates.read().unwrap_or_else(|e| e.into_inner())
    }

    /// Attaches the release checker and the durable operation record, and
    /// reconciles an operation interrupted by a restart.
    pub fn configure_updates(&self, catalog: release::Catalog, arch: &str) -> Arc<Checker> {
        let preferences = Arc::new(UpdatePreferences::new(UPDATE_PREFERENCES_PATH));
        let operations = Arc::new(UpdateOperationStore::new(UPDATE_OPERATION_PATH));
        let channel_source = preferences.clone();
        let checker = Arc::new(Checker::new(catalog, arch, Box::new(move || Some(channel_source.channel()))));
        *self.ext.updates.write().unwrap_or_else(|e| e.into_inner()) = UpdateService { checker: Some(checker.clone()), operations: Some(operations.clone()), preferences: Some(preferences) };
        self.reconcile_interrupted_update(&operations, Time::now());
        checker
    }

    /// The activation restart is the normal successful path: slot state and
    /// the running version decide the outcome.
    fn reconcile_interrupted_update(&self, operations: &UpdateOperationStore, now: Time) {
        let operation = match operations.current() {
            Ok(Some(op)) if !op.state.terminal() => op,
            Ok(_) => return,
            Err(err) => {
                log::info!("[UPDATE] Could not read the recorded update operation: {}", err);
                return;
            }
        };
        if let (Ok(state), true) = (appliance_update_state(), operation.state.past_activation()) {
            if state.current == operation.target_version {
                let _ = operations.finish(&operation.id, UpdateState::Succeeded, "", "", now);
                log::info!("[UPDATE] Operation {} completed: running {}", operation.id, state.current);
                self.append_audit("firmware.update_succeeded", "local", details(&[("operation", &operation.id), ("version", &operation.target_version)]));
                return;
            }
            if state.current == operation.from_version {
                let _ = operations.finish(&operation.id, UpdateState::RolledBack, "activation_rolled_back", "The new release did not become healthy and the previous version was restored.", now);
                log::info!("[UPDATE] Operation {} rolled back to {}", operation.id, state.current);
                self.append_audit("firmware.update_rolled_back", "local", details(&[("operation", &operation.id), ("version", &state.current)]));
                return;
            }
        }
        match operations.recover_interrupted(now) {
            Ok(Some(recovered)) => log::info!("[UPDATE] Operation {} marked {:?} after a restart", recovered.id, recovered.state),
            Ok(None) => {}
            Err(err) => log::info!("[UPDATE] Could not reconcile the interrupted update: {}", err),
        }
    }

    fn update_snapshot(&self) -> release::Snapshot {
        match self.updates().checker.clone() {
            Some(checker) => checker.snapshot(),
            None => release::Snapshot { stale_after: release::DEFAULT_STALE_AFTER, ..Default::default() },
        }
    }

    fn update_channel(&self) -> Channel {
        self.updates().preferences.as_ref().map(|p| p.channel()).unwrap_or(Channel::Stable)
    }

    fn current_update_operation(&self) -> Result<Option<UpdateOperation>> {
        match self.updates().operations.clone() {
            Some(store) => store.current(),
            None => Ok(None),
        }
    }

    fn operations_store(&self) -> Result<Arc<UpdateOperationStore>> {
        self.updates().operations.clone().ok_or_else(|| crate::errorf!("update operations are not configured"))
    }

    fn must_operation(&self, id: &str) -> Option<UpdateOperation> {
        self.current_update_operation().ok().flatten().filter(|op| op.id == id)
    }

    fn advance_operation(&self, id: &str, state: UpdateState) -> Result<()> {
        self.operations_store()?.advance(id, state, Time::now())
    }

    fn fail_operation(&self, id: &str, code: &str, message: &str) -> crate::goish::errors::Error {
        let Ok(store) = self.operations_store() else { return crate::errorf!("update operations are not configured") };
        log::info!("[UPDATE] Operation {} failed ({}): {}", id, code, message);
        self.append_audit("firmware.update_failed", "local", details(&[("operation", id), ("reason", code)]));
        match store.finish(id, UpdateState::Failed, code, message, Time::now()) {
            Err(err) => err,
            Ok(()) => crate::errorf!("{}", message),
        }
    }

    /// A UI aid from local state only: every condition is checked again at
    /// the privilege boundary. A greyed-out button is not access control.
    fn assess_update_capability(&self, state: &Result<SlotState>, snapshot: &release::Snapshot, operation: Option<&UpdateOperation>, now: Time) -> std::result::Result<(), &'static str> {
        if !self.update_trust_configured() {
            return Err(BLOCK_MISSING_TRUST_KEY);
        }
        if !matches!(crate::firmware::host_arch(), "amd64" | "arm64") {
            return Err(BLOCK_UNSUPPORTED_ARCH);
        }
        if !update_helper_installed() {
            return Err(BLOCK_MISSING_HELPER);
        }
        let Ok(state) = state else { return Err(BLOCK_LOCAL_STATE_UNKNOWN) };
        if state.current.is_empty() {
            return Err(BLOCK_MISSING_BASELINE);
        }
        let Ok(floor) = state.upgrade_floor() else { return Err(BLOCK_UNKNOWN_INSTALLED_VERSION) };
        if !state.pending.is_empty() {
            return Err(BLOCK_PENDING_ACTIVATION);
        }
        if operation.is_some_and(|op| !op.state.terminal()) {
            return Err(BLOCK_UPDATE_IN_PROGRESS);
        }
        let engine = self.engine.get_status();
        if engine.recovery_required {
            return Err(BLOCK_RECOVERY_REQUIRED);
        }
        if engine.applying || self.engine.get_pending_transaction().is_some() {
            return Err(BLOCK_CONFIGURATION_BUSY);
        }
        if available_update_bytes().is_ok_and(|free| free < UPDATE_FREE_SPACE_BYTES) {
            return Err(BLOCK_INSUFFICIENT_SPACE);
        }
        // Not knowing is not the same as being up to date.
        if snapshot.last_success_at.is_zero() || snapshot.stale(now) {
            return Err(BLOCK_CHECK_UNAVAILABLE);
        }
        let Some(candidate) = &snapshot.candidate else { return Err(BLOCK_NO_CANDIDATE) };
        if !release::is_newer_version(&candidate.version, &current_appliance_version(state)) {
            return Err(BLOCK_ALREADY_CURRENT);
        }
        if !release::is_newer_version(&candidate.version, &floor) {
            return Err(BLOCK_BELOW_INSTALLED_VERSION);
        }
        Ok(())
    }

    fn firmware_status(&self, now: Time) -> FirmwareStatusResponse {
        let state = appliance_update_state();
        let mut status = FirmwareStatusResponse { enabled: self.update_trust_configured(), running_version: crate::buildinfo::display_version(), channel: self.update_channel().as_str().into(), ..Default::default() };
        match &state {
            Ok(state) => {
                status.current_version = current_appliance_version(state);
                status.previous_version = state.previous.clone();
                status.pending_version = state.pending.clone();
            }
            Err(_) => {
                status.current_version = crate::buildinfo::display_version();
                status.check_error = "Local update state is unavailable.".into();
            }
        }
        let operation = self.current_update_operation();
        if operation.is_err() && status.check_error.is_empty() {
            status.check_error = "Update operation state is unavailable.".into();
        }
        status.operation = operation.ok().flatten();
        let snapshot = self.update_snapshot();
        status.checked_at = format_optional_time(snapshot.checked_at);
        status.last_successful_check_at = format_optional_time(snapshot.last_success_at);
        status.stale = snapshot.stale(now);
        status.rate_limited = snapshot.rate_limited;
        status.next_check_not_before = format_optional_time(snapshot.next_earliest);
        status.manual_check_not_before = format_optional_time(snapshot.cooldown_expires);
        if !snapshot.error.is_empty() {
            // The dashboard shows "check unavailable", never "up to date".
            status.check_error = "The release check did not complete.".into();
        }
        if let Some(newest) = &snapshot.newest {
            status.latest_version = newest.version.clone();
        }
        if let Some(candidate) = &snapshot.candidate {
            status.target_version = candidate.version.clone();
            status.candidate_id = candidate.id();
            status.prerelease = candidate.prerelease;
            status.published_at = format_optional_time(candidate.published_at);
            status.release_url = candidate.url.clone();
            status.release_notes = candidate.notes.clone();
            status.update_available = release::is_newer_version(&candidate.version, &status.current_version);
        }
        match self.assess_update_capability(&state, &snapshot, status.operation.as_ref(), now) {
            Ok(()) => status.can_install = true,
            Err(reason) => status.blocked_reason = reason.into(),
        }
        status
    }

    /// Observer sessions only observe; the helper remains the boundary.
    fn session_is_read_only(&self, r: &Request<'_>) -> bool {
        self.request_session(r).map(|s| s.read_only).unwrap_or(true)
    }

    pub(crate) fn handle_firmware_status(&self, _r: &mut Request<'_>) -> Response {
        // Answers from cache: the dashboard never waits on GitHub.
        write_json(200, &self.firmware_status(Time::now()))
    }

    pub(crate) fn handle_firmware_local_status(&self, _r: &mut Request<'_>) -> Response {
        match appliance_update_state() {
            Err(_) => write_json(503, &FirmwareStatusResponse { enabled: self.update_trust_configured(), check_error: "Local update state is unavailable.".into(), ..Default::default() }),
            Ok(state) => write_json(
                200,
                &FirmwareStatusResponse { enabled: self.update_trust_configured(), current_version: current_appliance_version(&state), previous_version: state.previous, pending_version: state.pending, ..Default::default() },
            ),
        }
    }

    pub(crate) fn handle_firmware_check(&self, r: &mut Request<'_>) -> Response {
        if self.session_is_read_only(r) {
            return write_json(403, &json!({"error": "This session is read-only."}));
        }
        let Some(checker) = self.updates().checker.clone() else { return write_json(503, &json!({"error": "Release checking is not configured."})) };
        let (_, result) = checker.check_now();
        let status = match result {
            Err(err) if is::<release::ErrCheckTooSoon>(&err) => 429,
            // Another tab is already checking; both see the shared result.
            _ => 200,
        };
        write_json(status, &self.firmware_status(Time::now()))
    }

    pub(crate) fn handle_firmware_channel(&self, r: &mut Request<'_>) -> Response {
        if self.session_is_read_only(r) {
            return write_json(403, &json!({"error": "This session is read-only."}));
        }
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            channel: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return write_json(400, &json!({"error": "Invalid JSON."})) };
        let Some(channel) = release::parse_channel(&req.channel) else { return write_json(422, &json!({"error": "Channel must be stable or beta."})) };
        let stored = self.updates().preferences.clone().ok_or_else(|| crate::errorf!("update preferences are not configured")).and_then(|p| p.set_channel(channel));
        if stored.is_err() {
            return write_json(500, &json!({"error": "Could not store the update channel."}));
        }
        self.append_audit("firmware.channel_changed", &audit_actor(&r.remote_addr), details(&[("channel", channel.as_str())]));
        write_json(200, &self.firmware_status(Time::now()))
    }

    /// Verification through the privileged helper, then activation; shared
    /// by published releases and offline uploads.
    fn stage_and_activate(&self, id: &str, current: &str, version: &str, payload_dir: &str, manifest_path: &str) -> Result<()> {
        let (expected_payload, expected_manifest) = expected_update_paths();
        if payload_dir != expected_payload || manifest_path != expected_manifest {
            return Err(self.fail_operation(id, "inbox_mismatch", "The prepared release path did not match the fixed update inbox."));
        }
        self.advance_operation(id, UpdateState::Staging)?;
        if let Err((output, _)) = run_update_stage() {
            let mut message = "Signed release verification and staging failed.".to_string();
            if !output.is_empty() {
                message = format!("{} {}", message, output);
            }
            return Err(self.fail_operation(id, "staging_failed", &message));
        }
        self.advance_operation(id, UpdateState::Activating)?;
        self.append_audit("firmware.update_staged", "local", details(&[("from", current), ("to", version)]));
        // Activation restarts routerd; the durable record plus the updater's
        // slot journal are what the next process reconciles.
        if activate_pending_update_detached().is_err() {
            self.append_audit("firmware.activation_start_failed", "local", details(&[("version", version)]));
            return Err(self.fail_operation(id, "activation_start_failed", "The verified release was staged, but activation could not be started."));
        }
        Ok(())
    }

    pub(crate) fn handle_firmware_update(&self, r: &mut Request<'_>) -> Response {
        if self.session_is_read_only(r) {
            return write_json(403, &json!({"error": "This session is read-only."}));
        }
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            candidate_id: String,
            target_version: String,
            idempotency_key: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return write_json(400, &json!({"error": "Invalid JSON."})) };
        // The confirmation must name what was confirmed.
        if req.candidate_id.trim().is_empty() || req.target_version.trim().is_empty() {
            return write_json(400, &json!({"error": "The update request must name the confirmed candidate.", "blocked_reason": BLOCK_CANDIDATE_SUPERSEDED}));
        }
        let Ok(_guard) = FIRMWARE_UPDATE.try_lock() else { return write_json(409, &json!({"error": "Another firmware operation is already in progress."})) };
        let now = Time::now();
        let state = appliance_update_state();
        let snapshot = self.update_snapshot();
        let operation = self.current_update_operation().ok().flatten();
        if let Err(reason) = self.assess_update_capability(&state, &snapshot, operation.as_ref(), now) {
            return write_json(409, &json!({"error": "This appliance cannot install an update right now.", "blocked_reason": reason, "status": self.firmware_status(now)}));
        }
        // Re-derived from the server's cache: a release published since the
        // dashboard rendered cannot take the confirmed one's place.
        let Some(candidate) = snapshot.candidate.clone().filter(|c| c.id() == req.candidate_id && c.version == req.target_version) else {
            return write_json(409, &json!({"error": "The confirmed release is no longer the current candidate. Review the new one and confirm again.", "blocked_reason": BLOCK_CANDIDATE_SUPERSEDED, "status": self.firmware_status(now)}));
        };
        let current = state.as_ref().map(current_appliance_version).unwrap_or_default();
        let begun = self.operations_store().and_then(|store| {
            store.begin(UpdateOperation {
                id: format!("upd-{}", now.unix_nano()),
                state: UpdateState::Queued,
                from_version: current.clone(),
                target_version: candidate.version.clone(),
                candidate_id: candidate.id(),
                idempotency_key: req.idempotency_key.trim().into(),
                source: "published_release".into(),
                started_at: now,
                ..Default::default()
            })
        });
        let accepted = match begun {
            Ok(accepted) => accepted,
            Err(err) if is::<ErrUpdateInProgress>(&err) => {
                return write_json(409, &json!({"error": "Another update is already running.", "blocked_reason": BLOCK_UPDATE_IN_PROGRESS, "status": self.firmware_status(now)}));
            }
            Err(_) => return write_json(500, &json!({"error": "Could not record the update operation."})),
        };
        if accepted.state != UpdateState::Queued {
            // A retry of an accepted request reports the existing outcome.
            return write_json(202, &json!({"success": true, "operation": accepted, "status": self.firmware_status(now)}));
        }
        self.append_audit("firmware.update_accepted", &audit_actor(&r.remote_addr), details(&[("from", &current), ("to", &candidate.version), ("operation", &accepted.id)]));
        // The work outlives this request deliberately.
        if let Some(server) = self.ext.self_ref.get().and_then(|w| w.upgrade()) {
            let operation = accepted.clone();
            std::thread::spawn(move || server.run_published_update(operation, candidate));
        }
        write_json(202, &json!({"success": true, "operation": accepted, "status": self.firmware_status(now)}))
    }

    fn run_published_update(&self, operation: UpdateOperation, candidate: release::Release) {
        if self.advance_operation(&operation.id, UpdateState::Downloading).is_err() {
            return;
        }
        let (payload_dir, manifest_path) = match release::prepare_published(&candidate, crate::firmware::host_arch(), UPDATE_INBOX) {
            Ok(paths) => paths,
            Err(err) => {
                let _ = self.fail_operation(&operation.id, "download_failed", &format!("The signed release could not be downloaded or unpacked: {}", err));
                return;
            }
        };
        if self.advance_operation(&operation.id, UpdateState::Verifying).is_err() {
            return;
        }
        let _ = self.stage_and_activate(&operation.id, &operation.from_version, &candidate.version, &payload_dir, &manifest_path);
    }

    pub(crate) fn handle_firmware_upload(&self, r: &mut Request<'_>) -> Response {
        if self.session_is_read_only(r) {
            return write_json(403, &json!({"error": "This session is read-only."}));
        }
        let Ok(_guard) = FIRMWARE_UPDATE.try_lock() else { return write_json(409, &json!({"error": "Another firmware operation is already in progress."})) };
        let now = Time::now();
        let state = appliance_update_state();
        let operation = self.current_update_operation().ok().flatten();
        // An upload carries its own payload: check freshness is irrelevant.
        let mut snapshot = self.update_snapshot();
        snapshot.last_success_at = now;
        snapshot.candidate = None;
        if let Err(reason) = self.assess_update_capability(&state, &snapshot, operation.as_ref(), now) {
            if reason != BLOCK_NO_CANDIDATE {
                return write_json(409, &json!({"error": "This appliance cannot install an update right now.", "blocked_reason": reason}));
            }
        }
        let current = state.as_ref().map(current_appliance_version).unwrap_or_default();
        extend_upload_read_deadline(r, FIRMWARE_UPLOAD_READ_TIMEOUT);
        r.limit_body(MAX_FIRMWARE_UPLOAD_BODY);
        let Ok(mut reader) = crate::httpd::MultipartReader::new(r) else {
            return write_json(400, &json!({"error": "Upload must be multipart/form-data with manifest and archive files."}));
        };
        let Ok(temp) = tempdir_in("/var/lib/minimalrouter", ".firmware-upload-") else {
            return write_json(507, &json!({"error": "Could not allocate private upload staging space."}));
        };
        let (manifest_source, archive_source) = (format!("{}/manifest.json", temp.0), format!("{}/release.tar.gz", temp.0));
        let (mut manifest_seen, mut archive_seen) = (false, false);
        loop {
            let part = match reader.next_part() {
                Ok(Some(part)) => part,
                Ok(None) => break,
                Err(err) if err.to_string().contains(crate::httpd::BODY_TOO_LARGE) => return write_json(413, &json!({"error": "Firmware upload exceeds the 131 MiB request limit."})),
                Err(_) => return write_json(400, &json!({"error": "Could not read firmware upload."})),
            };
            let saved = match part.name.as_str() {
                "manifest" if !manifest_seen && !part.filename.is_empty() => {
                    manifest_seen = true;
                    save_firmware_upload(&mut reader, &manifest_source, MAX_FIRMWARE_MANIFEST_UPLOAD)
                }
                "manifest" => return write_json(400, &json!({"error": "Exactly one signed manifest file is required."})),
                "archive" if !archive_seen && !part.filename.is_empty() => {
                    archive_seen = true;
                    save_firmware_upload(&mut reader, &archive_source, MAX_FIRMWARE_ARCHIVE_UPLOAD)
                }
                "archive" => return write_json(400, &json!({"error": "Exactly one release archive file is required."})),
                _ => return write_json(400, &json!({"error": "Firmware upload contains an unexpected form field."})),
            };
            if let Err(err) = saved {
                return if err.contains("size limit") || err.contains(crate::httpd::BODY_TOO_LARGE) { write_json(413, &json!({"error": err})) } else { write_json(400, &json!({"error": "Could not store firmware upload."})) };
            }
        }
        if !manifest_seen || !archive_seen {
            return write_json(400, &json!({"error": "Both signed manifest and release archive files are required."}));
        }
        let (payload_dir, manifest_path, version) = match release::prepare_local_release(&manifest_source, &archive_source, crate::firmware::host_arch(), UPDATE_INBOX) {
            Ok(prepared) => prepared,
            Err(err) => return write_json(422, &json!({"error": format!("Could not prepare uploaded release: {}", err)})),
        };
        if !release::is_newer_version(&version, &current) {
            return write_json(409, &json!({"error": "Uploaded build must have a version newer than the currently active release."}));
        }
        let begun = self.operations_store().and_then(|store| {
            store.begin(UpdateOperation { id: format!("upd-{}", now.unix_nano()), state: UpdateState::Queued, from_version: current.clone(), target_version: version.clone(), source: "upload".into(), started_at: now, ..Default::default() })
        });
        let Ok(accepted) = begun else { return write_json(409, &json!({"error": "Another update is already running."})) };
        self.append_audit("firmware.update_accepted", &audit_actor(&r.remote_addr), details(&[("from", &current), ("to", &version), ("operation", &accepted.id), ("source", "upload")]));
        if self.stage_and_activate(&accepted.id, &current, &version, &payload_dir, &manifest_path).is_err() {
            return write_json(422, &json!({"error": "The uploaded release was not installed.", "operation": self.must_operation(&accepted.id)}));
        }
        write_json(
            202,
            &json!({"success": true, "operation": self.must_operation(&accepted.id), "state": "activating", "version": version, "message": "Signed release verified and staged. Minimal Router is restarting into the new slot."}),
        )
    }
}

/// A private temporary directory removed on drop.
struct TempDir(String);

impl Drop for TempDir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

fn tempdir_in(dir: &str, pattern: &str) -> std::io::Result<TempDir> {
    for _ in 0..100 {
        let mut suffix = [0u8; 4];
        let _ = crate::goish::rand::fill(&mut suffix);
        let path = format!("{}/{}{}", dir, pattern, u32::from_le_bytes(suffix));
        match std::fs::create_dir(&path) {
            Ok(()) => {
                #[cfg(unix)]
                {
                    use std::os::unix::fs::PermissionsExt;
                    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o700))?;
                }
                return Ok(TempDir(path));
            }
            Err(err) if err.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(err) => return Err(err),
        }
    }
    Err(std::io::Error::other("temporary directory collisions"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn operation_store_is_idempotent_and_recovers_interruptions() {
        let path = std::env::temp_dir().join(format!("mr-update-op-{}.json", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let store = UpdateOperationStore::new(path.to_str().unwrap());
        let now = Time::from_unix(1_800_000_000, 0);
        let op = UpdateOperation { id: "upd-1".into(), idempotency_key: "k".into(), started_at: now, ..Default::default() };
        store.begin(op.clone()).unwrap();
        assert_eq!(store.begin(op.clone()).unwrap().id, "upd-1");
        assert!(is::<ErrUpdateInProgress>(&store.begin(UpdateOperation { id: "upd-2".into(), ..Default::default() }).unwrap_err()));
        store.advance("upd-1", UpdateState::Activating, now).unwrap();
        let fresh = UpdateOperationStore::new(path.to_str().unwrap());
        let recovered = fresh.recover_interrupted(now).unwrap().unwrap();
        assert_eq!((recovered.state, recovered.error_code.as_str()), (UpdateState::RecoveryRequired, "interrupted_during_activation"));
        std::fs::remove_file(path).ok();
    }
}
