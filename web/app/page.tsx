"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import SetupWizard from "./components/SetupWizard";

type Theme = "light" | "dark";

const navItems = [
  ["01", "Overview", "overview"],
  ["02", "System", "system"],
  ["03", "LAN & DHCP", "lan"],
  ["04", "Firewall", "firewall"],
  ["05", "WireGuard", "wireguard"],
  ["06", "Cloudflare", "cloudflare"],
  ["07", "Recovery", "recovery"],
] as const;

const trafficDown = [
  18, 26, 21, 34, 29, 42, 38, 63, 54, 70, 64, 82, 76, 91, 69, 84, 77, 96,
  88, 104, 92, 111, 98, 119, 108, 124, 115, 132, 126, 142, 134, 151,
];

const trafficUp = [
  8, 11, 9, 15, 13, 18, 16, 25, 21, 29, 25, 33, 30, 38, 28, 35, 31, 42, 36,
  46, 39, 49, 43, 52, 46, 56, 51, 61, 55, 64, 59, 68,
];

function TrafficChart({ theme }: { theme: Theme }) {
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
      const max = 160;

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

      plot(trafficDown, theme === "dark" ? "#0a84ff" : "#007aff", "rgba(0,122,255,.16)");
      plot(trafficUp, theme === "dark" ? "#bf8cff" : "#7655c7");
    };

    draw();
    const observer = new ResizeObserver(draw);
    observer.observe(canvas);
    return () => observer.disconnect();
  }, [theme]);

  return (
    <canvas
      ref={canvasRef}
      className="traffic-canvas"
      aria-label="Internet traffic over the last 24 hours. Download peaked at 151 megabits per second and upload peaked at 68 megabits per second."
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
}: {
  checked: boolean;
  onChange: () => void;
  label: string;
}) {
  return (
    <button
      className={`switch ${checked ? "is-on" : ""}`}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={onChange}
    >
      <span />
    </button>
  );
}

function QrPreview() {
  const cells = useMemo(() => {
    const size = 29;
    const data: boolean[] = [];
    const finder = (row: number, col: number, top: number, left: number) => {
      const x = col - left;
      const y = row - top;
      if (x < 0 || y < 0 || x > 6 || y > 6) return null;
      return (
        x === 0 ||
        y === 0 ||
        x === 6 ||
        y === 6 ||
        (x >= 2 && x <= 4 && y >= 2 && y <= 4)
      );
    };

    for (let row = 0; row < size; row += 1) {
      for (let col = 0; col < size; col += 1) {
        const topLeft = finder(row, col, 0, 0);
        const topRight = finder(row, col, 0, size - 7);
        const bottomLeft = finder(row, col, size - 7, 0);
        const fixed = topLeft ?? topRight ?? bottomLeft;
        if (fixed !== null) {
          data.push(fixed);
          continue;
        }
        const quietZone =
          (row <= 7 && col <= 7) ||
          (row <= 7 && col >= size - 8) ||
          (row >= size - 8 && col <= 7);
        if (quietZone) {
          data.push(false);
          continue;
        }
        const seed = (row * 47 + col * 31 + row * col * 7 + 19) % 17;
        data.push(seed < 7 || (row + col) % 11 === 0);
      }
    }
    return data;
  }, []);

  return (
    <div className="qr-shell" aria-label="WireGuard configuration QR preview">
      <div className="qr-grid" aria-hidden="true">
        {cells.map((cell, index) => (
          <span className={cell ? "qr-dark" : ""} key={index} />
        ))}
      </div>
    </div>
  );
}

