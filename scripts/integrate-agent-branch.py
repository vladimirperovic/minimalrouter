#!/usr/bin/env python3
from pathlib import Path
import re


def replace_once(path: str, old: str, new: str) -> None:
    file = Path(path)
    text = file.read_text()
    if new in text:
        return
    if old not in text:
        raise SystemExit(f"pattern missing in {path}: {old[:120]!r}")
    file.write_text(text.replace(old, new, 1))


def regex_once(path: str, pattern: str, replacement: str) -> None:
    file = Path(path)
    text = file.read_text()
    updated, count = re.subn(pattern, replacement, text, count=1, flags=re.S)
    if count == 0:
        if replacement.strip() in text:
            return
        raise SystemExit(f"regex missing in {path}: {pattern[:120]!r}")
    file.write_text(updated)


# Canonical validation: remove the obsolete hard failure and validate real profiles.
regex_once(
    "internal/config/validation.go",
    r"\n\tfor i, fd := range c\.AdGuard\.FilterDevices \{.*?\n\tif c\.AdGuard\.BlocklistURL != \"\" \{",
    "\n\terrs = append(errs, c.validateDeviceProfiles(lanNetwork)...)\n\tif c.AdGuard.BlocklistURL != \"\" {",
)

# Generate DNS-derived nftables sets and enforce schedules before established flows.
replace_once(
    "internal/services/nftables.go",
    '\tbuf.WriteString("table inet minimalrouter {\\n")\n\n\t// Input Chain',
    '\tbuf.WriteString("table inet minimalrouter {\\n")\n\twriteDeviceProfileObjects(&buf, cfg)\n\n\t// Input Chain',
)
replace_once(
    "internal/services/nftables.go",
    '\tbuf.WriteString("    # Reject invalid before established state\\n")\n\tbuf.WriteString("    ct state invalid drop\\n")\n\tbuf.WriteString("    # Allow established/related\\n")',
    '\tbuf.WriteString("    # Reject invalid before established state\\n")\n\tbuf.WriteString("    ct state invalid drop\\n")\n\tif len(activeManagedServices(cfg)) > 0 {\n\t\tbuf.WriteString("    # Device schedules run before established acceptance so expired streams are cut\\n")\n\t\tbuf.WriteString("    jump device_profiles\\n")\n\t}\n\tbuf.WriteString("    # Allow established/related\\n")',
)

# Setup-only and authenticated NIC discovery endpoints.
replace_once(
    "internal/api/server.go",
    '\tmux.HandleFunc("GET /api/v1/setup/status", sh(s.handleSetupStatus))',
    '\tmux.HandleFunc("GET /api/v1/setup/status", sh(s.handleSetupStatus))\n\tmux.HandleFunc("GET /api/v1/setup/interfaces", sh(s.handleDiscoverSetupInterfaces))',
)
replace_once(
    "internal/api/server.go",
    '\tmux.HandleFunc("GET /api/v1/system", sh(s.authMiddleware(s.handleGetSystem)))',
    '\tmux.HandleFunc("GET /api/v1/system", sh(s.authMiddleware(s.handleGetSystem)))\n\tmux.HandleFunc("GET /api/v1/system/interfaces", sh(s.authMiddleware(s.handleDiscoverInterfaces)))',
)

# Narrow the privileged process before creating its IPC listener.
replace_once(
    "cmd/router-applyd/main.go",
    '\tdebug.SetMemoryLimit(64 << 20)\n\n\tlog.Println',
    '\tdebug.SetMemoryLimit(64 << 20)\n\tif err := hardenProcess(); err != nil {\n\t\tlog.Fatalf("applyd process hardening failed: %v", err)\n\t}\n\n\tlog.Println',
)

# Production bundle now has no inline style or script attributes.
replace_once(
    "cmd/routerd/main.go",
    "style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
    "style-src 'self'; style-src-attr 'none'; script-src-attr 'none'; img-src 'self' data:; font-src 'self'; connect-src 'self'; worker-src 'none'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
)

# Keep fallback interface choices strongly typed.
replace_once(
    "web/src/components/SetupWizard.tsx",
    "  const options = useMemo(() => {",
    "  const options = useMemo<InterfaceInfo[]>(() => {",
)

