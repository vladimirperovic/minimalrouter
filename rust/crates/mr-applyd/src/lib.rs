//! `router-applyd`, the narrowly allowlisted privileged helper. Kept in its own
//! crate so it builds in parallel with the management API and cannot reach
//! into it.

#![deny(unsafe_code)]

pub use mr_services::*;

pub mod applyd;
