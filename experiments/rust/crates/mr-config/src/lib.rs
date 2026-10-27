//! Configuration model and store, authentication, TLS material, startup state
//! and the HTTP server. Re-exports `mr-core` so modules keep their
//! `crate::goish`-style paths.

#![deny(unsafe_code)]

pub use mr_core::*;

pub mod auth;
pub mod auth_persistent;
pub mod config;
pub mod httpd;
pub mod startup;
pub mod tlsutil;
