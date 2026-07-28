# Session 6 — Optimizacija i Migracija (26. Juli 2026)

> Istorijski radni zapis, ne trenutni sigurnosni ili release ugovor. Ako se
> razlikuje od `README.md`, `SECURITY.md` ili `docs/SECURITY_REVIEW.md`, važe
> trenutni dokumenti.

## Dnevni red
1. ~~Ukloni Python za CSRF~~ → Već čist Go (crypto/rand)
2. Bash → ash → završeno naknadno direktnim `wg`/`ip` adapterom
3. ~~Migiraj Next.js → Vite + React~~ → Kompletno
4. ~~Optimizuj memoriju~~ → 216 MB → 152 MB (-64 MB)
5. Poređenje sa OpenWrt i pfSense

---

## 1. Python3 dependency — VEĆ REŠENO
- CSRF token generiše se čistim Go-om: `crypto/rand` + `hex.EncodeToString` u `internal/auth/session.go:46-52`
- Python3 se koristi samo u VM test skriptama (ne u production)
- **Zaključak:** Nikakve promene nisu potrebne

## 2. Bash → ash — REŠENO I RETESTIRANO
- Sve router skripte koriste `#!/bin/sh` (BusyBox ash na Alpineu)
- Samo `create-vm.sh` (Proxmox helper) koristi bash — ne radi na routeru
- Router instalira samo `wireguard-tools-wg`; `wg-quick` i Bash nijesu prisutni.
- `router-applyd` koristi fiksne `wg setconf` i `ip` argumente bez shell hookova.
- ARM64 integration test je ostvario handshake i prenio šifrovane pakete.

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

### Istorijsko mjerenje
- Pre optimizacije: **216 MB** RAM (idle)
- Tadašnja procjena poslije optimizacije bila je 152 MB. Aktuelno mjerenje od
  2026-07-28 je 140 MiB nakon restarta i 203 MiB poslije setup/config rada.

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
argonMemory = 32 * 1024  // istorijski eksperiment; sada je 64 MiB
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

## 5. Aktuelno poređenje sa OpenWrt i pfSense

| Resurs | Naš Router | OpenWrt | pfSense |
|--------|-----------|---------|---------|
| **RAM** | 140 MiB idle; 203 MiB poslije rada; 512 MiB testirani minimum, 1 GiB komotno | 64 MiB minimum, 128 MiB poželjnije | 1 GiB minimum |
| **Disk** | ~60 MiB početni payload; 4 GiB bench / 8 GiB produkcija | >32 MiB flash preporučeno | 8 GB minimum |
| **Web dashboard** | 360 KiB | zavisi od image-a | uključen |
| **Shell** | BusyBox ash; bez Basha i `wg-quick` | BusyBox ash | FreeBSD shell |

OpenWrt i pfSense brojevi su njihove zvanične preporuke/minimumi, a naši su
izmjereni u ARM64 VM-u. Veličina ne dokazuje veću sigurnost niti feature
paritet. Važeći detalji su u
[`docs/RESOURCE_AND_HARDWARE_TEST.md`](docs/RESOURCE_AND_HARDWARE_TEST.md).

## VM Test Environment
- Alpine 3.22.5 aarch64 (VM via Apple Virtualization.framework)
- 2 ARM cores, 2 GB RAM, 1 GB disk
- NAT networking (eth0) + dummy LAN (eth1)
- API: `https://192.168.1.1:8443`

## Fajlovi koje treba zapamtiti
- `cmd/routerd/main.go` — GOGC, GOMEMLIMIT, IdleTimeout
- `cmd/router-applyd/main.go` — GOGC, GOMEMLIMIT
- `internal/config/store.go` — SQLite pool limits, cache_size pragma
- `internal/auth/argon2.go` — sada 64 MiB; 32 MiB je napušten eksperiment
- `web/package.json` — Vite dependencies
- `web/vite.config.ts` — Vite config
- `web/src/` — novi source direktorijum
- `packaging/alpine/install.sh` — web/dist/ reference
