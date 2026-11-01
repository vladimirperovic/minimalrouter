#![no_main]
use libfuzzer_sys::fuzz_target;
use mr_config::config::SystemConfig;
fuzz_target!(|data: &[u8]| {
    if data.len() > 64 * 1024 {
        return;
    }
    if let Ok(config) = serde_json::from_slice::<SystemConfig>(data) {
        let _ = config.validate();
        let encoded = serde_json::to_vec(&config).expect("parsed configuration must serialize");
        let decoded: SystemConfig = serde_json::from_slice(&encoded).expect("serialized configuration must decode");
        assert_eq!(serde_json::to_vec(&decoded).unwrap(), encoded, "canonical configuration must be stable");
    }
});