export default function Home() {
  const [theme, setTheme] = useState<Theme>("light");
  const [menuOpen, setMenuOpen] = useState(false);
  const [qrOpen, setQrOpen] = useState(false);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [statefulRules, setStatefulRules] = useState(true);
  const [portForward, setPortForward] = useState(true);
  const [activeSection, setActiveSection] = useState("overview");
  const [fontScale, setFontScale] = useState(100);
  const [apiConnected, setApiConnected] = useState(false);
  const [systemInfo, setSystemInfo] = useState<{
    status?: string;
    version?: string;
    uptime?: string;
    public_ip?: string;
    last_backup?: string;
    last_snap?: string;
    update?: string;
  }>({});

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

  // Fetch live status from Go REST API (/api/v1/system)
  useEffect(() => {
    fetch("/api/v1/system")
      .then((res) => {
        if (!res.ok) throw new Error("API Offline");
        return res.json();
      })
      .then((data) => {
        setSystemInfo(data);
        setApiConnected(true);
      })
      .catch(() => {
        setApiConnected(false);
      });
  }, []);

  const [staticLeases, setStaticLeases] = useState([
    { hostname: "Synology NAS", mac: "00:11:22:33:44:55", ip: "10.0.0.5" },
    { hostname: "Home Assistant", mac: "00:e0:4c:68:01:91", ip: "10.0.0.10" },
  ]);
  const [leaseModalOpen, setLeaseModalOpen] = useState(false);
  const [newLeaseHost, setNewLeaseHost] = useState("");
  const [newLeaseMAC, setNewLeaseMAC] = useState("");
  const [newLeaseIP, setNewLeaseIP] = useState("");

  const [portForwardRules, setPortForwardRules] = useState([
    { name: "Home Assistant", proto: "TCP", extPort: 8123, intIP: "10.0.0.10", intPort: 8123, enabled: true },
  ]);
  const [pfModalOpen, setPfModalOpen] = useState(false);
  const [newPfName, setNewPfName] = useState("");
  const [newPfProto, setNewPfProto] = useState("tcp");
  const [newPfExtPort, setNewPfExtPort] = useState("");
  const [newPfIntIP, setNewPfIntIP] = useState("");
  const [newPfIntPort, setNewPfIntPort] = useState("");

  const [wgPeers, setWgPeers] = useState([
    { id: "p1", name: "MacBook Pro", ip: "10.8.0.2", traffic: "4.8 GB", active: "18s ago" },
    { id: "p2", name: "iPhone", ip: "10.8.0.3", traffic: "1.2 GB", active: "2m ago" },
    { id: "p3", name: "Travel laptop", ip: "10.8.0.4", traffic: "Offline", active: "3d ago" },
  ]);
  const [addWgModalOpen, setAddWgModalOpen] = useState(false);
  const [newWgPeerName, setNewWgPeerName] = useState("");
  const [newWgPeerIP, setNewWgPeerIP] = useState("");

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
      fetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.cloudflare = {
            ddns_enabled: true,
            domain: editCfDomain,
            zone_id: editCfZone,
            api_token: editCfToken,
          };
          return fetch("/api/v1/config", {
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
  const [dnsOverHttps, setDnsOverHttps] = useState(true);

  const handleSaveDnsSettings = (e: React.FormEvent) => {
    e.preventDefault();
    setDnsModalOpen(false);
    if (apiConnected) {
      fetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.dhcp.dns_servers = [dnsPrimary, dnsSecondary];
          return fetch("/api/v1/config", {
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
      fetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.cloudflare = {
            ddns_enabled: true,
            provider: ddnsProvider,
            domain: ddnsDomain,
            zone_id: ddnsZoneId,
            api_token: ddnsPass,
          };
          return fetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const [profileModalOpen, setProfileModalOpen] = useState(false);
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

    setPassNotice("✓ Administrator lozinka je uspešno promijenjena!");
    setOldPassword("");
    setNewPassword("");
    setConfirmPassword("");
    setTimeout(() => setPassNotice(""), 4000);
  };

  const [snapshotsList, setSnapshotsList] = useState([
    { id: "snap-42", revision: 42, label: "Firewall rule update", time: "8 min ago", checksum: "a1b2c3d4e5f6..." },
    { id: "snap-41", revision: 41, label: "Initial system bootstrap", time: "2 hours ago", checksum: "f9e8d7c6b5a4..." },
  ]);
  const [snapshotsModalOpen, setSnapshotsModalOpen] = useState(false);
  const [snapshotSuccessMsg, setSnapshotSuccessMsg] = useState("");

  const [backupModalOpen, setBackupModalOpen] = useState(false);
  const [includeSecrets, setIncludeSecrets] = useState(true);
  const [backupNotice, setBackupNotice] = useState("");

  const handleExportBackup = () => {
    const backupObj = {
      app: "Minimal Router OS",
      version: "0.1.0",
      timestamp: new Date().toISOString(),
      config: {
        staticLeases,
        portForwardRules,
        wgPeers,
        cfConfig,
        dhcpRangeStart,
        dhcpRangeEnd,
        dhcpLeaseHours,
        dhcpGateway,
      },
    };
    const blob = new Blob([JSON.stringify(backupObj, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `minimalrouter-backup-${Date.now()}.json`;
    a.click();
    URL.revokeObjectURL(url);
    setBackupNotice("✓ Sigurnosna kopija (backup) uspešno preuzeta!");
    setTimeout(() => setBackupNotice(""), 4000);
  };

  const handleImportBackup = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (event) => {
      try {
        const parsed = JSON.parse(event.target?.result as string);
        if (parsed.config) {
          if (parsed.config.staticLeases) setStaticLeases(parsed.config.staticLeases);
          if (parsed.config.portForwardRules) setPortForwardRules(parsed.config.portForwardRules);
          if (parsed.config.wgPeers) setWgPeers(parsed.config.wgPeers);
          if (parsed.config.cfConfig) setCfConfig(parsed.config.cfConfig);
          setBackupNotice("✓ Konfiguracija uspešno uvezena iz backup fajla!");
          setTimeout(() => setBackupNotice(""), 4000);
        }
      } catch (err) {
        alert("Neispravan backup fajl!");
      }
    };
    reader.readAsText(file);
  };

  const handleMakeSnapshot = () => {
    const nextRev = snapshotsList.length > 0 ? snapshotsList[0].revision + 1 : 1;
    const newSnap = {
      id: `snap-${nextRev}`,
      revision: nextRev,
      label: "Manual user snapshot",
      time: "Just now",
      checksum: Math.random().toString(16).substring(2, 14) + "...",
    };
    setSnapshotsList([newSnap, ...snapshotsList]);
    setSnapshotSuccessMsg(`✓ Snapshot snap-${nextRev} kreiran!`);
    setTimeout(() => setSnapshotSuccessMsg(""), 4000);

    if (apiConnected) {
      fetch("/api/v1/snapshots", { method: "POST" }).catch(console.error);
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
      fetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.dhcp.range_start = dhcpRangeStart;
          cfg.dhcp.range_end = dhcpRangeEnd;
          cfg.dhcp.lease_hours = dhcpLeaseHours;
          cfg.lan.ip_address = dhcpGateway;
          return fetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
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

    if (type === "wg") {
      const updated = wgPeers.filter((p) => p.id !== idOrIndex);
      setWgPeers(updated);
    } else if (type === "lease") {
      const updated = staticLeases.filter((_, idx) => idx !== idOrIndex);
      setStaticLeases(updated);
    } else if (type === "pf") {
      const updated = portForwardRules.filter((_, idx) => idx !== idOrIndex);
      setPortForwardRules(updated);
    }

    setDeleteConfirmTarget(null);

    if (apiConnected) {
      fetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          return fetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleAddWgPeer = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newWgPeerName || !newWgPeerIP) return;
    const newPeer = {
      id: `p-${Date.now()}`,
      name: newWgPeerName,
      ip: newWgPeerIP,
      traffic: "0 MB",
      active: "Just added",
    };
    setWgPeers([...wgPeers, newPeer]);
    setNewWgPeerName("");
    setNewWgPeerIP("");
    setAddWgModalOpen(false);
    setQrOpen(true);
  };

  // Sync stateful firewall toggle with Go REST API
  const handleToggleStateful = (val: boolean) => {
    setStatefulRules(val);
    if (apiConnected) {
      fetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.firewall.stateful_firewall = val;
          return fetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch((err) => console.error("API update error:", err));
    }
  };

  const handleAddStaticLease = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newLeaseHost || !newLeaseMAC || !newLeaseIP) return;
    const item = { hostname: newLeaseHost, mac: newLeaseMAC, ip: newLeaseIP };
    const updated = [...staticLeases, item];
    setStaticLeases(updated);
    setNewLeaseHost("");
    setNewLeaseMAC("");
    setNewLeaseIP("");
    setLeaseModalOpen(false);

    if (apiConnected) {
      fetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.dhcp.static_leases = updated.map((l, i) => ({
            id: `lease-${i + 1}`,
            hostname: l.hostname,
            mac: l.mac,
            ip_address: l.ip,
          }));
          return fetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  const handleAddPortForward = (e: React.FormEvent) => {
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
    setPortForwardRules(updated);
    setNewPfName("");
    setNewPfExtPort("");
    setNewPfIntIP("");
    setNewPfIntPort("");
    setPfModalOpen(false);

    if (apiConnected) {
      fetch("/api/v1/config")
        .then((res) => res.json())
        .then((cfg) => {
          cfg.firewall.port_forwards = updated.map((r, i) => ({
            id: `pf-${i + 1}`,
            name: r.name,
            protocol: r.proto.toLowerCase(),
            external_port: r.extPort,
            internal_ip: r.intIP,
            internal_port: r.intPort,
            enabled: r.enabled,
          }));
          return fetch("/api/v1/config", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(cfg),
          });
        })
        .catch(console.error);
    }
  };

  useEffect(() => {
    const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    const initialTheme = prefersDark ? "dark" : "light";
    setTheme(initialTheme);
    document.documentElement.dataset.theme = initialTheme;
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

  return (
    <main className="app-shell">
      <aside className={`sidebar ${menuOpen ? "is-open" : ""}`}>
        <div className="brand-row">
          <div className="brand-mark brand-favicon-wrap" aria-hidden="true">
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
          <div className="header-status">
            <span className="status-dot" />
            <div>
              <strong>Connected</strong>
              <span>PPPoE session active</span>
            </div>
          </div>
          <div className="topbar-divider" />
          <div className="service-chips">
            <span className="chip ok"><i className="status-dot" /> Firewall</span>
            <span className="chip ok"><i className="status-dot" /> WireGuard</span>
            <span className="chip ok"><i className="status-dot" /> DHCP</span>
            <span className="chip ok"><i className="status-dot" /> DNS</span>
            <span className="chip ok"><i className="status-dot" /> DDNS</span>
            <span className="chip ok"><i className="status-dot" /> Tunnel</span>
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
              style={{ fontSize: "13px", padding: "6px 12px", borderRadius: "10px", height: "40px" }}
            >
              Setup Wizard
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

        <div className="content">
          <section className="page-intro" id="overview">
            <div className="intro-strip">
              <span className="eyebrow">Friday, 24 July</span>
              <span className="intro-meta">Uptime <strong>{systemInfo.uptime || "18d 04h"}</strong></span>
              <span className="intro-meta">Public IP <strong>{systemInfo.public_ip || "185.33.42.117"}</strong></span>
              <span className="intro-meta">Last backup <strong>{systemInfo.last_backup || "6 days ago"}</strong></span>
              <span className="intro-meta">Last snapshot <strong>{systemInfo.last_snap || "8 min ago"}</strong></span>
              <span className="intro-meta"><strong className="up-to-date">✓ {systemInfo.update || "Up to date"}</strong></span>
            </div>
          </section>

          <section className="internet-card card" aria-labelledby="internet-title">
            <div className="internet-head">
              <div>
                <div className="section-label">
                  <span className="status-dot" />
                  Internet
                </div>
                <h2 id="internet-title">Online and stable</h2>
                <div className="internet-meta">
                  <span>
                    Public IP <code>185.33.42.117</code>
                  </span>
                  <span>Uptime 12d 08h 41m</span>
                  <span>MTU 1492</span>
                </div>
              </div>
              <div className="pppoe-pill">
                <span className="status-dot" />
                PPPoE connected
              </div>
            </div>

            <div className="traffic-summary">
              <div className="traffic-value">
                <span className="traffic-arrow download-arrow">↓</span>
                <div>
                  <span>Download</span>
                  <strong>924.8 <small>Mbps</small></strong>
                </div>
              </div>
              <div className="traffic-value">
                <span className="traffic-arrow upload-arrow">↑</span>
                <div>
                  <span>Upload</span>
                  <strong>96.2 <small>Mbps</small></strong>
                </div>
              </div>
              <div className="latency-value">
                <span>Latency</span>
                <strong>8 <small>ms</small></strong>
                <em>0.2% packet loss</em>
              </div>
            </div>

            <div className="chart-wrap">
              <div className="chart-head">
                <div>
                  <strong>Network traffic</strong>
                  <span>Last 24 hours</span>
                </div>
                <div className="chart-legend" aria-hidden="true">
                  <span><i className="legend-download" /> Download</span>
                  <span><i className="legend-upload" /> Upload</span>
                </div>
              </div>
              <TrafficChart theme={theme} />
              <div className="chart-axis" aria-hidden="true">
                <span>00:00</span>
                <span>06:00</span>
                <span>12:00</span>
                <span>18:00</span>
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
              <span className="quiet-meta">Alpine Linux · x86_64 · 42°C</span>
            </div>

            <div className="system-grid">
              <article className="card resource-card">
                <div className="resource-top">
                  <span>CPU</span>
                  <strong>14%</strong>
                </div>
                <Meter label="CPU usage" value={14} detail="4 cores · 1.4 GHz" />
                <p>Load average 0.18 · 0.22 · 0.19</p>
              </article>

              <article className="card resource-card">
                <div className="resource-top">
                  <span>Memory</span>
                  <strong>182 <small>MB</small></strong>
                </div>
                <Meter label="Memory usage" value={18} detail="182 MB of 1 GB" tone="violet" />
                <p>818 MB available</p>
              </article>

              <article className="card resource-card">
                <div className="resource-top">
                  <span>Disk</span>
                  <strong>1.8 <small>GB</small></strong>
                </div>
                <Meter label="Disk usage" value={23} detail="1.8 GB of 8 GB" tone="green" />
                <p>6.2 GB available · disk healthy</p>
              </article>
            </div>

            <div className="facts-strip card">
              <div>
                <span>Router uptime</span>
                <strong>18 days, 4 hours</strong>
              </div>
              <div>
                <span>WAN</span>
                <strong>2.5 GbE · full duplex</strong>
              </div>
              <div>
                <span>LAN</span>
                <strong>10.0.0.1/24</strong>
              </div>
              <div>
                <span>DNS</span>
                <strong>dnsmasq · 4 ms avg.</strong>
              </div>
            </div>
          </section>

          <section className="section-block" id="lan">
            <div className="section-heading">
              <div>
                <p className="eyebrow">LAN & DHCP</p>
                <h2>{staticLeases.length + 12} devices at home.</h2>
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

            <div className="two-column wide-left">
              <article className="card table-card">
                <div className="card-title-row">
                  <div>
                    <h3>Active leases</h3>
                    <p>12 dynamic · {staticLeases.length} static</p>
                  </div>
                  <button className="quiet-button" type="button">View all</button>
                </div>
                <div className="table-scroll">
                  <table>
                    <caption className="sr-only">Currently active DHCP leases</caption>
                    <thead>
                      <tr>
                        <th>Device</th>
                        <th>IP address</th>
                        <th>Connection</th>
                        <th>Lease</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td><strong>Vladimir’s MacBook Pro</strong><span>64:bc:58:1a:72:03</span></td>
                        <td><code>10.0.0.21</code></td>
                        <td><span className="micro-status"><i /> Active</span></td>
                        <td>18h 42m</td>
                      </tr>
                      <tr>
                        <td><strong>Living Room TV</strong><span>c8:3a:35:9e:41:20</span></td>
                        <td><code>10.0.0.32</code></td>
                        <td><span className="micro-status"><i /> Active</span></td>
                        <td>11h 04m</td>
                      </tr>
                      {staticLeases.map((lease, idx) => (
                        <tr key={idx}>
                          <td><strong>{lease.hostname}</strong><span>{lease.mac}</span></td>
                          <td><code>{lease.ip}</code></td>
                          <td><span className="micro-status static"><i /> Static</span></td>
                          <td style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
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
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </article>

              <aside className="card lan-summary">
                <div className="summary-icon">{staticLeases.length + 12}</div>
                <h3>Connected devices</h3>
                <p>Everything looks normal. No new devices joined in the last 24 hours.</p>
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
                    onChange={() => setStatefulRules((value) => !value)}
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
                    <span>Router dashboard is available from LAN only</span>
                  </div>
                  <span className="small-status neutral">Blocked</span>
                </div>
              </article>

              <article className="card settings-card">
                <div className="card-title-row">
                  <div>
                    <h3>Port forwarding</h3>
                    <p>{portForwardRules.length} service{portForwardRules.length === 1 ? "" : "s"} reachable from the internet.</p>
                  </div>
                  <button
                    className="quiet-button"
                    type="button"
                    onClick={() => setPfModalOpen(true)}
                  >
                    Add rule
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
                      onChange={() => {
                        const copy = [...portForwardRules];
                        copy[idx].enabled = !copy[idx].enabled;
                        setPortForwardRules(copy);
                      }}
                      label={`${rule.name} port forward`}
                    />
                  </div>
                ))}
                <div className="firewall-stat">
                  <div><strong>2,841</strong><span>Blocked today</span></div>
                  <div><strong>0</strong><span>Rules need attention</span></div>
                </div>
              </article>
            </div>
          </section>

          <section className="section-block" id="wireguard">
            <div className="section-heading">
              <div>
                <p className="eyebrow">WireGuard</p>
                <h2>Private access, anywhere.</h2>
              </div>
              <button className="button primary" type="button" onClick={() => setQrOpen(true)}>
                Generate QR code
              </button>
            </div>

            <div className="two-column wide-left">
              <article className="card wireguard-hero">
                <div className="wireguard-status">
                  <div className="wg-mark" aria-hidden="true">W</div>
                  <div>
                    <span>Interface wg0</span>
                    <h3>Running normally</h3>
                    <p><code>10.8.0.1/24</code> · UDP 51820 · 3 peers</p>
                  </div>
                  <span className="status-label success"><i className="status-dot" /> Active</span>
                </div>
                <div className="peer-list">
                  {wgPeers.map((peer) => (
                    <div className="peer-row" key={peer.id} style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <div style={{ display: "flex", gap: "12px", alignItems: "center" }}>
                        <div className="peer-avatar">{peer.name.substring(0, 2).toUpperCase()}</div>
                        <div><strong>{peer.name}</strong><span>{peer.ip} · latest handshake {peer.active}</span></div>
                      </div>
                      <div style={{ display: "flex", gap: "16px", alignItems: "center" }}>
                        <div className="peer-traffic"><strong>{peer.traffic}</strong><span>↓ 3.9 · ↑ 0.9</span></div>
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
                <span className="mini-label">This month</span>
                <strong>18.4 <small>GB</small></strong>
                <p>Secure traffic across {wgPeers.length} peers.</p>
                <div className="split-meter" aria-hidden="true">
                  <span style={{ width: "72%" }} />
                  <i style={{ width: "28%" }} />
                </div>
                <div className="split-legend">
                  <span><i className="download-key" /> Download 13.2 GB</span>
                  <span><i className="upload-key" /> Upload 5.2 GB</span>
                </div>
                <button
                  className="button secondary full"
                  type="button"
                  onClick={() => setAddWgModalOpen(true)}
                >
                  + Add WireGuard Peer
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
                      <p>Public IP is synchronized.</p>
                    </div>
                    <span className="status-label success"><i className="status-dot" /> Updated</span>
                  </div>
                  <div className="cloud-host">
                    <span>Hostname</span>
                    <code>{cfConfig.domain}</code>
                  </div>
                  <div className="cloud-meta">
                    <span>185.33.42.117</span>
                    <span>Checked 42 seconds ago</span>
                  </div>
                </div>
              </article>

              <article className="card cloud-card">
                <div className="cloud-icon tunnel" aria-hidden="true">CT</div>
                <div>
                  <div className="card-title-row">
                    <div>
                      <h3>Cloudflare Tunnel</h3>
                      <p>Encrypted outbound connection.</p>
                    </div>
                    <span className="status-label success"><i className="status-dot" /> Healthy</span>
                  </div>
                  <div className="cloud-host">
                    <span>Tunnel</span>
                    <code>minimalrouter-home</code>
                  </div>
                  <div className="cloud-meta">
                    <span>2 connections</span>
                    <span>Frankfurt · 24 ms</span>
                  </div>
                </div>
              </article>
            </div>
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
                  <h3>{snapshotsList.length > 0 ? `Revision ${snapshotsList[0].revision} (${snapshotsList[0].time})` : "Protected 8 minutes ago"}</h3>
                  <p>{snapshotsList.length > 0 ? snapshotsList[0].label : "Firewall rule update · Configuration revision 42"}</p>
                  {snapshotSuccessMsg && (
                    <div style={{ color: "#34C759", fontSize: "12px", fontWeight: 600, marginTop: "4px" }}>
                      {snapshotSuccessMsg}
                    </div>
                  )}
                </div>
                <div style={{ display: "flex", gap: "10px", flexWrap: "wrap" }}>
                  <button
                    className="button primary"
                    type="button"
                    onClick={handleMakeSnapshot}
                  >
                    + Make snapshot
                  </button>
                  <button
                    className="button secondary"
                    type="button"
                    onClick={() => setSnapshotsModalOpen(true)}
                  >
                    View snapshots
                  </button>
                </div>
              </article>
              <article className="card recovery-card">
                <span className="recovery-index">02</span>
                <div>
                  <span className="mini-label">System update</span>
                  <h3>You’re up to date</h3>
                  <p>Minimal Router OS 0.1.0 · Alpine 3.22</p>
                </div>
                <button className="button secondary" type="button">Check again</button>
              </article>
              <article className="card recovery-card">
                <span className="recovery-index">03</span>
                <div>
                  <span className="mini-label">Backup</span>
                  <h3>Encrypted backup ready</h3>
                  <p>Last exported 6 days ago · Secrets included</p>
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

          <footer>
            <span>Minimal Router OS · Design preview</span>
            <span>Local management · HTTPS only · WAN access blocked</span>
          </footer>
        </div>
      </div>

      {qrOpen && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setQrOpen(false)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="qr-title"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button
              className="modal-close"
              type="button"
              aria-label="Close QR code"
              onClick={() => setQrOpen(false)}
            >
              ×
            </button>
            <p className="eyebrow">WireGuard peer</p>
            <h2 id="qr-title">Connect your phone</h2>
            <p className="modal-copy">
              Scan this code from the WireGuard app. The private configuration
              is shown only for this preview session.
            </p>
            <QrPreview />
            <div className="qr-peer">
              <div>
                <span>Peer name</span>
                <strong>New iPhone</strong>
              </div>
              <div>
                <span>Address</span>
                <code>10.8.0.5/32</code>
              </div>
            </div>
            <div className="modal-actions">
              <button className="button secondary" type="button" onClick={() => setQrOpen(false)}>
                Cancel
              </button>
              <button className="button primary" type="button">
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
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" onClick={() => setLeaseModalOpen(false)}>×</button>
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
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" onClick={() => setPfModalOpen(false)}>×</button>
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
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" onClick={() => setAddWgModalOpen(false)}>×</button>
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
              <div className="modal-actions" style={{ marginTop: '8px' }}>
                <button className="button secondary" type="button" onClick={() => setAddWgModalOpen(false)}>Cancel</button>
                <button className="button primary" type="submit">Generate QR Code & Add</button>
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
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" onClick={() => setCfModalOpen(false)}>×</button>
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
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" onClick={() => setDhcpModalOpen(false)}>×</button>
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
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button className="modal-close" type="button" onClick={() => setDdnsModalOpen(false)}>×</button>
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
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "600px" }}
          >
            <button className="modal-close" type="button" onClick={() => setSnapshotsModalOpen(false)}>×</button>
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
                    onClick={() => {
                      alert(`Sistem vraćen na snapshot ${snap.id} (Revision ${snap.revision})!`);
                      setSnapshotsModalOpen(false);
                    }}
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
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "560px" }}
          >
            <button className="modal-close" type="button" onClick={() => setBackupModalOpen(false)}>×</button>
            <p className="eyebrow">Backup & Recovery</p>
            <h2>Backup & Restore Configuration</h2>
            <p className="modal-copy">
              Export encrypted JSON backup bundles or restore system configuration from a file.
            </p>

            {backupNotice && (
              <div style={{ padding: "12px 16px", borderRadius: "10px", background: "#34C75915", color: "#34C759", fontWeight: 600, fontSize: "14px", marginTop: "12px" }}>
                {backupNotice}
              </div>
            )}

            <div style={{ marginTop: "24px", display: "flex", flexDirection: "column", gap: "20px" }}>
              <div style={{ padding: "20px", borderRadius: "16px", background: "var(--surface-muted)", border: "1px solid var(--separator)" }}>
                <h3 style={{ fontSize: "16px", fontWeight: 650, marginBottom: "6px" }}>Export Backup File</h3>
                <p style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
                  Download an encrypted backup bundle containing all static leases, firewall port forwards, WireGuard peers, and Cloudflare DDNS settings.
                </p>
                <button className="button primary" type="button" onClick={handleExportBackup}>
                  ⬇ Download Backup (.json)
                </button>
              </div>

              <div style={{ padding: "20px", borderRadius: "16px", background: "var(--surface-muted)", border: "1px solid var(--separator)" }}>
                <h3 style={{ fontSize: "16px", fontWeight: 650, marginBottom: "6px" }}>Restore From Backup File</h3>
                <p style={{ fontSize: "13px", color: "var(--text-secondary)", marginBottom: "16px" }}>
                  Upload a previously exported Minimal Router OS `.json` backup file to restore full system configuration.
                </p>
                <input
                  type="file"
                  accept=".json"
                  onChange={handleImportBackup}
                  style={{ fontSize: "14px" }}
                />
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
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "600px", borderRadius: "24px" }}
          >
            <button className="modal-close" type="button" onClick={() => setProfileModalOpen(false)}>×</button>
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
              <h3 style={{ fontSize: "15px", fontWeight: 650, marginTop: "8px" }}>Promjena Administrator Lozinke (Argon2id)</h3>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Trenutna lozinka</label>
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
                  <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Nova lozinka (min 15 karaktera)</label>
                  <input
                    type="password"
                    placeholder="Najmanje 15 karaktera"
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                    required
                  />
                </div>
                <div style={{ flex: 1 }}>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>Potvrdite novu lozinku</label>
                  <input
                    type="password"
                    placeholder="Ponovite lozinku"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)" }}
                    required
                  />
                </div>
              </div>
              <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "4px" }}>
                <button className="button primary" type="submit" style={{ fontSize: "13px", padding: "8px 18px" }}>
                  Sačuvaj Novu Lozinku
                </button>
              </div>
            </form>

            <div style={{ marginTop: "24px", paddingTop: "16px", borderTop: "1px solid var(--separator)", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
              <div>
                <strong style={{ display: "block", fontSize: "14px" }}>Sistemska Dijagnostika</strong>
                <span style={{ fontSize: "12px", color: "var(--text-secondary)" }}>Izvoz tehničkog izvještaja sa cenzurisanim tajnama</span>
              </div>
              <button
                className="button secondary"
                type="button"
                style={{ fontSize: "13px" }}
                onClick={() => {
                  window.location.href = "/api/v1/system/diagnostics";
                }}
              >
                ⬇ Dijagnostika
              </button>
            </div>

            <div className="modal-actions" style={{ marginTop: "24px", borderTop: "1px solid var(--separator)", paddingTop: "16px" }}>
              <button className="button secondary" type="button" onClick={() => setProfileModalOpen(false)}>Zatvori</button>
              <button
                className="button primary"
                type="button"
                style={{ background: "#FF3B30", borderColor: "#FF3B30" }}
                onClick={() => {
                  fetch("/api/v1/auth/logout", { method: "POST" }).finally(() => {
                    alert("Odjavljeni ste sa Minimal Router OS!");
                    setProfileModalOpen(false);
                  });
                }}
              >
                Odjavi se (Logout)
              </button>
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
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "540px" }}
          >
            <button className="modal-close" type="button" onClick={() => setDnsModalOpen(false)}>×</button>
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
                  <strong style={{ display: 'block', fontSize: '13px' }}>Enforce DNS-over-HTTPS / TLS</strong>
                  <span style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>Encrypt outgoing DNS queries to prevent ISP tracking</span>
                </div>
                <input
                  type="checkbox"
                  checked={dnsOverHttps}
                  onChange={(e) => setDnsOverHttps(e.target.checked)}
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

      {deleteConfirmTarget && (
        <div className="modal-backdrop" role="presentation" onMouseDown={() => setDeleteConfirmTarget(null)}>
          <section
            className="modal"
            role="dialog"
            aria-modal="true"
            onMouseDown={(event) => event.stopPropagation()}
            style={{ maxWidth: "440px", borderRadius: "20px" }}
          >
            <button className="modal-close" type="button" onClick={() => setDeleteConfirmTarget(null)}>×</button>
            <p className="eyebrow" style={{ color: "#FF3B30" }}>Potvrda brisanja</p>
            <h2>Da li ste sigurni?</h2>
            <p className="modal-copy" style={{ margin: "12px 0 24px", color: "var(--text-secondary)" }}>
              Da li ste sigurni da želite obrisati <strong>"{deleteConfirmTarget.name}"</strong>? Ova akcija će odmah ukloniti podešavanja iz rutera.
            </p>
            <div className="modal-actions">
              <button className="button secondary" type="button" onClick={() => setDeleteConfirmTarget(null)}>
                Otkaži
              </button>
              <button
                className="button primary"
                type="button"
                onClick={handleConfirmDelete}
                style={{ background: "#FF3B30", borderColor: "#FF3B30" }}
              >
                Da, izbriši uređaj
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
