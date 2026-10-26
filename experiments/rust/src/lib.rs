//! Minimal Router OS management plane — Rust port of the Go implementation.
//!
//! Module layout mirrors the Go `internal/` packages one to one so the two
//! implementations can be compared package by package.

pub mod goish;

pub mod buildinfo;
pub mod config;
pub mod kdf;
pub mod network;
pub mod runtimebudget;
pub mod services;
pub mod storage;
