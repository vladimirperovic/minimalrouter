//! A `log` backend in the Go standard logger format:
//! `2006/01/02 15:04:05 message` on stderr.

use std::io::Write;

struct GoLogger;

impl log::Log for GoLogger {
    fn enabled(&self, metadata: &log::Metadata<'_>) -> bool {
        metadata.level() <= log::Level::Info
    }

    fn log(&self, record: &log::Record<'_>) {
        if !self.enabled(record.metadata()) {
            return;
        }
        let now = chrono::Local::now().format("%Y/%m/%d %H:%M:%S");
        let _ = writeln!(std::io::stderr().lock(), "{} {}", now, record.args());
    }

    fn flush(&self) {}
}

static LOGGER: GoLogger = GoLogger;

pub fn init() {
    if log::set_logger(&LOGGER).is_ok() {
        log::set_max_level(log::LevelFilter::Info);
    }
}
