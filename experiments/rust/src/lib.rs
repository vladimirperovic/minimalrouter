//! Minimal Router OS management plane — Rust port of the Go implementation.
//!
//! Module layout mirrors the Go `internal/` packages one to one so the two
//! implementations can be compared package by package.

#![deny(unsafe_code)]

pub mod goish;

pub mod accounting;
pub mod api;
pub mod apply;
pub mod applyd;
pub mod auth;
pub mod auth_persistent;
pub mod buildinfo;
pub mod config;
pub mod dnsactivity;
pub mod dnsfilter;
pub mod dnsrisk;
pub mod faultinject;
pub mod firmware;
pub mod gateway;
pub mod health;
pub mod httpd;
pub mod kdf;
pub mod network;
pub mod proc;
pub mod recoverybackup;
pub mod release;
pub mod recovery;
pub mod runtimebudget;
pub mod services;
pub mod shutdown;
pub mod startup;
pub mod storage;
pub mod sys;
pub mod telemetry;
pub mod tlsutil;
