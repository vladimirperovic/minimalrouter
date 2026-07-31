import { expect, test } from "@playwright/test";

test("opens DNS Filter and the visual Kids weekly scheduler", async ({ page, isMobile }) => {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/v1/auth/session") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ authenticated: true, csrf_token: "test-csrf" }) });
      return;
    }
    if (url.pathname === "/api/v1/config") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({
        revision: 1,
        system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
        wan: { interface: "eth0", enabled: false, username: "", password: "", mtu: 1492, use_peer_dns: true },
        lan: { interface: "eth1", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
        dhcp: { enabled: true, dns_enabled: false, range_start: "192.168.1.100", range_end: "192.168.1.200", lease_time: "12h", dns_servers: ["1.1.1.1"], static_leases: [] },
        firewall: { default_wan_input_policy: "deny", wan_ingress_mode: "wireguard_only", stateful_firewall: true, port_forwards: [], custom_rules: [] },
        wireguard: { enabled: false, interface: "wg0", listen_port: 51820, address: "10.8.0.1/24", peers: [] },
        cloudflare: {}, squid_proxy: { enabled: false, port: 3128, username: "proxyadmin", restricted_ips: [] },
        adguard: { enabled: false, filter_devices: [], device_profiles: [] },
        qos: { enabled: false, algorithm: "cake", download_limit_mbps: 100, upload_limit_mbps: 20 },
        wifi: { enabled: false, interface: "wlan0", ssid: "MinimalRouter-Home", band: "5ghz", channel: 36, hide_ssid: false },
      }) });
      return;
    }
    if (url.pathname === "/api/v1/system") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ status: "running", version: "test", runtime: { available: true, dhcp_leases: [] } }) });
      return;
    }
    await route.fulfill({ contentType: "application/json", body: "{}" });
  });

  await page.goto("/");
  const dnsFilterLink = page.getByRole("link", { name: /DNS Filter/i });
  if (isMobile) {
    await page.getByRole("button", { name: "Open navigation" }).click();
  }
  await expect(dnsFilterLink).toBeVisible();
  await dnsFilterLink.click();
  await expect(page.getByRole("heading", { name: "Scheduled service access" })).toBeVisible();
  await page.getByRole("button", { name: "Add device profile" }).click();
  await expect(page.getByRole("heading", { name: "Device profile", exact: true })).toBeVisible();
  await page.getByLabel("Profile type").selectOption("kids");
  await expect(page.getByRole("group", { name: "Allowed time" })).toBeVisible();
  await expect(page.getByLabel("Pon 19:00 allowed")).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("Pon 18:00 blocked")).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByLabel("Sub 03:00 allowed")).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("YouTube")).toBeChecked();
  await expect(page.getByLabel("Steam")).toBeChecked();
  await expect(page.getByLabel("Wikipedia / Wikimedia")).toBeChecked();
  await page.getByLabel("Pon 18:00 blocked").click();
  await expect(page.getByLabel("Pon 18:00 allowed")).toHaveAttribute("aria-pressed", "true");
});

test("shows bounded gateway quality status, history, and settings", async ({ page, isMobile }) => {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/v1/auth/session") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ authenticated: true, csrf_token: "test-csrf" }) });
      return;
    }
    if (url.pathname === "/api/v1/config") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({
        revision: 1,
        system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
        wan: { interface: "eth0", enabled: true, username: "user@isp", password: "[REDACTED]", mtu: 1492, use_peer_dns: true },
        lan: { interface: "eth1", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
        dhcp: { enabled: true, dns_enabled: false, range_start: "192.168.1.100", range_end: "192.168.1.200", lease_time: "12h", dns_servers: ["1.1.1.1"], static_leases: [] },
        firewall: { default_wan_input_policy: "deny", wan_ingress_mode: "wireguard_only", stateful_firewall: true, port_forwards: [], custom_rules: [] },
        wireguard: { enabled: false, interface: "wg0", listen_port: 51820, address: "10.8.0.1/24", peers: [] },
        cloudflare: {}, squid_proxy: { enabled: false, port: 3128, username: "proxyadmin", restricted_ips: [] },
        adguard: { enabled: false, filter_devices: [], device_profiles: [] },
        qos: { enabled: false, algorithm: "cake", download_limit_mbps: 100, upload_limit_mbps: 20 },
        wifi: { enabled: false, interface: "wlan0", ssid: "MinimalRouter-Home", band: "5ghz", channel: 36, hide_ssid: false },
      }) });
      return;
    }
    if (url.pathname === "/api/v1/system") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ status: "Connected", version: "test", runtime: { available: true, wan_connected: true, dhcp_leases: [] } }) });
      return;
    }
    if (url.pathname === "/api/v1/gateway/settings") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ enabled: true, targets: ["1.1.1.1", "8.8.8.8"], interval_seconds: 30 }) });
      return;
    }
    if (url.pathname === "/api/v1/gateway/summary") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({
        available: true,
        enabled: true,
        state: "healthy",
        timestamp: "2026-07-31T18:00:00Z",
        link: { connected: true, interface: "ppp0", local_ip: "203.0.113.10", peer_ip: "198.51.100.1" },
        targets: [
          { target: "1.1.1.1", reachable: true, packets_sent: 4, packets_received: 4, packet_loss_percent: 0, latency_ms: 18, jitter_ms: 2 },
          { target: "8.8.8.8", reachable: true, packets_sent: 4, packets_received: 4, packet_loss_percent: 0, latency_ms: 22, jitter_ms: 3 },
        ],
        latency_ms: 20,
        jitter_ms: 2.5,
        packet_loss_percent: 0,
        pppoe_uptime_seconds: 3660,
        reconnects_1h: 0,
        reconnects_24h: 1,
      }) });
      return;
    }
    if (url.pathname === "/api/v1/gateway/history") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({
        window: url.searchParams.get("window") || "1h",
        points: [
          { timestamp: "2026-07-31T17:00:00Z", state: "healthy", latency_ms: 18, jitter_ms: 2, packet_loss_percent: 0, pppoe_uptime_seconds: 60 },
          { timestamp: "2026-07-31T17:30:00Z", state: "degraded", latency_ms: 80, jitter_ms: 20, packet_loss_percent: 5, pppoe_uptime_seconds: 1860 },
          { timestamp: "2026-07-31T18:00:00Z", state: "healthy", latency_ms: 20, jitter_ms: 2.5, packet_loss_percent: 0, pppoe_uptime_seconds: 3660 },
        ],
      }) });
      return;
    }
    await route.fulfill({ contentType: "application/json", body: "{}" });
  });

  await page.goto("/");
  if (isMobile) {
    await page.getByRole("button", { name: "Open navigation" }).click();
  }
  await page.getByRole("link", { name: /Gateway Quality/i }).click();
  await expect(page.getByRole("heading", { name: "Gateway quality", exact: true })).toBeVisible();
  await expect(page.getByText("Healthy", { exact: true })).toBeVisible();
  await expect(page.getByText("20 ms", { exact: true })).toBeVisible();
  await expect(page.getByRole("img", { name: "Latency and packet loss history" })).toBeVisible();
  await expect(page.getByLabel("Primary public IPv4")).toHaveValue("1.1.1.1");
  await expect(page.getByLabel("Secondary public IPv4")).toHaveValue("8.8.8.8");
  await page.getByRole("button", { name: "24h" }).click();
  await expect(page.getByRole("button", { name: "24h" })).toHaveClass(/is-active/);
});
