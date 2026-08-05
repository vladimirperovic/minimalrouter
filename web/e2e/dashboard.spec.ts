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
        wan: { interface: "eth0", enabled: false, username: "", password: "", mtu: 1492 },
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
