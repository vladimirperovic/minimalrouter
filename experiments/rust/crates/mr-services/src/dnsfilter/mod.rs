//! Opt-in network DNS blocking outside the immutable bootstrap configuration
//! contract. Downloads run as routerd; the root helper accepts only validated
//! domains and writes one fixed dnsmasq drop-in.

mod catalog;
mod policy;
mod service;

pub use catalog::Catalog;
pub use policy::*;
pub use service::{decode_metadata, policy_hash, Check, Client, Emit, Helper, Match, Operation, Service, Status, OPERATION_RETENTION};
