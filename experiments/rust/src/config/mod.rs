//! Canonical configuration model, validation and SQLite persistence.

mod audit_query;
mod backup;
mod model;
mod offline;
mod pfsense;
mod policy;
mod profiles;
mod recovery_status;
mod store;
mod validation;

pub use audit_query::{AuditPage, AuditQuery};
pub use backup::{decrypt_config_backup, encrypt_config_backup, BackupEncryption, BackupEnvelope, BackupPayload};
pub use model::*;
pub use offline::{validate_offline_candidate, OfflineConfig};
pub use pfsense::{import_pfsense_xml, import_pfsense_xml_with_mapping, PfSenseImportReport, PfSenseInterfaceMapping};
pub use policy::{equal_section, firewall_rule_scope, network_contains_network, requires_confirmation, validate_live_candidate};
pub use profiles::SUPPORTED_PROFILE_SERVICES;
pub use recovery_status::validate_snapshot_label;
pub use store::{
    is_high_volume_audit_event, AuditEvent, ErrNoRows, ErrSnapshotCorrupt, ErrSnapshotNotFound, FileStore, SQLiteStore, Snapshot,
    HIGH_VOLUME_AUDIT_EVENT_TYPES, SNAPSHOT_KIND_AUTOMATIC, SNAPSHOT_KIND_MANUAL,
};
pub use validation::{ValidationError, ValidationErrors};

#[allow(unused_imports)]
pub(crate) use validation::{go_atoi, has_unsafe_control, parse_ipv4, valid_interface_name, valid_wireguard_key};
