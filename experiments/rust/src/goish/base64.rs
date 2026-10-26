//! `encoding/base64` decoding rules: Go's decoders skip `\r` and `\n` and do
//! not reject non-zero unused trailing bits (non-strict mode).

use base64::alphabet;
use base64::engine::general_purpose::{GeneralPurpose, GeneralPurposeConfig};
use base64::engine::DecodePaddingMode;
use base64::Engine;

const STD: GeneralPurpose = GeneralPurpose::new(
    &alphabet::STANDARD,
    GeneralPurposeConfig::new().with_decode_allow_trailing_bits(true).with_decode_padding_mode(DecodePaddingMode::RequireCanonical),
);
const RAW_STD: GeneralPurpose = GeneralPurpose::new(
    &alphabet::STANDARD,
    GeneralPurposeConfig::new()
        .with_encode_padding(false)
        .with_decode_allow_trailing_bits(true)
        .with_decode_padding_mode(DecodePaddingMode::RequireNone),
);

fn strip_newlines(text: &str) -> std::borrow::Cow<'_, str> {
    if text.contains(['\r', '\n']) { text.replace(['\r', '\n'], "").into() } else { text.into() }
}

/// `base64.StdEncoding.DecodeString`.
pub fn std_decode(text: &str) -> Option<Vec<u8>> {
    STD.decode(strip_newlines(text).as_bytes()).ok()
}

/// `base64.RawStdEncoding.DecodeString`.
pub fn raw_std_decode(text: &str) -> Option<Vec<u8>> {
    RAW_STD.decode(strip_newlines(text).as_bytes()).ok()
}

#[cfg(test)]
mod tests {
    #[test]
    fn accepts_what_go_accepts() {
        assert_eq!(super::std_decode("Zm9v\nYmFy").unwrap(), b"foobar");
        assert_eq!(super::std_decode("Zh==").unwrap(), b"f");
        assert!(super::std_decode("Zg").is_none());
        assert_eq!(super::raw_std_decode("Zg").unwrap(), b"f");
        assert!(super::raw_std_decode("Zg==").is_none());
    }
}
