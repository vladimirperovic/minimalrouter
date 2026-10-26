use serde::{Deserialize, Serialize};

use super::dhcp_leases::{current_dhcp_lease_snapshot, DHCPLease, DHCPLeaseStatus};
use crate::goish::json::{is_zero_f64, is_zero_i64, is_zero_u64, List};
use crate::storage;

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RuntimeStatus {
    pub available: bool,
    pub os: String,
    pub architecture: String,
    pub wan_connected: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub wan_mac: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub lan_mac: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub public_ip: String,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub uptime_seconds: i64,
    pub cpu_count: i64,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub cpu_load_percent: f64,
    #[serde(skip_serializing_if = "List::is_empty")]
    pub load_average: List<f64>,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub memory_used_bytes: u64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub memory_total_bytes: u64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub app_memory_bytes: u64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub disk_used_bytes: u64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub disk_total_bytes: u64,
    pub storage: storage::Status,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub rx_bytes: u64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub tx_bytes: u64,
    pub time_synchronized: bool,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub conntrack_count: u64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub conntrack_max: u64,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub conntrack_usage_percent: f64,
    pub dhcp_leases: List<DHCPLease>,
    pub dhcp_lease_status: DHCPLeaseStatus,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub wireguard_active_peers: i64,
    #[serde(skip_serializing_if = "List::is_empty")]
    pub wireguard_peers: List<WireGuardPeerStatus>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wireguard_client: Option<WireGuardClientStatus>,
    pub qos: QoSStatus,
    pub ddns: DDNSStatus,
}

/// The live state of the outbound tunnel (wg1). Online means a handshake
/// succeeded within the last 3 minutes.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WireGuardClientStatus {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub endpoint: String,
    #[serde(rename = "last_handshake_epoch", skip_serializing_if = "is_zero_i64")]
    pub last_handshake: i64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub rx_bytes: u64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub tx_bytes: u64,
    pub online: bool,
}

/// One peer of `wg show wg0`.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WireGuardPeerStatus {
    pub public_key: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub endpoint: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub allowed_ips: String,
    #[serde(rename = "last_handshake_epoch", skip_serializing_if = "is_zero_i64")]
    pub last_handshake: i64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub rx_bytes: u64,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub tx_bytes: u64,
    pub online: bool,
}

