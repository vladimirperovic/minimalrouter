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
pub use backup::{BackupEncryption, BackupEnvelope, BackupPayload, decrypt_config_backup, encrypt_config_backup};
pub use model::*;
pub use offline::{OfflineConfig, validate_offline_candidate};
pub use pfsense::{PfSenseImportReport, PfSenseInterfaceMapping, import_pfsense_xml, import_pfsense_xml_with_mapping};
pub use policy::{equal_section, firewall_rule_scope, network_contains_network, requires_confirmation, validate_live_candidate};
pub use profiles::SUPPORTED_PROFILE_SERVICES;
pub use recovery_status::validate_snapshot_label;
pub use store::set_mode;
pub use store::{
    AuditEvent, ErrNoRows, ErrSnapshotCorrupt, ErrSnapshotNotFound, FileStore, HIGH_VOLUME_AUDIT_EVENT_TYPES, SNAPSHOT_KIND_AUTOMATIC, SNAPSHOT_KIND_MANUAL,
    SQLiteStore, Snapshot, is_high_volume_audit_event,
};
pub use validation::{ValidationError, ValidationErrors};

#[allow(unused_imports)]
pub use validation::go_atoi;
#[allow(unused_imports)]
pub(crate) use validation::{has_unsafe_control, parse_ipv4, valid_interface_name, valid_wireguard_key};
