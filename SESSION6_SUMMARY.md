# Session 6 — Optimizacija i Migracija (26. Juli 2026)

## Dnevni red
1. ~~Ukloni Python za CSRF~~ → Već čist Go (crypto/rand)
2. ~~Zameni bash sa ash~~ → Već koristi #!/bin/sh (BusyBox ash)
3. ~~Migiraj Next.js → Vite + React~~ → Kompletno
4. ~~Optimizuj memoriju~~ → 216 MB → 152 MB (-64 MB)
5. Poređenje sa OpenWrt i pfSense

---

## 1. Python3 dependency — VEĆ REŠENO
- CSRF token generiše se čistim Go-om: `crypto/rand` + `hex.EncodeToString` u `internal/auth/session.go:46-52`
- Python3 se koristi samo u VM test skriptama (ne u production)
- **Zaključak:** Nikakve promene nisu potrebne

## 2. Bash → ash — VEĆ REŠENO
- Sve router skripte koriste `#!/bin/sh` (BusyBox ash na Alpineu)
- Samo `create-vm.sh` (Proxmox helper) koristi bash — ne radi na routeru
- **Zaključak:** Nikakve promene nisu potrebne

## 3. Migracija vinext → Vite + React

### Razlog
- Dashboard je čist React SPA (3903 linije, `"use client"`)
- Next.js se koristio samo kao build tool — nema SSR, nema API routes, nema server components
- Cloudflare Worker nepotreban za router
- Vinext = Cloudflare-ov Next.js kompatibilni sloj na Vite-u

### Šta je urađeno
- `app/page.tsx` → `src/App.tsx` (uklonjena `"use client"` direktiva)
- `app/layout.tsx` → `index.html` + `src/main.tsx`
- `app/globals.css` → `src/index.css`
- `app/lib/api.ts` → `src/lib/api.ts`
- `app/components/` → `src/components/`
- Kreiran novi `vite.config.ts` (samo `@vitejs/plugin-react`)
- Kreiran novi `package.json` (2 dependencies, 7 devDependencies)
- Ažuriran `tsconfig.json` (path alias `@/*` → `./src/*`)
- Ažuriran `install.sh` i `build-iso.sh` (`web/dist/client/` → `web/dist/`)
- Uklonjeno: `worker/`, `scripts/`, `build/`, `next.config.ts`, `.next/`, `.vinext/`, `.wrangler/`

### Uklonjeni dependencies
- `next`, `vinext`, `@cloudflare/vite-plugin`, `@vitejs/plugin-rsc`, `react-server-dom-webpack`, `eslint-config-next`, `sharp`, `wrangler`

### Rezultat
| Metrika | Pre (vinext) | Posle (Vite) |
|---------|-------------|--------------|
| Dependencies | 3 | 2 |
| DevDependencies | 13 | 7 |
| Build time | ~5s | 266ms |
| Bundle JS | ~92 KB | 84 KB gzipped |
| Web dashboard | ~30 MB | **360 KB** |
| Total output | ~358 KB | 93 KB gzipped |

### Popravljeni bugovi
- 3x `setOperationError(null)` → `setOperationError("")` (TypeScript greška — state je `string`, ne `string | null`)

## 4. Optimizacija memorije

### Problem
- Pre optimizacije: **216 MB** RAM (idle)
- OpenWrt koristi 30-60 MB, pfSense 300-500 MB

### Analiza potrošnje
| Komponenta | Procena |
|-----------|---------|
| Linux kernel + Alpine base | ~30-40 MB |
| Go runtime (GOGC=100, nema GOMEMLIMIT) | ~60-80 MB |
| SQLite pool (unlimited connections) | ~20-40 MB |
| dnsmasq | ~5-10 MB |
| nftables (kernel) | ~5-10 MB |
| TLS + HTTP buffers | ~5-10 MB |

### Implementirane optimizacije

#### 1. Go runtime memory (štedi ~40-60 MB)
```go
// cmd/routerd/main.go i cmd/router-applyd/main.go
debug.SetGCPercent(50)           // GC na 1.5x umesto 2x
debug.SetMemoryLimit(128 << 20)  // 128 MB hard limit (routerd)
debug.SetMemoryLimit(64 << 20)   // 64 MB hard limit (router-applyd)
```

