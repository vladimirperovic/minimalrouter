//! A `log` backend in the Go standard logger format:
//! `2006/01/02 15:04:05 message` on stderr.
//!
//! One log call is always exactly one line: messages routinely embed request
//! paths and other client-controlled text, so control characters are escaped
//! (a decoded `%0a` cannot forge a second log line) and the length is capped.

use std::io::Write;

/// The longest message written, in bytes, before the truncation marker.
const MAX_MESSAGE: usize = 2048;
const TRUNCATED: &str = "...[truncated]";

struct GoLogger;

/// Escapes every control character visibly and truncates an over-long message
/// at a character boundary. Ordinary Unicode is left alone.
fn sanitize(message: &str) -> String {
    let mut out = String::with_capacity(message.len().min(MAX_MESSAGE + TRUNCATED.len()));
    for ch in message.chars() {
        let escaped = match ch {
            '\n' => "\\n".to_string(),
            '\r' => "\\r".to_string(),
            '\t' => "\\t".to_string(),
            // U+2028/U+2029 are not Unicode control characters but many
            // viewers break lines on them.
            c if c.is_control() || c == '\u{2028}' || c == '\u{2029}' => {
                if (c as u32) < 0x100 {
                    format!("\\x{:02x}", c as u32)
                } else {
                    format!("\\u{{{:x}}}", c as u32)
                }
            }
            c => c.to_string(),
        };
        if out.len() + escaped.len() > MAX_MESSAGE {
            out.push_str(TRUNCATED);
            break;
        }
        out.push_str(&escaped);
    }
    out
}

impl log::Log for GoLogger {
    fn enabled(&self, metadata: &log::Metadata<'_>) -> bool {
        metadata.level() <= log::Level::Info
    }

    fn log(&self, record: &log::Record<'_>) {
        if !self.enabled(record.metadata()) {
            return;
        }
        let now = chrono::Local::now().format("%Y/%m/%d %H:%M:%S");
        let _ = writeln!(std::io::stderr().lock(), "{} {}", now, sanitize(&record.args().to_string()));
    }

    fn flush(&self) {}
}

static LOGGER: GoLogger = GoLogger;

pub fn init() {
    if log::set_logger(&LOGGER).is_ok() {
        log::set_max_level(log::LevelFilter::Info);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normal_messages_are_unchanged() {
        let message = "[API] GET /api/v1/config from 192.168.1.5 \u{e9}\u{4e2d}\u{1f600}";
        assert_eq!(sanitize(message), message);
    }

    #[test]
    fn control_characters_cannot_forge_lines() {
        let forged = "[AUTH] Unauthorized request to /x\n2026/11/14 12:00:00 [AUTH] Successful login from 1.2.3.4\r\x1b[2J\u{85}\u{2028}\u{7f}";
        let got = sanitize(forged);
        assert!(!got.chars().any(|c| c.is_control() || c == '\u{2028}'), "{got:?}");
        assert_eq!(got, "[AUTH] Unauthorized request to /x\\n2026/11/14 12:00:00 [AUTH] Successful login from 1.2.3.4\\r\\x1b[2J\\x85\\u{2028}\\x7f");
    }

    #[test]
    fn long_messages_are_cut_at_a_character_boundary() {
        let got = sanitize(&"a".repeat(MAX_MESSAGE * 4));
        assert_eq!(got, format!("{}{}", "a".repeat(MAX_MESSAGE), TRUNCATED));

        // A three-byte character straddling the cap is dropped whole.
        let straddling = format!("{}\u{4e2d}", "a".repeat(MAX_MESSAGE - 1));
        assert_eq!(sanitize(&straddling), format!("{}{}", "a".repeat(MAX_MESSAGE - 1), TRUNCATED));
        // Exactly at the cap nothing is cut.
        let exact = "a".repeat(MAX_MESSAGE);
        assert_eq!(sanitize(&exact), exact);
    }

    #[test]
    fn escapes_count_towards_the_cap() {
        let got = sanitize(&"\n".repeat(MAX_MESSAGE));
        assert_eq!(got.len(), MAX_MESSAGE + TRUNCATED.len());
        assert!(got.ends_with(TRUNCATED) && !got.contains('\n'));
    }
}
