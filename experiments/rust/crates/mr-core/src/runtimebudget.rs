//! The appliance's bounded startup contract. Keep this module dependency-free:
//! bootstrap updates must not accidentally depend on management-plane details.

use crate::goish::time::Duration;

pub const RECONCILE: Duration = Duration::secs(270);
pub const STARTUP: Duration = Duration(RECONCILE.0 + Duration::secs(30).0);
pub const SERVICE_COMMAND: Duration = Duration(STARTUP.0 + Duration::secs(30).0);
