//! Signed firmware manifests, appliance payload policy and the A/B slot
//! manager. Verification always uses a key pinned in the installed operating
//! system, never the manifest-supplied public key.

mod fsutil;
mod slots;

pub use slots::{inspect_local_payload, SlotManager, SlotState};

use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use regex::Regex;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::sync::LazyLock;
use subtle::ConstantTimeEq;

use crate::errorf;
use crate::goish::errors::{wrapf, Error, Result};
use crate::goish::filepath;

pub const PUBLIC_KEY_SIZE: usize = 32;
pub const PRIVATE_KEY_SIZE: usize = 64;
const SIGNATURE_SIZE: usize = 64;

crate::sentinel!(ErrInvalidSignature, "invalid firmware signature");
crate::sentinel!(ErrInvalidPublicKey, "invalid trusted public key (must be 32 bytes Ed25519)");
crate::sentinel!(ErrInvalidSignatureFormat, "invalid signature format (must be 64 bytes Ed25519)");
crate::sentinel!(ErrUntrustedPublicKey, "manifest signer does not match the pinned firmware key");

pub(crate) static RELEASE_VERSION_PATTERN: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$").unwrap());

/// The signed firmware manifest. `files` maps a relative path to its SHA-256.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FirmwareManifest {
    pub version: String,
    pub build_date: String,
    pub git_commit: String,
    pub files: Option<BTreeMap<String, String>>,
    /// Hex-encoded Ed25519 signature.
    pub signature: String,
    /// Informational only; verification never uses this value.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub public_key: String,
}

static NO_FILES: BTreeMap<String, String> = BTreeMap::new();

impl FirmwareManifest {
    pub fn files(&self) -> &BTreeMap<String, String> {
        self.files.as_ref().unwrap_or(&NO_FILES)
    }

    pub fn files_mut(&mut self) -> &mut BTreeMap<String, String> {
        self.files.get_or_insert_with(BTreeMap::new)
    }
}

/// The canonical payload: Go's json.Marshal of the four signed fields, map
/// keys sorted and HTML characters escaped.
fn signed_payload(manifest: &FirmwareManifest) -> Result<Vec<u8>> {
    #[derive(Serialize)]
    struct Payload<'a> {
        version: &'a str,
        build_date: &'a str,
        git_commit: &'a str,
        files: &'a Option<BTreeMap<String, String>>,
    }
    Ok(crate::goish::json::marshal(&Payload { version: &manifest.version, build_date: &manifest.build_date, git_commit: &manifest.git_commit, files: &manifest.files })?)
}

/// A new Ed25519 key pair: 32-byte public key, Go-format 64-byte private key
/// (seed followed by public key).
pub fn generate_key_pair() -> Result<(Vec<u8>, Vec<u8>)> {
    let mut seed = [0u8; 32];
    crate::goish::rand::fill(&mut seed).map_err(|e| errorf!("{}", e))?;
    let key = SigningKey::from_bytes(&seed);
    Ok((key.verifying_key().to_bytes().to_vec(), key.to_keypair_bytes().to_vec()))
}

fn signing_key(private_key: &[u8]) -> Result<SigningKey> {
    let bytes: &[u8; PRIVATE_KEY_SIZE] = private_key.try_into().map_err(|_| errorf!("ed25519: bad private key length: {}", private_key.len()))?;
    // Go derives the key from the seed half; the trailing public half is not
    // re-validated by ed25519.Sign either.
    let seed: &[u8; 32] = bytes[..32].try_into().expect("32 bytes");
    Ok(SigningKey::from_bytes(seed))
}

/// Signs the canonical payload after callers set release metadata.
pub fn sign_manifest(manifest: &mut FirmwareManifest, private_key: &[u8]) -> Result<()> {
    let key = signing_key(private_key)?;
    let payload = signed_payload(manifest)?;
    manifest.signature = hex::encode(key.sign(&payload).to_bytes());
    manifest.public_key = hex::encode(key.verifying_key().to_bytes());
    Ok(())
}

/// Signs every regular file of a firmware directory.
pub fn sign_firmware(firmware_dir: &str, private_key: &[u8]) -> Result<FirmwareManifest> {
    let mut files = BTreeMap::new();
    fsutil::walk_files(firmware_dir, &mut |path, relative, regular| {
        if !regular {
            return Err(errorf!("firmware contains non-regular file: {}", path));
        }
        files.insert(relative.to_string(), fsutil::sha256_file(path)?);
        Ok(())
    })?;
    let mut manifest = FirmwareManifest { version: "1.0.0".into(), build_date: "2024-01-01T00:00:00Z".into(), git_commit: "unknown".into(), files: Some(files), ..Default::default() };
    sign_manifest(&mut manifest, private_key)?;
    Ok(manifest)
}

