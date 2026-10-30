//! Build metadata. Go injects these with `-ldflags -X`; the Rust build reads
//! them from the compile-time environment instead. Development builds keep
//! safe, explicit fallbacks instead of pretending to be a published release.

pub const VERSION: &str = match option_env!("MINIMALROUTER_VERSION") {
    Some(v) => v,
    None => "dev",
};

pub const COMMIT: &str = match option_env!("MINIMALROUTER_COMMIT") {
    Some(v) => v,
    None => "unknown",
};

pub const BUILD_DATE: &str = match option_env!("MINIMALROUTER_BUILD_DATE") {
    Some(v) => v,
    None => "unknown",
};

/// Stable human-facing version string. Release builds normally inject a
/// leading-v tag (for example v0.1.3), but a bare semantic version is accepted.
pub fn display_version() -> String {
    display(VERSION)
}

fn display(version: &str) -> String {
    let version = version.trim();
    if version.is_empty() || version == "dev" {
        return "dev".into();
    }
    if version.starts_with('v') {
        return version.into();
    }
    format!("v{}", version)
}

#[cfg(test)]
mod tests {
    #[test]
    fn display_version_normalises_prefix() {
        assert_eq!(super::display(""), "dev");
        assert_eq!(super::display(" dev "), "dev");
        assert_eq!(super::display("0.3.3"), "v0.3.3");
        assert_eq!(super::display("v0.3.3"), "v0.3.3");
    }
}
