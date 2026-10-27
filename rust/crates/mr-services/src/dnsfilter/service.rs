//! The routerd-side DNS filter service: downloads and indexes lists as routerd,
//! streams validated domains to the root helper and keeps an operation journal.

use rusqlite::{Connection, OptionalExtension, params};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::io::Write;
use std::path::PathBuf;
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

use super::catalog::{Catalog, catalog_match, catalog_path, download_catalog, emit_catalog, read_catalog};
#[cfg(unix)]
use super::policy::Response;
use super::policy::{Applied, MAX_DOMAINS, MAX_METADATA_BYTES, Policy, Request, SOCKET_PATH, Source, default_policy, domain, sources};
use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json;
use crate::goish::time::{Duration as GoDuration, Time};

pub const OPERATION_RETENTION: usize = 20;

pub type Emit<'a> = &'a mut dyn FnMut(&mut dyn Write) -> Result<()>;

pub trait Helper: Send + Sync {
    fn call(&self, deadline: Instant, request: Request, emit: Option<Emit<'_>>) -> Result<Applied, (Applied, crate::goish::errors::Error)>;
}

pub fn decode_metadata<T: serde::de::DeserializeOwned>(data: &[u8]) -> Result<T> {
    if data.len() > MAX_METADATA_BYTES {
        return Err(errorf!("DNS filter metadata is too large"));
    }
    match crate::apply::decode_single(data) {
        Ok(v) => Ok(v),
        Err(crate::apply::SingleDecodeError::Trailing) => Err(errorf!("trailing DNS filter metadata")),
        Err(crate::apply::SingleDecodeError::Invalid(e)) => Err(e.into()),
    }
}

pub struct Client {
    pub socket_path: String,
}

impl Client {
    pub fn new() -> Client {
        Client { socket_path: SOCKET_PATH.into() }
    }
}

impl Default for Client {
    fn default() -> Self {
        Client::new()
    }
}

impl Helper for Client {
    /// Domains are streamed through a bounded socket, never as paths or
    /// dnsmasq directives. `emit` runs only for an apply operation.
    #[cfg(unix)]
    fn call(&self, deadline: Instant, mut request: Request, emit: Option<Emit<'_>>) -> Result<Applied, (Applied, crate::goish::errors::Error)> {
        use std::io::Read;
        use std::os::unix::net::UnixStream;
        let fail = |e: crate::goish::errors::Error| (Applied::default(), e);
        let stream = UnixStream::connect(&self.socket_path).map_err(|_| fail(errorf!("DNS filter helper unavailable")))?;
        let deadline = deadline.min(Instant::now() + Duration::from_secs(90));
        let remaining = deadline.saturating_duration_since(Instant::now()).max(Duration::from_millis(1));
        stream.set_read_timeout(Some(remaining)).map_err(|e| fail(e.into()))?;
        stream.set_write_timeout(Some(remaining)).map_err(|e| fail(e.into()))?;
        let mut writer = std::io::BufWriter::with_capacity(32768, &stream);
        request.version = 1;
        let mut header = json::marshal(&request).map_err(|e| fail(e.into()))?;
        header.push(b'\n');
        writer.write_all(&header).map_err(|e| fail(e.into()))?;
        if let Some(emit) = emit {
            emit(&mut writer).map_err(fail)?;
        }
        writer.flush().map_err(|e| fail(e.into()))?;
        drop(writer);
        stream.shutdown(std::net::Shutdown::Write).map_err(|e| fail(e.into()))?;
        let mut raw = Vec::new();
        (&stream).take(MAX_METADATA_BYTES as u64 + 1).read_to_end(&mut raw).map_err(|e| fail(e.into()))?;
        let response: Response = decode_metadata(&raw).map_err(fail)?;
        if !response.error.is_empty() {
            return Err((response.state, errorf!("{}", response.error)));
        }
        Ok(response.state)
    }