/// Verifies signed metadata against an operating-system-pinned trust anchor.
pub fn verify_manifest(manifest: Option<&FirmwareManifest>, trusted_key: &[u8]) -> Result<()> {
    let trusted: &[u8; PUBLIC_KEY_SIZE] = trusted_key.try_into().map_err(|_| Error::new(ErrInvalidPublicKey))?;
    let Some(manifest) = manifest.filter(|m| !m.version.is_empty() && !m.files().is_empty()) else {
        return Err(errorf!("incomplete firmware manifest"));
    };
    if !manifest.public_key.is_empty() {
        let claimed = hex::decode(&manifest.public_key).ok().filter(|c| c.len() == PUBLIC_KEY_SIZE);
        if !claimed.is_some_and(|c| bool::from(c.ct_eq(trusted_key))) {
            return Err(ErrUntrustedPublicKey.into());
        }
    }
    let signature = hex::decode(&manifest.signature).ok().and_then(|s| <[u8; SIGNATURE_SIZE]>::try_from(s.as_slice()).ok()).ok_or_else(|| Error::new(ErrInvalidSignatureFormat))?;
    let payload = signed_payload(manifest)?;
    let key = VerifyingKey::from_bytes(trusted).map_err(|_| Error::new(ErrInvalidSignature))?;
    key.verify(&payload, &Signature::from_bytes(&signature)).map_err(|_| Error::new(ErrInvalidSignature))
}

/// Rejects a correctly signed but incomplete update: an appliance release
/// must carry the reviewed system integration files and exactly one complete
/// architecture binary set.
pub fn validate_appliance_payload(manifest: Option<&FirmwareManifest>) -> Result<()> {
    let manifest = manifest.ok_or_else(|| errorf!("missing appliance manifest"))?;
    let files = manifest.files();
    for role in APPLIANCE_FILE_ROLES.iter().filter(|r| r.required && !r.architecture_binary) {
        if !files.contains_key(role.path) {
            return Err(errorf!("incomplete appliance payload: missing {}", role.path));
        }
    }
    let mut complete_architectures = 0;
    for arch in SUPPORTED_ARCHITECTURES {
        let roles = appliance_file_roles(arch)?;
        let binaries: Vec<_> = roles.iter().filter(|r| r.required && r.architecture_binary).collect();
        let present = binaries.iter().any(|r| files.contains_key(&r.path));
        let complete = binaries.iter().all(|r| files.contains_key(&r.path));
        if present && !complete {
            return Err(errorf!("incomplete appliance payload: architecture binary set is partial"));
        }
        if complete {
            complete_architectures += 1;
        }
    }
    if complete_architectures != 1 {
        return Err(errorf!("incomplete appliance payload: exactly one supported architecture binary set is required"));
    }
    Ok(())
}

/// Verifies signed metadata and every regular file in a fixed,
/// already-extracted staging directory.
pub fn verify_firmware(firmware_dir: &str, manifest: Option<&FirmwareManifest>, trusted_key: &[u8]) -> Result<()> {
    verify_manifest(manifest, trusted_key)?;
    for (relative, expected_hash) in manifest.map(|m| m.files()).unwrap_or(&NO_FILES) {
        let clean = filepath::clean(relative);
        if clean == "." || filepath::is_abs(&clean) || &clean != relative || clean.starts_with("../") {
            return Err(errorf!("unsafe firmware path: {:?}", relative));
        }
        let expected = hex::decode(expected_hash).ok().filter(|h| h.len() == 32).ok_or_else(|| errorf!("invalid hash for {}", relative))?;
        let full = fsutil::join(firmware_dir, &clean);
        if !std::fs::symlink_metadata(&full).is_ok_and(|m| m.file_type().is_file()) {
            return Err(errorf!("file missing or unsafe: {}", relative));
        }
        let actual = fsutil::sha256_bytes(&full).map_err(|_| errorf!("file missing: {}", relative))?;
        if !bool::from(actual.as_slice().ct_eq(&expected)) {
            return Err(errorf!("hash mismatch for {}", relative));
        }
    }
    Ok(())
}

/// Reads a hex-encoded Ed25519 key from a root-controlled OS file.
pub fn load_trusted_public_key(path: &str) -> Result<Vec<u8>> {
    let data = std::fs::read_to_string(path)?;
    hex::decode(data.trim()).ok().filter(|k| k.len() == PUBLIC_KEY_SIZE).ok_or_else(|| Error::new(ErrInvalidPublicKey))
}

