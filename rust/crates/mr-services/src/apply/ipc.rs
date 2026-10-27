//! The typed local IPC between routerd and router-applyd. The JSON encoding is
//! identical to the Go implementation, so a Go routerd can drive a Rust
//! router-applyd and vice versa.

use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::time::Instant;

use crate::config::{Revision, SystemConfig};
use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json::is_false;
use crate::goish::time::Time;

/// Location of the privileged Unix domain socket.
pub const DEFAULT_SOCKET_PATH: &str = "/run/minimalrouter/apply.sock";

pub const PROTOCOL_VERSION: i64 = 1;
pub const MAX_REQUEST_BYTES: usize = 2 << 20;
pub const MAX_RESPONSE_BYTES: usize = 256 << 10;
#[cfg_attr(not(unix), allow(dead_code))]
const DEFAULT_IPC_DEADLINE: std::time::Duration = std::time::Duration::from_secs(120);

/// The allowlisted privileged RPC operations. Kept as a string newtype so an
/// unknown operation decodes and is then rejected explicitly by the helper.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(transparent)]
pub struct OperationType(pub String);

impl OperationType {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

macro_rules! ops {
    ($($name:ident = $value:expr),* $(,)?) => {
        $(pub fn $name() -> OperationType { OperationType($value.to_string()) })*
    };
}

pub mod op {
    use super::OperationType;
    ops!(
        apply_all = "APPLY_ALL",
        confirm = "CONFIRM",
        commit_confirmed = "COMMIT_CONFIRMED",
        reconcile = "RECONCILE",
        load_nftables = "LOAD_NFTABLES",
        reload_service = "RELOAD_SERVICE",
        restore_snapshot = "RESTORE_SNAPSHOT",
        // Read-only telemetry: the helper returns sanitized fields only.
        wg_tunnel_status = "WG_TUNNEL_STATUS",
        firewall_counters = "FIREWALL_COUNTERS",
    );
}

/// A size-limited RPC payload sent from routerd to router-applyd.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct ApplyRequest {
    pub version: i64,
    pub id: String,
    pub op: OperationType,
    pub revision: Revision,
    pub config: SystemConfig,
    pub nftables: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub pppoe_peer: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub pppoe_secret: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub dnsmasq: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hostapd: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub wireguard: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub service_name: String,
    #[serde(skip_serializing_if = "is_false")]
    pub require_confirmation: bool,
    /// Scopes the wg1 handshake proof to transactions that changed wg1.
    #[serde(skip_serializing_if = "is_false")]
    pub verify_wg_client: bool,
    /// The interface for the read-only WG telemetry RPC.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub tunnel_interface: String,
    /// Withholds last-good until the caller committed the canonical store.
    #[serde(skip_serializing_if = "is_false")]
    pub defer_last_good: bool,
    /// Relaxes commit-ack WAN verification for two-phase commits.
    #[serde(skip_serializing_if = "is_false")]
    pub skip_wan_verify: bool,
}

/// Sanitized per-peer WireGuard projection; private and preshared keys never
/// cross the root helper boundary.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct TunnelPeerStatus {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub public_key: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub endpoint: String,
    pub last_handshake: i64,
    pub rx_bytes: i64,
    pub tx_bytes: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct TunnelStatus {
    pub interface: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub endpoint: String,
    pub last_handshake: i64,
    pub rx_bytes: i64,
    pub tx_bytes: i64,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub peers: Vec<TunnelPeerStatus>,
}

/// Bounded counter and base-policy metadata; never raw rules, addresses or
/// dynamic set contents.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FirewallCounters {
    pub seen: u64,
    pub accepted: u64,
    pub generation: String,
    pub observed_at: Time,
    pub policies: BTreeMap<String, String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub loaded_revision: Option<Revision>,
    pub rule_packets: BTreeMap<String, u64>,
}

/// The structured execution output from router-applyd.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct ApplyResponse {
    pub id: String,
    pub success: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub logs: String,
    pub verified: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub rolled_back: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub recovery_required: bool,
    pub timestamp: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tunnel_status: Option<TunnelStatus>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub firewall_counters: Option<FirewallCounters>,
}

