"use client";

import { useEffect, useMemo, useRef, useState } from "react";

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
  const [statefulRules, setStatefulRules] = useState(true);
  const [portForward, setPortForward] = useState(true);
  const [activeSection, setActiveSection] = useState("overview");
  const [fontScale, setFontScale] = useState(100);

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
            <button className="avatar-button" type="button" aria-label="Administrator menu">
              VP
            </button>
          </div>
        </header>

        <div className="content">
          <section className="page-intro" id="overview">
            <div className="intro-strip">
              <span className="eyebrow">Friday, 24 July</span>
              <span className="intro-meta">Uptime <strong>18d 04h</strong></span>
              <span className="intro-meta">Public IP <strong>185.33.42.117</strong></span>
              <span className="intro-meta">Last backup <strong>6 days ago</strong></span>
              <span className="intro-meta">Last snapshot <strong>8 min ago</strong></span>
              <span className="intro-meta"><strong className="up-to-date">✓ Up to date</strong></span>
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
                <h2>14 devices at home.</h2>
              </div>
              <button className="button secondary" type="button">Manage DHCP</button>
            </div>

            <div className="two-column wide-left">
              <article className="card table-card">
                <div className="card-title-row">
                  <div>
                    <h3>Active leases</h3>
                    <p>12 dynamic · 2 static</p>
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
                      <tr>
                        <td><strong>iPhone</strong><span>72:11:ed:bc:0c:95</span></td>
                        <td><code>10.0.0.44</code></td>
                        <td><span className="micro-status"><i /> Active</span></td>
                        <td>22h 18m</td>
                      </tr>
                      <tr>
                        <td><strong>Home Assistant</strong><span>00:e0:4c:68:01:91</span></td>
                        <td><code>10.0.0.10</code></td>
                        <td><span className="micro-status static"><i /> Static</span></td>
                        <td>Reserved</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </article>

              <aside className="card lan-summary">
                <div className="summary-icon">14</div>
                <h3>Connected devices</h3>
                <p>Everything looks normal. No new devices joined in the last 24 hours.</p>
                <div className="summary-list">
                  <div><span>DHCP range</span><code>10.0.0.20–200</code></div>
                  <div><span>Lease time</span><strong>24 hours</strong></div>
                  <div><span>Static addresses</span><strong>2 reserved</strong></div>
                  <div><span>Gateway</span><code>10.0.0.1</code></div>
                </div>
                <button className="button primary full" type="button">Add static lease</button>
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
                    <p>1 service is reachable from the internet.</p>
                  </div>
                  <button className="quiet-button" type="button">Add rule</button>
                </div>
                <div className="forward-rule">
                  <div className="port-badge">443</div>
                  <div>
                    <strong>Home Assistant</strong>
                    <span>TCP · 10.0.0.10:8123</span>
                  </div>
                  <Toggle
                    checked={portForward}
                    onChange={() => setPortForward((value) => !value)}
                    label="Home Assistant port forward"
                  />
                </div>
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
                  <div className="peer-row">
                    <div className="peer-avatar">MB</div>
                    <div><strong>MacBook Pro</strong><span>10.8.0.2 · latest handshake 18s ago</span></div>
                    <div className="peer-traffic"><strong>4.8 GB</strong><span>↓ 3.9 · ↑ 0.9</span></div>
                  </div>
                  <div className="peer-row">
                    <div className="peer-avatar violet">IP</div>
                    <div><strong>iPhone</strong><span>10.8.0.3 · latest handshake 2m ago</span></div>
                    <div className="peer-traffic"><strong>1.2 GB</strong><span>↓ 0.9 · ↑ 0.3</span></div>
                  </div>
                  <div className="peer-row">
                    <div className="peer-avatar gray">TR</div>
                    <div><strong>Travel laptop</strong><span>10.8.0.4 · last seen 3d ago</span></div>
                    <div className="peer-traffic muted"><strong>Offline</strong><span>No traffic</span></div>
                  </div>
                </div>
              </article>

              <aside className="card wireguard-summary">
                <span className="mini-label">This month</span>
                <strong>18.4 <small>GB</small></strong>
                <p>Secure traffic across all peers.</p>
                <div className="split-meter" aria-hidden="true">
                  <span style={{ width: "72%" }} />
                  <i style={{ width: "28%" }} />
                </div>
                <div className="split-legend">
                  <span><i className="download-key" /> Download 13.2 GB</span>
                  <span><i className="upload-key" /> Upload 5.2 GB</span>
                </div>
                <button className="button secondary full" type="button">Manage peers</button>
              </aside>
            </div>
          </section>

          <section className="section-block" id="cloudflare">
            <div className="section-heading">
              <div>
                <p className="eyebrow">Cloudflare</p>
                <h2>Your home, reliably reachable.</h2>
              </div>
            </div>

            <div className="cloud-grid">
              <article className="card cloud-card">
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
                    <code>home.example.net</code>
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
                  <h3>Protected 8 minutes ago</h3>
                  <p>Firewall rule update · Configuration revision 42</p>
                </div>
                <button className="button secondary" type="button">View snapshots</button>
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
                <button className="button secondary" type="button">Backup & restore</button>
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
    </main>
  );
}
