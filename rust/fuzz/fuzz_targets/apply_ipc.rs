#![no_main]
use libfuzzer_sys::fuzz_target;
use mr_services::apply::decode_single;
use mr_services::apply::{ApplyRequest, ApplyResponse};
fuzz_target!(|data: &[u8]| {
    if data.len() > 64 * 1024 {
        return;
    }
    if let Ok(request) = decode_single::<ApplyRequest>(data) {
        let _ = request.config.validate();
        let mut trailing = data.to_vec();
        trailing.extend_from_slice(b"\n{}");
        assert!(decode_single::<ApplyRequest>(&trailing).is_err(), "IPC must contain exactly one document");
    }
    if let Ok(response) = decode_single::<ApplyResponse>(data) {
        if response.validate().is_ok() {
            assert!(!response.success || (response.verified && !response.rolled_back && !response.recovery_required));
            assert!(!response.rolled_back || !response.recovery_required);
        }
    }
});
