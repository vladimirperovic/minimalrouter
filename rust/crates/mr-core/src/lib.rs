//! Runtime primitives shared by every other crate: the Go-compatibility layer,
//! the audited libc calls, the process runner, shutdown handling, key
//! derivation, storage helpers and firmware verification.

#![deny(unsafe_code)]

pub mod goish;

pub mod buildinfo;
pub mod faultinject;
pub mod firmware;
pub mod kdf;
pub mod network;
pub mod proc;
pub mod runtimebudget;
pub mod shutdown;
pub mod storage;
pub mod sys;
