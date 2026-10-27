//! Read-only runtime telemetry and redacted diagnostics.

mod dhcp_leases;
mod diagnostics;
mod runtime;

pub use dhcp_leases::{DHCPLease, DHCPLeaseSnapshot, DHCPLeaseStatus, current_dhcp_lease_snapshot, current_dhcp_leases, parse_dhcp_leases};
pub use diagnostics::{DiagnosticBundle, build_diagnostic_bundle, redact_secrets, redacted_system_config};
pub use runtime::*;