impl ApplyResponse {
    /// Rejects contradictory privileged outcomes: a malformed response is never
    /// proof that either the candidate or the previous runtime is active.
    pub fn validate(&self) -> Result<()> {
        if self.success {
            if !self.verified {
                return Err(errorf!("successful response is not verified"));
            }
            if self.rolled_back {
                return Err(errorf!("successful response also reports rollback"));
            }
            if self.recovery_required {
                return Err(errorf!("successful response also requires recovery"));
            }
            return Ok(());
        }
        if self.verified {
            return Err(errorf!("failed response cannot report verified success"));
        }
        if self.rolled_back && self.recovery_required {
            return Err(errorf!("response cannot report both rollback and recovery required"));
        }
        Ok(())
    }
}

/// The only interface the unprivileged control plane uses to request
/// privileged configuration application. `deadline` replaces Go's context.
pub trait Client: Send + Sync {
    fn apply(&self, deadline: Instant, req: &ApplyRequest) -> Result<ApplyResponse>;
}

pub struct UnixClient {
    pub socket_path: String,
}

impl UnixClient {
    pub fn new(socket_path: &str) -> UnixClient {
        UnixClient { socket_path: if socket_path.is_empty() { DEFAULT_SOCKET_PATH.into() } else { socket_path.into() } }
    }
}

#[cfg(unix)]
impl Client for UnixClient {
    fn apply(&self, deadline: Instant, req: &ApplyRequest) -> Result<ApplyResponse> {
        use std::io::{Read, Write};
        use std::os::unix::net::UnixStream;

        let mut stream = UnixStream::connect(&self.socket_path)
            .map_err(|e| errorf!("cannot connect to router-applyd at {}: dial unix {}: {}", self.socket_path, self.socket_path, e))?;
        let deadline = deadline.min(Instant::now() + DEFAULT_IPC_DEADLINE);
        let remaining = deadline.saturating_duration_since(Instant::now()).max(std::time::Duration::from_millis(1));
        stream.set_read_timeout(Some(remaining)).map_err(|e| errorf!("failed to set IPC deadline: {}", e))?;
        stream.set_write_timeout(Some(remaining)).map_err(|e| errorf!("failed to set IPC deadline: {}", e))?;

        let mut req = req.clone();
        req.version = PROTOCOL_VERSION;
        let mut body = crate::goish::json::marshal(&req).map_err(|e| errorf!("failed to send IPC request: {}", e))?;
        body.push(b'\n');
        stream.write_all(&body).map_err(|e| errorf!("failed to send IPC request: {}", e))?;
        // Signal the end of the one-object request without closing the read
        // side; the helper verifies EOF to reject trailing objects.
        stream.shutdown(std::net::Shutdown::Write).map_err(|e| errorf!("failed to finalize IPC request: {}", e))?;

        let mut data = Vec::new();
        stream.take(MAX_RESPONSE_BYTES as u64).read_to_end(&mut data).map_err(|e| errorf!("failed to read IPC response: {}", e))?;
        let resp = decode_single::<ApplyResponse>(&data).map_err(|e| match e {
            SingleDecodeError::Trailing => errorf!("router-applyd returned trailing data"),
            SingleDecodeError::Invalid(e) => errorf!("failed to read IPC response: {}", e),
        })?;
        if resp.id != req.id {
            return Err(errorf!("router-applyd response ID mismatch"));
        }
        Ok(resp)
    }
}

#[cfg(not(unix))]
impl Client for UnixClient {
    fn apply(&self, _deadline: Instant, _req: &ApplyRequest) -> Result<ApplyResponse> {
        Err(errorf!("cannot connect to router-applyd at {}: unix sockets are unavailable on this platform", self.socket_path))
    }
}

pub enum SingleDecodeError {
    Invalid(serde_json::Error),
    Trailing,
}

/// One JSON value followed only by whitespace, as Go's decoder loop checks.
pub fn decode_single<T: serde::de::DeserializeOwned>(data: &[u8]) -> std::result::Result<T, SingleDecodeError> {
    let mut stream = serde_json::Deserializer::from_slice(data).into_iter::<serde_json::Value>();
    let value = match stream.next() {
        Some(Ok(value)) => value,
        Some(Err(e)) => return Err(SingleDecodeError::Invalid(e)),
        None => return Err(SingleDecodeError::Invalid(serde::de::Error::custom("EOF"))),
    };
    if stream.next().is_some() {
        return Err(SingleDecodeError::Trailing);
    }
    crate::goish::json::from_value(&value).map_err(SingleDecodeError::Invalid)
}