# Integrate the new audit component into the component dashboard.
replace_once(
    "web/src/DashboardApp.tsx",
    'import DNSFilterPanel from "./components/DNSFilterPanel";',
    'import DNSFilterPanel from "./components/DNSFilterPanel";\nimport AuditLogPanel from "./components/AuditLogPanel";',
)
replace_once(
    "web/src/DashboardApp.tsx",
    '| "recovery" | "security";',
    '| "recovery" | "security" | "logs";',
)
replace_once(
    "web/src/DashboardApp.tsx",
    '  ["security", "Security"],\n];',
    '  ["security", "Security"],\n  ["logs", "Logs"],\n];',
)
replace_once(
    "web/src/DashboardApp.tsx",
    '      </main>\n    </div>',
    '        {active === "logs" && <AuditLogPanel />}\n      </main>\n    </div>',
)

css = Path("web/src/DashboardApp.css")
css_text = css.read_text()
if ".audit-table-scroll" not in css_text:
    css.write_text(css_text + """

.section-copy {
  margin: 6px 0 0;
  color: var(--text-secondary, #6e6e73);
  font-size: 13px;
  line-height: 1.5;
}

.toolbar,
.filter-buttons {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.audit-table-scroll {
  max-height: 620px;
  overflow: auto;
}

.audit-category {
  border-radius: 999px;
  padding: 3px 7px;
  background: rgba(0, 113, 227, 0.1);
  color: #0071e3;
  font-size: 10px;
  font-weight: 750;
  text-transform: capitalize;
}
""")

# Recovery/update utilities are installed with every distribution.
replace_once(
    "Makefile",
    '\tgo build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd ./cmd/router-applyd',
    '\tgo build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd ./cmd/router-applyd\n\tgo build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-recovery ./cmd/router-recovery\n\tgo build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-update ./cmd/router-update\n\tgo build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/firmware-sign ./cmd/firmware-sign',
)
replace_once(
    "Makefile",
    '\tCGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd-linux-amd64 ./cmd/router-applyd',
    '\tCGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd-linux-amd64 ./cmd/router-applyd\n\tCGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-recovery-linux-amd64 ./cmd/router-recovery\n\tCGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-update-linux-amd64 ./cmd/router-update',
)
replace_once(
    "Makefile",
    '\tCGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd-linux-arm64 ./cmd/router-applyd',
    '\tCGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-applyd-linux-arm64 ./cmd/router-applyd\n\tCGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-recovery-linux-arm64 ./cmd/router-recovery\n\tCGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/router-update-linux-arm64 ./cmd/router-update',
)
replace_once(
    "Makefile",
    '\t@cp bin/router-applyd-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/router-applyd-arm64',
    '\t@cp bin/router-applyd-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/router-applyd-arm64\n\t@cp bin/router-recovery-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/router-recovery-arm64\n\t@cp bin/router-update-linux-arm64 build/dist/minimalrouter-linux-arm64/bin/router-update-arm64',
)
replace_once(
    "Makefile",
    '\t@cp bin/router-applyd-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/router-applyd-amd64',
    '\t@cp bin/router-applyd-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/router-applyd-amd64\n\t@cp bin/router-recovery-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/router-recovery-amd64\n\t@cp bin/router-update-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/router-update-amd64',
)
replace_once(
    "packaging/alpine/install-dist.sh",
    '    "bin/router-applyd-${BIN_ARCH}" \\\n    "web/dist/index.html"',
    '    "bin/router-applyd-${BIN_ARCH}" \\\n    "bin/router-recovery-${BIN_ARCH}" \\\n    "bin/router-update-${BIN_ARCH}" \\\n    "web/dist/index.html"',
)
replace_once(
    "packaging/alpine/install-dist.sh",
    'install -m 0755 "bin/router-applyd-${BIN_ARCH}" /usr/sbin/router-applyd',
    'install -m 0755 "bin/router-applyd-${BIN_ARCH}" /usr/sbin/router-applyd\ninstall -m 0750 "bin/router-recovery-${BIN_ARCH}" /usr/sbin/router-recovery\ninstall -m 0750 "bin/router-update-${BIN_ARCH}" /usr/sbin/router-update',
)

# CI now runs the frontend unit and browser test layers.
replace_once(
    ".github/workflows/ci.yml",
    "      - name: Type-check and build dashboard\n        run: pnpm build",
    "      - name: Run dashboard unit tests\n        run: pnpm test\n      - name: Type-check and build dashboard\n        run: pnpm build\n      - name: Install Playwright Chromium\n        run: pnpm exec playwright install --with-deps chromium\n      - name: Run dashboard E2E tests\n        run: pnpm test:e2e",
)

# Remove legacy monolithic/portal implementations from the compiled source tree.
for obsolete in ("web/src/App.tsx", "web/src/components/DashboardLogs.tsx"):
    Path(obsolete).unlink(missing_ok=True)