pub fn load_manifest(path: &str) -> Result<FirmwareManifest> {
    let data = std::fs::read(path)?;
    Ok(crate::goish::json::unmarshal(&data)?)
}

pub fn save_manifest(manifest: &FirmwareManifest, path: &str) -> Result<()> {
    let data = crate::goish::json::marshal_indent(manifest, "  ")?;
    fsutil::write_file(path, &data, 0o644)?;
    Ok(())
}

/// Compiled policy, never manifest-controlled root routing. `system_path` is
/// the stricter installed integration/rollback check.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ApplianceFileRole {
    pub path: String,
    pub system_path: String,
    pub mode: u32,
    pub required: bool,
    pub architecture_binary: bool,
    pub bootstrap: bool,
}

struct StaticRole {
    path: &'static str,
    system_path: &'static str,
    mode: u32,
    required: bool,
    architecture_binary: bool,
    bootstrap: bool,
}

const fn role(path: &'static str, system_path: &'static str, mode: u32, architecture_binary: bool, bootstrap: bool) -> StaticRole {
    StaticRole { path, system_path, mode, required: true, architecture_binary, bootstrap }
}

pub(crate) const SUPPORTED_ARCHITECTURES: [&str; 2] = ["amd64", "arm64"];

static APPLIANCE_FILE_ROLES: [StaticRole; 20] = [
    role("web/dist/index.html", "", 0o644, false, false),
    role("compatibility.json", "/etc/minimalrouter/compatibility.json", 0o644, false, false),
    role("slot-exec", "/usr/libexec/minimalrouter/slot-exec", 0o755, false, false),
    role("firstboot", "/usr/libexec/minimalrouter/firstboot", 0o755, false, false),
    role("init.d/minimalrouter-firstboot", "/etc/init.d/minimalrouter-firstboot", 0o755, false, false),
    role("firstboot-ready", "/usr/libexec/minimalrouter/firstboot-ready", 0o755, false, false),
    role("install.sh", "", 0o755, false, false),
    role("install-core.sh", "", 0o755, false, false),
    role("init.d/routerd", "/etc/init.d/routerd", 0o755, false, false),
    role("init.d/router-applyd", "/etc/init.d/router-applyd", 0o755, false, false),
    role("init.d/pppoe-wan", "/etc/init.d/pppoe-wan", 0o755, false, false),
    role("sysctl/99-minimalrouter.conf", "/etc/sysctl.d/99-minimalrouter.conf", 0o644, false, false),
    role("modules/minimalrouter.conf", "/etc/modules-load.d/minimalrouter.conf", 0o644, false, false),
    role("logrotate/minimalrouter", "/etc/logrotate.d/minimalrouter", 0o644, false, false),
    role("ip-up.d-minimalrouter-qos", "/etc/ppp/ip-up.d/minimalrouter-qos", 0o755, false, false),
    role("bin/routerd-{arch}", "", 0o755, true, false),
    role("bin/router-applyd-{arch}", "", 0o755, true, false),
    // Recovery self-execs its database worker after permanently dropping to
    // routerd with no supplementary groups.
    role("bin/router-recovery-{arch}", "/usr/libexec/minimalrouter/bootstrap/bin/router-recovery-{arch}", 0o755, true, true),
    role("bin/router-update-{arch}", "/usr/libexec/minimalrouter/bootstrap/bin/router-update-{arch}", 0o750, true, true),
    role("bin/router-setup-{arch}", "/usr/sbin/router-setup", 0o755, true, true),
];

/// An independent resolved copy of the static policy.
pub fn appliance_file_roles(arch: &str) -> Result<Vec<ApplianceFileRole>> {
    if arch != "amd64" && arch != "arm64" {
        return Err(errorf!("unsupported update architecture {:?}", arch));
    }
    Ok(APPLIANCE_FILE_ROLES
        .iter()
        .map(|r| ApplianceFileRole {
            path: r.path.replace("{arch}", arch),
            system_path: r.system_path.replace("{arch}", arch),
            mode: r.mode,
            required: r.required,
            architecture_binary: r.architecture_binary,
            bootstrap: r.bootstrap,
        })
        .collect())
}

/// The installed role contract, independent of archive ownership and umask.
pub fn appliance_file_mode(path: &str) -> u32 {
    for arch in SUPPORTED_ARCHITECTURES {
        if let Some(role) = APPLIANCE_FILE_ROLES.iter().find(|r| path == r.path.replace("{arch}", arch)) {
            return role.mode;
        }
    }
    if filepath::dir(path) == "bin" || path.starts_with("init.d/") { 0o755 } else { 0o644 }
}

