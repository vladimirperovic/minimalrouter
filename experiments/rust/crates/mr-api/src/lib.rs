//! The management API and the features behind it: gateway monitoring, health,
//! accounting, DNS risk feeds and release handling.

#![deny(unsafe_code)]

pub use mr_services::*;

pub mod accounting;
pub mod api;
pub mod dnsrisk;
pub mod gateway;
pub mod health;
pub mod release;
