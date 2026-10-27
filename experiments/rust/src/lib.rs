//! Minimal Router OS management plane — Rust port of the Go implementation.
//!
//! Module layout mirrors the Go `internal/` packages one to one so the two
//! implementations can be compared package by package. The code lives in the
//! layered crates under `crates/` (`mr-core` ← `mr-config` ← `mr-services` ←
//! `mr-api` and `mr-applyd`); this facade re-exports them under the original
//! paths so the binaries and the integration tests are unchanged.

#![deny(unsafe_code)]

pub use mr_api::*;
pub use mr_applyd::*;