    #[cfg(not(unix))]
    fn call(&self, _deadline: Instant, _request: Request, _emit: Option<Emit<'_>>) -> Result<Applied, (Applied, crate::goish::errors::Error)> {
        Err((Applied::default(), errorf!("DNS filter helper unavailable")))
    }
}

/// Bounded diagnostic metadata, never credentials or domain lists.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Operation {
    pub id: String,
    pub kind: String,
    pub state: String,
    pub phase: String,
    pub actor: String,
    pub base_revision: u64,
    pub target_revision: u64,
    #[serde(skip_serializing_if = "crate::goish::json::is_zero_u64")]
    pub applied_revision: u64,
    pub target_hash: String,
    pub started_at: Time,
    pub updated_at: Time,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub finished_at: Option<Time>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct OperationJournal {
    last_attempt: Time,
    operations: Vec<Operation>,
}

/// Binds restart reconciliation to the exact requested policy.
pub fn policy_hash(p: &Policy) -> String {
    let mut next = default_policy();
    for (key, enabled) in &p.categories {
        if *enabled {
            next.categories.insert(key.clone(), true);
        }
    }
    next.exceptions = p.exceptions.clone();
    next.exceptions.sort_by(|a, b| a.domain.cmp(&b.domain));
    hex::encode(Sha256::digest(json::marshal(&next).unwrap_or_default()))
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Status {
    #[serde(flatten)]
    pub applied: Applied,
    #[serde(rename = "lists")]
    pub sources_status: Vec<Catalog>,
    pub updating: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    pub next_refresh_at: i64,
    pub refresh_allowed_at: i64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub operation: Option<Operation>,
    pub generated_at: Time,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Match {
    pub category: String,
    pub domain: String,
    pub enabled: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Check {
    pub domain: String,
    pub action: String,
    pub exception: bool,
    pub matches: Vec<Match>,
    pub healthy: bool,
    pub policy_revision: u64,
    pub checked_at: Time,
}

type Download = dyn Fn(&std::path::Path, &Source, &Catalog, Option<&(dyn Fn() -> bool + Send + Sync)>) -> Result<Catalog> + Send + Sync;

struct State {
    busy: bool,
    last_error: String,
    last_attempt: Time,
    closed: bool,
    generation: u64,
    operations: Vec<Operation>,
    observer: Option<Box<dyn Fn(&Operation) + Send>>,
}

pub struct Service {
    dir: PathBuf,
    helper: Arc<dyn Helper>,
    allow_write: Option<Arc<dyn Fn() -> bool + Send + Sync>>,
    journal: Mutex<Option<Connection>>,
    state: Mutex<State>,
    wake: Condvar,
    download: Arc<Download>,
    workers: Mutex<Vec<std::thread::JoinHandle<()>>>,
}

fn now_utc() -> Time {
    Time::now_utc()
}

impl Service {
    pub fn open(data_dir: &str, helper: Arc<dyn Helper>, allow_write: Option<Arc<dyn Fn() -> bool + Send + Sync>>) -> Result<Arc<Service>> {
        let dir = std::path::Path::new(data_dir).join("dns-filter");
        std::fs::create_dir_all(&dir)?;
        crate::config::set_mode(&dir.to_string_lossy(), 0o700)?;
        let service = Arc::new(Service {
            dir,
            helper,
            allow_write,
            journal: Mutex::new(None),
            state: Mutex::new(State {
                busy: false,
                last_error: String::new(),
                last_attempt: Time::zero(),
                closed: false,
                generation: 0,
                operations: Vec::new(),
                observer: None,
            }),
            wake: Condvar::new(),
            download: Arc::new(|path, source, previous, allow| download_catalog(path, source, previous, allow)),
            workers: Mutex::new(Vec::new()),
        });
        service.open_journal()?;
        let weak = Arc::downgrade(&service);
        let handle = std::thread::spawn(move || run(weak));
        service.workers.lock().unwrap().push(handle);
        Ok(service)
    }

    fn state(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    pub fn close(&self) {
        self.state().closed = true;
        self.wake.notify_all();
        let handles: Vec<_> = self.workers.lock().unwrap().drain(..).collect();
        for handle in handles {
            let _ = handle.join();
        }
        *self.journal.lock().unwrap() = None;
    }

    fn open_journal(&self) -> Result<()> {
        let path = self.dir.join("operations.db");
        let conn = Connection::open(&path)?;
        conn.execute_batch("PRAGMA busy_timeout=1000; PRAGMA synchronous=FULL; PRAGMA trusted_schema=OFF;")?;
        conn.execute_batch("CREATE TABLE IF NOT EXISTS state(id INTEGER PRIMARY KEY CHECK(id=1),body TEXT NOT NULL)")?;
        crate::config::set_mode(&path.to_string_lossy(), 0o600)?;
        let data: Option<String> = conn.query_row("SELECT body FROM state WHERE id=1", [], |r| r.get(0)).optional()?;
        *self.journal.lock().unwrap() = Some(conn);
        let Some(data) = data else { return Ok(()) };
        let journal: OperationJournal = match json::unmarshal::<OperationJournal>(data.as_bytes()) {
            Ok(j) if data.len() <= 128 << 10 && j.operations.len() <= OPERATION_RETENTION => j,
            _ => {
                *self.journal.lock().unwrap() = None;
                return Err(errorf!("DNS operation history is invalid"));
            }
        };
        let mut st = self.state();
        st.operations = journal.operations;
        st.last_attempt = journal.last_attempt;
        // A restart is not proof of success and never replays a mutation.
        for op in st.operations.iter_mut() {
            if op.state == "running" {
                op.state = "needs_review".into();
                op.phase = "interrupted".into();
                op.error = "Router management restarted before the DNS outcome was recorded. Verify the applied policy.".into();
                op.updated_at = now_utc();
            }
        }
        self.save_journal(&st)
    }

    fn save_journal(&self, st: &State) -> Result<()> {
        let journal = self.journal.lock().unwrap();
        let Some(conn) = journal.as_ref() else { return Ok(()) };
        let data = json::marshal_string(&OperationJournal { last_attempt: st.last_attempt, operations: st.operations.clone() })?;
        conn.execute("INSERT INTO state(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", params![data])?;
        Ok(())
    }

    /// Records requests and terminal outcomes; the callback runs with the
    /// service locked and must not call Service methods.
    pub fn set_observer(&self, observer: Box<dyn Fn(&Operation) + Send>) {
        self.state().observer = Some(observer);
    }

    pub fn history(&self) -> Vec<Operation> {
        self.state().operations.clone()
    }

    fn operation_phase(&self, phase: &str) -> Result<()> {
        let mut st = self.state();
        if let Some(op) = st.operations.first_mut() {
            op.phase = phase.into();
            op.updated_at = now_utc();
        }
        self.save_journal(&st)
    }

    fn reconcile_operation(&self, st: &mut State, state: &Applied) {
        let Some(op) = st.operations.first() else { return };
        if op.state != "needs_review" || state.activation_pending || !state.healthy || op.phase == "recovery_required" {
            return;
        }
        if state.policy.revision != op.target_revision || policy_hash(&state.policy) != op.target_hash {
            return;
        }
        let previous = op.clone();
        let now = now_utc();
        {
            let op = &mut st.operations[0];
            op.state = "completed".into();
            op.phase = "verified".into();
            op.error.clear();
            op.applied_revision = state.policy.revision;
            op.updated_at = now;
            op.finished_at = Some(now);
        }
        if self.save_journal(st).is_err() {
            st.operations[0] = previous;
            return;
        }
        st.last_error.clear();
        if let Some(observer) = &st.observer {
            observer(&st.operations[0]);
        }
    }

    fn catalog(&self, source: &Source, hash: &str) -> (Catalog, i64, Result<()>) {
        let mut chosen = Catalog::default();
        let mut slot: i64 = -1;
        for i in 0..2 {
            let (meta, result) = read_catalog(&catalog_path(&self.dir, source.id, i), source);
            if result.is_err() {
                continue;
            }
            if !hash.is_empty() && meta.hash == hash {
                return (meta, i as i64, Ok(()));
            }
            if hash.is_empty() && meta.updated_at >= chosen.updated_at {
                chosen = meta;
                slot = i as i64;
            }
        }
        if slot < 0 {
            let empty = Catalog { category: source.id.into(), label: source.label.into(), url: source.url(), ..Default::default() };
            return (empty, -1, Err(errorf!("list not downloaded")));
        }
        (chosen, slot, Ok(()))
    }

    pub fn status(&self, deadline: Instant) -> Result<Status> {
        let generation = self.state().generation;
        let state = self.helper.call(deadline, Request { operation: "status".into(), ..Default::default() }, None).map_err(|(_, e)| e)?;
        let mut status = Status { applied: state.clone(), generated_at: now_utc(), ..Default::default() };
        for source in sources() {
            let hash = state.sources.get(source.id).cloned().unwrap_or_default();
            let (mut meta, _, result) = self.catalog(&source, &hash);
            let enabled = state.policy.category(source.id);
            if result.is_err() && enabled {
                meta.error = "Installed list index unavailable; domain checks cannot verify it".into();
                status.next_refresh_at = Time::now().unix();
            }
            if enabled && meta.updated_at > 0 && (status.next_refresh_at == 0 || meta.updated_at + 86400 < status.next_refresh_at) {
                status.next_refresh_at = meta.updated_at + 86400;
            }
            status.sources_status.push(meta);
        }
        let mut st = self.state();
        self.reconcile_operation(&mut st, &state);
        status.updating = st.busy || generation != st.generation;
        status.error = st.last_error.clone();
        if let Some(op) = st.operations.first() {
            status.operation = Some(op.clone());
            if status.error.is_empty() {
                status.error = op.error.clone();
            }
        }
        if !st.last_attempt.is_zero() {
            status.refresh_allowed_at = st.last_attempt.add(GoDuration::minutes(5)).unix();
            let floor = st.last_attempt.add(GoDuration::hours(6)).unix();
            if status.next_refresh_at > 0 && status.next_refresh_at < floor {
                status.next_refresh_at = floor;
            }
        }
        Ok(status)
    }

    pub fn start(self: &Arc<Self>, policy: &Policy, refresh: bool) -> Result<()> {
        self.start_operation(policy, refresh, "system").map(|_| ())
    }

    /// Accepts a revision-bound policy; the download/build runs in one owned
    /// worker while the previously verified rules keep running.
    pub fn start_operation(self: &Arc<Self>, policy: &Policy, refresh: bool, actor: &str) -> Result<Operation> {
        policy.validate()?;
        let mut st = self.state();
        if st.closed {
            return Err(errorf!("DNS filter is stopping"));
        }
        if st.busy {
            return Err(errorf!("DNS filter update is already running"));
        }
        if refresh && Time::now().sub(&st.last_attempt) < GoDuration::minutes(5) {
            return Err(errorf!("wait five minutes before refreshing lists again"));
        }
        if policy.revision == u64::MAX {
            return Err(errorf!("DNS filter revision exhausted"));
        }
        let next = policy.clone();
        let op = new_operation(&next, refresh, actor);
        let (old_operations, old_attempt) = (st.operations.clone(), st.last_attempt);
        st.operations.insert(0, op.clone());
        st.operations.truncate(OPERATION_RETENTION);
        st.last_attempt = op.started_at;
        if self.save_journal(&st).is_err() {
            st.operations = old_operations;
            st.last_attempt = old_attempt;
            return Err(errorf!("DNS operation could not be recorded; no change started"));
        }
        if let Some(observer) = &st.observer {
            observer(&op);
        }
        st.busy = true;
        st.generation += 1;
        st.last_error.clear();
        drop(st);
        let this = self.clone();
        let handle = std::thread::spawn(move || {
            let result = this.apply(&next, refresh);
            let mut st = this.state();
            st.busy = false;
            st.generation += 1;
            let finished = now_utc();
            let mut last_error = None;
            {
                let current = &mut st.operations[0];
                current.updated_at = finished;
                current.finished_at = Some(finished);
                current.state = "completed".into();
                current.applied_revision = next.revision + 1;
                match &result {
                    Ok(()) => current.phase = "verified".into(),
                    Err(err) => {
                        let msg = err.to_string();
                        last_error = Some(msg.clone());
                        current.state = "failed".into();
                        current.applied_revision = 0;
                        current.error = msg.clone();
                        if msg.contains("previous DNS configuration restored") {
                            current.state = "rolled_back".into();
                        }
                        if msg.contains("outcome unconfirmed") {
                            current.state = "needs_review".into();
                            current.phase = "outcome_unknown".into();
                        }
                        if msg.contains("recovery required") {
                            current.state = "needs_review".into();
                            current.phase = "recovery_required".into();
                        }
                    }
                }
            }
            if let Some(msg) = last_error {
                st.last_error = msg;
            }
            if this.save_journal(&st).is_err() {
                st.operations[0].state = "needs_review".into();
                st.operations[0].error = "DNS outcome could not be recorded. Refresh to verify the applied policy.".into();
                st.last_error = st.operations[0].error.clone();
            }
            if let Some(observer) = &st.observer {
                observer(&st.operations[0]);
            }
        });
        self.workers.lock().unwrap().push(handle);
        Ok(op)
    }

    fn apply(&self, policy: &Policy, refresh: bool) -> Result<()> {
        let history = || errorf!("DNS operation history unavailable; no change applied");
        self.operation_phase("preparing").map_err(|_| history())?;
        let deadline = Instant::now() + Duration::from_secs(600);
        let before = self.helper.call(deadline, Request { operation: "status".into(), ..Default::default() }, None).map_err(|(_, e)| e)?;
        if before.policy.revision != policy.revision {
            return Err(errorf!("DNS filter changed in another session; reload before saving"));
        }
        let mut paths = Vec::new();
        let mut hashes = std::collections::BTreeMap::new();
        let mut total = 0i64;
        for source in sources() {
            if !policy.category(source.id) {
                continue;
            }
            let installed = before.sources.get(source.id).cloned().unwrap_or_default();
            let (mut meta, mut slot, read) = self.catalog(&source, &installed);
            // An explicit policy edit reuses an installed, hash-matched catalog.
            if read.is_err() || refresh {
                self.operation_phase("downloading").map_err(|_| history())?;
                if self.allow_write.as_ref().is_some_and(|f| !f()) {
                    return Err(errorf!("storage pressure prevents DNS list refresh; current policy retained"));
                }
                let next_slot = if slot == 0 { 1 } else { 0 };
                let allow = self.allow_write.as_deref();
                meta = (self.download)(&catalog_path(&self.dir, source.id, next_slot), &source, &meta, allow)
                    .map_err(|e| errorf!("{}: refresh failed; current policy and lists retained ({})", source.label, e))?;
                slot = next_slot as i64;
            }
            total += meta.entries;
            if total > MAX_DOMAINS as i64 {
                return Err(errorf!("selected lists exceed the appliance domain limit"));
            }
            paths.push(catalog_path(&self.dir, source.id, slot as usize));
            hashes.insert(source.id.to_string(), meta.hash.clone());
        }
        self.operation_phase("applying").map_err(|_| history())?;
        let mut emit = |w: &mut dyn Write| -> Result<()> {
            for path in &paths {
                emit_catalog(path, w)?;
            }
            Ok(())
        };
        let request = Request { operation: "apply".into(), expected_revision: policy.revision, policy: policy.clone(), sources: hashes, ..Default::default() };
        match self.helper.call(deadline, request, Some(&mut emit)) {
            Ok(applied) => {
                if !applied.healthy
                    || applied.activation_pending
                    || applied.policy.revision != policy.revision + 1
                    || policy_hash(&applied.policy) != policy_hash(policy)
                {
                    return Err(errorf!("DNS outcome unconfirmed; verify the applied policy before retrying"));
                }
                Ok(())
            }
            Err((applied, err)) if applied.policy.revision == 0 => Err(errorf!("DNS outcome unconfirmed: {}", err)),
            Err((_, err)) => Err(err),
        }
    }

    pub fn check(&self, deadline: Instant, name: &str) -> Result<Check> {
        let d = domain(name).ok_or_else(|| errorf!("enter a public domain name without a URL or IP address"))?;
        let state = self.helper.call(deadline, Request { operation: "status".into(), ..Default::default() }, None).map_err(|(_, e)| e)?;
        let mut result = Check {
            domain: d.clone(),
            action: "No category block".into(),
            matches: Vec::new(),
            healthy: state.healthy && !state.activation_pending,
            exception: state.policy.allowed(&d),
            policy_revision: state.policy.revision,
            checked_at: now_utc(),
        };
        for source in sources() {
            let hash = state.sources.get(source.id).cloned().unwrap_or_default();
            let (_, slot, read) = self.catalog(&source, &hash);
            if read.is_err() {
                if state.policy.category(source.id) {
                    return Err(errorf!("installed list index is unavailable"));
                }
                continue;
            }
            let found = catalog_match(&catalog_path(&self.dir, source.id, slot as usize), &d)?;
            if !found.is_empty() {
                let on = state.policy.category(source.id);
                result.matches.push(Match { category: source.id.into(), domain: found, enabled: on });
                if on {
                    result.action = "Block".into();
                }
            }
        }
        if result.exception {
            result.action = "Allow exception".into();
        }
        Ok(result)
    }
}

fn new_operation(policy: &Policy, refresh: bool, actor: &str) -> Operation {
    let now = now_utc();
    Operation {
        id: crate::auth::generate_random_hex(16).unwrap_or_default(),
        kind: if refresh { "refresh" } else { "policy" }.into(),
        state: "running".into(),
        phase: "queued".into(),
        actor: actor.into(),
        base_revision: policy.revision,
        target_revision: policy.revision + 1,
        target_hash: policy_hash(policy),
        started_at: now,
        updated_at: now,
        ..Default::default()
    }
}

/// Hourly status check that starts an automatic refresh when lists are due.
fn run(service: std::sync::Weak<Service>) {
    loop {
        let Some(this) = service.upgrade() else { return };
        if let Ok(status) = this.status(Instant::now() + Duration::from_secs(5)) {
            if status.next_refresh_at > 0 && status.next_refresh_at <= Time::now().unix() && !status.updating {
                let _ = this.start(&status.applied.policy, true);
            }
        }
        let st = this.state();
        if st.closed {
            return;
        }
        let (st, _) = this.wake.wait_timeout(st, Duration::from_secs(3600)).unwrap_or_else(|e| e.into_inner());
        if st.closed {
            return;
        }
    }
}
