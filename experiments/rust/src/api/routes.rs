//! Route registration. Every endpoint passes the security-header and
//! trusted-network gate; all but login and first-run setup also require an
//! authenticated session.

use std::sync::Arc;

use super::{Handler, Server};
use crate::httpd::Router;

/// Per-feature state owned by handler files.
#[derive(Default)]
pub struct Extensions {}

impl Server {
    fn route(self: &Arc<Self>, router: &mut Router, pattern: &str, authenticated: bool, handler: Handler) {
        let server = self.clone();
        router.handle_fn(pattern, move |r| server.gate(r, authenticated, handler));
    }

    /// Attaches every /api/v1 endpoint.
    pub fn register_routes(self: &Arc<Self>, router: &mut Router) {
        let public: [(&str, Handler); 1] = [("POST /api/v1/auth/login", Server::handle_login)];
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
        ];
        for (pattern, handler) in protected {
            self.route(router, pattern, true, handler);
        }
    }
}

impl Server {
    /// Placeholder until recovery_diagnostics.rs is ported.
    pub(crate) fn build_recovery_diagnostics(&self, _cfg: &crate::config::SystemConfig) -> crate::goish::errors::Result<Vec<u8>> {
        Err(crate::errorf!("not yet ported"))
    }
}
