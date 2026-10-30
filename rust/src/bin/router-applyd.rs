//! Privileged helper entry point; see `minimalrouter::applyd`.

#![forbid(unsafe_code)]
fn main() {
    minimalrouter::applyd::main();
}
