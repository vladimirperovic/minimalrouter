export type Snapshot = { id: string; revision: number; created_at: string; checksum: string };

export type WireGuardPeer = {
  id: string;
  name: string;
  public_key: string;
  preshared_key?: string;
  allowed_ips: string[];
  endpoint?: string;
  enabled: boolean;
};

export type PortForwardRule = {
  id: string;
  name: string;
  protocol: string;
  external_port: number;
  internal_ip: string;
  internal_port: number;
  enabled: boolean;
};

export type RouterConfig = {
  revision: number;
  updated_at: string;
  system: {
    hostname: string;
    domain: string;
    https_enabled: boolean;
    https_port: number;
    management_access: string;
  };
  wan: {
    interface: string;
    enabled: boolean;
    username: string;
    password?: string;
    mtu: number;
    use_peer_dns: boolean;
  };
  lan: { interface: string; ip_address: string; netmask: string; cidr: string };
  dhcp: {
    enabled: boolean;
    dns_enabled: boolean;
    range_start: string;
    range_end: string;
    lease_time: string;
    dns_servers: string[];
    static_leases: Array<{ id: string; hostname: string; mac: string; ip_address: string }>;
  };
  firewall: {
    default_wan_input_policy: string;
    wan_ingress_mode: string;
    stateful_firewall: boolean;
    port_forwards: PortForwardRule[];
    custom_rules: Array<{
      id: string;
      name: string;
      action: string;
      direction: string;
      protocol: string;
      src_ip?: string;
      dst_port?: number;
      enabled: boolean;
    }>;
  };
  wireguard: {
    enabled: boolean;
    interface: string;
    private_key?: string;
    listen_port: number;
    address: string;
    peers: WireGuardPeer[];
  };
  cloudflare: {
    ddns_enabled: boolean;
    ddns_provider?: "noip" | "cloudflare" | string;
    ddns_username?: string;
    api_token?: string;
    zone_id?: string;
    zone_name?: string;
    domain: string;
    tunnel_enabled: boolean;
    tunnel_token?: string;
  };
  squid_proxy: {
    enabled: boolean;
    port: number;
    username: string;
    password?: string;
    restricted_ips: Array<{ hostname: string; ip_address: string; enabled: boolean }>;
  };
  adguard: {
    enabled: boolean;
    blocklist_url: string;
    last_updated: string;
    filter_devices?: unknown[];
    device_profiles: unknown[];
  };
  qos: { enabled: boolean; algorithm: string; download_limit_mbps: number; upload_limit_mbps: number };
  wifi: {
    enabled: boolean;
    interface: string;
    ssid: string;
    passphrase?: string;
    band: string;
    channel: number;
    hide_ssid: boolean;
  };
};

export type PendingTransaction = {
  pending?: boolean;
  id: string;
  state: string;
  confirmation_deadline?: string;
  management_access?: string;
  error?: string;
};

export type StorageStatus = {
  available: boolean;
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
  usage_percent: number;
  level: "unknown" | "normal" | "warning" | "critical";
  nonessential_writes_allowed: boolean;
  durable_writes_allowed: boolean;
};

export type SystemStatus = {
  status?: string;
  version?: string;
  wan_iface?: string;
  lan_ip?: string;
  revision?: number;
  update_trust_configured?: boolean;
  apply_in_progress?: boolean;
  recovery_required?: boolean;
  recovery_reason?: string;
  transaction_id?: string;
  transaction_state?: string;
  runtime?: {
    available?: boolean;
    os?: string;
    architecture?: string;
    wan_connected?: boolean;
    public_ip?: string;
    uptime_seconds?: number;
    cpu_count?: number;
    cpu_load_percent?: number;
    memory_used_bytes?: number;
    memory_total_bytes?: number;
    rx_bytes?: number;
    tx_bytes?: number;
    disk_used_bytes?: number;
    disk_total_bytes?: number;
    storage?: StorageStatus;
    temperature_c?: number;
    time_synchronized?: boolean;
    conntrack_count?: number;
    conntrack_max?: number;
    conntrack_usage_percent?: number;
    dhcp_leases?: Array<{ expires_at: number; mac: string; ip_address: string; hostname?: string }>;
  };
};

export type GatewaySettings = {
  enabled: boolean;
  targets: string[];
  interval_seconds: number;
};

export type GatewayTargetResult = {
  target: string;
  reachable: boolean;
  packets_sent: number;
  packets_received: number;
  packet_loss_percent: number;
  latency_ms?: number;
  jitter_ms?: number;
  error?: string;
};

export type GatewaySummary = {
  available: boolean;
  enabled: boolean;
  state: "unknown" | "healthy" | "degraded" | "offline" | "flapping";
  timestamp?: string;
  link: {
    connected: boolean;
    interface: string;
    local_ip?: string;
    peer_ip?: string;
  };
  targets?: GatewayTargetResult[];
  peer_probe?: GatewayTargetResult;
  latency_ms?: number;
  jitter_ms?: number;
  packet_loss_percent: number;
  pppoe_uptime_seconds: number;
  reconnects_1h: number;
  reconnects_24h: number;
};

export type GatewayHistoryPoint = {
  timestamp: string;
  state: GatewaySummary["state"];
  latency_ms?: number;
  jitter_ms?: number;
  packet_loss_percent: number;
  pppoe_uptime_seconds: number;
};

export type ApplianceHealthState = "healthy" | "warning" | "degraded" | "recovery_required" | "unknown";

export type ApplianceHealthCheck = {
  id: string;
  label: string;
  state: ApplianceHealthState;
  summary: string;
};

export type ApplianceHealth = {
  state: ApplianceHealthState;
  headline: string;
  checks: ApplianceHealthCheck[];
  generated_at: string;
};
