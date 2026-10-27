//! Minimal Router OS management plane — Rust port of the Go implementation.
//!
//! Module layout mirrors the Go `internal/` packages one to one so the two
//! implementations can be compared package by package.

pub mod goish;

pub mod apply;
pub mod applyd;
pub mod auth;
pub mod buildinfo;
pub mod config;
pub mod dnsactivity;
pub mod dnsfilter;
pub mod faultinject;
pub mod gateway;
pub mod health;
pub mod kdf;
pub mod network;
pub mod recoverybackup;
pub mod recovery;
pub mod runtimebudget;
pub mod services;
pub mod startup;
pub mod storage;
pub mod telemetry;
pub mod tlsutil;
