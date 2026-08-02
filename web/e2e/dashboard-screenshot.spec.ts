import { expect, test } from "@playwright/test";

test("captures current dashboard overview for documentation", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => {
    const FixedDate = class extends Date {
      constructor(...args: ConstructorParameters<typeof Date>) {
        super(...(args.length ? args : ["2026-08-02T08:00:00Z"]));
      }
      static now() {
        return new Date("2026-08-02T08:00:00Z").getTime();
      }
    };
    // @ts-expect-error deterministic documentation screenshot
    window.Date = FixedDate;
  });

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const json = (body: unknown) => route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });

    if (url.pathname === "/api/v1/auth/session") {
      await json({ authenticated: true, csrf_token: "docs-csrf" });
      return;
    }
    if (url.pathname === "/api/v1/config") {
      await json({
        revision: 42,
        system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
        wan: { interface: "eth1", enabled: true, username: "demo@isp", password: "[REDACTED]", mtu: 1492, use_peer_dns: true },
        lan: { interface: "eth0", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
        dhcp: { enabled: true, dns_enabled: true, range_start: "192.168.1.100", range_end: "192.168.1.200", lease_time: "12h", dns_servers: ["1.1.1.1", "8.8.8.8"], static_leases: [] },
        firewall: { default_wan_input_policy: "deny", wan_ingress_mode: "wireguard_only", stateful_firewall: true, port_forwards: [], custom_rules: [] },
        wireguard: { enabled: true, interface: "wg0", listen_port: 51820, address: "10.8.0.1/24", peers: [{ name: "Phone", enabled: true }] },
        cloudflare: { ddns_enabled: true, ddns_provider: "noip", ddns_username: "demo", domain: "router.example.invalid", zone_name: "", api_token: "[REDACTED]", tunnel_enabled: false },
        squid_proxy: { enabled: false, port: 3128, username: "proxyadmin", restricted_ips: [] },
        adguard: { enabled: false, filter_devices: [], device_profiles: [] },
        qos: { enabled: false, algorithm: "cake", download_limit_mbps: 1000, upload_limit_mbps: 400 },
        wifi: { enabled: false, interface: "wlan0", ssid: "MinimalRouter", band: "5ghz", channel: 36, hide_ssid: false },
      });
      return;
    }
    if (url.pathname === "/api/v1/system") {
      await json({
        status: "running",
        version: "alpha",
        update_trust_configured: true,
        runtime: {
          available: true,
          wan_connected: true,
          public_ip: "203.0.113.42",
          uptime_seconds: 288000,
          os: "Alpine Linux 3.22",
          architecture: "amd64",
          temperature_c: 39,
          cpu_load_percent: 7,
          cpu_count: 2,
          memory_used_bytes: 180355072,
          memory_total_bytes: 2147483648,
          disk_used_bytes: 1181116006,
          disk_total_bytes: 8589934592,
          storage: { level: "normal", usage_percent: 13.8 },
          conntrack_count: 2411,
          conntrack_max: 131072,
          conntrack_usage_percent: 1.8,
          time_synchronized: true,
          dhcp_leases: [
            { hostname: "workstation", ip_address: "192.168.1.110", mac: "02:00:00:00:00:10", expires_at: 1785686400 },
            { hostname: "phone", ip_address: "192.168.1.121", mac: "02:00:00:00:00:21", expires_at: 1785688200 },
            { hostname: "media", ip_address: "192.168.1.134", mac: "02:00:00:00:00:34", expires_at: 1785690000 },
          ],
        },
      });
      return;
    }
    if (url.pathname === "/api/v1/gateway/summary") {
      await json({ available: true, enabled: true, state: "healthy", latency_ms: 8.7, jitter_ms: 1.2, packet_loss_percent: 0, pppoe_uptime_seconds: 287820, reconnects_1h: 0, reconnects_24h: 0 });
      return;
    }
    if (url.pathname === "/api/v1/gateway/settings") {
      await json({ enabled: true, targets: ["1.1.1.1", "8.8.8.8"], interval_seconds: 30 });
      return;
    }
    if (url.pathname === "/api/v1/gateway/history") {
      await json({
        window: "1h",
        points: [
          { timestamp: "2026-08-02T07:00:00Z", state: "healthy", latency_ms: 8.1, packet_loss_percent: 0 },
          { timestamp: "2026-08-02T07:08:00Z", state: "healthy", latency_ms: 9.4, packet_loss_percent: 0 },
          { timestamp: "2026-08-02T07:16:00Z", state: "healthy", latency_ms: 8.6, packet_loss_percent: 0 },
          { timestamp: "2026-08-02T07:24:00Z", state: "healthy", latency_ms: 10.2, packet_loss_percent: 0 },
          { timestamp: "2026-08-02T07:32:00Z", state: "healthy", latency_ms: 8.8, packet_loss_percent: 0 },
          { timestamp: "2026-08-02T07:40:00Z", state: "healthy", latency_ms: 9.1, packet_loss_percent: 0 },
          { timestamp: "2026-08-02T07:48:00Z", state: "healthy", latency_ms: 8.4, packet_loss_percent: 0 },
          { timestamp: "2026-08-02T08:00:00Z", state: "healthy", latency_ms: 8.7, packet_loss_percent: 0 },
        ],
      });
      return;
    }
    if (url.pathname === "/api/v1/snapshots") {
      await json([]);
      return;
    }
    if (url.pathname === "/api/v1/transactions/pending") {
      await json({});
      return;
    }
    await json({});
  });

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Online and verified" })).toBeVisible();
  await expect(page.getByText("No-IP", { exact: true })).toBeVisible();
  await page.screenshot({ path: "../docs/images/dashboard-overview.png", fullPage: false });
});
