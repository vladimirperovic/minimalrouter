#![no_main]
use libfuzzer_sys::fuzz_target;
fuzz_target!(|data: &[u8]| {
    mr_config::httpd::fuzz_http_connection(data);
});
