import { useEffect, useRef, useState } from "react";
import SetupWizard from "./components/SetupWizard";
import AuthGate from "./components/AuthGate";
import { apiFetch } from "./lib/api";

type Theme = "light" | "dark";

const navItems = [
  ["01", "Overview", "overview"],
  ["02", "System", "system"],
  ["03", "LAN & DHCP", "lan"],
  ["04", "Firewall", "firewall"],
  ["05", "WireGuard", "wireguard"],
  ["06", "Cloudflare", "cloudflare"],
  ["07", "Squid Proxy", "squid"],
  ["08", "AdGuard Filter", "adguard"],
  ["09", "Wi-Fi AP", "wifi"],
  ["10", "Recovery", "recovery"],
] as const;

function formatBytes(value = 0) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let amount = Math.max(0, value);
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit < 2 ? 0 : 1)} ${units[unit]}`;
}

function formatUptime(seconds = 0) {
  if (seconds <= 0) return "Unavailable";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return days > 0 ? `${days}d ${hours}h ${minutes}m` : `${hours}h ${minutes}m`;
}

function TrafficChart({ theme, download, upload }: { theme: Theme; download: number[]; upload: number[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const draw = () => {
      const rect = canvas.getBoundingClientRect();
      const ratio = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = rect.width * ratio;
      canvas.height = rect.height * ratio;

      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.scale(ratio, ratio);
      ctx.clearRect(0, 0, rect.width, rect.height);

      const padX = 4;
      const padY = 10;
      const width = rect.width - padX * 2;
      const height = rect.height - padY * 2;
      const max = Math.max(1, ...download, ...upload);

      ctx.lineWidth = 1;
      ctx.strokeStyle =
        theme === "dark" ? "rgba(235,235,245,.09)" : "rgba(60,60,67,.10)";
      for (let i = 0; i < 4; i += 1) {
        const y = padY + (height / 3) * i;
        ctx.beginPath();
        ctx.moveTo(padX, y);
        ctx.lineTo(rect.width - padX, y);
        ctx.stroke();
      }

      const plot = (values: number[], color: string, fill?: string) => {
        const points = values.map((value, index) => ({
          x: padX + (index / (values.length - 1)) * width,
          y: padY + height - (value / max) * height,
        }));

        if (fill) {
          const gradient = ctx.createLinearGradient(0, padY, 0, rect.height);
          gradient.addColorStop(0, fill);
          gradient.addColorStop(1, "rgba(0,122,255,0)");
          ctx.beginPath();
          ctx.moveTo(points[0].x, rect.height - padY);
          points.forEach((point) => ctx.lineTo(point.x, point.y));
          ctx.lineTo(points[points.length - 1].x, rect.height - padY);
          ctx.closePath();
          ctx.fillStyle = gradient;
          ctx.fill();
        }

        ctx.beginPath();
        points.forEach((point, index) => {
          if (index === 0) ctx.moveTo(point.x, point.y);
          else ctx.lineTo(point.x, point.y);
        });
        ctx.lineWidth = 2.25;
        ctx.lineJoin = "round";
        ctx.lineCap = "round";
        ctx.strokeStyle = color;
        ctx.stroke();
      };

      const normalizedDownload = download.length > 1 ? download : [0, 0];
      const normalizedUpload = upload.length > 1 ? upload : [0, 0];
      plot(normalizedDownload, theme === "dark" ? "#0a84ff" : "#007aff", "rgba(0,122,255,.16)");
      plot(normalizedUpload, theme === "dark" ? "#bf8cff" : "#7655c7");
    };

    draw();
    const observer = new ResizeObserver(draw);
    observer.observe(canvas);
    return () => observer.disconnect();
  }, [theme, download, upload]);

  return (
    <canvas
      ref={canvasRef}
      className="traffic-canvas"
      aria-label="Live internet traffic samples in megabits per second."
      role="img"
    />
  );
}

function Meter({
  label,
  value,
  detail,
  tone = "blue",
}: {
  label: string;
  value: number;
  detail: string;
  tone?: "blue" | "violet" | "green";
}) {
  return (
    <div className="meter-row">
      <div className="meter-copy">
        <span>{label}</span>
        <strong>{detail}</strong>
      </div>
      <div
        className="meter-track"
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={value}
      >
        <span
          className={`meter-fill meter-${tone}`}
          style={{ width: `${value}%` }}
        />
      </div>
    </div>
  );
}

function Toggle({
  checked,
  onChange,
  label,
  disabled = false,
}: {
  checked: boolean;
  onChange: () => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <button
      className={`switch ${checked ? "is-on" : ""}`}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={onChange}
    >
      <span />
    </button>
  );
}

function QrPreview({ source }: { source: string }) {
  return (
    <div className="qr-shell" aria-label="WireGuard configuration QR code">
      {/* A one-time data URL cannot use an image-optimization endpoint. */}
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img alt="Scannable WireGuard client configuration" src={source} width={280} height={280} />
    </div>
  );
}

function Dashboard() {
  const [theme, setTheme] = useState<Theme>("light");
  const [menuOpen, setMenuOpen] = useState(false);
  const [qrOpen, setQrOpen] = useState(false);
  const [wireGuardProvisioning, setWireGuardProvisioning] = useState<{
    peerName: string;
    clientIP: string;
    clientConfig: string;
    qrCodeData: string;
  } | null>(null);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [statefulRules, setStatefulRules] = useState(true);
  const [activeSection, setActiveSection] = useState("overview");
  const [fontScale, setFontScale] = useState(100);
  const [apiConnected, setApiConnected] = useState(false);
  const [operationError, setOperationError] = useState("");
  const [pendingConfirmationID, setPendingConfirmationID] = useState("");
  const [systemInfo, setSystemInfo] = useState<{
    status?: string;
    version?: string;
    mtu?: number;
    lan_ip?: string;
    wan_iface?: string;
    update_trust_configured?: boolean;
    runtime?: {
      available?: boolean;
      os?: string;
      architecture?: string;
      wan_connected?: boolean;
      public_ip?: string;
      uptime_seconds?: number;
      cpu_count?: number;
      cpu_load_percent?: number;
      load_average?: number[];
      memory_used_bytes?: number;
      memory_total_bytes?: number;
      disk_used_bytes?: number;
      disk_total_bytes?: number;
      rx_bytes?: number;
      tx_bytes?: number;
      temperature_c?: number;
    };
  }>({});
  const [trafficDown, setTrafficDown] = useState<number[]>([]);
  const [trafficUp, setTrafficUp] = useState<number[]>([]);
  const [downloadMbps, setDownloadMbps] = useState(0);
  const [uploadMbps, setUploadMbps] = useState(0);
  const trafficPrevious = useRef<{ rx: number; tx: number; at: number } | null>(null);

  const applyScale = (scale: number) => {
    setFontScale(scale);
    if (typeof document !== "undefined") {
      document.documentElement.style.fontSize = `${scale}%`;
      (document.body as HTMLElement).style.zoom = `${scale}%`;
    }
  };

  const decreaseFontScale = () => {
    applyScale(Math.max(80, fontScale - 5));
  };

  const increaseFontScale = () => {
    applyScale(Math.min(130, fontScale + 5));
  };

  const resetFontScale = () => {
    applyScale(100);
  };

  const [staticLeases, setStaticLeases] = useState<Array<{ hostname: string; mac: string; ip: string }>>([]);
  const [leaseModalOpen, setLeaseModalOpen] = useState(false);
  const [newLeaseHost, setNewLeaseHost] = useState("");
  const [newLeaseMAC, setNewLeaseMAC] = useState("");
  const [newLeaseIP, setNewLeaseIP] = useState("");

  const [portForwardRules, setPortForwardRules] = useState<Array<{
    name: string; proto: string; extPort: number; intIP: string; intPort: number; enabled: boolean;
  }>>([]);
  const [pfModalOpen, setPfModalOpen] = useState(false);
  const [newPfName, setNewPfName] = useState("");
  const [newPfProto, setNewPfProto] = useState("tcp");
  const [newPfExtPort, setNewPfExtPort] = useState("");
  const [newPfIntIP, setNewPfIntIP] = useState("");
  const [newPfIntPort, setNewPfIntPort] = useState("");

  const [wgPeers, setWgPeers] = useState<Array<{
    id: string; name: string; ip: string; traffic: string; active: string;
  }>>([]);
  const [wireGuardEnabled, setWireGuardEnabled] = useState(false);
  const [cloudflareEnabled, setCloudflareEnabled] = useState(false);
  const [addWgModalOpen, setAddWgModalOpen] = useState(false);
  const [newWgPeerName, setNewWgPeerName] = useState("");
  const [newWgPeerIP, setNewWgPeerIP] = useState("");
  const [newWgEndpoint, setNewWgEndpoint] = useState("");
  const [wireGuardSubmitting, setWireGuardSubmitting] = useState(false);

  const [cfConfig, setCfConfig] = useState({
    domain: "home.example.net",
    zoneId: "cf-zone-12345",
    apiToken: "",
    tunnelDomain: "minimalrouter-home",
  });
  const [cfModalOpen, setCfModalOpen] = useState(false);
  const [editCfDomain, setEditCfDomain] = useState(cfConfig.domain);
  const [editCfZone, setEditCfZone] = useState(cfConfig.zoneId);
  const [editCfToken, setEditCfToken] = useState("");

  const handleSaveCfConfig = (e: React.FormEvent) => {
    e.preventDefault();
    setCfConfig({
      ...cfConfig,
      domain: editCfDomain,
      zoneId: editCfZone,
      apiToken: editCfToken,
    });
    setCfModalOpen(false);
    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.cloudflare = {
            ddns_enabled: true,
            domain: editCfDomain,
            zone_id: editCfZone,
            api_token: editCfToken,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const [squidEnabled, setSquidEnabled] = useState(false);
  const [squidPort, setSquidPort] = useState(3128);
  const [squidUser, setSquidUser] = useState("proxyadmin");
  const [squidPass, setSquidPass] = useState("");
  const [squidRestrictedIPs, setSquidRestrictedIPs] = useState<
    { hostname: string; ip_address: string; enabled: boolean }[]
  >([
    { hostname: "Smart TV", ip_address: "10.0.0.50", enabled: true },
    { hostname: "Guest Laptop", ip_address: "10.0.0.51", enabled: true },
  ]);
  const [newRestrictedHost, setNewRestrictedHost] = useState("");
  const [newRestrictedIP, setNewRestrictedIP] = useState("");
  const [addRestrictedModalOpen, setAddRestrictedModalOpen] = useState(false);
  const [squidCredsModalOpen, setSquidCredsModalOpen] = useState(false);

  const handleToggleSquid = (enabled: boolean) => {
    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => {
          if (!res.ok) throw new Error("Could not load router configuration");
          return res.json();
        })
        .then((cfg) => {
          cfg.squid_proxy = {
            ...cfg.squid_proxy,
            enabled: enabled,
            port: squidPort,
            username: squidUser,
            password: squidPass || "[REDACTED]",
            restricted_ips: squidRestrictedIPs,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .then(async (response) => {
          if (!response.ok) throw new Error((await response.json().catch(() => ({}))).error ?? "Squid update failed");
          setSquidEnabled(enabled);
          setOperationError("");
        })
        .catch((error) => setOperationError(error instanceof Error ? error.message : "Squid update failed"));
    } else {
      setSquidEnabled(enabled);
    }
  };

  const handleToggleRestrictedIPItem = (targetIp: string) => {
    const updated = squidRestrictedIPs.map((item) =>
      item.ip_address === targetIp ? { ...item, enabled: !item.enabled } : item
    );
    setSquidRestrictedIPs(updated);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.squid_proxy = {
            ...cfg.squid_proxy,
            enabled: squidEnabled,
            port: squidPort,
            username: squidUser,
            password: squidPass || "[REDACTED]",
            restricted_ips: updated,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleSaveSquidCreds = (e: React.FormEvent) => {
    e.preventDefault();
    setSquidCredsModalOpen(false);
    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.squid_proxy = {
            ...cfg.squid_proxy,
            enabled: squidEnabled,
            port: squidPort,
            username: squidUser,
            password: squidPass || "[REDACTED]",
            restricted_ips: squidRestrictedIPs,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleAddRestrictedIP = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newRestrictedIP) return;
    const updated = [
      ...squidRestrictedIPs,
      { hostname: newRestrictedHost || "Device", ip_address: newRestrictedIP, enabled: true },
    ];
    setSquidRestrictedIPs(updated);
    setNewRestrictedHost("");
    setNewRestrictedIP("");
    setAddRestrictedModalOpen(false);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.squid_proxy = {
            ...cfg.squid_proxy,
            enabled: squidEnabled,
            port: squidPort,
            username: squidUser,
            password: squidPass || "[REDACTED]",
            restricted_ips: updated,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleRemoveRestrictedIP = (ip: string) => {
    const updated = squidRestrictedIPs.filter((item) => item.ip_address !== ip);
    setSquidRestrictedIPs(updated);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.squid_proxy = {
            ...cfg.squid_proxy,
            enabled: squidEnabled,
            port: squidPort,
            username: squidUser,
            password: squidPass || "[REDACTED]",
            restricted_ips: updated,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const [wifiEnabled, setWifiEnabled] = useState(false);
  const [wifiSSID, setWifiSSID] = useState("MinimalRouter-Home");
  const [wifiPass, setWifiPass] = useState("change-this-wifi-pass");
  const [wifiBand, setWifiBand] = useState("5ghz");
  const [wifiChannel, setWifiChannel] = useState("36");
  const [wifiHideSSID, setWifiHideSSID] = useState(false);
  const [wifiModalOpen, setWifiModalOpen] = useState(false);

  const handleSaveWiFi = (e: React.FormEvent) => {
    e.preventDefault();
    setWifiModalOpen(false);
    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.wifi = {
            enabled: wifiEnabled,
            interface: "wlan0",
            ssid: wifiSSID,
            passphrase: wifiPass,
            band: wifiBand,
            channel: parseInt(wifiChannel, 10) || 36,
            hide_ssid: wifiHideSSID,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const [qosEnabled, setQosEnabled] = useState(false);
  const [qosAlgorithm, setQosAlgorithm] = useState("cake");
  const [qosDown, setQosDown] = useState("100");
  const [qosUp, setQosUp] = useState("20");
  const [qosModalOpen, setQosModalOpen] = useState(false);

  const handleSaveQoS = (e: React.FormEvent) => {
    e.preventDefault();
    setQosModalOpen(false);
    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.qos = {
            enabled: qosEnabled,
            algorithm: qosAlgorithm,
            download_limit_mbps: parseInt(qosDown, 10) || 100,
            upload_limit_mbps: parseInt(qosUp, 10) || 20,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const [adguardEnabled, setAdguardEnabled] = useState(false);
  const [filterDevices, setFilterDevices] = useState<
    { id: string; hostname: string; ip_address: string; blocked_services: string[]; enabled: boolean }[]
  >([]);
  const [addFilterModalOpen, setAddFilterModalOpen] = useState(false);
  const [newFilterHost, setNewFilterHost] = useState("");
  const [newFilterIP, setNewFilterIP] = useState("");
  const [newFilterServices, setNewFilterServices] = useState<string[]>(["youtube", "tiktok"]);

  const handleToggleAdGuard = (enabled: boolean) => {
    setAdguardEnabled(enabled);
    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.adguard = {
            ...cfg.adguard,
            enabled: enabled,
            filter_devices: filterDevices,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleUpdateBlocklist = () => {
    if (!apiConnected) return;
    setOperationError("");
    apiFetch("/api/v1/adguard/blocklist/update", { method: "POST" })
      .then((res) => res.json())
      .then((data) => {
        if (data.error) {
          setOperationError(data.error);
        } else {
          setOperationError("");
          // Refresh config to get updated last_updated
          apiFetch("/api/v1/config")
            .then((r) => r.json())
            .then((cfg) => {
              if (cfg.adguard) {
                setAdguardEnabled(cfg.adguard.enabled ?? false);
              }
            })
            .catch(console.error);
        }
      })
      .catch((err) => setOperationError("Blocklist update failed: " + String(err)));
  };

  const handleToggleFilterDevice = (id: string) => {
    const updated = filterDevices.map((item) =>
      item.id === id ? { ...item, enabled: !item.enabled } : item
    );
    setFilterDevices(updated);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.adguard = {
            ...cfg.adguard,
            enabled: adguardEnabled,
            filter_devices: updated,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleRemoveFilterDevice = (id: string) => {
    const updated = filterDevices.filter((item) => item.id !== id);
    setFilterDevices(updated);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.adguard = {
            ...cfg.adguard,
            enabled: adguardEnabled,
            filter_devices: updated,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleAddFilterDevice = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newFilterIP) return;
    const newItem = {
      id: `f-${Date.now()}`,
      hostname: newFilterHost || "Device",
      ip_address: newFilterIP,
      blocked_services: newFilterServices,
      enabled: true,
    };
    const updated = [...filterDevices, newItem];
    setFilterDevices(updated);
    setNewFilterHost("");
    setNewFilterIP("");
    setNewFilterServices(["youtube", "tiktok"]);
    setAddFilterModalOpen(false);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.adguard = {
            ...cfg.adguard,
            enabled: adguardEnabled,
            filter_devices: updated,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const [dnsModalOpen, setDnsModalOpen] = useState(false);
  const [dnsPrimary, setDnsPrimary] = useState("1.1.1.1");
  const [dnsSecondary, setDnsSecondary] = useState("1.0.0.1");
  const [dnsProvider, setDnsProvider] = useState("cloudflare");
  const [dohEnabled, setDohEnabled] = useState(false);

  const handleSaveDnsSettings = (e: React.FormEvent) => {
    e.preventDefault();
    setDnsModalOpen(false);
    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.dhcp.dns_servers = [dnsPrimary, dnsSecondary];
          cfg.dhcp.dns_enabled = dohEnabled;
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const [ddnsModalOpen, setDdnsModalOpen] = useState(false);
  const [ddnsProvider, setDdnsProvider] = useState("cloudflare");
  const [ddnsDomain, setDdnsDomain] = useState("home.example.net");
  const [ddnsUser, setDdnsUser] = useState("");
  const [ddnsPass, setDdnsPass] = useState("");
  const [ddnsZoneId, setDdnsZoneId] = useState("cf-zone-12345");

  const handleSaveDdns = (e: React.FormEvent) => {
    e.preventDefault();
    setCfConfig({
      ...cfConfig,
      domain: ddnsDomain,
    });
    setDdnsModalOpen(false);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.cloudflare = {
            ddns_enabled: true,
            provider: ddnsProvider,
            domain: ddnsDomain,
            zone_id: ddnsZoneId,
            api_token: ddnsPass,
          };
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const [profileModalOpen, setProfileModalOpen] = useState(false);
  const [auditModalOpen, setAuditModalOpen] = useState(false);
  const [auditEvents, setAuditEvents] = useState<Array<{
    id: string;
    event_type: string;
    actor: string;
    timestamp: string;
    details: Record<string, string>;
  }>>([]);
  const [auditLoading, setAuditLoading] = useState(false);
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passNotice, setPassNotice] = useState("");
  const [passError, setPassError] = useState("");

  const handleChangePassword = (e: React.FormEvent) => {
    e.preventDefault();
    setPassError("");
    setPassNotice("");

    if (newPassword.length < 15) {
      setPassError("Nova lozinka mora imati najmanje 15 karaktera.");
      return;
    }

    if (newPassword !== confirmPassword) {
      setPassError("Nove lozinke se ne poklapaju.");
      return;
    }

    void apiFetch("/api/v1/auth/change-password", {
      method: "POST",
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
    }).then(async (response) => {
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(body.error ?? "Password change failed");
      }
      setPassNotice("Administrator password changed. Sign in again.");
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
      window.setTimeout(() => window.location.reload(), 1200);
    }).catch((error: Error) => setPassError(error.message));
  };

  const openAuditLog = async () => {
    setAuditLoading(true);
    setOperationError("");
    try {
      const response = await apiFetch("/api/v1/audit/events?limit=100");
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "Could not load security audit log");
      setAuditEvents(body.events ?? []);
      setAuditModalOpen(true);
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : "Could not load security audit log");
    } finally {
      setAuditLoading(false);
    }
  };

  const [snapshotsList, setSnapshotsList] = useState<Array<{
    id: string; revision: number; label: string; time: string; checksum: string;
  }>>([]);
  const [snapshotsModalOpen, setSnapshotsModalOpen] = useState(false);
  const [snapshotSuccessMsg, setSnapshotSuccessMsg] = useState("");

  const [backupModalOpen, setBackupModalOpen] = useState(false);
  const [backupNotice, setBackupNotice] = useState("");
  const [backupAdminPassword, setBackupAdminPassword] = useState("");
  const [backupPassphrase, setBackupPassphrase] = useState("");
  const [backupImportFile, setBackupImportFile] = useState<File | null>(null);
  const [pendingBackupImportID, setPendingBackupImportID] = useState("");
  const [pfSenseFile, setPfSenseFile] = useState<File | null>(null);
  const [pfSenseWANInterface, setPfSenseWANInterface] = useState("eth0");
  const [pfSenseLANInterface, setPfSenseLANInterface] = useState("eth1");
  const [pendingPfSenseImportID, setPendingPfSenseImportID] = useState("");
  const [pfSenseWarnings, setPfSenseWarnings] = useState<string[]>([]);

  const handleExportBackup = async () => {
    setBackupNotice("");
    try {
      const response = await apiFetch("/api/v1/backup/export", {
        method: "POST",
        body: JSON.stringify({
          current_password: backupAdminPassword,
          backup_passphrase: backupPassphrase,
        }),
      });
      if (!response.ok) throw new Error(await response.text() || "Backup export failed");
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = "minimalrouter-backup.mrbak";
      anchor.click();
      URL.revokeObjectURL(url);
      setBackupNotice("Encrypted backup downloaded.");
    } catch (error) {
      setBackupNotice(error instanceof Error ? error.message : "Backup export failed");
    }
  };

  const handleImportBackup = (e: React.ChangeEvent<HTMLInputElement>) => {
    setBackupImportFile(e.target.files?.[0] ?? null);
    setPendingBackupImportID("");
  };

  const handlePreviewBackupRestore = async () => {
    if (!backupImportFile) {
      setBackupNotice("Choose an encrypted .mrbak file.");
      return;
    }
    const form = new FormData();
    form.set("backup", backupImportFile);
    form.set("current_password", backupAdminPassword);
    form.set("backup_passphrase", backupPassphrase);
    try {
      const response = await apiFetch("/api/v1/backup/import/preview", {
        method: "POST",
        body: form,
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "Backup validation failed");
      setPendingBackupImportID(body.import_id);
      setBackupNotice("Backup authenticated and validated. Review before applying.");
    } catch (error) {
      setBackupNotice(error instanceof Error ? error.message : "Backup validation failed");
    }
  };

  const handleApplyBackupRestore = async () => {
    if (!pendingBackupImportID) return;
    try {
      const response = await apiFetch(`/api/v1/import/backup/${encodeURIComponent(pendingBackupImportID)}/apply`, {
        method: "POST",
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "Restore failed");
      if (body.state === "AwaitingConfirmation") {
        setPendingConfirmationID(body.id);
        setBackupNotice("Backup is provisionally active. Verify LAN access, then confirm within 90 seconds.");
      } else {
        setBackupNotice("Backup restored and verified.");
      }
      setPendingBackupImportID("");
      if (body.state !== "AwaitingConfirmation") {
        window.setTimeout(() => window.location.reload(), 1200);
      }
    } catch (error) {
      setBackupNotice(error instanceof Error ? error.message : "Restore failed");
    }
  };

  const handlePreviewPfSenseImport = async () => {
    if (!pfSenseFile) {
      setBackupNotice("Choose an unencrypted pfSense config.xml file.");
      return;
    }
    try {
      const query = new URLSearchParams({
        wan: pfSenseWANInterface,
        lan: pfSenseLANInterface,
      });
      const response = await apiFetch(`/api/v1/import/pfsense/preview?${query}`, {
        method: "POST",
        headers: { "Content-Type": "application/xml" },
        body: await pfSenseFile.arrayBuffer(),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "pfSense import validation failed");
      setPendingPfSenseImportID(body.import_id);
      setPfSenseWarnings([
        ...(body.report?.warnings ?? []),
        ...((body.report?.unsupported_sections ?? []).map((section: string) => `Manual migration required: ${section}`)),
      ]);
      setBackupNotice("pfSense configuration parsed and validated. Review every warning before applying.");
    } catch (error) {
      setPendingPfSenseImportID("");
      setBackupNotice(error instanceof Error ? error.message : "pfSense import validation failed");
    }
  };

  const handleApplyPfSenseImport = async () => {
    if (!pendingPfSenseImportID) return;
    try {
      const response = await apiFetch(`/api/v1/import/pfsense/${encodeURIComponent(pendingPfSenseImportID)}/apply`, {
        method: "POST",
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "pfSense migration failed");
      setBackupNotice(
        body.state === "AwaitingConfirmation"
          ? "Migration is active temporarily and requires LAN confirmation."
          : "pfSense migration applied and verified.",
      );
      if (body.state === "AwaitingConfirmation") {
        setPendingConfirmationID(body.id);
      }
      setPendingPfSenseImportID("");
    } catch (error) {
      setBackupNotice(error instanceof Error ? error.message : "pfSense migration failed");
    }
  };

  const handleConfirmPendingTransaction = async () => {
    if (!pendingConfirmationID) return;
    try {
      const response = await apiFetch(`/api/v1/transactions/${encodeURIComponent(pendingConfirmationID)}/confirm`, {
        method: "POST",
      });
      if (!response.ok) throw new Error(await response.text() || "Confirmation failed");
      setPendingConfirmationID("");
      setBackupNotice("Configuration confirmed and committed.");
      window.setTimeout(() => window.location.reload(), 1000);
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : "Confirmation failed");
    }
  };

  const handleMakeSnapshot = async () => {
    if (!apiConnected) {
      setOperationError("Connect to the router API to create a real snapshot.");
      return;
    }
    try {
      const response = await apiFetch("/api/v1/snapshots", { method: "POST" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "Snapshot creation failed");
      const snapshot = body.snapshot;
      setSnapshotsList((current) => [{
        id: snapshot.id,
        revision: snapshot.revision,
        label: "Manual configuration snapshot",
        time: new Date(snapshot.created_at).toLocaleString(),
        checksum: snapshot.checksum,
      }, ...current]);
      setSnapshotSuccessMsg(`Snapshot ${snapshot.id} created and checksummed.`);
      window.setTimeout(() => setSnapshotSuccessMsg(""), 4000);
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : "Snapshot creation failed");
    }
  };

  const handleRestoreSnapshot = async (snapshotID: string) => {
    try {
      const response = await apiFetch(`/api/v1/snapshots/${encodeURIComponent(snapshotID)}/restore`, {
        method: "POST",
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "Snapshot restore failed");
      if (body.state === "AwaitingConfirmation") {
        setPendingConfirmationID(body.id);
        setSnapshotSuccessMsg(`Snapshot ${snapshotID} is provisionally active. Confirm connectivity within 90 seconds.`);
      } else {
        setSnapshotSuccessMsg(`Snapshot ${snapshotID} restored and verified.`);
      }
      setSnapshotsModalOpen(false);
      if (body.state !== "AwaitingConfirmation") {
        window.setTimeout(() => window.location.reload(), 1200);
      }
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : "Snapshot restore failed");
    }
  };

  const [dhcpModalOpen, setDhcpModalOpen] = useState(false);
  const [dhcpRangeStart, setDhcpRangeStart] = useState("10.0.0.20");
  const [dhcpRangeEnd, setDhcpRangeEnd] = useState("10.0.0.200");
  const [dhcpLeaseHours, setDhcpLeaseHours] = useState(24);
  const [dhcpGateway, setDhcpGateway] = useState("10.0.0.1");

  const handleSaveDhcpSettings = (e: React.FormEvent) => {
    e.preventDefault();
    setDhcpModalOpen(false);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.dhcp.range_start = dhcpRangeStart;
          cfg.dhcp.range_end = dhcpRangeEnd;
          cfg.dhcp.lease_time = `${dhcpLeaseHours}h`;
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .then(async (response) => {
          if (!response.ok) throw new Error(await response.text());
          setOperationError("");
        })
        .catch((error) => setOperationError(error instanceof Error ? error.message : "DHCP update failed"));
    }
  };

  const [deleteConfirmTarget, setDeleteConfirmTarget] = useState<{
    type: "wg" | "lease" | "pf";
    idOrIndex: string | number;
    name: string;
  } | null>(null);

  const handleConfirmDelete = () => {
    if (!deleteConfirmTarget) return;
    const { type, idOrIndex } = deleteConfirmTarget;

    let updatedLeases = staticLeases;
    let updatedForwards = portForwardRules;
    if (type === "wg") {
      const updated = wgPeers.filter((p) => p.id !== idOrIndex);
      setWgPeers(updated);
    } else if (type === "lease") {
      updatedLeases = staticLeases.filter((_, idx) => idx !== idOrIndex);
      setStaticLeases(updatedLeases);
    } else if (type === "pf") {
      updatedForwards = portForwardRules.filter((_, idx) => idx !== idOrIndex);
      setPortForwardRules(updatedForwards);
    }

    setDeleteConfirmTarget(null);

    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          if (type === "wg") {
            cfg.wireguard.peers = (cfg.wireguard?.peers ?? []).filter(
              (peer: { id: string }) => peer.id !== idOrIndex,
            );
          }
          if (type === "lease") {
            cfg.dhcp.static_leases = updatedLeases.map((lease, index) => ({
              id: `lease-${index + 1}`,
              hostname: lease.hostname,
              mac: lease.mac,
              ip_address: lease.ip,
            }));
          }
          if (type === "pf") {
            cfg.firewall.port_forwards = updatedForwards.map((rule, index) => ({
              id: `pf-${index + 1}`,
              name: rule.name,
              protocol: rule.proto.toLowerCase(),
              external_port: rule.extPort,
              internal_ip: rule.intIP,
              internal_port: rule.intPort,
              enabled: rule.enabled,
            }));
          }
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleAddWgPeer = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newWgPeerName || !newWgPeerIP || !newWgEndpoint || !apiConnected) return;
    setWireGuardSubmitting(true);
    setOperationError("");
    try {
      const response = await apiFetch("/api/v1/wireguard/peers", {
        method: "POST",
        body: JSON.stringify({
          name: newWgPeerName,
          client_ip_address: newWgPeerIP,
          server_endpoint: newWgEndpoint,
        }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error ?? "WireGuard peer provisioning failed");
      const newPeer = {
        id: body.peer.id as string,
        name: body.peer.name as string,
        ip: String(body.peer.client_ip ?? "").replace(/\/32$/, ""),
        traffic: "Configured",
        active: "Awaiting first handshake",
      };
      setWgPeers((current) => [...current, newPeer]);
      setWireGuardEnabled(true);
      setWireGuardProvisioning({
        peerName: newPeer.name,
        clientIP: String(body.peer.client_ip),
        clientConfig: String(body.client_config),
        qrCodeData: String(body.qr_code_data),
      });
      setNewWgPeerName("");
      setNewWgPeerIP("");
      setAddWgModalOpen(false);
      setQrOpen(true);
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : "WireGuard peer provisioning failed");
    } finally {
      setWireGuardSubmitting(false);
    }
  };

  const closeWireGuardProvisioning = () => {
    setQrOpen(false);
    setWireGuardProvisioning(null);
  };

  const downloadWireGuardConfig = () => {
    if (!wireGuardProvisioning) return;
    const blob = new Blob([wireGuardProvisioning.clientConfig], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `${wireGuardProvisioning.peerName.toLowerCase().replace(/[^a-z0-9]+/g, "-") || "wireguard-peer"}.conf`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  // Sync stateful firewall toggle with Go REST API
  const handleToggleStateful = (val: boolean) => {
    if (!val) {
      setOperationError("The stateful firewall is a mandatory security control and cannot be disabled.");
      return;
    }
    setStatefulRules(val);
    if (apiConnected) {
      apiFetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.firewall.stateful_firewall = val;
          return apiFetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch((err) => console.error("API update error:", err));
    }
  };

  const handleTogglePortForward = (index: number) => {
    const updated = portForwardRules.map((rule, itemIndex) =>
      itemIndex === index ? { ...rule, enabled: !rule.enabled } : rule
    );
    if (!apiConnected) {
      setPortForwardRules(updated);
      return;
    }
    apiFetch("/api/v1/config")
      .then((response) => {
        if (!response.ok) throw new Error("Could not load router configuration");
        return response.json();
      })
      .then((cfg) => {
        cfg.firewall.port_forwards = updated.map((rule, itemIndex) => ({
          id: `pf-${itemIndex + 1}`,
          name: rule.name,
          protocol: rule.proto.toLowerCase(),
          external_port: rule.extPort,
          internal_ip: rule.intIP,
          internal_port: rule.intPort,
          enabled: rule.enabled,
        }));
        return apiFetch("/api/v1/config", {
          method: "PUT",
          body: JSON.stringify(cfg),
        });
      })
      .then(async (response) => {
        if (!response.ok) throw new Error((await response.json().catch(() => ({}))).error ?? "Port-forward update failed");
        setPortForwardRules(updated);
        setOperationError("");
      })
      .catch((error) => setOperationError(error instanceof Error ? error.message : "Port-forward update failed"));
  };

  const handleAddStaticLease = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newLeaseHost || !newLeaseMAC || !newLeaseIP) return;
    const item = { hostname: newLeaseHost, mac: newLeaseMAC, ip: newLeaseIP };
    const updated = [...staticLeases, item];
    try {
      if (apiConnected) {
        const current = await apiFetch("/api/v1/config");
        if (!current.ok) throw new Error("Could not load router configuration");
        const cfg = await current.json();
        cfg.dhcp.static_leases = updated.map((lease, index) => ({
          id: `lease-${index + 1}`,
          hostname: lease.hostname,
          mac: lease.mac,
          ip_address: lease.ip,
        }));
        const response = await apiFetch("/api/v1/config", {
          method: "PUT",
          body: JSON.stringify(cfg),
        });
        if (!response.ok) throw new Error((await response.json().catch(() => ({}))).error ?? "Static lease update failed");
      }
      setStaticLeases(updated);
      setNewLeaseHost("");
      setNewLeaseMAC("");
      setNewLeaseIP("");
      setLeaseModalOpen(false);
      setOperationError("");
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : "Static lease update failed");
    }
  };

  const handleAddPortForward = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newPfName || !newPfExtPort || !newPfIntIP || !newPfIntPort) return;
    const item = {
      name: newPfName,
      proto: newPfProto.toUpperCase(),
      extPort: parseInt(newPfExtPort, 10),
      intIP: newPfIntIP,
      intPort: parseInt(newPfIntPort, 10),
      enabled: true,
    };
    const updated = [...portForwardRules, item];
    try {
      if (apiConnected) {
        const current = await apiFetch("/api/v1/config");
        if (!current.ok) throw new Error("Could not load router configuration");
        const cfg = await current.json();
        cfg.firewall.port_forwards = updated.map((rule, index) => ({
          id: `pf-${index + 1}`,
          name: rule.name,
          protocol: rule.proto.toLowerCase(),
          external_port: rule.extPort,
          internal_ip: rule.intIP,
          internal_port: rule.intPort,
          enabled: rule.enabled,
        }));
        const response = await apiFetch("/api/v1/config", {
          method: "PUT",
          body: JSON.stringify(cfg),
        });
        if (!response.ok) throw new Error((await response.json().catch(() => ({}))).error ?? "Port-forward update failed");
      }
      setPortForwardRules(updated);
      setNewPfName("");
      setNewPfExtPort("");
      setNewPfIntIP("");
      setNewPfIntPort("");
      setPfModalOpen(false);
      setOperationError("");
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : "Port-forward update failed");
    }
  };

  // Hydrate the dashboard from the canonical Go API. Preview data is used only
  // when the appliance API is unavailable. This effect stays below all state
  // declarations so React's compiler can safely track every setter it uses.
  useEffect(() => {
    const load = async () => {
      try {
        const [systemResponse, configResponse, snapshotsResponse, pendingResponse] = await Promise.all([
          apiFetch("/api/v1/system"),
          apiFetch("/api/v1/config"),
          apiFetch("/api/v1/snapshots"),
          apiFetch("/api/v1/transactions/pending"),
        ]);
        if (!systemResponse.ok || !configResponse.ok || !snapshotsResponse.ok || !pendingResponse.ok) {
          throw new Error("Router API unavailable");
        }
        const [system, cfg, snapshotPayload, pendingPayload] = await Promise.all([
          systemResponse.json(),
          configResponse.json(),
          snapshotsResponse.json(),
          pendingResponse.json(),
        ]);
        setSystemInfo(system);
        setStaticLeases((cfg.dhcp?.static_leases ?? []).map((lease: { hostname: string; mac: string; ip_address: string }) => ({
          hostname: lease.hostname,
          mac: lease.mac,
          ip: lease.ip_address,
        })));
        setPortForwardRules((cfg.firewall?.port_forwards ?? []).map((rule: {
          name: string; protocol: string; external_port: number; internal_ip: string; internal_port: number; enabled: boolean;
        }) => ({
          name: rule.name,
          proto: rule.protocol.toUpperCase(),
          extPort: rule.external_port,
          intIP: rule.internal_ip,
          intPort: rule.internal_port,
          enabled: rule.enabled,
        })));
        setStatefulRules(cfg.firewall?.stateful_firewall === true);
        setWireGuardEnabled(cfg.wireguard?.enabled === true);
        setWgPeers((cfg.wireguard?.peers ?? []).map((peer: {
          id: string; name: string; allowed_ips: string[]; enabled: boolean;
        }) => ({
          id: peer.id,
          name: peer.name,
          ip: peer.allowed_ips?.[0]?.replace(/\/32$/, "") ?? "",
          traffic: peer.enabled ? "Configured" : "Disabled",
          active: "Awaiting runtime telemetry",
        })));
        setCloudflareEnabled(cfg.cloudflare?.ddns_enabled === true || cfg.cloudflare?.tunnel_enabled === true);
        setDhcpRangeStart(cfg.dhcp?.range_start ?? "");
        setDhcpRangeEnd(cfg.dhcp?.range_end ?? "");
        setDhcpLeaseHours(Number.parseInt(cfg.dhcp?.lease_time ?? "24", 10) || 24);
        setDhcpGateway(cfg.lan?.ip_address ?? "");
        setDnsPrimary(cfg.dhcp?.dns_servers?.[0] ?? "");
        setDnsSecondary(cfg.dhcp?.dns_servers?.[1] ?? "");
        setDohEnabled(cfg.dhcp?.dns_enabled === true);
        setSquidEnabled(cfg.squid_proxy?.enabled === true);
        setSquidPort(cfg.squid_proxy?.port ?? 3128);
        setSquidUser(cfg.squid_proxy?.username ?? "");
        setSquidRestrictedIPs(cfg.squid_proxy?.restricted_ips ?? []);
        setAdguardEnabled(cfg.adguard?.enabled === true);
        setFilterDevices(cfg.adguard?.filter_devices ?? []);
        setWifiEnabled(cfg.wifi?.enabled === true);
        setWifiSSID(cfg.wifi?.ssid ?? "");
        setWifiBand(cfg.wifi?.band ?? "5ghz");
        setWifiChannel(String(cfg.wifi?.channel ?? 36));
        setWifiHideSSID(cfg.wifi?.hide_ssid === true);
        setQosEnabled(cfg.qos?.enabled === true);
        setQosAlgorithm(cfg.qos?.algorithm ?? "cake");
        setQosDown(String(cfg.qos?.download_limit_mbps ?? 100));
        setQosUp(String(cfg.qos?.upload_limit_mbps ?? 20));
        setCfConfig({
          domain: cfg.cloudflare?.domain ?? "",
          zoneId: cfg.cloudflare?.zone_id ?? "",
          apiToken: "",
          tunnelDomain: cfg.cloudflare?.domain ?? "",
        });
        setSnapshotsList((snapshotPayload.snapshots ?? []).map((snapshot: {
          id: string; revision: number; created_at: string; checksum: string;
        }) => ({
          id: snapshot.id,
          revision: snapshot.revision,
          label: "Configuration snapshot",
          time: new Date(snapshot.created_at).toLocaleString(),
          checksum: snapshot.checksum,
        })));
        setPendingConfirmationID(pendingPayload.pending === true ? pendingPayload.id : "");
        setApiConnected(true);
        setOperationError("");
      } catch {
        setApiConnected(false);
      }
    };
    void load();
  }, []);

  useEffect(() => {
    if (!apiConnected) return;
    let cancelled = false;
    const sample = async () => {
      try {
        const response = await apiFetch("/api/v1/system");
        if (!response.ok) return;
        const next = await response.json();
        if (cancelled) return;
        setSystemInfo(next);
        const rx = Number(next.runtime?.rx_bytes ?? 0);
        const tx = Number(next.runtime?.tx_bytes ?? 0);
        const at = Date.now();
        const previous = trafficPrevious.current;
        if (previous && at > previous.at && rx >= previous.rx && tx >= previous.tx) {
          const seconds = (at - previous.at) / 1000;
          const down = ((rx - previous.rx) * 8) / seconds / 1_000_000;
          const up = ((tx - previous.tx) * 8) / seconds / 1_000_000;
          setDownloadMbps(down);
          setUploadMbps(up);
          setTrafficDown((samples) => [...samples, down].slice(-32));
          setTrafficUp((samples) => [...samples, up].slice(-32));
        }
        trafficPrevious.current = { rx, tx, at };
      } catch {
        // The regular API availability banner handles prolonged failures.
      }
    };
    void sample();
    const timer = window.setInterval(() => void sample(), 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [apiConnected]);

  useEffect(() => {
    if (typeof window !== "undefined") {
      const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
      const initialTheme = prefersDark ? "dark" : "light";
      document.documentElement.dataset.theme = initialTheme;
    }
  }, []);

  useEffect(() => {
    const sectionIds = navItems.map(([, , id]) => id);
    const elements = sectionIds
      .map((id) => document.getElementById(id))
      .filter((el): el is HTMLElement => el !== null);

    if (elements.length === 0) return;

    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            setActiveSection(entry.target.id);
          }
        });
      },
      {
        rootMargin: "-20% 0px -60% 0px",
        threshold: 0,
      }
    );

    elements.forEach((el) => observer.observe(el));

    return () => observer.disconnect();
  }, []);

  const switchTheme = () => {
    const nextTheme = theme === "light" ? "dark" : "light";
    setTheme(nextTheme);
    document.documentElement.dataset.theme = nextTheme;
  };

  const closeMenu = () => setMenuOpen(false);
  const runtime = systemInfo.runtime ?? {};
  const cpuPercent = Math.max(0, Math.min(100, Math.round(runtime.cpu_load_percent ?? 0)));
  const memoryPercent = runtime.memory_total_bytes
    ? Math.round(((runtime.memory_used_bytes ?? 0) / runtime.memory_total_bytes) * 100)
    : 0;
  const diskPercent = runtime.disk_total_bytes
    ? Math.round(((runtime.disk_used_bytes ?? 0) / runtime.disk_total_bytes) * 100)
    : 0;

  return (
    <main className="app-shell">
      <aside className={`sidebar ${menuOpen ? "is-open" : ""}`}>
        <div className="brand-row">
          <div className="brand-mark brand-favicon-wrap" aria-hidden="true">
            {/* The static appliance has no image-optimization route. */}
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src="/favicon.svg" alt="Minimal Router logo" width={26} height={26} />
          </div>
          <div>
            <strong>Minimal Router</strong>
            <span>Home gateway</span>
          </div>
          <button
            className="sidebar-close"
            type="button"
            aria-label="Close navigation"
            onClick={closeMenu}
          >
            ×
          </button>
        </div>

        <nav className="side-nav" aria-label="Dashboard sections">
          {navItems.map(([number, label, id]) => (
            <a
              className={activeSection === id ? "active" : ""}
              href={`#${id}`}
              key={id}
              onClick={() => {
                setActiveSection(id);
                closeMenu();
              }}
            >
              <span>{number}</span>
              {label}
            </a>
          ))}
        </nav>


      </aside>

      {menuOpen && (
        <button
          className="menu-scrim"
          type="button"
          aria-label="Close navigation"
          onClick={closeMenu}
        />
      )}

      <div className="main-panel">
        <header className="topbar">
          <button
            className="menu-button"
            type="button"
            aria-label="Open navigation"
            onClick={() => setMenuOpen(true)}
          >
            <span />
            <span />
          </button>
          <div className="service-chips">
            <span className="chip ok" title="Stateful Packet Filtering (nftables): Inspects all network traffic and blocks unauthorized WAN access">
              <i className="status-dot" /> Firewall
            </span>
            <span className={`chip ${wireGuardEnabled ? "ok" : ""}`} title="WireGuard status from the committed router configuration">
              <i className="status-dot" /> WireGuard {wireGuardEnabled ? "" : "off"}
            </span>
            <span className="chip ok" title="Dynamic Host Configuration Protocol (dnsmasq): Automatically assigns IP addresses to home devices">
              <i className="status-dot" /> DHCP
            </span>
            <span className="chip ok" title="Local DNS forwarding through dnsmasq">
              <i className="status-dot" /> DNS
            </span>
            <span className={`chip ${cloudflareEnabled ? "ok" : ""}`} title="Cloudflare integration status from the committed router configuration">
              <i className="status-dot" /> Cloudflare {cloudflareEnabled ? "" : "off"}
            </span>
          </div>
          <div className="top-actions">
            <div className="font-scale-control" aria-label="Font size control">
              <button
                type="button"
                className="scale-btn small"
                onClick={decreaseFontScale}
                title="Smanji font (a)"
              >
                a
              </button>
              <button
                type="button"
                className="scale-btn reset"
                onClick={resetFontScale}
                title="Resetuj veličinu fonta (100%)"
              >
                {fontScale}%
              </button>
              <button
                type="button"
                className="scale-btn large"
                onClick={increaseFontScale}
                title="Povećaj font (A)"
              >
                A
              </button>
            </div>
            <button
              className="icon-button"
              type="button"
              onClick={switchTheme}
              aria-label={`Switch to ${theme === "light" ? "dark" : "light"} mode`}
            >
              {theme === "light" ? "◐" : "◑"}
            </button>
            <button
              className="button secondary"
              type="button"
              onClick={() => setWizardOpen(true)}
              disabled={apiConnected}
              title={apiConnected ? "Initial setup has already been completed" : "Open the offline design preview"}
              style={{ fontSize: "13px", padding: "6px 12px", borderRadius: "10px", height: "40px" }}
            >
              {apiConnected ? "Setup complete" : "Setup preview"}
            </button>
            <button
              className="avatar-button"
              type="button"
              aria-label="Administrator profile"
              onClick={() => setProfileModalOpen(true)}
              title="Administrator Profil & Sigurnost"
            >
              VP
            </button>
          </div>
        </header>
        {operationError && (
          <div className="operation-error" role="alert">
            <span>{operationError}</span>
            <button type="button" aria-label="Dismiss error" onClick={() => setOperationError("")}>×</button>
          </div>
        )}
        {apiConnected && runtime.available === false && (
          <div className="preview-runtime-banner" role="status">
            macOS control-plane preview — configuration is stored locally, but Linux networking services are not applied.
          </div>
        )}
        {pendingConfirmationID && (
          <div className="confirmation-banner" role="status">
            <span>Verify connectivity through the intended LAN or WireGuard path. Automatic rollback occurs after 90 seconds.</span>
            <button className="button primary" type="button" onClick={() => void handleConfirmPendingTransaction()}>
              Confirm connectivity
            </button>
          </div>
        )}

        <div className="content">
          <section className="internet-card card" aria-labelledby="internet-title">
            <div className="internet-head">
              <div>
                <div className="section-label">
                  <span className="status-dot" />
                  Internet
                </div>
                <h2 id="internet-title">{systemInfo.status === "Connected" ? "Online and verified" : "WAN not connected"}</h2>
                <div className="internet-meta" style={{ display: "flex", gap: "16px", flexWrap: "wrap", alignItems: "center" }}>
                  <span>Public IP <code>{runtime.public_ip || "Unavailable"}</code></span>
                  <span>Uptime {formatUptime(runtime.uptime_seconds)}</span>
                  <span>MTU {systemInfo.mtu ?? "—"}</span>
                  <span style={{ borderLeft: "1px solid var(--separator)", paddingLeft: "12px" }}>
                    Last snapshot <strong>{snapshotsList.length > 0 ? `Revision ${snapshotsList[0].revision} (${snapshotsList[0].time})` : "No snapshot"}</strong>
                  </span>
                  <span style={{ borderLeft: "1px solid var(--separator)", paddingLeft: "12px", color: systemInfo.update_trust_configured ? "#34C759" : "var(--warning)", fontWeight: 600 }}>
                    {systemInfo.update_trust_configured ? "✓ Signed update trust configured" : "Signed updates disabled"}
                  </span>
                </div>
              </div>
              <div className={`pppoe-pill ${systemInfo.status === "Connected" ? "" : "is-offline"}`}>
                <span className="status-dot" />
                {systemInfo.status === "Connected" ? "PPPoE connected" : "PPPoE offline"}
              </div>
            </div>

            <div className="traffic-summary">
              <div className="traffic-value">
                <span className="traffic-arrow download-arrow">↓</span>
                <div>
                  <span>Download now</span>
                  <strong>{downloadMbps.toFixed(2)} <small>Mbps</small></strong>
                </div>
              </div>
              <div className="traffic-value">
                <span className="traffic-arrow upload-arrow">↑</span>
                <div>
                  <span>Upload now</span>
                  <strong>{uploadMbps.toFixed(2)} <small>Mbps</small></strong>
                </div>
              </div>
              <div className="latency-value">
                <span>Interface totals</span>
                <strong>{formatBytes(runtime.rx_bytes)}</strong>
                <em>Sent {formatBytes(runtime.tx_bytes)}</em>
              </div>
            </div>

            <div className="chart-wrap">
              <div className="chart-head">
                <div>
                  <strong>Network traffic</strong>
                  <span>Live samples · 5 second interval</span>
                </div>
                <div className="chart-legend" aria-hidden="true">
                  <span><i className="legend-download" /> Download</span>
                  <span><i className="legend-upload" /> Upload</span>
                </div>
              </div>
              <TrafficChart theme={theme} download={trafficDown} upload={trafficUp} />
              <div className="chart-axis" aria-hidden="true">
                <span>Older</span>
                <span />
                <span />
                <span />
                <span>Now</span>
              </div>
            </div>
          </section>

          <section className="section-block" id="system">
            <div className="section-heading">
              <div>
                <p className="eyebrow">System</p>
                <h2>Quietly doing its job.</h2>
              </div>
              <span className="quiet-meta">
                {runtime.os || "Runtime unavailable"} · {runtime.architecture || "unknown"}
                {runtime.temperature_c ? ` · ${runtime.temperature_c.toFixed(0)}°C` : ""}
              </span>
            </div>

            <div className="system-grid">
              <article className="card resource-card">
                <div className="resource-top">
                  <span>CPU</span>
                  <strong>{cpuPercent}%</strong>
                </div>
                <Meter label="CPU usage" value={cpuPercent} detail={`${runtime.cpu_count ?? 0} cores`} />
                <p>Load average {runtime.load_average?.length ? runtime.load_average.map((value) => value.toFixed(2)).join(" · ") : "unavailable"}</p>
              </article>

              <article className="card resource-card">
                <div className="resource-top">
                  <span>Memory</span>
                  <strong>{formatBytes(runtime.memory_used_bytes)}</strong>
                </div>
                <Meter label="Memory usage" value={memoryPercent} detail={`${formatBytes(runtime.memory_used_bytes)} of ${formatBytes(runtime.memory_total_bytes)}`} tone="violet" />
                <p>{formatBytes(Math.max(0, (runtime.memory_total_bytes ?? 0) - (runtime.memory_used_bytes ?? 0)))} available</p>
              </article>

              <article className="card resource-card">
                <div className="resource-top">
                  <span>Disk</span>
                  <strong>{formatBytes(runtime.disk_used_bytes)}</strong>
                </div>
                <Meter label="Disk usage" value={diskPercent} detail={`${formatBytes(runtime.disk_used_bytes)} of ${formatBytes(runtime.disk_total_bytes)}`} tone="green" />
                <p>{formatBytes(Math.max(0, (runtime.disk_total_bytes ?? 0) - (runtime.disk_used_bytes ?? 0)))} available</p>
              </article>
            </div>

            <div className="facts-strip card">
              <div>
                <span>Router uptime</span>
                <strong>{formatUptime(runtime.uptime_seconds)}</strong>
              </div>
              <div>
                <span>WAN</span>
                <strong>{systemInfo.status ?? "Unavailable"} · {systemInfo.wan_iface || "no interface"}</strong>
              </div>
              <div>
                <span>LAN</span>
                <strong>{systemInfo.lan_ip || "Unavailable"}</strong>
              </div>
              <div>
                <span>DNS</span>
                <strong>dnsmasq · configured upstreams</strong>
              </div>
            </div>
          </section>

          <section className="section-block" id="lan">
            <div className="section-heading">
              <div>
                <p className="eyebrow">LAN & DHCP</p>
                <h2>{staticLeases.length} reserved DHCP addresses.</h2>
              </div>
              <div style={{ display: "flex", gap: "10px" }}>
                <button
                  className="button secondary"
                  type="button"
                  onClick={() => setDnsModalOpen(true)}
                >
                  Configure DNS
                </button>
                <button
                  className="button secondary"
                  type="button"
                  onClick={() => setDhcpModalOpen(true)}
                >
                  Manage DHCP
                </button>
              </div>
            </div>

            <div
              className="card"
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                padding: "18px 22px",
                borderRadius: "18px",
                marginBottom: "24px",
                gap: "24px",
                flexWrap: "wrap",
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: "14px", flexWrap: "wrap" }}>
                <span className="status-label success" style={{ padding: "6px 12px", fontSize: "12px", fontWeight: 650, borderRadius: "20px" }}>
                  <i className="status-dot" /> DNS configured
                </span>
                <div>
                  <strong style={{ fontSize: "14px", fontWeight: 700, color: "var(--text-primary)", display: "block" }}>
                    {dnsProvider === "cloudflare" ? "Cloudflare DNS" : dnsProvider === "quad9" ? "Quad9 Malware Block" : dnsProvider === "adguard" ? "AdGuard AdBlock" : dnsProvider === "google" ? "Google DNS" : "Custom DNS"}
                    <span style={{ fontWeight: 400, color: "var(--text-secondary)", marginLeft: "8px" }}>({dnsPrimary}, {dnsSecondary})</span>
                  </strong>
                </div>
                <span style={{ fontSize: "11px", background: "var(--surface-muted)", color: "var(--text-secondary)", padding: "4px 10px", borderRadius: "8px", fontWeight: 650 }}>
                  Plain DNS forwarding
                </span>
              </div>
              <p style={{ margin: 0, fontSize: "12.5px", color: "var(--text-secondary)", maxWidth: "480px", lineHeight: 1.45, borderLeft: "1px solid var(--separator)", paddingLeft: "20px" }}>
                dnsmasq provides LAN DNS and forwards queries to these upstream resolvers. DNS-over-HTTPS is not implemented in this build.
              </p>
            </div>

            <div className="two-column wide-left">
              <article className="card table-card">
                <div className="card-title-row">
                  <div>
                    <h3>Static DHCP reservations</h3>
                    <p>{staticLeases.length} configured · live lease telemetry not collected</p>
                  </div>
                </div>
                <div className="table-scroll">
                  <table>
                    <caption className="sr-only">Configured static DHCP reservations</caption>
                    <thead>
                      <tr>
                        <th>Device</th>
                        <th>IP address</th>
                        <th>Type</th>
                        <th>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {staticLeases.length === 0 && (
                        <tr><td colSpan={4}>No static reservations configured.</td></tr>
                      )}
                      {staticLeases.map((lease, idx) => (
                        <tr key={idx}>
                          <td><strong>{lease.hostname}</strong><span>{lease.mac}</span></td>
                          <td><code>{lease.ip}</code></td>
                          <td><span className="micro-status static"><i /> Static</span></td>
                          <td>
                            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                              <span>Reserved</span>
                              <button
                                type="button"
                                onClick={() => setDeleteConfirmTarget({ type: "lease", idOrIndex: idx, name: lease.hostname })}
                                style={{
                                  border: "none",
                                  background: "#FF3B3015",
                                  color: "#FF3B30",
                                  width: "24px",
                                  height: "24px",
                                  borderRadius: "50%",
                                  cursor: "pointer",
                                  fontWeight: "bold",
                                  fontSize: "12px",
                                  display: "grid",
                                  placeItems: "center",
                                }}
                                title="Izbriši statički lease"
                              >
                                ✕
                              </button>
                            </div>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </article>

              <aside className="card lan-summary">
                <div className="summary-icon">{staticLeases.length}</div>
                <h3>DHCP configuration</h3>
                <p>Active dnsmasq leases will be added after runtime lease telemetry is implemented.</p>
                <div className="summary-list">
                  <div><span>DHCP range</span><code>{dhcpRangeStart}–{dhcpRangeEnd.split('.').pop()}</code></div>
                  <div><span>Lease time</span><strong>{dhcpLeaseHours} hours</strong></div>
                  <div><span>Static addresses</span><strong>{staticLeases.length} reserved</strong></div>
                  <div><span>Gateway</span><code>{dhcpGateway}</code></div>
                </div>
                <button
                  className="button primary full"
                  type="button"
                  onClick={() => setLeaseModalOpen(true)}
                >
                  Add static lease
                </button>
              </aside>
            </div>
          </section>

          <section className="section-block" id="firewall">
            <div className="section-heading">
              <div>
                <p className="eyebrow">Firewall</p>
                <h2>Protected by default.</h2>
              </div>
              <div className="status-label success">
                <span className="status-dot" />
                Active
              </div>
            </div>

            <div className="two-column">
              <article className="card settings-card">
                <div className="card-title-row">
                  <div>
                    <h3>Internet protection</h3>
                    <p>Unsolicited WAN traffic is blocked.</p>
                  </div>
                  <span className="shield-mark" aria-hidden="true">✓</span>
                </div>
                <div className="setting-row">
                  <div>
                    <strong>Stateful firewall</strong>
                    <span>Allow established connections and block invalid traffic</span>
                  </div>
                  <Toggle
                    checked={statefulRules}
                    onChange={() => handleToggleStateful(!statefulRules)}
                    label="Stateful firewall"
                  />
                </div>
                <div className="setting-row">
                  <div>
                    <strong>NAT masquerade</strong>
                    <span>Share the public connection with LAN devices</span>
                  </div>
                  <span className="small-status">Enabled</span>
                </div>
                <div className="setting-row">
                  <div>
                    <strong>WAN management</strong>
                    <span>Dashboard and MCP are available from LAN or an authenticated WireGuard tunnel</span>
                  </div>
                  <span className="small-status neutral">Blocked</span>
                </div>
              </article>

              <article className="card settings-card">
                <div className="card-title-row">
                  <div>
                    <h3>Port forwarding</h3>
                    <p>Disabled. WireGuard is the only permitted external entry point.</p>
                  </div>
                  <button
                    className="quiet-button"
                    type="button"
                    disabled={apiConnected}
                    title={apiConnected ? "WAN port forwarding is disabled by the secure appliance profile" : undefined}
                    onClick={() => setPfModalOpen(true)}
                  >
                    {apiConnected ? "Locked" : "Add rule"}
                  </button>
                </div>
                {portForwardRules.map((rule, idx) => (
                  <div className="forward-rule" key={idx}>
                    <div className="port-badge">{rule.extPort}</div>
                    <div>
                      <strong>{rule.name}</strong>
                      <span>{rule.proto} · {rule.intIP}:{rule.intPort}</span>
                    </div>
                    <Toggle
                      checked={rule.enabled}
                      onChange={() => handleTogglePortForward(idx)}
                      label={`${rule.name} port forward`}
                      disabled={apiConnected}
                    />
                  </div>
                ))}
                <div className="firewall-stat">
                  <div><strong>Deny</strong><span>Default WAN input policy</span></div>
                  <div><strong>{portForwardRules.length}</strong><span>Port forwards enabled</span></div>
                </div>
              </article>
            </div>

            <div className="card" style={{ marginTop: "24px", padding: "20px" }}>
              <div className="card-title-row">
                <div>
                  <h3 style={{ margin: 0, fontSize: "16px", fontWeight: 700 }}>QoS & Bufferbloat Prevention (CAKE)</h3>
                  <p style={{ margin: "4px 0 0", fontSize: "12px", color: "var(--text-secondary)" }}>
                    Traffic shaping prevents ping spikes during heavy downloads so gaming & video calls stay smooth
                  </p>
                </div>
                <button
                  className="button secondary"
                  type="button"
                  onClick={() => setQosModalOpen(true)}
                  disabled={!apiConnected}
                  style={{ fontSize: "13px" }}
                >
                  Configure QoS
                </button>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginTop: "16px", paddingTop: "16px", borderTop: "1px solid var(--separator)" }}>
                <div>
                  <span className="quiet-meta">
                    Status: <strong style={{ color: qosEnabled ? "#34C759" : "var(--text-tertiary)" }}>{qosEnabled ? "Active (CAKE Shaping ON)" : "Disabled (Default)"}</strong>
                  </span>
                </div>
                <div style={{ display: "flex", gap: "24px" }}>
                  <div><span style={{ fontSize: "12px", color: "var(--text-secondary)" }}>Download Cap:</span> <strong style={{ fontSize: "14px" }}>{qosDown} Mbps</strong></div>
                  <div><span style={{ fontSize: "12px", color: "var(--text-secondary)" }}>Upload Cap:</span> <strong style={{ fontSize: "14px" }}>{qosUp} Mbps</strong></div>
                </div>
              </div>
            </div>
          </section>

          <section className="section-block" id="wireguard">
            <div className="section-heading">
              <div>
                <p className="eyebrow">WireGuard</p>
                <h2>Private access, anywhere.</h2>
              </div>
              <button
                className="button primary"
                type="button"
                onClick={() => setQrOpen(true)}
                disabled={!wireGuardProvisioning}
                title={wireGuardProvisioning ? "Show the latest one-time client configuration" : "Add a peer to generate a client QR code"}
              >
                {wireGuardProvisioning ? "Show latest QR code" : "Add a peer first"}
              </button>
            </div>

            <div className="two-column wide-left">
              <article className="card wireguard-hero">
                <div className="wireguard-status">
                  <div className="wg-mark" aria-hidden="true">W</div>
                  <div>
                    <span>Interface wg0</span>
                    <h3>{wireGuardEnabled ? "Running normally" : "Disabled"}</h3>
                    <p><code>10.8.0.1/24</code> · UDP 51820 · {wgPeers.length} peers</p>
                  </div>
                  <span className={`status-label ${wireGuardEnabled ? "success" : ""}`}><i className="status-dot" /> {wireGuardEnabled ? "Active" : "Off"}</span>
                </div>
                <div className="peer-list">
                  {wgPeers.map((peer) => (
                    <div className="peer-row" key={peer.id} style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <div style={{ display: "flex", gap: "12px", alignItems: "center" }}>
                        <div className="peer-avatar">{peer.name.substring(0, 2).toUpperCase()}</div>
                        <div><strong>{peer.name}</strong><span>{peer.ip} · latest handshake {peer.active}</span></div>
                      </div>
                      <div style={{ display: "flex", gap: "16px", alignItems: "center" }}>
                        <div className="peer-traffic"><strong>{peer.traffic}</strong><span>Transfer telemetry not collected</span></div>
                        <button
                          type="button"
                          onClick={() => setDeleteConfirmTarget({ type: "wg", idOrIndex: peer.id, name: peer.name })}
                          style={{
                            border: "none",
                            background: "#FF3B3015",
                            color: "#FF3B30",
                            width: "28px",
                            height: "28px",
                            borderRadius: "50%",
                            cursor: "pointer",
                            fontWeight: "bold",
                            fontSize: "14px",
                            display: "grid",
                            placeItems: "center",
                          }}
                          title="Izbriši uređaj / peer"
                        >
                          ✕
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              </article>

              <aside className="card wireguard-summary">
                <span className="mini-label">Configured peers</span>
                <strong>{wgPeers.length}</strong>
                <p>Private keys are returned once and never stored by the router.</p>
                <button
                  className="button secondary full"
                  type="button"
                  onClick={() => setAddWgModalOpen(true)}
                  disabled={!apiConnected}
                  title={!apiConnected ? "Connect to the router API to provision a peer" : undefined}
                >
                  {apiConnected ? "+ Add WireGuard Peer" : "Router API required"}
                </button>
              </aside>
            </div>
          </section>

          <section className="section-block" id="cloudflare">
            <div className="section-heading">
              <div>
                <p className="eyebrow">Cloudflare</p>
                <h2>Your home, reliably reachable.</h2>
              </div>
              <button
                className="button secondary"
                type="button"
                onClick={() => setCfModalOpen(true)}
                disabled={!apiConnected}
              >
                Configure Cloudflare
              </button>
            </div>

            <div className="cloud-grid">
              <article
                className="card cloud-card"
                onClick={() => setDdnsModalOpen(true)}
                style={{ cursor: "pointer" }}
              >
                <div className="cloud-icon" aria-hidden="true">DD</div>
                <div>
                  <div className="card-title-row">
                    <div>
                      <h3>Dynamic DNS</h3>
                      <p>{apiConnected ? "Cloudflare DNS record updater" : "Offline design preview."}</p>
                    </div>
                    <span className="status-label"><i className="status-dot" /> {cfConfig.domain ? "Configured" : "Not configured"}</span>
                  </div>
                  <div className="cloud-host">
                    <span>Hostname</span>
                    <code>{cfConfig.domain}</code>
                  </div>
                  <div className="cloud-meta">
                    <span>No runtime status</span>
                    <span>Fail closed</span>
                  </div>
                </div>
              </article>

              <article className="card cloud-card">
                <div className="cloud-icon tunnel" aria-hidden="true">CT</div>
                <div>
                  <div className="card-title-row">
                    <div>
                      <h3>Cloudflare Tunnel</h3>
                      <p>Secure tunnel to Cloudflare edge network.</p>
                    </div>
                    <span className="status-label"><i className="status-dot" /> {cfConfig.tunnelDomain ? "Configured" : "Not configured"}</span>
                  </div>
                  <div className="cloud-host">
                    <span>Tunnel</span>
                    <code>minimalrouter-home</code>
                  </div>
                  <div className="cloud-meta">
                    <span>0 connections</span>
                    <span>No public service exposure</span>
                  </div>
                </div>
              </article>
            </div>
          </section>

          <section className="section-block" id="squid">
            <div className="section-heading">
              <div>
                <p className="eyebrow">Squid Proxy & Access Control</p>
                <h2>Non-caching HTTP/HTTPS Forward Proxy</h2>
              </div>
              <div style={{ display: "flex", gap: "10px", alignItems: "center" }}>
                <span className="quiet-meta">
                  Status: <strong style={{ color: squidEnabled ? "#34C759" : "var(--text-tertiary)" }}>{squidEnabled ? "Active (Port 3128)" : "Disabled (Default)"}</strong>
                </span>
                <button
                  className="button secondary"
                  type="button"
                  onClick={() => handleToggleSquid(!squidEnabled)}
                  style={{ fontSize: "13px" }}
                >
                  {squidEnabled ? "Disable Squid" : "Enable Squid"}
                </button>
              </div>
            </div>

            <article className="card table-card">
              <div className="card-title-row">
                <div>
                  <h3>Restricted IP Alias Group</h3>
                  <p>Devices in this IP Alias are blocked from direct WAN access and must authenticate via Squid Proxy</p>
                </div>
                <div style={{ display: "flex", gap: "16px", alignItems: "center" }}>
                  <button
                    className="quiet-button"
                    type="button"
                    onClick={() => setSquidCredsModalOpen(true)}
                    style={{ color: "#0071E3", fontWeight: 650 }}
                  >
                    🔑 Set User/Pass for Squid
                  </button>
                  <button
                    className="quiet-button"
                    type="button"
                    onClick={() => setAddRestrictedModalOpen(true)}
                    style={{ color: "#0071E3", fontWeight: 650 }}
                  >
                    + Add Restricted IP
                  </button>
                </div>
              </div>
              <div className="table-scroll">
                <table>
                  <caption className="sr-only">Restricted IP Alias list</caption>
                  <thead>
                    <tr>
                      <th>Host</th>
                      <th>IP Address</th>
                      <th>Direct WAN Access</th>
                      <th>Status</th>
                      <th>Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {squidRestrictedIPs.length === 0 ? (
                      <tr>
                        <td colSpan={5} style={{ textAlign: "center", color: "var(--text-tertiary)", padding: "20px" }}>
                          No restricted IPs defined. All LAN devices have direct WAN access.
                        </td>
                      </tr>
                    ) : (
                      squidRestrictedIPs.map((item, idx) => (
                        <tr key={idx}>
                          <td><strong style={{ fontSize: "13px", color: "var(--text-primary)" }}>{item.hostname || "Device"}</strong></td>
                          <td><code>{item.ip_address}</code></td>
                          <td>
                            {item.enabled ? (
                              <span style={{ fontSize: "11px", background: "#FF3B3015", color: "#FF3B30", padding: "3px 8px", borderRadius: "6px", fontWeight: 600 }}>
                                🚫 Dropped (Blocked)
                              </span>
                            ) : (
                              <span style={{ fontSize: "11px", background: "#34C75915", color: "#34C759", padding: "3px 8px", borderRadius: "6px", fontWeight: 600 }}>
                                ✓ Allowed (Bypassed)
                              </span>
                            )}
                          </td>
                          <td>
                            <label style={{ display: "inline-flex", alignItems: "center", gap: "6px", cursor: "pointer", fontSize: "12px", fontWeight: 600 }}>
                              <input
                                type="checkbox"
                                checked={item.enabled}
                                onChange={() => handleToggleRestrictedIPItem(item.ip_address)}
                                style={{ width: "16px", height: "16px", cursor: "pointer" }}
                              />
                              {item.enabled ? "Active" : "Disabled"}
                            </label>
                          </td>
                          <td>
                            <button
                              type="button"
                              onClick={() => handleRemoveRestrictedIP(item.ip_address)}
                              style={{
                                border: "none",
                                background: "#FF3B3015",
                                color: "#FF3B30",
                                width: "24px",
                                height: "24px",
                                borderRadius: "50%",
                                cursor: "pointer",
                                fontWeight: "bold",
                                fontSize: "12px",
                                display: "grid",
                                placeItems: "center",
                              }}
                              title="Remove IP from restricted list"
                            >
                              ✕
                            </button>
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </article>
          </section>

          <section className="section-block" id="adguard">
            <div className="section-heading">
              <div>
                <p className="eyebrow">AdGuard Home & Content Filter</p>
                <h2>DNS Sinkhole & Per-Device Service Blocking</h2>
              </div>
              <div style={{ display: "flex", gap: "10px", alignItems: "center" }}>
                <span className="quiet-meta">
                  Status: <strong style={{ color: adguardEnabled ? "#34C759" : "var(--text-tertiary)" }}>{adguardEnabled ? "Active (AdBlock ON)" : "Disabled (Default)"}</strong>
                </span>
                <button
                  className="button secondary"
                  type="button"
                  onClick={handleUpdateBlocklist}
                  disabled={!apiConnected}
                  style={{ fontSize: "13px" }}
                >
                  Update Blocklist
                </button>
                <button
                  className="button secondary"
                  type="button"
                  onClick={() => handleToggleAdGuard(!adguardEnabled)}
                  disabled={!apiConnected}
                  style={{ fontSize: "13px" }}
                >
                  {adguardEnabled ? "Disable Filter" : "Enable Filter"}
                </button>
              </div>
            </div>

            <article className="card table-card">
              <div className="card-title-row">
                <div>
                  <h3>Target Devices & Blocked Services</h3>
                  <p>Selectively block YouTube, TikTok, Facebook, Adult or Gaming services per device IP address</p>
                </div>
                <button
                  className="quiet-button"
                  type="button"
                  onClick={() => setAddFilterModalOpen(true)}
                  style={{ color: "#0071E3", fontWeight: 650 }}
                >
                  + Add Filtered Device
                </button>
              </div>
              <div className="table-scroll">
                <table>
                  <caption className="sr-only">AdGuard Device Filter list</caption>
                  <thead>
                    <tr>
                      <th>Host</th>
                      <th>IP Address</th>
                      <th>Blocked Services</th>
                      <th>Status</th>
                      <th>Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filterDevices.length === 0 ? (
                      <tr>
                        <td colSpan={5} style={{ textAlign: "center", color: "var(--text-tertiary)", padding: "20px" }}>
                          No device filters added. All LAN devices have unrestricted access.
                        </td>
                      </tr>
                    ) : (
                      filterDevices.map((item) => (
                        <tr key={item.id}>
                          <td><strong style={{ fontSize: "13px", color: "var(--text-primary)" }}>{item.hostname}</strong></td>
                          <td><code>{item.ip_address}</code></td>
                          <td>
                            <div style={{ display: "flex", gap: "6px", flexWrap: "wrap" }}>
                              {item.blocked_services.includes("youtube") && (
                                <span style={{ fontSize: "11px", background: "#FF3B3015", color: "#FF3B30", padding: "2px 6px", borderRadius: "4px", fontWeight: 600 }}>
                                  YouTube
                                </span>
                              )}
                              {item.blocked_services.includes("tiktok") && (
                                <span style={{ fontSize: "11px", background: "#00000015", color: "var(--text-primary)", padding: "2px 6px", borderRadius: "4px", fontWeight: 600 }}>
                                  TikTok
                                </span>
                              )}
                              {item.blocked_services.includes("facebook") && (
                                <span style={{ fontSize: "11px", background: "#0071E315", color: "#0071E3", padding: "2px 6px", borderRadius: "4px", fontWeight: 600 }}>
                                  Facebook/IG
                                </span>
                              )}
                              {item.blocked_services.includes("adult") && (
                                <span style={{ fontSize: "11px", background: "#FF950015", color: "#FF9500", padding: "2px 6px", borderRadius: "4px", fontWeight: 600 }}>
                                  Adult
                                </span>
                              )}
                              {item.blocked_services.includes("gaming") && (
                                <span style={{ fontSize: "11px", background: "#AF52DE15", color: "#AF52DE", padding: "2px 6px", borderRadius: "4px", fontWeight: 600 }}>
                                  Gaming
                                </span>
                              )}
                            </div>
                          </td>
                          <td>
                            <label style={{ display: "inline-flex", alignItems: "center", gap: "6px", cursor: "pointer", fontSize: "12px", fontWeight: 600 }}>
                              <input
                                type="checkbox"
                                checked={item.enabled}
                                onChange={() => handleToggleFilterDevice(item.id)}
                                style={{ width: "16px", height: "16px", cursor: "pointer" }}
                              />
                              {item.enabled ? "Active" : "Disabled"}
                            </label>
                          </td>
                          <td>
                            <button
                              type="button"
                              onClick={() => handleRemoveFilterDevice(item.id)}
                              style={{
                                border: "none",
                                background: "#FF3B3015",
                                color: "#FF3B30",
                                width: "24px",
                                height: "24px",
                                borderRadius: "50%",
                                cursor: "pointer",
                                fontWeight: "bold",
                                fontSize: "12px",
                                display: "grid",
                                placeItems: "center",
                              }}
                              title="Remove filter rule for this device"
                            >
                              ✕
                            </button>
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </article>
          </section>

          <section className="section-block" id="wifi">
            <div className="section-heading">
              <div>
                <p className="eyebrow">Wi-Fi Access Point</p>
                <h2>Wireless LAN & WPA2/WPA3 Security</h2>
              </div>
              <div style={{ display: "flex", gap: "10px", alignItems: "center" }}>
                <span className="quiet-meta">
                  Status: <strong style={{ color: wifiEnabled ? "#34C759" : "var(--text-tertiary)" }}>{wifiEnabled ? "Active (Broadcasting)" : "Disabled (Default)"}</strong>
                </span>
                <button
                  className="button secondary"
                  type="button"
                  onClick={() => setWifiModalOpen(true)}
                  disabled={!apiConnected}
                  style={{ fontSize: "13px" }}
                >
                  Configure Wi-Fi
                </button>
              </div>
            </div>

            <article className="card" style={{ padding: "20px" }}>
              <div className="setting-row" style={{ paddingBottom: "16px", borderBottom: "1px solid var(--separator)" }}>
                <div>
                  <strong style={{ fontSize: "15px" }}>Enable Wireless Access Point (hostapd)</strong>
                  <span>Broadcast Wi-Fi network for phones, laptops, and smart home devices</span>
                </div>
                <Toggle
                  checked={wifiEnabled}
                  onChange={() => {
                    const nextState = !wifiEnabled;
                    setWifiEnabled(nextState);
                    if (apiConnected) {
                      apiFetch("/api/v1/config")
                        .then((res) => res.json())
                        .then((cfg) => {
                          cfg.wifi = {
                            ...cfg.wifi,
                            enabled: nextState,
                          };
                          return apiFetch("/api/v1/config", {
                            method: "PUT",
                            headers: { "Content-Type": "application/json" },
                            body: JSON.stringify(cfg),
                          });
                        })
                        .catch(console.error);
                    }
                  }}
                  label="Enable Wi-Fi Access Point"
                />
              </div>

              <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))", gap: "20px", marginTop: "20px" }}>
                <div>
                  <span style={{ fontSize: "12px", color: "var(--text-secondary)", display: "block", marginBottom: "4px" }}>Network Name (SSID)</span>
                  <strong style={{ fontSize: "15px", color: "var(--text-primary)" }}>{wifiSSID}</strong>
                </div>
                <div>
                  <span style={{ fontSize: "12px", color: "var(--text-secondary)", display: "block", marginBottom: "4px" }}>Frequency Band</span>
                  <strong style={{ fontSize: "15px", color: "var(--text-primary)" }}>{wifiBand === "5ghz" ? "5 GHz (802.11ac High Speed)" : "2.4 GHz (802.11n Long Range)"}</strong>
                </div>
                <div>
                  <span style={{ fontSize: "12px", color: "var(--text-secondary)", display: "block", marginBottom: "4px" }}>Wi-Fi Channel</span>
                  <strong style={{ fontSize: "15px", color: "var(--text-primary)" }}>Channel {wifiChannel}</strong>
                </div>
                <div>
                  <span style={{ fontSize: "12px", color: "var(--text-secondary)", display: "block", marginBottom: "4px" }}>Broadcast Mode</span>
                  <strong style={{ fontSize: "15px", color: "var(--text-primary)" }}>{wifiHideSSID ? "Hidden SSID" : "Visible Broadcast"}</strong>
                </div>
              </div>
            </article>
          </section>

          <section className="section-block" id="recovery">
            <div className="section-heading">
              <div>
                <p className="eyebrow">Recovery & updates</p>
                <h2>Changes stay reversible.</h2>
              </div>
            </div>

            <div className="recovery-grid">
              <article className="card recovery-card snapshot-card">
                <span className="recovery-index">01</span>
                <div>
                  <span className="mini-label">Latest snapshot</span>
                  <h3 style={{ fontSize: "17px", fontWeight: 700, margin: "6px 0 4px" }}>
                    {snapshotsList.length > 0 ? `Revision ${snapshotsList[0].revision} (${snapshotsList[0].time})` : "No snapshot yet"}
                  </h3>
                  <p style={{ color: "var(--text-secondary)", fontSize: "13px", margin: 0 }}>
                    {snapshotsList.length > 0 ? snapshotsList[0].label : "Create a checksummed snapshot before major changes."}
                  </p>
                  {snapshotSuccessMsg && (
                    <div style={{ color: "#34C759", fontSize: "12px", fontWeight: 600, marginTop: "6px" }}>
                      {snapshotSuccessMsg}
                    </div>
                  )}
                </div>
                <div className="recovery-actions" style={{ marginTop: "auto", paddingTop: "20px", display: "flex", gap: "10px", flexWrap: "wrap", width: "100%" }}>
                  <button
                    className="button primary"
                    type="button"
                    onClick={() => void handleMakeSnapshot()}
                    disabled={!apiConnected}
                    style={{ flex: 1, whiteSpace: "nowrap" }}
                  >
                    + Make snapshot
                  </button>
                  <button
                    className="button secondary"
                    type="button"
                    onClick={() => setSnapshotsModalOpen(true)}
                    style={{ flex: 1, whiteSpace: "nowrap" }}
                  >
                    View snapshots
                  </button>
                </div>
              </article>
              <article className="card recovery-card">
                <span className="recovery-index">02</span>
                <div>
                  <span className="mini-label">System update</span>
                  <h3>{systemInfo.update_trust_configured ? "Signed update verification ready" : "Manual updates available"}</h3>
                  <p>{systemInfo.version ?? "Minimal Router OS"} · check for signed package updates</p>
                </div>
                <button
                  className="button secondary"
                  type="button"
                  disabled={!apiConnected}
                  onClick={() => {
                    if (!apiConnected) return;
                    apiFetch("/api/v1/system/update/check")
                      .then((res) => res.json())
                      .then((data) => {
                        if (data.update_available) {
                          if (window.confirm(`Update available: ${data.latest_version}\n\n${data.release_notes || "No release notes"}\n\nInstall now?`)) {
                            apiFetch("/api/v1/system/update/install", { method: "POST" })
                              .then((res) => res.json())
                              .then((result) => {
                                if (result.error) {
                                  setOperationError(result.error);
                                } else {
                                  setOperationError("");
                                  alert(result.message || "Update installed. Reboot recommended.");
                                }
                              })
                              .catch((err) => setOperationError("Install failed: " + String(err)));
                          }
                        } else {
                          alert(data.error || "System is up to date.");
                        }
                      })
                      .catch((err) => setOperationError("Check failed: " + String(err)));
                  }}
                >
                  Check for Updates
                </button>
              </article>
              <article className="card recovery-card">
                <span className="recovery-index">03</span>
                <div>
                  <span className="mini-label">Backup</span>
                  <h3>Encrypted export available</h3>
                  <p>Argon2id + AES-GCM · export history is not retained</p>
                </div>
                <button
                  className="button secondary"
                  type="button"
                  onClick={() => setBackupModalOpen(true)}
                >
                  Backup & restore
                </button>
              </article>
            </div>
          </section>

        </div>
      </div>

      {qrOpen && wireGuardProvisioning && (
        <div className="modal-backdrop" role="presentation" onMouseDown={closeWireGuardProvisioning}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            aria-labelledby="qr-title"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button
              className="modal-close"
              type="button"
              aria-label="Close QR code"
              onClick={closeWireGuardProvisioning}
            >
              ×
            </button>
            <p className="eyebrow">WireGuard peer</p>
            <h2 id="qr-title">Connect your phone</h2>
            <p className="modal-copy">
              Scan this code from the WireGuard app. The private configuration
              is returned once and is not stored by the router. Download it
              before closing this window.
            </p>
            <QrPreview source={wireGuardProvisioning.qrCodeData} />
            <div className="qr-peer">
              <div>
                <span>Peer name</span>
                <strong>{wireGuardProvisioning.peerName}</strong>
              </div>
              <div>
                <span>Address</span>
                <code>{wireGuardProvisioning.clientIP}</code>
              </div>
            </div>
            <div className="modal-actions">
              <button className="button secondary" type="button" onClick={closeWireGuardProvisioning}>
                Close and erase
              </button>
              <button className="button primary" type="button" onClick={downloadWireGuardConfig}>
                Download configuration
              </button>
            </div>
          </section>
        </div>
      )}

      {leaseModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setLeaseModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setLeaseModalOpen(false)}>×</button>
            <p className="eyebrow">DHCP Static Lease</p>
            <h2>Add static lease</h2>
            <form onSubmit={handleAddStaticLease} style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Device Hostname</label>
                <input
                  type="text"
                  placeholder="e.g. Synology NAS"
                  value={newLeaseHost}
                  onChange={(e) => setNewLeaseHost(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>MAC Address</label>
                <input
                  type="text"
                  placeholder="e.g. 00:11:22:33:44:55"
                  value={newLeaseMAC}
                  onChange={(e) => setNewLeaseMAC(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Static IP Address</label>
                <input
                  type="text"
                  placeholder="e.g. 10.0.0.50"
                  value={newLeaseIP}
                  onChange={(e) => setNewLeaseIP(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>
              <div className="modal-actions" style={{ marginTop: '8px' }}>
                <button className="button secondary" type="button" onClick={() => setLeaseModalOpen(false)}>Cancel</button>
                <button className="button primary" type="submit">Save static lease</button>
              </div>
            </form>
          </section>
        </div>
      )}

      {pfModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setPfModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setPfModalOpen(false)}>×</button>
            <p className="eyebrow">Firewall Port Forward</p>
            <h2>Add port forward rule</h2>
            <form onSubmit={handleAddPortForward} style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Rule Name</label>
                <input
                  type="text"
                  placeholder="e.g. Home Assistant"
                  value={newPfName}
                  onChange={(e) => setNewPfName(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>
              <div style={{ display: 'flex', gap: '12px' }}>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Protocol</label>
                  <select
                    value={newPfProto}
                    onChange={(e) => setNewPfProto(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  >
                    <option value="tcp">TCP</option>
                    <option value="udp">UDP</option>
                    <option value="both">TCP & UDP</option>
                  </select>
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>WAN Port</label>
                  <input
                    type="number"
                    placeholder="8123"
                    value={newPfExtPort}
                    onChange={(e) => setNewPfExtPort(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
              </div>
              <div style={{ display: 'flex', gap: '12px' }}>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Internal IP</label>
                  <input
                    type="text"
                    placeholder="10.0.0.10"
                    value={newPfIntIP}
                    onChange={(e) => setNewPfIntIP(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>LAN Port</label>
                  <input
                    type="number"
                    placeholder="8123"
                    value={newPfIntPort}
                    onChange={(e) => setNewPfIntPort(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
              </div>
              <div className="modal-actions" style={{ marginTop: '8px' }}>
                <button className="button secondary" type="button" onClick={() => setPfModalOpen(false)}>Cancel</button>
                <button className="button primary" type="submit">Save rule</button>
              </div>
            </form>
          </section>
        </div>
      )}

      {addWgModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setAddWgModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setAddWgModalOpen(false)}>×</button>
            <p className="eyebrow">WireGuard VPN</p>
            <h2>Add WireGuard Peer</h2>
            <form onSubmit={handleAddWgPeer} style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Peer Name</label>
                <input
                  type="text"
                  placeholder="e.g. Vlad Work Laptop"
                  value={newWgPeerName}
                  onChange={(e) => setNewWgPeerName(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Allowed Client IP</label>
                <input
                  type="text"
                  placeholder="e.g. 10.8.0.5"
                  value={newWgPeerIP}
                  onChange={(e) => setNewWgPeerIP(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Public WireGuard Endpoint</label>
                <input
                  type="text"
                  placeholder="e.g. vpn.example.net:51820"
                  value={newWgEndpoint}
                  onChange={(e) => setNewWgEndpoint(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
                <span style={{ display: "block", marginTop: "5px", fontSize: "11px", color: "var(--text-tertiary)" }}>
                  Use your public IP or DDNS hostname and the WireGuard UDP port.
                </span>
              </div>
              <div className="modal-actions" style={{ marginTop: '8px' }}>
                <button className="button secondary" type="button" onClick={() => setAddWgModalOpen(false)} disabled={wireGuardSubmitting}>Cancel</button>
                <button className="button primary" type="submit" disabled={wireGuardSubmitting}>
                  {wireGuardSubmitting ? "Applying securely…" : "Generate QR Code & Add"}
                </button>
              </div>
            </form>
          </section>
        </div>
      )}

      {cfModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setCfModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setCfModalOpen(false)}>×</button>
            <p className="eyebrow">Cloudflare DDNS & Tunnel</p>
            <h2>Configure Cloudflare</h2>
            <form onSubmit={handleSaveCfConfig} style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Domain Name</label>
                <input
                  type="text"
                  placeholder="home.example.net"
                  value={editCfDomain}
                  onChange={(e) => setEditCfDomain(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Cloudflare Zone ID</label>
                <input
                  type="text"
                  placeholder="Zone ID string"
                  value={editCfZone}
                  onChange={(e) => setEditCfZone(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Cloudflare API Token</label>
                <input
                  type="password"
                  placeholder="••••••••••••••••••••"
                  value={editCfToken}
                  onChange={(e) => setEditCfToken(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                />
              </div>
              <div className="modal-actions" style={{ marginTop: '8px' }}>
                <button className="button secondary" type="button" onClick={() => setCfModalOpen(false)}>Cancel</button>
                <button className="button primary" type="submit">Save Cloudflare Settings</button>
              </div>
            </form>
          </section>
        </div>
      )}
      {dhcpModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setDhcpModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setDhcpModalOpen(false)}>×</button>
            <p className="eyebrow">LAN & DHCP Server</p>
            <h2>Manage DHCP Settings</h2>
            <form onSubmit={handleSaveDhcpSettings} style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div style={{ display: 'flex', gap: '12px' }}>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>DHCP Pool Start</label>
                  <input
                    type="text"
                    placeholder="10.0.0.20"
                    value={dhcpRangeStart}
                    onChange={(e) => setDhcpRangeStart(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>DHCP Pool End</label>
                  <input
                    type="text"
                    placeholder="10.0.0.200"
                    value={dhcpRangeEnd}
                    onChange={(e) => setDhcpRangeEnd(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
              </div>
              <div style={{ display: 'flex', gap: '12px' }}>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Lease Time (Hours)</label>
                  <input
                    type="number"
                    placeholder="24"
                    value={dhcpLeaseHours}
                    onChange={(e) => setDhcpLeaseHours(parseInt(e.target.value, 10) || 24)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>LAN Gateway IP</label>
                  <input
                    type="text"
                    placeholder="10.0.0.1"
                    value={dhcpGateway}
                    onChange={(e) => setDhcpGateway(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
              </div>
              <div className="modal-actions" style={{ marginTop: '8px' }}>
                <button className="button secondary" type="button" onClick={() => setDhcpModalOpen(false)}>Cancel</button>
                <button className="button primary" type="submit">Save DHCP Configuration</button>
              </div>
            </form>
          </section>
        </div>
      )}

      {ddnsModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setDdnsModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setDdnsModalOpen(false)}>×</button>
            <p className="eyebrow">Dynamic DNS (DDNS)</p>
            <h2>Configure Dynamic DNS</h2>
            <form onSubmit={handleSaveDdns} style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: '14px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>DDNS Provider</label>
                <select
                  value={ddnsProvider}
                  onChange={(e) => setDdnsProvider(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                >
                  <option value="cloudflare">Cloudflare DDNS</option>
                  <option value="noip">No-IP</option>
                  <option value="duckdns">DuckDNS</option>
                  <option value="custom">Custom DynDNS Service</option>
                </select>
              </div>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Domain / Hostname</label>
                <input
                  type="text"
                  placeholder="e.g. home.example.net or myhome.duckdns.org"
                  value={ddnsDomain}
                  onChange={(e) => setDdnsDomain(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  required
                />
              </div>

              {ddnsProvider === "cloudflare" && (
                <div>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Cloudflare Zone ID</label>
                  <input
                    type="text"
                    placeholder="cf-zone-12345"
                    value={ddnsZoneId}
                    onChange={(e) => setDdnsZoneId(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  />
                </div>
              )}

              {(ddnsProvider === "noip" || ddnsProvider === "custom") && (
                <div>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Username / Account Email</label>
                  <input
                    type="text"
                    placeholder="user@example.com"
                    value={ddnsUser}
                    onChange={(e) => setDdnsUser(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                  />
                </div>
              )}

              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>
                  {ddnsProvider === "cloudflare" ? "API Token" : ddnsProvider === "duckdns" ? "Token" : "Password / Key"}
                </label>
                <input
                  type="password"
                  placeholder="••••••••••••••••••••"
                  value={ddnsPass}
                  onChange={(e) => setDdnsPass(e.target.value)}
                  style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                />
              </div>

              <div className="modal-actions" style={{ marginTop: '8px' }}>
                <button className="button secondary" type="button" onClick={() => setDdnsModalOpen(false)}>Cancel</button>
                <button className="button primary" type="submit">Save Dynamic DNS</button>
              </div>
            </form>
          </section>
        </div>
      )}

      {snapshotsModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setSnapshotsModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "600px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setSnapshotsModalOpen(false)}>×</button>
            <p className="eyebrow">Recovery & Rollbacks</p>
            <h2>System Snapshots</h2>
            <p className="modal-copy">
              Immutable pre-apply point-in-time configuration snapshots with sha256 integrity verification.
            </p>

            <div style={{ marginTop: "20px", display: "flex", flexDirection: "column", gap: "12px" }}>
              {snapshotsList.map((snap) => (
                <div
                  key={snap.id}
                  style={{
                    padding: "16px",
                    borderRadius: "14px",
                    background: "var(--surface-muted)",
                    border: "1px solid var(--separator)",
                    display: "flex",
                    justifyContent: "space-between",
                    alignItems: "center",
                  }}
                >
                  <div>
                    <div style={{ fontSize: "14px", fontWeight: 650 }}>{snap.id} (Revision {snap.revision})</div>
                    <div style={{ fontSize: "13px", color: "var(--text-secondary)" }}>{snap.label} · {snap.time}</div>
                    <div style={{ fontSize: "11px", color: "var(--text-tertiary)", marginTop: "4px" }}>Checksum: <code>{snap.checksum}</code></div>
                  </div>
                  <button
                    className="button secondary"
                    type="button"
                    style={{ fontSize: "13px", padding: "6px 14px" }}
                    onClick={() => void handleRestoreSnapshot(snap.id)}
                  >
                    Restore
                  </button>
                </div>
              ))}
            </div>

            <div className="modal-actions" style={{ marginTop: "20px" }}>
              <button className="button secondary" type="button" onClick={() => setSnapshotsModalOpen(false)}>Close</button>
              <button className="button primary" type="button" onClick={() => { setSnapshotsModalOpen(false); handleMakeSnapshot(); }}>+ Create New Snapshot</button>
            </div>
          </section>
        </div>
      )}

      {backupModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setBackupModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "560px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setBackupModalOpen(false)}>×</button>
            <p className="eyebrow">Backup & Recovery</p>
            <h2>Backup & Restore Configuration</h2>
            <p className="modal-copy">
              Password-protected AES-256-GCM backup with Argon2id key derivation.
            </p>

            {backupNotice && (
              <div style={{ padding: "12px 16px", borderRadius: "10px", background: "#34C75915", color: "#34C759", fontWeight: 600, fontSize: "14px", marginTop: "12px" }}>
                {backupNotice}
              </div>
            )}

            <div style={{ marginTop: "24px", display: "flex", flexDirection: "column", gap: "20px" }}>
              <div style={{ display: "grid", gap: "14px" }}>
                <label>
                  Current administrator password
                  <input
                    type="password"
                    autoComplete="current-password"
                    value={backupAdminPassword}
                    onChange={(event) => setBackupAdminPassword(event.target.value)}
                  />
                </label>
                <label>
                  Backup passphrase
                  <input
                    type="password"
                    autoComplete="new-password"
                    minLength={15}
                    value={backupPassphrase}
                    onChange={(event) => setBackupPassphrase(event.target.value)}
                  />
                </label>
              </div>
              <div style={{ padding: "20px", borderRadius: "16px", background: "var(--surface-muted)", border: "1px solid var(--separator)" }}>
                <h3 style={{ fontSize: "16px", fontWeight: 650, marginBottom: "6px" }}>Export Backup File</h3>
                <p style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
                  Download the complete canonical configuration, including secrets, in an authenticated encrypted envelope.
                </p>
                <button className="button primary" type="button" onClick={() => void handleExportBackup()}>
                  Download encrypted backup
                </button>
              </div>

              <div style={{ padding: "20px", borderRadius: "16px", background: "var(--surface-muted)", border: "1px solid var(--separator)" }}>
                <h3 style={{ fontSize: "16px", fontWeight: 650, marginBottom: "6px" }}>Restore From Backup File</h3>
                <p style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
                  Validate the encrypted file first. Applying it uses the same snapshot, preflight, verification, and rollback pipeline as normal changes.
                </p>
                <input
                  type="file"
                  accept=".mrbak,application/vnd.minimalrouter.backup+json"
                  onChange={handleImportBackup}
                  style={{ fontSize: "14px" }}
                />
                <div style={{ display: "flex", gap: "10px", marginTop: "14px" }}>
                  <button className="button secondary" type="button" onClick={() => void handlePreviewBackupRestore()}>
                    Validate backup
                  </button>
                  {pendingBackupImportID && (
                    <button className="button danger" type="button" onClick={() => void handleApplyBackupRestore()}>
                      Apply validated backup
                    </button>
                  )}
                </div>
              </div>
              <div style={{ padding: "20px", borderRadius: "16px", background: "var(--surface-muted)", border: "1px solid var(--separator)" }}>
                <h3 style={{ fontSize: "16px", fontWeight: 650, marginBottom: "6px" }}>Migrate from pfSense</h3>
                <p style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
                  Upload an unencrypted full `config.xml`. Interface mapping is mandatory; unsupported sections are never silently ignored.
                </p>
                <input
                  type="file"
                  accept=".xml,application/xml,text/xml"
                  onChange={(event) => {
                    setPfSenseFile(event.target.files?.[0] ?? null);
                    setPendingPfSenseImportID("");
                    setPfSenseWarnings([]);
                  }}
                />
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "12px", marginTop: "14px" }}>
                  <label>
                    Target WAN interface
                    <input value={pfSenseWANInterface} onChange={(event) => setPfSenseWANInterface(event.target.value)} />
                  </label>
                  <label>
                    Target LAN interface
                    <input value={pfSenseLANInterface} onChange={(event) => setPfSenseLANInterface(event.target.value)} />
                  </label>
                </div>
                {pfSenseWarnings.length > 0 && (
                  <ul className="import-warnings">
                    {pfSenseWarnings.map((warning) => <li key={warning}>{warning}</li>)}
                  </ul>
                )}
                <div style={{ display: "flex", gap: "10px", marginTop: "14px" }}>
                  <button className="button secondary" type="button" onClick={() => void handlePreviewPfSenseImport()}>
                    Parse and validate
                  </button>
                  {pendingPfSenseImportID && (
                    <button className="button danger" type="button" onClick={() => void handleApplyPfSenseImport()}>
                      Apply validated migration
                    </button>
                  )}
                </div>
              </div>
            </div>

            <div className="modal-actions" style={{ marginTop: "24px" }}>
              <button className="button secondary" type="button" onClick={() => setBackupModalOpen(false)}>Close</button>
            </div>
          </section>
        </div>
      )}

      {profileModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setProfileModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "600px", borderRadius: "24px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setProfileModalOpen(false)}>×</button>
            <div style={{ display: "flex", alignItems: "center", gap: "14px", marginBottom: "20px" }}>
              <div style={{ width: "48px", height: "48px", borderRadius: "50%", background: "#0071E3", color: "#FFF", display: "grid", placeItems: "center", fontWeight: 700, fontSize: "18px" }}>
                VP
              </div>
              <div>
                <p className="eyebrow" style={{ margin: 0 }}>Administrator Profil</p>
                <h2 style={{ margin: 0, fontSize: "22px" }}>Vladimir Perović</h2>
                <span style={{ fontSize: "12px", color: "var(--text-tertiary)" }}>Role: Root Administrator · Session Active</span>
              </div>
            </div>

            {passNotice && (
              <div style={{ padding: "12px 16px", borderRadius: "10px", background: "#34C75915", color: "#34C759", fontWeight: 600, fontSize: "14px", marginBottom: "16px" }}>
                {passNotice}
              </div>
            )}

            {passError && (
              <div style={{ padding: "12px 16px", borderRadius: "10px", background: "#FF3B3015", color: "#FF3B30", fontWeight: 600, fontSize: "14px", marginBottom: "16px" }}>
                {passError}
              </div>
            )}

            <form onSubmit={handleChangePassword} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <h3 style={{ fontSize: "15px", fontWeight: 650, marginTop: "8px" }}>Change Administrator Password (Argon2id)</h3>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Current password</label>
                <input
                  type="password"
                  placeholder="••••••••••••"
                  value={oldPassword}
                  onChange={(e) => setOldPassword(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  required
                />
              </div>
              <div style={{ display: "flex", gap: "12px" }}>
                <div style={{ flex: 1 }}>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>New password (min 15 chars)</label>
                  <input
                    type="password"
                    placeholder="Minimum 15 characters"
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                    required
                  />
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Confirm new password</label>
                  <input
                    type="password"
                    placeholder="Repeat new password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                    required
                  />
                </div>
              </div>
              <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "4px" }}>
                <button className="button primary" type="submit" style={{ fontSize: "13px", padding: "8px 18px" }}>
                  Save New Password
                </button>
              </div>
            </form>

            <div style={{ marginTop: "24px", paddingTop: "16px", borderTop: "1px solid var(--separator)", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
              <div>
                <strong style={{ display: "block", fontSize: "14px" }}>System Diagnostics</strong>
                <span style={{ fontSize: "12px", color: "var(--text-secondary)" }}>Export technical diagnostic report with redacted secrets</span>
              </div>
              <button
                className="button secondary"
                type="button"
                style={{ fontSize: "13px" }}
                onClick={() => {
                  window.location.href = "/api/v1/system/diagnostics";
                }}
              >
                ⬇ Diagnostics
              </button>
            </div>

            <div style={{ marginTop: "12px", paddingTop: "16px", borderTop: "1px solid var(--separator)", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
              <div>
                <strong style={{ display: "block", fontSize: "14px" }}>Security audit log</strong>
                <span style={{ fontSize: "12px", color: "var(--text-secondary)" }}>Authentication and configuration actions; no request bodies or secrets</span>
              </div>
              <button
                className="button secondary"
                type="button"
                style={{ fontSize: "13px" }}
                onClick={() => void openAuditLog()}
                disabled={auditLoading}
              >
                {auditLoading ? "Loading…" : "View audit"}
              </button>
            </div>

            <div className="modal-actions" style={{ marginTop: "24px", borderTop: "1px solid var(--separator)", paddingTop: "16px" }}>
              <button className="button secondary" type="button" onClick={() => setProfileModalOpen(false)}>Close</button>
              <button
                className="button primary"
                type="button"
                style={{ background: "#FF3B30", borderColor: "#FF3B30" }}
                onClick={() => {
                  apiFetch("/api/v1/auth/logout", { method: "POST" }).finally(() => {
                    alert("You have been logged out of Minimal Router OS.");
                    setProfileModalOpen(false);
                  });
                }}
              >
                Sign Out (Logout)
              </button>
            </div>
          </section>
        </div>
      )}

      {auditModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setAuditModalOpen(false)}>
          <section
            className="modal modal-wide"
            role="dialog"
            aria-modal="true"
            aria-labelledby="audit-title"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" aria-label="Close audit log" onClick={() => setAuditModalOpen(false)}>×</button>
            <p className="eyebrow">Security</p>
            <h2 id="audit-title">Audit log</h2>
            <p className="modal-copy">Stored locally in SQLite. Passwords, keys, tokens, request bodies, and generated configurations are never recorded.</p>
            <div style={{ overflowX: "auto", maxHeight: "440px", overflowY: "auto", marginTop: "18px" }}>
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Event</th>
                    <th>Actor</th>
                    <th>Result</th>
                  </tr>
                </thead>
                <tbody>
                  {auditEvents.length === 0 ? (
                    <tr><td colSpan={4} style={{ textAlign: "center", color: "var(--text-tertiary)" }}>No audit events yet.</td></tr>
                  ) : auditEvents.map((event) => (
                    <tr key={event.id}>
                      <td>{new Date(event.timestamp).toLocaleString()}</td>
                      <td><code>{event.event_type}</code><br /><span className="quiet-meta">{event.details.method} {event.details.path}</span></td>
                      <td>{event.actor}</td>
                      <td>{event.details.status ?? event.details.result ?? event.details.mode ?? "Recorded"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="modal-actions">
              <button className="button secondary" type="button" onClick={() => setAuditModalOpen(false)}>Close</button>
              <button className="button primary" type="button" onClick={() => void openAuditLog()} disabled={auditLoading}>Refresh</button>
            </div>
          </section>
        </div>
      )}

      {dnsModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setDnsModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "540px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setDnsModalOpen(false)}>×</button>
            <p className="eyebrow">Network & Security</p>
            <h2>DNS Server Settings</h2>
            <form onSubmit={handleSaveDnsSettings} style={{ marginTop: '16px', display: 'flex', flexDirection: 'column', gap: '16px' }}>
              <div>
                <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '6px' }}>DNS Provider Preset</label>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
                  {[
                    { id: 'cloudflare', name: 'Cloudflare', pri: '1.1.1.1', sec: '1.0.0.1' },
                    { id: 'quad9', name: 'Quad9 (Malware Block)', pri: '9.9.9.9', sec: '149.112.112.112' },
                    { id: 'adguard', name: 'AdGuard (Ad Blocking)', pri: '94.140.14.14', sec: '94.140.15.15' },
                    { id: 'google', name: 'Google DNS', pri: '8.8.8.8', sec: '8.8.4.4' },
                  ].map((p) => (
                    <button
                      key={p.id}
                      type="button"
                      onClick={() => {
                        setDnsProvider(p.id);
                        setDnsPrimary(p.pri);
                        setDnsSecondary(p.sec);
                      }}
                      style={{
                        padding: '10px 12px',
                        borderRadius: '10px',
                        border: dnsProvider === p.id ? '2px solid #0071E3' : '1px solid var(--separator)',
                        background: dnsProvider === p.id ? '#0071E310' : 'var(--surface)',
                        textAlign: 'left',
                        cursor: 'pointer',
                      }}
                    >
                      <div style={{ fontWeight: 650, fontSize: '13px' }}>{p.name}</div>
                      <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>{p.pri} · {p.sec}</div>
                    </button>
                  ))}
                </div>
              </div>

              <div style={{ display: 'flex', gap: '12px' }}>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Primary Upstream DNS</label>
                  <input
                    type="text"
                    placeholder="1.1.1.1"
                    value={dnsPrimary}
                    onChange={(e) => setDnsPrimary(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, marginBottom: '4px' }}>Secondary Upstream DNS</label>
                  <input
                    type="text"
                    placeholder="1.0.0.1"
                    value={dnsSecondary}
                    onChange={(e) => setDnsSecondary(e.target.value)}
                    style={{ width: '100%', padding: '8px 12px', borderRadius: '8px', border: '1px solid var(--separator)', background: 'var(--surface)' }}
                    required
                  />
                </div>
              </div>

              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px', borderRadius: '10px', background: 'var(--surface-muted)', border: '1px solid var(--separator)' }}>
                <div>
                  <strong style={{ display: 'block', fontSize: '13px' }}>DNS-over-HTTPS / TLS</strong>
                  <span style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>Encrypt DNS queries via Cloudflare, Google, or Quad9 DoH endpoints</span>
                </div>
                <input
                  type="checkbox"
                  checked={dohEnabled}
                  onChange={(e) => setDohEnabled(e.target.checked)}
                  style={{ width: '18px', height: '18px', cursor: 'pointer' }}
                />
              </div>

              <div className="modal-actions" style={{ marginTop: '8px' }}>
                <button className="button secondary" type="button" onClick={() => setDnsModalOpen(false)}>Cancel</button>
                <button className="button primary" type="submit">Save DNS Settings</button>
              </div>
            </form>
          </section>
        </div>
      )}

      {addRestrictedModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setAddRestrictedModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "460px", borderRadius: "20px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setAddRestrictedModalOpen(false)}>×</button>
            <p className="eyebrow">Firewall & Squid Proxy</p>
            <h2>Add Restricted IP Alias</h2>
            <p className="modal-copy" style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
              Direct WAN access will be blocked in nftables for this IP. The device must configure browser proxy settings to port 3128 with username & password to access the internet.
            </p>
            <form onSubmit={handleAddRestrictedIP} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Device Name / Host</label>
                <input
                  type="text"
                  placeholder="e.g. Smart TV, Guest Laptop"
                  value={newRestrictedHost}
                  onChange={(e) => setNewRestrictedHost(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Target Device IP Address</label>
                <input
                  type="text"
                  placeholder="10.0.0.50"
                  value={newRestrictedIP}
                  onChange={(e) => setNewRestrictedIP(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  required
                />
              </div>
              <div className="modal-actions" style={{ marginTop: "8px" }}>
                <button className="button secondary" type="button" onClick={() => setAddRestrictedModalOpen(false)}>
                  Cancel
                </button>
                <button className="button primary" type="submit">
                  Add to Restricted Group
                </button>
              </div>
            </form>
          </section>
        </div>
      )}

      {squidCredsModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setSquidCredsModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "460px", borderRadius: "20px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setSquidCredsModalOpen(false)}>×</button>
            <p className="eyebrow">Squid Proxy Authentication</p>
            <h2>Set User/Pass for Squid</h2>
            <p className="modal-copy" style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
              NCSA Basic htpasswd credentials used by restricted devices to authenticate with the proxy server on port 3128.
            </p>
            <form onSubmit={handleSaveSquidCreds} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Proxy Username</label>
                <input
                  type="text"
                  value={squidUser}
                  onChange={(e) => setSquidUser(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  required
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Proxy Password</label>
                <input
                  type="password"
                  placeholder="••••••••"
                  value={squidPass}
                  onChange={(e) => setSquidPass(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                />
              </div>
              <div className="modal-actions" style={{ marginTop: "8px" }}>
                <button className="button secondary" type="button" onClick={() => setSquidCredsModalOpen(false)}>
                  Cancel
                </button>
                <button className="button primary" type="submit">
                  Save Credentials
                </button>
              </div>
            </form>
          </section>
        </div>
      )}

      {addFilterModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setAddFilterModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "480px", borderRadius: "20px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setAddFilterModalOpen(false)}>×</button>
            <p className="eyebrow">AdGuard Content Filter</p>
            <h2>Add Filtered Device</h2>
            <p className="modal-copy" style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
              Select which online services to block for this device IP address via DNS sinkhole.
            </p>
            <form onSubmit={handleAddFilterDevice} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Device Name / Host</label>
                <input
                  type="text"
                  placeholder="e.g. Kid's Tablet, Living Room TV"
                  value={newFilterHost}
                  onChange={(e) => setNewFilterHost(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Target Device IP Address</label>
                <input
                  type="text"
                  placeholder="10.0.0.80"
                  value={newFilterIP}
                  onChange={(e) => setNewFilterIP(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  required
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "6px" }}>Blocked Services</label>
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "10px" }}>
                  {[
                    { id: "youtube", label: "YouTube" },
                    { id: "tiktok", label: "TikTok" },
                    { id: "facebook", label: "Facebook & IG" },
                    { id: "adult", label: "Adult Content" },
                    { id: "gaming", label: "Gaming & Roblox" },
                  ].map((s) => (
                    <label key={s.id} style={{ display: "flex", alignItems: "center", gap: "8px", fontSize: "13px", cursor: "pointer", background: "var(--surface)", padding: "8px 10px", borderRadius: "8px", border: "1px solid var(--separator)" }}>
                      <input
                        type="checkbox"
                        checked={newFilterServices.includes(s.id)}
                        onChange={(e) => {
                          if (e.target.checked) {
                            setNewFilterServices([...newFilterServices, s.id]);
                          } else {
                            setNewFilterServices(newFilterServices.filter((x) => x !== s.id));
                          }
                        }}
                      />
                      {s.label}
                    </label>
                  ))}
                </div>
              </div>
              <div className="modal-actions" style={{ marginTop: "8px" }}>
                <button className="button secondary" type="button" onClick={() => setAddFilterModalOpen(false)}>
                  Cancel
                </button>
                <button className="button primary" type="submit">
                  Save Device Filter
                </button>
              </div>
            </form>
          </section>
        </div>
      )}

      {qosModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setQosModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "460px", borderRadius: "20px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setQosModalOpen(false)}>×</button>
            <p className="eyebrow">QoS & Traffic Management</p>
            <h2>Configure CAKE Traffic Shaping</h2>
            <p className="modal-copy" style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
              Set your ISP connection speed caps so CAKE algorithm can manage queue latency and prevent bufferbloat spikes.
            </p>
            <form onSubmit={handleSaveQoS} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Max Download Speed (Mbps)</label>
                <input
                  type="number"
                  placeholder="100"
                  value={qosDown}
                  onChange={(e) => setQosDown(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  required
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Max Upload Speed (Mbps)</label>
                <input
                  type="number"
                  placeholder="20"
                  value={qosUp}
                  onChange={(e) => setQosUp(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  required
                />
              </div>
              <div className="modal-actions" style={{ marginTop: "8px" }}>
                <button className="button secondary" type="button" onClick={() => setQosModalOpen(false)}>
                  Cancel
                </button>
                <button className="button primary" type="submit">
                  Save QoS Settings
                </button>
              </div>
            </form>
          </section>
        </div>
      )}

      {wifiModalOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setWifiModalOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "480px", borderRadius: "20px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setWifiModalOpen(false)}>×</button>
            <p className="eyebrow">Wi-Fi Access Point</p>
            <h2>Configure Wireless Network</h2>
            <p className="modal-copy" style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
              Configure your router&apos;s wireless access point settings (SSID, WPA2/WPA3 passphrase, frequency band, and channel).
            </p>
            <form onSubmit={handleSaveWiFi} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Wi-Fi Network Name (SSID)</label>
                <input
                  type="text"
                  placeholder="e.g. MinimalRouter-Home"
                  value={wifiSSID}
                  onChange={(e) => setWifiSSID(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  required
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Wi-Fi WPA2/WPA3 Passphrase</label>
                <input
                  type="password"
                  placeholder="At least 8 characters"
                  value={wifiPass}
                  onChange={(e) => setWifiPass(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  required
                />
              </div>
              <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "12px" }}>
                <div>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Frequency Band</label>
                  <select
                    value={wifiBand}
                    onChange={(e) => setWifiBand(e.target.value)}
                    style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  >
                    <option value="5ghz">5 GHz (High Speed)</option>
                    <option value="2.4ghz">2.4 GHz (Long Range)</option>
                  </select>
                </div>
                <div>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Wi-Fi Channel</label>
                  <input
                    type="number"
                    placeholder="36"
                    value={wifiChannel}
                    onChange={(e) => setWifiChannel(e.target.value)}
                    style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                  />
                </div>
              </div>
              <div>
                <label style={{ display: "flex", alignItems: "center", gap: "8px", fontSize: "13px", cursor: "pointer", marginTop: "4px" }}>
                  <input
                    type="checkbox"
                    checked={wifiHideSSID}
                    onChange={(e) => setWifiHideSSID(e.target.checked)}
                  />
                  Hide SSID (Invisible Broadcast)
                </label>
              </div>
              <div className="modal-actions" style={{ marginTop: "8px" }}>
                <button className="button secondary" type="button" onClick={() => setWifiModalOpen(false)}>
                  Cancel
                </button>
                <button className="button primary" type="submit">
                  Save Wi-Fi Settings
                </button>
              </div>
            </form>
          </section>
        </div>
      )}

      {deleteConfirmTarget && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setDeleteConfirmTarget(null)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="Router configuration dialog"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "440px", borderRadius: "20px" }}
          >
            <button className="modal-close" type="button" aria-label="Close dialog" onClick={() => setDeleteConfirmTarget(null)}>×</button>
            <p className="eyebrow" style={{ color: "#FF3B30" }}>Confirm Deletion</p>
            <h2>Are you sure?</h2>
            <p className="modal-copy" style={{ margin: "12px 0 24px", color: "var(--text-secondary)" }}>
              Are you sure you want to delete <strong>&quot;{deleteConfirmTarget.name}&quot;</strong>? This action will immediately remove the item from the router configuration.
            </p>
            <div className="modal-actions">
              <button className="button secondary" type="button" onClick={() => setDeleteConfirmTarget(null)}>
                Cancel
              </button>
              <button
                className="button primary"
                type="button"
                onClick={handleConfirmDelete}
                style={{ background: "#FF3B30", borderColor: "#FF3B30" }}
              >
                Yes, Delete
              </button>
            </div>
          </section>
        </div>
      )}
      {wizardOpen && (
        <SetupWizard
          onClose={() => setWizardOpen(false)}
          onComplete={() => {
            setWizardOpen(false);
            window.location.reload();
          }}
        />
      )}
    </main>
  );
}

export default function Home() {
  return (
    <AuthGate>
      <Dashboard />
    </AuthGate>
  );
}
