//! Service generators, telemetry, the apply engine, DNS filtering and the
//! recovery backups. Re-exports the lower layers so modules keep their
//! `crate::config`-style paths.

#![deny(unsafe_code)]

pub use mr_config::*;

pub mod apply;
pub mod dnsactivity;
pub mod dnsfilter;
pub mod recovery;
pub mod recoverybackup;
pub mod services;
pub mod telemetry;
