//! Go-style error values: sentinel errors compared with `errors.Is`, and
//! `fmt.Errorf("...: %w", err)` wrapping whose message is the full Go string.

use std::fmt;

pub type Error = anyhow::Error;
pub type Result<T, E = Error> = std::result::Result<T, E>;

/// An error whose message is already formatted the Go way and which keeps the
/// wrapped error reachable for `is`.
#[derive(Debug)]
pub struct Wrapped {
    message: String,
    source: Error,
}

impl fmt::Display for Wrapped {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for Wrapped {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        Some(self.source.as_ref())
    }
}

/// `fmt.Errorf(format, ..., err)` with `%w`: `message` must already contain the
/// wrapped error's text where Go would print it.
pub fn wrap(source: impl Into<Error>, message: impl Into<String>) -> Error {
    Error::new(Wrapped { message: message.into(), source: source.into() })
}

/// `fmt.Errorf("%s: %w", prefix, err)`.
pub fn wrapf(prefix: impl fmt::Display, source: impl Into<Error>) -> Error {
    let source = source.into();
    let message = format!("{}: {}", prefix, source);
    wrap(source, message)
}

/// `errors.Is(err, target)` for sentinel types.
pub fn is<T: std::error::Error + Send + Sync + 'static>(err: &Error) -> bool {
    err.chain().any(|e| e.is::<T>())
}

/// `errors.As(err, &target)`.
pub fn as_ref<T: std::error::Error + Send + Sync + 'static>(err: &Error) -> Option<&T> {
    err.chain().find_map(|e| e.downcast_ref::<T>())
}

/// `errors.New(message)` as a plain error value.
pub fn new(message: impl Into<String>) -> Error {
    anyhow::Error::msg(message.into())
}

/// Declares a sentinel error type, the equivalent of
/// `var ErrX = errors.New("message")`.
#[macro_export]
macro_rules! sentinel {
    ($(#[$meta:meta])* $name:ident, $message:expr) => {
        $(#[$meta])*
        #[derive(Debug, Clone, Copy, PartialEq, Eq)]
        pub struct $name;
        impl ::std::fmt::Display for $name {
            fn fmt(&self, f: &mut ::std::fmt::Formatter<'_>) -> ::std::fmt::Result {
                f.write_str($message)
            }
        }
        impl ::std::error::Error for $name {}
    };
}

/// `fmt.Errorf` that returns an `anyhow::Error`.
#[macro_export]
macro_rules! errorf {
    ($($arg:tt)*) => { $crate::goish::errors::new(format!($($arg)*)) };
}

#[cfg(test)]
mod tests {
    use super::*;

    crate::sentinel!(ErrSample, "sample failure");

    #[test]
    fn wrapped_sentinels_remain_detectable() {
        let err = wrap(ErrSample, format!("{}: 93.0% used", ErrSample));
        assert_eq!(err.to_string(), "sample failure: 93.0% used");
        assert!(is::<ErrSample>(&err));
        let outer = wrapf("save", err);
        assert_eq!(outer.to_string(), "save: sample failure: 93.0% used");
        assert!(is::<ErrSample>(&outer));
        assert!(!is::<ErrSample>(&new("other")));
    }
}
