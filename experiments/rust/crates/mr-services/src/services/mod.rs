//! Deterministic generators for every Linux service the appliance drives, plus
//! read-only policy explanations. Generators never execute anything.

mod device_profiles;
mod generators;
mod nftables;
mod review;
mod wireguard;

pub(crate) mod firewall_counter {
    pub use super::review::firewall_rule_counter;
}

pub use device_profiles::service_domains;
pub use generators::*;
pub use nftables::{generate_nftables, ACCOUNTING_SET_RX, ACCOUNTING_SET_TX};
pub use review::{
    describe_firewall_changes, describe_network_changes, device_profile_statuses, explain_firewall_flow, firewall_rule_counter,
    firewall_warnings, FirewallExplanation, FirewallFlow, ProfileStatus,
};
pub use wireguard::*;

pub type WireGuardConfig = crate::config::WireGuardConfig;
pub type WireGuardPeer = crate::config::WireGuardPeer;
