//! Small compatibility layer that reproduces Go standard-library semantics the
//! router depends on. Keeping these rules in one place makes behavioural
//! differences between the Go and Rust implementations visible and testable.

pub mod decode;
pub mod errors;
pub mod json;
pub mod net;
pub mod rand;
pub mod strings;
pub mod time;
