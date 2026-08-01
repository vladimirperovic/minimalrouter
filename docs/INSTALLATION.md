# Controlled Alpine installation

Minimal Router OS is early-alpha software. Keep local/Proxmox console access and
a known-good router ready for rollback throughout installation and testing.

For the owner's validated Proxmox PPPoE path use:

- Alpine Linux 3.22 x86_64;
- **Alpine `linux-lts`**;
- 1 vCPU and 1 GiB RAM as comfortable pilot starting values;
- 8 GiB reliable disk;
- two known VirtIO NICs with unambiguous WAN/LAN roles.

## Mandatory PPPoE kernel preflight

The 2026-08-01 pilot first used `linux-virt`, whose running kernel did not
provide the PPPoE module required by the appliance. After switching to
`linux-lts`, real PPPoE succeeded.

Before installation:

```sh
cat /etc/alpine-release
uname -a
modprobe pppoe
ip -brief link
```

If `modprobe pppoe` fails, stop. Boot/install `linux-lts`, reboot into it and
repeat the check. Both Minimal Router installers now enforce the same capability
check and abort rather than install a router that cannot establish PPPoE.

## Build

Use a trusted private checkout and record the exact commit:

```sh
git status --short
git rev-parse HEAD
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

Verify the checksum again after transfer to the guest.

## Install

```sh
mkdir -p /tmp/minimalrouter-install
tar xzf minimalrouter-linux-amd64.tar.gz -C /tmp/minimalrouter-install
cd /tmp/minimalrouter-install/minimalrouter-linux-amd64
sudo sh install.sh
```

The installer sets up the required Alpine packages, OpenRC services, dashboard,
sysctl settings and required kernel modules including `pppoe`.

## Verify

```sh
modprobe pppoe
rc-service router-applyd status
rc-service routerd status
rc-update show | grep -E 'routerd|router-applyd'
```

Complete initial configuration from the isolated LAN and keep the real pfSense
router/fallback path available.

## No-IP after base routing works

New configurations default to **No-IP Dynamic DNS**. Configure it only after the
base PPPoE/Internet path is healthy. Prefer a scoped No-IP DDNS Key and follow
[`DYNAMIC_DNS.md`](DYNAMIC_DNS.md).

For the next owner test, success means MinimalRouter itself updates the No-IP
record, `inadyn` stays healthy, external DNS resolves to the current public IPv4,
and WireGuard connects through that hostname without any Proxmox-host DDNS
workaround.

Never commit or paste live PPPoE, No-IP, administrator or WireGuard credentials.

See [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md) before modifying the existing
candidate VM.
