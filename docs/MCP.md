# Minimal Router OS MCP

`minimalrouter-mcp` is a local stdio bridge between an AI client and the
router's authenticated HTTPS API. It has no network listener of its own.

## Security model

- MCP starts in **read-only mode**. Only redacted status and configuration
  tools are advertised.
- The API marks the MCP session read-only and rejects every `POST`, `PUT`,
  `PATCH`, and `DELETE` request made with that session. Hiding tools in the MCP
  list is not the authorization boundary.
- MCP works over the LAN management address while the computer is on the LAN.
- From outside the LAN it works only after the computer has established an
  authenticated WireGuard tunnel and uses the router's WireGuard address.
- The public WAN address does not expose MCP, HTTPS, SSH, port forwards, or any
  other management service. Only the configured WireGuard UDP endpoint accepts
  new WAN traffic.
- Full AI mutation access is an explicit local opt-in. Treat it as equivalent
  to giving the AI administrator control.

## Build

```bash
go build -trimpath -o bin/minimalrouter-mcp ./cmd/minimalrouter-mcp
```

## Client configuration

Create a password file readable only by your local account:

```bash
install -m 0600 /dev/null /path/to/minimalrouter-password
```

Put exactly one router administrator password line in that file. A single
trailing newline is removed; leading/trailing spaces in the password itself
are preserved. Copy the router's
verified certificate to a local CA file, then configure the MCP process:

```json
{
  "mcpServers": {
    "minimalrouter": {
      "command": "/absolute/path/to/bin/minimalrouter-mcp",
      "env": {
        "MINIMALROUTER_API_URL": "https://192.168.1.1:8443",
        "MINIMALROUTER_CA_CERT": "/absolute/path/to/router-server.crt",
        "MINIMALROUTER_PASSWORD_FILE": "/absolute/path/to/minimalrouter-password"
      }
    }
  }
}
```

For remote use, connect WireGuard first and change the API URL to the configured
tunnel address, for example `https://10.8.0.1:8443`. Certificate validation is
mandatory; HTTP and insecure TLS are rejected.

If TOTP is enabled, provide a current one-time code as
`MINIMALROUTER_TOTP_CODE` when the MCP process starts. Do not store a TOTP seed
in the MCP configuration.

## Tools

Read-only mode advertises:

| Tool | Capability |
|---|---|
| `get_router_status` | Read redacted runtime status |
| `get_full_config` | Read the redacted canonical configuration |

Explicit admin mode additionally advertises validated DNS updates and
snapshot/rollback operations. Enable it only in a controlled local session:

```text
MINIMALROUTER_MCP_MODE=admin
```

WAN port forwarding is not available in either mode. WireGuard is the only
permitted external entry point.