/// Runtime access that a content digest cannot establish: both daemons and
/// all served assets must be usable by their runtime accounts.
pub fn validate_appliance_file_modes(root: &str, manifest: Option<&FirmwareManifest>) -> Result<()> {
    validate_appliance_payload(manifest)?;
    for relative in manifest.map(|m| m.files()).unwrap_or(&NO_FILES).keys() {
        let mode = std::fs::symlink_metadata(fsutil::join(root, relative)).ok().filter(|m| m.file_type().is_file()).map(|m| fsutil::mode_bits(&m));
        let Some(mode) = mode else {
            return Err(errorf!("appliance executable is missing or unsafe: {}", relative));
        };
        let required = appliance_file_mode(relative) & 0o555;
        if mode & 0o777 & required != required || mode & 0o022 != 0 || mode & 0o7000 != 0 {
            return Err(errorf!("appliance file has unsafe runtime permissions: {}", relative));
        }
    }
    Ok(())
}

/// Go's runtime.GOARCH for the host.
pub fn host_arch() -> &'static str {
    match std::env::consts::ARCH {
        "x86_64" => "amd64",
        "aarch64" => "arm64",
        other => other,
    }
}

/// The single pre-stage validation boundary: signed paths and contents
/// first, then the layout and host-architecture contracts.
pub fn validate_release_candidate(root: &str, manifest: Option<&FirmwareManifest>, trusted_key: &[u8]) -> Result<()> {
    validate_release_candidate_for_arch(root, manifest, trusted_key, host_arch())
}

pub fn validate_release_candidate_for_arch(root: &str, manifest: Option<&FirmwareManifest>, trusted_key: &[u8], arch: &str) -> Result<()> {
    verify_firmware(root, manifest, trusted_key).map_err(|e| wrapf("verify signed release", e))?;
    validate_appliance_file_modes(root, manifest).map_err(|e| wrapf("validate appliance layout", e))?;
    validate_appliance_architecture(manifest, arch).map_err(|e| wrapf("validate appliance architecture", e))?;
    Ok(())
}

/// The one complete binary set in the signed payload matches the appliance.
pub fn validate_appliance_architecture(manifest: Option<&FirmwareManifest>, arch: &str) -> Result<()> {
    let manifest = manifest.ok_or_else(|| errorf!("missing appliance manifest"))?;
    for role in appliance_file_roles(arch)?.iter().filter(|r| r.architecture_binary && r.required) {
        if !manifest.files().contains_key(&role.path) {
            return Err(errorf!("signed appliance is not built for {}", arch));
        }
    }
    Ok(())
}

struct ParsedReleaseVersion {
    core: [String; 3],
    prerelease: Vec<String>,
}

fn parse_release_version(value: &str) -> Result<ParsedReleaseVersion> {
    if !RELEASE_VERSION_PATTERN.is_match(value) {
        return Err(errorf!("invalid release version"));
    }
    let mut value = value.strip_prefix('v').unwrap_or(value);
    if let Some(plus) = value.find('+') {
        value = &value[..plus];
    }
    let mut pre = "";
    if let Some(dash) = value.find('-') {
        pre = &value[dash + 1..];
        value = &value[..dash];
    }
    let parts: Vec<&str> = value.split('.').collect();
    if parts.len() != 3 {
        return Err(errorf!("invalid release version"));
    }
    Ok(ParsedReleaseVersion {
        core: [parts[0].into(), parts[1].into(), parts[2].into()],
        prerelease: if pre.is_empty() { Vec::new() } else { pre.split('.').map(String::from).collect() },
    })
}

fn numeric_identifier(value: &str) -> bool {
    !value.is_empty() && value.bytes().all(|b| b.is_ascii_digit())
}

fn compare_numeric_strings(left: &str, right: &str) -> std::cmp::Ordering {
    let left = match left.trim_start_matches('0') {
        "" => "0",
        v => v,
    };
    let right = match right.trim_start_matches('0') {
        "" => "0",
        v => v,
    };
    left.len().cmp(&right.len()).then_with(|| left.cmp(right))
}

