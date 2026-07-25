# 🤖 Model Context Protocol (MCP) Server for Minimal Router OS

Minimal Router OS includes an official **Model Context Protocol (MCP)** server (`cmd/minimalrouter-mcp`), allowing AI agents (Claude Desktop, Antigravity, Cursor, Windsurf, ChatGPT, etc.) to inspect router metrics, configure firewall rules, manage DNS/DoH, control Squid Proxy, and manage snapshots.

---

## ⚡ Build the MCP Server

```bash
# Build the binary
go build -o bin/minimalrouter-mcp ./cmd/minimalrouter-mcp
```

---

## 🔧 Claude Desktop / AI Agent Configuration

Add `minimalrouter-mcp` to your MCP configuration file (e.g. `claude_desktop_config.json` or `~/.gemini/config/mcp.json`):

```json
{
  "mcpServers": {
    "minimalrouter": {
      "command": "/Users/Vladimir/Documents/minimalrouter/bin/minimalrouter-mcp",
      "env": {
        "MINIMALROUTER_API_URL": "http://127.0.0.1:8080"
      }
    }
  }
}
```

---

## 🛠️ Available MCP Tools for AI Agents

| Tool Name | Description |
|---|---|
| `get_router_status` | Retrieve live system status, public IP, uptime, and active DHCP leases |
| `get_full_config` | Fetch complete canonical router configuration JSON |
| `add_port_forward` | Add WAN port forwarding rule (external port -> internal LAN IP & port) |
| `block_device_ip` | Block IP from direct WAN in nftables & add to Squid Proxy restricted list |
| `configure_dns` | Update upstream DNS servers (Cloudflare, Quad9, AdGuard) and DoH status |
| `configure_squid_proxy` | Enable/disable Squid Proxy and set NCSA basic credentials |
| `create_snapshot` | Take instant system state snapshot before making big changes |
| `rollback_snapshot` | Restore router configuration to a previous snapshot revision ID |

---

## 🗣️ Example AI Prompts

You can speak to your AI agent naturally:
- *"Check my router status and public IP address."*
- *"Forward external port 8123 to 192.168.1.10 port 8123 for Home Assistant."*
- *"Block 192.168.1.50 from direct internet and force it through Squid Proxy."*
- *"Switch my router DNS to Quad9 with DoH encryption enabled."*
- *"Take a snapshot and then enable Squid Proxy on port 3128 with user admin/secret."*