/// The live state of the in-router inadyn daemon.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DDNSStatus {
    pub running: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hostname: String,
    #[serde(rename = "last_update_epoch", skip_serializing_if = "is_zero_i64")]
    pub last_update: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub last_ip: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct QoSDevice {
    pub interface: String,
    pub kind: String,
    pub root: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub parent: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct QoSStatus {
    pub available: bool,
    pub devices: Vec<QoSDevice>,
}

const MAX_QOS_OUTPUT_BYTES: usize = 256 << 10;

pub fn parse_qos_status(data: &[u8]) -> QoSStatus {
    let empty = QoSStatus::default();
    if data.len() > MAX_QOS_OUTPUT_BYTES {
        return empty;
    }
    #[derive(Deserialize, Default)]
    #[serde(default)]
    struct Record {
        dev: String,
        kind: String,
        root: bool,
        parent: String,
    }
    let Ok(Some(records)) = crate::goish::json::unmarshal::<Option<Vec<Record>>>(data) else {
        return empty;
    };
    if records.len() > 1024 {
        return empty;
    }
    let mut status = QoSStatus::default();
    for r in records {
        if r.dev.is_empty() || r.dev.len() > 15 || r.kind.is_empty() || r.kind.len() > 32 || r.parent.len() > 32 {
            return QoSStatus::default();
        }
        status.devices.push(QoSDevice { interface: r.dev, kind: r.kind, root: r.root, parent: r.parent });
    }
    status.available = true;
    status
}

/// Parses `wg show wg0 dump`: the first line describes the interface; every
/// following line is one peer.
pub fn parse_wireguard_dump(out: &str) -> Vec<WireGuardPeerStatus> {
    let now = crate::goish::time::Time::now().unix();
    let mut peers = Vec::new();
    for line in out.split('\n') {
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.len() < 8 || fields[0] == "interface" {
            continue;
        }
        let mut status = WireGuardPeerStatus {
            public_key: fields[0].into(),
            endpoint: if fields[2] == "(none)" { String::new() } else { fields[2].into() },
            allowed_ips: fields[3].into(),
            ..Default::default()
        };
        if let Some(ts) = crate::config::go_atoi(fields[4]).filter(|ts| *ts > 0) {
            status.last_handshake = ts;
            status.online = now - ts < 180;
        }
        status.rx_bytes = fields[5].parse().unwrap_or(0);
        status.tx_bytes = fields[6].parse().unwrap_or(0);
        peers.push(status);
    }
    peers
}

#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
fn count_active(peers: &[WireGuardPeerStatus]) -> i64 {
    peers.iter().filter(|p| p.online).count() as i64
}

#[cfg(not(target_os = "linux"))]
pub fn runtime_snapshot(_wan: &str, _lan: &str, _data_dir: &str) -> RuntimeStatus {
    RuntimeStatus {
        available: false,
        os: std::env::consts::OS.into(),
        architecture: go_arch(),
        cpu_count: std::thread::available_parallelism().map(|n| n.get() as i64).unwrap_or(1),
        storage: storage::inspect(""),
        dhcp_leases: List::empty(),
        dhcp_lease_status: current_dhcp_lease_snapshot().status,
        ..Default::default()
    }
}

/// Go's GOARCH naming.
pub fn go_arch() -> String {
    match std::env::consts::ARCH {
        "x86_64" => "amd64".into(),
        "aarch64" => "arm64".into(),
        "x86" => "386".into(),
        other => other.into(),
    }
}

#[cfg(target_os = "linux")]
pub use linux::runtime_snapshot;

#[cfg(target_os = "linux")]
mod linux {
    use super::*;
    use std::collections::HashMap;
    use std::process::Command;
    use std::sync::Mutex;
    use std::time::{Duration, Instant};

    struct CpuSample {
        total: u64,
        idle: u64,
        at: Option<Instant>,
        pct: f64,
    }

    static CPU: Mutex<CpuSample> = Mutex::new(CpuSample { total: 0, idle: 0, at: None, pct: 0.0 });

    const WIREGUARD_TELEMETRY_TTL: Duration = Duration::from_secs(30);
    static WG_CACHE: Mutex<Option<HashMap<String, (Vec<WireGuardPeerStatus>, Instant)>>> = Mutex::new(None);

    fn read_uint(path: &str) -> u64 {
        std::fs::read_to_string(path).ok().and_then(|s| s.trim().parse().ok()).unwrap_or(0)
    }

    fn read_cpu_load_percent() -> f64 {
        let mut sample = CPU.lock().unwrap_or_else(|e| e.into_inner());
        if sample.total != 0 && sample.at.is_some_and(|at| at.elapsed() < Duration::from_millis(200)) {
            return sample.pct;
        }
        let Ok(data) = std::fs::read_to_string("/proc/stat") else { return 0.0 };
        let first = data.split('\n').next().unwrap_or("");
        let line: Vec<&str> = first.split_whitespace().collect();
        if line.len() < 5 || line[0] != "cpu" {
            return 0.0;
        }
        let (mut total, mut idle) = (0u64, 0u64);
        for (i, field) in line[1..].iter().enumerate() {
            let value: u64 = field.parse().unwrap_or(0);
            total += value;
            if i == 3 || i == 4 {
                idle += value;
            }
        }
        if sample.total == 0 || total < sample.total {
            *sample = CpuSample { total, idle, at: Some(Instant::now()), pct: sample.pct };
            return 0.0;
        }
        let d_total = total - sample.total;
        let d_idle = idle.saturating_sub(sample.idle);
        sample.total = total;
        sample.idle = idle;
        sample.at = Some(Instant::now());
        if d_total == 0 {
            return 0.0;
        }
        let pct = ((d_total - d_idle.min(d_total)) as f64 / d_total as f64 * 100.0).min(100.0);
        sample.pct = pct;
        pct
    }

    fn interface_exists(name: &str) -> bool {
        std::path::Path::new("/sys/class/net").join(name).exists()
    }

    fn interface_mac(name: &str) -> String {
        if name.is_empty() {
            return String::new();
        }
        std::fs::read_to_string(format!("/sys/class/net/{}/address", name))
            .ok()
            .and_then(|s| crate::goish::net::parse_mac(s.trim()))
            .filter(|mac| mac.iter().any(|b| *b != 0))
            .map(|mac| crate::goish::net::mac_string(&mac))
            .unwrap_or_default()
    }

    /// The first IPv4 address of ppp0 when it is up, from the kernel's own
    /// view of local addresses.
    fn ppp0_ipv4() -> Option<String> {
        let flags = std::fs::read_to_string("/sys/class/net/ppp0/flags").ok()?;
        let flags = u32::from_str_radix(flags.trim().trim_start_matches("0x"), 16).ok()?;
        if flags & 0x1 == 0 {
            return None;
        }
        // SAFETY: getifaddrs allocates a list we free with freeifaddrs.
        unsafe {
            let mut addrs: *mut libc::ifaddrs = std::ptr::null_mut();
            if libc::getifaddrs(&mut addrs) != 0 {
                return None;
            }
            let mut cursor = addrs;
            let mut found = None;
            while !cursor.is_null() {
                let entry = &*cursor;
                if !entry.ifa_addr.is_null()
                    && (*entry.ifa_addr).sa_family as i32 == libc::AF_INET
                    && std::ffi::CStr::from_ptr(entry.ifa_name).to_bytes() == b"ppp0"
                {
                    let sin = &*(entry.ifa_addr as *const libc::sockaddr_in);
                    found = Some(std::net::Ipv4Addr::from(u32::from_be(sin.sin_addr.s_addr)).to_string());
                    break;
                }
                cursor = entry.ifa_next;
            }
            libc::freeifaddrs(addrs);
            found
        }
    }

    pub fn runtime_snapshot(wan_interface: &str, lan_interface: &str, data_dir: &str) -> RuntimeStatus {
        let mut status = RuntimeStatus {
            available: true,
            os: "linux".into(),
            architecture: go_arch(),
            cpu_count: std::thread::available_parallelism().map(|n| n.get() as i64).unwrap_or(1),
            ..Default::default()
        };
        // SAFETY: adjtimex with modes=0 only reads the kernel clock state.
        let mut timex: libc::timex = unsafe { std::mem::zeroed() };
        let state = unsafe { libc::adjtimex(&mut timex) };
        status.time_synchronized = state >= 0 && state != libc::TIME_ERROR;
        if let Ok(data) = std::fs::read_to_string("/proc/uptime") {
            if let Some(first) = data.split_whitespace().next() {
                status.uptime_seconds = first.parse::<f64>().unwrap_or(0.0) as i64;
            }
        }
        if let Ok(data) = std::fs::read_to_string("/proc/loadavg") {
            for field in data.split_whitespace().take(3) {
                if let Ok(value) = field.parse::<f64>() {
                    status.load_average.push(value);
                }
            }
        }
        status.cpu_load_percent = read_cpu_load_percent();
        if let Ok(data) = std::fs::read_to_string("/proc/meminfo") {
            let (mut total_kb, mut available_kb) = (0u64, 0u64);
            for line in data.lines() {
                let fields: Vec<&str> = line.split_whitespace().collect();
                if fields.len() < 2 {
                    continue;
                }
                let value: u64 = fields[1].parse().unwrap_or(0);
                match fields[0].trim_end_matches(':') {
                    "MemTotal" => total_kb = value,
                    "MemAvailable" => available_kb = value,
                    _ => {}
                }
            }
            status.memory_total_bytes = total_kb * 1024;
            if total_kb >= available_kb {
                status.memory_used_bytes = (total_kb - available_kb) * 1024;
            }
        }
        status.app_memory_bytes = read_process_memory_bytes();
        let storage_status = storage::inspect(data_dir);
        status.disk_total_bytes = storage_status.total_bytes;
        status.disk_used_bytes = storage_status.used_bytes;
        status.storage = storage_status;
        if let Some(ip) = ppp0_ipv4() {
            status.wan_connected = true;
            status.public_ip = ip;
        }
        let stats_interface = if status.wan_connected { "ppp0" } else { wan_interface };
        status.wan_mac = interface_mac(wan_interface);
        status.lan_mac = interface_mac(lan_interface);
        status.rx_bytes = read_uint(&format!("/sys/class/net/{}/statistics/rx_bytes", stats_interface));
        status.tx_bytes = read_uint(&format!("/sys/class/net/{}/statistics/tx_bytes", stats_interface));
        status.conntrack_count = read_uint("/proc/sys/net/netfilter/nf_conntrack_count");
        status.conntrack_max = read_uint("/proc/sys/net/netfilter/nf_conntrack_max");
        if status.conntrack_max > 0 {
            status.conntrack_usage_percent = (status.conntrack_count as f64 / status.conntrack_max as f64 * 100.0).min(100.0);
        }
        let leases = current_dhcp_lease_snapshot();
        status.dhcp_leases = leases.leases.into();
        status.dhcp_lease_status = leases.status;
        status.wireguard_peers = read_wireguard_interface("wg0").into();
        status.wireguard_active_peers = count_active(&status.wireguard_peers);
        status.wireguard_client = read_wireguard_client_status();
        status.ddns = inspect_ddns();
        status.qos = read_qos_status();
        status
    }

    fn read_qos_status() -> QoSStatus {
        // Reading qdiscs is unprivileged netlink; no helper capability needed.
        match run_bounded("/sbin/tc", &["-j", "qdisc", "show"], Duration::from_secs(2), MAX_QOS_OUTPUT_BYTES) {
            Some(out) => parse_qos_status(&out),
            None => QoSStatus::default(),
        }
    }

    /// Runs a command with a deadline and an output cap; None on any failure.
    pub(crate) fn run_bounded(program: &str, args: &[&str], timeout: Duration, max_output: usize) -> Option<Vec<u8>> {
        use std::io::Read;
        let mut child = Command::new(program)
            .args(args)
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::null())
            .spawn()
            .ok()?;
        let mut stdout = child.stdout.take()?;
        let reader = std::thread::spawn(move || {
            let mut buf = Vec::new();
            let _ = (&mut stdout).take(max_output as u64 + 1).read_to_end(&mut buf);
            buf
        });
        let started = Instant::now();
        loop {
            match child.try_wait() {
                Ok(Some(status)) => {
                    let out = reader.join().ok()?;
                    return (status.success() && out.len() <= max_output).then_some(out);
                }
                Ok(None) if started.elapsed() < timeout => std::thread::sleep(Duration::from_millis(10)),
                _ => {
                    let _ = child.kill();
                    let _ = child.wait();
                    return None;
                }
            }
        }
    }

    fn inspect_ddns() -> DDNSStatus {
        let mut st = DDNSStatus { running: std::path::Path::new("/run/supervise-inadyn.pid").exists(), ..Default::default() };
        let Ok(entries) = std::fs::read_dir("/var/cache/inadyn") else {
            return st;
        };
        for entry in entries.flatten() {
            let name = entry.file_name().to_string_lossy().into_owned();
            if entry.file_type().map(|t| t.is_dir()).unwrap_or(true) || !name.ends_with(".cache") {
                continue;
            }
            let path = entry.path();
            let Ok(data) = std::fs::read_to_string(&path) else { continue };
            for field in data.split_whitespace() {
                if crate::goish::net::parse_ip(field).is_some() {
                    st.last_ip = field.into();
                    continue;
                }
                if let Some(epoch) = crate::config::go_atoi(field).filter(|e| *e > 1_000_000_000) {
                    st.last_update = epoch;
                }
            }
            if st.last_update == 0 {
                if let Ok(modified) = std::fs::metadata(&path).and_then(|m| m.modified()) {
                    st.last_update = modified.duration_since(std::time::UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0);
                }
            }
            break;
        }
        st.hostname = inadyn_hostname();
        st
    }

    fn inadyn_hostname() -> String {
        let Ok(data) = std::fs::read_to_string("/etc/inadyn/inadyn.conf") else {
            return String::new();
        };
        for line in data.split('\n') {
            if let Some(rest) = line.trim().strip_prefix("hostname = ") {
                return rest.trim_matches('"').to_string();
            }
        }
        String::new()
    }

    /// One of the installer-authorised, non-secret `wg show` projections; none
    /// returns a private or preshared key.
    fn safe_wg_show(interface: &str, field: &str) -> Option<String> {
        if interface != "wg0" && interface != "wg1" {
            return None;
        }
        if !matches!(field, "endpoints" | "allowed-ips" | "latest-handshakes" | "transfer") {
            return None;
        }
        run_bounded("doas", &["/usr/bin/wg", "show", interface, field], Duration::from_secs(3), 1 << 20)
            .map(|out| String::from_utf8_lossy(&out).into_owned())
    }

    fn read_wireguard_interface(interface: &str) -> Vec<WireGuardPeerStatus> {
        // A disabled tunnel is the common case; avoid spawning doas at all.
        if !interface_exists(interface) {
            return Vec::new();
        }
        let now = Instant::now();
        {
            let cache = WG_CACHE.lock().unwrap_or_else(|e| e.into_inner());
            if let Some((peers, taken)) = cache.as_ref().and_then(|c| c.get(interface)) {
                if now.duration_since(*taken) < WIREGUARD_TELEMETRY_TTL {
                    return peers.clone();
                }
            }
        }
        let mut order: Vec<String> = Vec::new();
        let mut peers: HashMap<String, WireGuardPeerStatus> = HashMap::new();
        let mut ensure = |key: &str, order: &mut Vec<String>| {
            if !peers.contains_key(key) {
                peers.insert(key.to_string(), WireGuardPeerStatus { public_key: key.to_string(), ..Default::default() });
                order.push(key.to_string());
            }
        };
        let lines_of = |field: &str| safe_wg_show(interface, field).unwrap_or_default();
        let allowed = lines_of("allowed-ips");
        let endpoints = lines_of("endpoints");
        let handshakes = lines_of("latest-handshakes");
        let transfer = lines_of("transfer");
        for line in allowed.split('\n') {
            let f: Vec<&str> = line.split_whitespace().collect();
            if f.len() >= 2 {
                ensure(f[0], &mut order);
            }
        }
        for line in endpoints.split('\n').chain(handshakes.split('\n')).chain(transfer.split('\n')) {
            let f: Vec<&str> = line.split_whitespace().collect();
            if f.len() >= 2 {
                ensure(f[0], &mut order);
            }
        }
        for line in allowed.split('\n') {
            let f: Vec<&str> = line.split_whitespace().collect();
            if f.len() >= 2 {
                peers.get_mut(f[0]).unwrap().allowed_ips = f[1..].join(", ");
            }
        }
        for line in endpoints.split('\n') {
            let f: Vec<&str> = line.split_whitespace().collect();
            if f.len() >= 2 && f[1] != "(none)" {
                peers.get_mut(f[0]).unwrap().endpoint = f[1].into();
            }
        }
        let now_unix = crate::goish::time::Time::now().unix();
        for line in handshakes.split('\n') {
            let f: Vec<&str> = line.split_whitespace().collect();
            if f.len() >= 2 {
                if let Some(ts) = crate::config::go_atoi(f[1]).filter(|ts| *ts > 0) {
                    let peer = peers.get_mut(f[0]).unwrap();
                    peer.last_handshake = ts;
                    peer.online = now_unix >= ts && now_unix - ts < 180;
                }
            }
        }
        for line in transfer.split('\n') {
            let f: Vec<&str> = line.split_whitespace().collect();
            if f.len() >= 3 {
                let peer = peers.get_mut(f[0]).unwrap();
                peer.rx_bytes = f[1].parse().unwrap_or(0);
                peer.tx_bytes = f[2].parse().unwrap_or(0);
            }
        }
        let result: Vec<WireGuardPeerStatus> = order.iter().filter_map(|k| peers.get(k).cloned()).collect();
        let mut cache = WG_CACHE.lock().unwrap_or_else(|e| e.into_inner());
        cache.get_or_insert_with(HashMap::new).insert(interface.to_string(), (result.clone(), now));
        result
    }

    fn read_wireguard_client_status() -> Option<WireGuardClientStatus> {
        let peer = read_wireguard_interface("wg1").into_iter().next()?;
        Some(WireGuardClientStatus {
            endpoint: peer.endpoint,
            last_handshake: peer.last_handshake,
            rx_bytes: peer.rx_bytes,
            tx_bytes: peer.tx_bytes,
            online: peer.online,
        })
    }

    /// Sums the resident memory of every userspace process (thread-group
    /// leaders only) from /proc/<pid>/stat field 24.
    fn read_process_memory_bytes() -> u64 {
        // SAFETY: sysconf has no preconditions.
        let page_size = unsafe { libc::sysconf(libc::_SC_PAGESIZE) }.max(1) as u64;
        let Ok(entries) = std::fs::read_dir("/proc") else { return 0 };
        let mut total = 0u64;
        for entry in entries.flatten() {
            let name = entry.file_name();
            let name = name.to_string_lossy();
            if !name.as_bytes().first().is_some_and(u8::is_ascii_digit) {
                continue;
            }
            let Ok(data) = std::fs::read_to_string(entry.path().join("stat")) else { continue };
            let Some(closing) = data.rfind(')') else { continue };
            let fields: Vec<&str> = data[closing + 1..].split_whitespace().collect();
            if let Some(pages) = fields.get(21).and_then(|f| f.parse::<u64>().ok()) {
                total += pages * page_size;
            }
        }
        total
    }
}