/// SemVer precedence; build metadata is ignored and a stable version outranks
/// a prerelease of the same core version.
pub fn compare_release_versions(left: &str, right: &str) -> Result<i32> {
    use std::cmp::Ordering;
    let (a, b) = (parse_release_version(left)?, parse_release_version(right)?);
    let to_int = |o: Ordering| o as i32;
    for i in 0..3 {
        let cmp = compare_numeric_strings(&a.core[i], &b.core[i]);
        if cmp != Ordering::Equal {
            return Ok(to_int(cmp));
        }
    }
    match (a.prerelease.is_empty(), b.prerelease.is_empty()) {
        (true, true) => return Ok(0),
        (true, false) => return Ok(1),
        (false, true) => return Ok(-1),
        _ => {}
    }
    for (l, r) in a.prerelease.iter().zip(&b.prerelease) {
        let cmp = match (numeric_identifier(l), numeric_identifier(r)) {
            (true, true) => compare_numeric_strings(l, r),
            (true, false) => Ordering::Less,
            (false, true) => Ordering::Greater,
            (false, false) => l.as_str().cmp(r.as_str()),
        };
        if cmp != Ordering::Equal {
            return Ok(to_int(cmp));
        }
    }
    Ok(to_int(a.prerelease.len().cmp(&b.prerelease.len())))
}

pub fn is_release_version(value: &str) -> bool {
    RELEASE_VERSION_PATTERN.is_match(value)
}

pub(crate) fn validate_forward_upgrade(candidate: &str, current: &str) -> Result<()> {
    if current.is_empty() {
        return Ok(());
    }
    if compare_release_versions(candidate, current)? <= 0 {
        return Err(errorf!("refusing non-forward update; use the explicit rollback command to return to the previous release"));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn release_version_precedence() {
        assert_eq!(compare_release_versions("v0.2.1", "0.2.0").unwrap(), 1);
        assert_eq!(compare_release_versions("0.2.0-rc.1", "0.2.0").unwrap(), -1);
        assert_eq!(compare_release_versions("0.2.0-rc.2", "0.2.0-rc.10").unwrap(), -1);
        assert_eq!(compare_release_versions("0.2.0-alpha", "0.2.0-1").unwrap(), 1);
        assert_eq!(compare_release_versions("1.0.0+build.5", "1.0.0").unwrap(), 0);
        assert!(compare_release_versions("1.0", "1.0.0").is_err());
        assert!(validate_forward_upgrade("0.2.0", "0.2.0").is_err());
    }

    #[test]
    fn signature_round_trip_and_tamper_detection() {
        let (public, private) = generate_key_pair().unwrap();
        let mut manifest = FirmwareManifest { version: "0.3.0".into(), build_date: "2026-10-01T00:00:00Z".into(), git_commit: "abc".into(), ..Default::default() };
        manifest.files_mut().insert("web/dist/index.html".into(), "00".repeat(32));
        manifest.files_mut().insert("bin/<x>&".into(), "11".repeat(32));
        sign_manifest(&mut manifest, &private).unwrap();
        verify_manifest(Some(&manifest), &public).unwrap();
        // The canonical payload escapes HTML characters exactly like Go.
        assert!(String::from_utf8(signed_payload(&manifest).unwrap()).unwrap().contains("bin/\\u003cx\\u003e\\u0026"));
        let mut tampered = manifest.clone();
        tampered.version = "0.3.1".into();
        assert!(crate::goish::errors::is::<ErrInvalidSignature>(&verify_manifest(Some(&tampered), &public).unwrap_err()));
        let (other, _) = generate_key_pair().unwrap();
        assert!(crate::goish::errors::is::<ErrUntrustedPublicKey>(&verify_manifest(Some(&manifest), &other).unwrap_err()));
        assert!(crate::goish::errors::is::<ErrInvalidPublicKey>(&verify_manifest(Some(&manifest), &public[..31]).unwrap_err()));
    }

    #[test]
    fn appliance_payload_requires_one_complete_architecture() {
        let mut manifest = FirmwareManifest::default();
        for role in appliance_file_roles("amd64").unwrap() {
            manifest.files_mut().insert(role.path, "00".repeat(32));
        }
        validate_appliance_payload(Some(&manifest)).unwrap();
        validate_appliance_architecture(Some(&manifest), "amd64").unwrap();
        assert!(validate_appliance_architecture(Some(&manifest), "arm64").is_err());
        manifest.files_mut().insert("bin/routerd-arm64".into(), "00".repeat(32));
        assert_eq!(validate_appliance_payload(Some(&manifest)).unwrap_err().to_string(), "incomplete appliance payload: architecture binary set is partial");
        assert_eq!(appliance_file_mode("bin/router-update-arm64"), 0o750);
        assert_eq!(appliance_file_mode("bin/other"), 0o755);
        assert_eq!(appliance_file_mode("web/dist/app.js"), 0o644);
    }
}