#### 2. SQLite connection pool (štedi ~20-40 MB)
```go
// internal/config/store.go
db.SetMaxOpenConns(4)
db.SetMaxIdleConns(2)
db.SetConnMaxIdleTime(5 * time.Minute)
```

#### 3. SQLite cache_size (štedi ~10-20 MB)
```sql
PRAGMA cache_size=-2000;  -- 2 MB limit po konekciji
```

#### 4. Argon2id memory (štedi ~32 MB transient)
```go
// internal/auth/argon2.go
argonMemory = 32 * 1024  // 32 MiB (bilo 64 MiB)
```

#### 5. HTTP IdleTimeout (štedi ~5-10 MB)
```go
// cmd/routerd/main.go
IdleTimeout: 15 * time.Second  // bilo 60s
```

#### 6. Go binary strip (-ldflags="-s -w")
- routerd: 11.1 MB → 11 MB (-0.1 MB)
- router-applyd: 9.5 MB → 9.5 MB

### Rezultat
| Metrika | Pre | Posle | Ušteda |
|---------|-----|-------|--------|
| RAM (free -m) | 216 MB | **152 MB** | **-64 MB (30%)** |
| RAM (API) | 355 MB | **290 MB** | **-65 MB (18%)** |

### Nova memorija raspodela
| Komponenta | Pre | Posle |
|-----------|-----|-------|
| Go runtime (GOGC+GOMEMLIMIT) | ~80 MB | ~40 MB |
| SQLite pool | ~30 MB | ~10 MB |
| HTTP idle connections | ~10 MB | ~3 MB |
| Kernel + Alpine | ~40 MB | ~40 MB |
| dnsmasq + nftables | ~15 MB | ~15 MB |
| Ostalo | ~41 MB | ~44 MB |
| **Ukupno** | **~216 MB** | **~152 MB** |

## 5. Poređenje sa OpenWrt i pfSense

| Resurs | Naš Router | OpenWrt | pfSense | OPNsense |
|--------|-----------|---------|---------|----------|
| **RAM (idle)** | **152 MB** | 30-60 MB | 300-500 MB | 500+ MB |
| **Disk** | **77 MB** | 8-16 MB | 8+ GB | 8+ GB |
| **Paketi** | **100** | ~100 | ~500+ | ~500+ |
| **Web dashboard** | **360 KB** | ~5 MB | ~50 MB | ~50 MB |
| **Go binary** | **20.5 MB** | N/A | N/A | N/A |
| **Shell** | ash/busybox | ash/busybox | csh | csh |
| **Firewall** | nftables | nftables | pf (BSD) | pf (BSD) |
| **Config format** | JSON+SQLite | UCI (text) | XML (text) | XML (text) |
| **Init system** | OpenRC | procd | rc | rc |
| **DNS** | dnsmasq+AdGuard | dnsmasq | unbound | unbound |
| **VPN** | WireGuard | WireGuard | OpenVPN+WG | OpenVPN+WG |

### Zaključak
- Naš router je **3x lakši od pfSense** po RAM-u
- **100x manji disk** od pfSense
- **80x lakši web dashboard** od pfSense
- Još uvek **3-5x teži od OpenWrt** — to je cena modernog Go + React stack-a
- Ali zato imamo: ECDSA TLS, bcrypt, LUKS, Ed25519 firmware verification, SQLite audit log

## VM Test Environment
- Alpine 3.22.5 aarch64 (VM via Apple Virtualization.framework)
- 2 ARM cores, 2 GB RAM, 1 GB disk
- NAT networking (eth0) + dummy LAN (eth1)
- API: `https://192.168.1.1:8443`

## Fajlovi koje treba zapamtiti
- `cmd/routerd/main.go` — GOGC, GOMEMLIMIT, IdleTimeout
- `cmd/router-applyd/main.go` — GOGC, GOMEMLIMIT
- `internal/config/store.go` — SQLite pool limits, cache_size pragma
- `internal/auth/argon2.go` — argonMemory 32 MiB
- `web/package.json` — Vite dependencies
- `web/vite.config.ts` — Vite config
- `web/src/` — novi source direktorijum
- `packaging/alpine/install.sh` — web/dist/ reference
