//! Read-only runtime telemetry and redacted diagnostics.

mod dhcp_leases;
mod diagnostics;
mod runtime;

pub use dhcp_leases::{current_dhcp_lease_snapshot, current_dhcp_leases, parse_dhcp_leases, DHCPLease, DHCPLeaseSnapshot, DHCPLeaseStatus};
pub use diagnostics::{build_diagnostic_bundle, redact_secrets, redacted_system_config, DiagnosticBundle};
pub use runtime::*;
