//! Route registration. Every endpoint passes the security-header and
//! trusted-network gate; all but login and first-run setup also require an
//! authenticated session.

use std::sync::Arc;

use super::{Handler, Server};
use crate::httpd::Router;

/// Per-feature state owned by handler files.
#[derive(Default)]
pub struct Extensions {
    pub(crate) firewall: std::sync::Mutex<Option<super::observability::FirewallObservation>>,
    pub(crate) gateway_recovery: std::sync::Mutex<Option<super::gateway::GatewayRecoveryStatus>>,
    pub(crate) gateway_recovery_stop: std::sync::Mutex<Option<crate::shutdown::Shutdown>>,
    pub(crate) updates: super::update::UpdateServiceSlot,
    /// Lets handlers start work that outlives the request.
    pub(crate) self_ref: std::sync::OnceLock<std::sync::Weak<Server>>,
}

impl Server {
    fn route(self: &Arc<Self>, router: &mut Router, pattern: &str, authenticated: bool, handler: Handler) {
        let server = self.clone();
        router.handle_fn(pattern, move |r| server.gate(r, authenticated, handler));
    }

    /// Attaches every /api/v1 endpoint.
    pub fn register_routes(self: &Arc<Self>, router: &mut Router) {
        let _ = self.ext.self_ref.set(Arc::downgrade(self));
        let public: Vec<(&str, Handler)> = vec![
            ("POST /api/v1/auth/login", Server::handle_login),
            ("GET /api/v1/setup/interfaces", Server::handle_discover_setup_interfaces),
            ("GET /api/v1/setup/status", Server::handle_setup_status),
            // First-run only; the handler guards itself.
            ("POST /api/v1/setup/apply", Server::handle_setup_apply),
        ];
        for (pattern, handler) in public {
            self.route(router, pattern, false, handler);
        }
        let protected: Vec<(&str, Handler)> = vec![
            ("POST /api/v1/auth/logout", Server::handle_logout),
            ("GET /api/v1/auth/session", Server::handle_get_session),
            ("POST /api/v1/auth/change-password", Server::handle_change_password),
            ("POST /api/v1/auth/totp/enable", Server::handle_totp_enable),
            ("POST /api/v1/auth/totp/enroll", Server::handle_totp_qr),
            ("POST /api/v1/auth/totp/disable", Server::handle_totp_disable),
            ("GET /api/v1/system", Server::handle_get_system),
            ("GET /api/v1/system/diagnostics", Server::handle_get_diagnostics),
            ("GET /api/v1/audit/events", Server::handle_get_audit_events),
            ("GET /api/v1/config", Server::handle_get_config),
            ("PUT /api/v1/config", Server::handle_update_config),
            ("GET /api/v1/transactions/pending", Server::handle_get_pending_transaction),
            ("POST /api/v1/transactions/{id}/confirm", Server::handle_confirm_transaction),
            ("POST /api/v1/network/wol", Server::handle_wake_on_lan),
            ("POST /api/v1/recovery/reconcile", Server::handle_recovery_reconcile),
            ("GET /api/v1/recovery/status", Server::handle_recovery_status),
            ("POST /api/v1/recovery/operations/{id}/dns", Server::handle_recovery_dns),
            ("POST /api/v1/recovery/operations/{id}/dismiss", Server::handle_recovery_dismiss),
            ("GET /api/v1/snapshots", Server::handle_get_snapshots),
            ("POST /api/v1/snapshots", Server::handle_create_snapshot),
            ("DELETE /api/v1/snapshots/{id}", Server::handle_delete_snapshot),
            ("POST /api/v1/snapshots/{id}/restore", Server::handle_restore_snapshot),
            ("GET /api/v1/snapshots/{id}/preview", Server::handle_snapshot_preview),
            ("POST /api/v1/import/pfsense/preview", Server::handle_pfsense_import_preview),
            ("POST /api/v1/import/pfsense/{id}/apply", Server::handle_pfsense_import_apply),
            ("POST /api/v1/backup/export", Server::handle_backup_export),
            ("POST /api/v1/backup/import/preview", Server::handle_backup_import_preview),
            ("POST /api/v1/import/backup/{id}/apply", Server::handle_backup_import_apply),
            ("POST /api/v1/firmware/verify", Server::handle_firmware_verify),
            ("GET /api/v1/system/interfaces", Server::handle_discover_interfaces),
            ("GET /api/v1/network/lan", Server::handle_lan_status),
            ("GET /api/v1/network/devices", Server::handle_network_devices),
            ("GET /api/v1/network/leases", Server::handle_dhcp_leases),
            ("GET /api/v1/network/address-check", Server::handle_lan_address_check),
            // Gateway, devices and service recovery.
            ("GET /api/v1/gateway/summary", Server::handle_get_gateway_summary),
            ("GET /api/v1/gateway/history", Server::handle_get_gateway_history),
            ("GET /api/v1/gateway/insights", Server::handle_get_gateway_insights),
            ("POST /api/v1/gateway/probe", Server::handle_gateway_probe),
            ("GET /api/v1/gateway/events", Server::handle_gateway_events),
            ("GET /api/v1/gateway/recovery", Server::handle_gateway_recovery_status),
            ("GET /api/v1/gateway/settings", Server::handle_get_gateway_settings),
            ("PUT /api/v1/gateway/settings", Server::handle_put_gateway_settings),
            ("POST /api/v1/gateway/diagnose", Server::handle_network_diagnose),
            ("POST /api/v1/system/actions/{action}", Server::handle_service_action),
            ("GET /api/v1/devices/pauses", Server::handle_get_device_pauses),
            ("POST /api/v1/devices/pause", Server::handle_pause_device),
            ("POST /api/v1/devices/resume", Server::handle_resume_device),
            ("POST /api/v1/config/preview", Server::handle_config_preview),
            // Health, startup, firewall and accounting.
            ("GET /api/v1/health", Server::handle_get_health),
            ("GET /api/v1/startup/boots", Server::handle_startup_boots),
            ("GET /api/v1/startup/boots/{id}", Server::handle_startup_boot),
            ("GET /api/v1/firewall/status", Server::handle_firewall_status),
            ("GET /api/v1/firewall/explain", Server::handle_firewall_explain),
            ("GET /api/v1/firewall/activity", Server::handle_firewall_activity),
            ("GET /api/v1/accounting/insights", Server::handle_accounting_insights),
            ("GET /api/v1/accounting", Server::handle_get_accounting),
            ("POST /api/v1/wireguard/peers", Server::handle_provision_wireguard_peer),
            ("POST /api/v1/wireguard/peers/{id}/configuration", Server::handle_reissue_wireguard_peer_configuration),
            ("DELETE /api/v1/wireguard/peers/{id}", Server::handle_delete_wireguard_peer),
            ("GET /api/v1/wireguard/provisioning-preview", Server::handle_wireguard_provisioning_preview),
            ("POST /api/v1/wireguard/client/keys", Server::handle_wireguard_client_keys),
            ("POST /api/v1/qos/speedtest", Server::handle_speedtest),
            // DNS activity, risk alerts and filtering.
            ("GET /api/v1/dns-activity", Server::handle_dns_activity),
            ("GET /api/v1/dns-activity/settings", Server::handle_get_dns_activity_settings),
            ("PUT /api/v1/dns-activity/settings", Server::handle_put_dns_activity_settings),
            ("GET /api/v1/dns-activity/recent", Server::handle_dns_activity_recent),
            ("POST /api/v1/dns-activity/clear", Server::handle_dns_activity_clear),
            ("GET /api/v1/dns-activity/alerts/summary", Server::handle_dns_risk_summary),
            ("GET /api/v1/dns-activity/alerts", Server::handle_dns_risk_alerts),
            ("POST /api/v1/dns-activity/alerts/{id}/acknowledge", Server::handle_dns_risk_acknowledge),
            ("POST /api/v1/dns-activity/alerts/{id}/ignore", Server::handle_dns_risk_ignore),
            ("GET /api/v1/dns-activity/alerts/exceptions", Server::handle_dns_risk_exceptions),
            ("DELETE /api/v1/dns-activity/alerts/exceptions/{id}", Server::handle_dns_risk_remove_exception),
            ("POST /api/v1/dns-activity/alerts/refresh", Server::handle_dns_risk_refresh),
            ("GET /api/v1/dns-filter", Server::handle_dns_filter_status),
            ("PUT /api/v1/dns-filter", Server::handle_dns_filter_save),
            ("POST /api/v1/dns-filter/refresh", Server::handle_dns_filter_refresh),
            ("POST /api/v1/dns-filter/check", Server::handle_dns_filter_check),
            ("GET /api/v1/dns-filter/check", Server::handle_dns_filter_check),
            ("GET /api/v1/dns-filter/profiles", Server::handle_dns_filter_profiles),
            ("GET /api/v1/dns-filter/operations", Server::handle_dns_filter_operations),
            // Signed firmware updates.
            ("GET /api/v1/firmware/status", Server::handle_firmware_status),
            ("POST /api/v1/firmware/check", Server::handle_firmware_check),
            ("POST /api/v1/firmware/channel", Server::handle_firmware_channel),
            ("POST /api/v1/firmware/update", Server::handle_firmware_update),
            ("POST /api/v1/firmware/upload", Server::handle_firmware_upload),
            ("GET /api/v1/firmware/local-status", Server::handle_firmware_local_status),
        ];
        for (pattern, handler) in protected {
            self.route(router, pattern, true, handler);
        }
    }
}
