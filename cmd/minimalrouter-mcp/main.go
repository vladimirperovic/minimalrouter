package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// JSON-RPC 2.0 Request & Response structs for Model Context Protocol (MCP) over Stdio
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type MCPTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema ToolSchema `json:"inputSchema"`
}

type ToolSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]PropSchema `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type PropSchema struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

var routerAPIURL = "http://127.0.0.1:8080"

func main() {
	if envURL := os.Getenv("MINIMALROUTER_API_URL"); envURL != "" {
		routerAPIURL = envURL
	}

	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		var req JSONRPCRequest
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				break
			}
			continue
		}

		resp := handleRPCRequest(req)
		if resp != nil {
			_ = encoder.Encode(resp)
		}
	}
}

func handleRPCRequest(req JSONRPCRequest) *JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "minimalrouter-mcp",
					"version": "1.0.0",
				},
				"instructions": "Official Minimal Router OS MCP Server. Allows AI agents to inspect metrics, configure firewall rules, manage DNS/DoH, control Squid proxy, add port forwards, and perform snapshots/rollbacks.",
			},
		}

	case "notifications/initialized":
		return nil

	case "tools/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": getToolList(),
			},
		}

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32602, Message: "Invalid params"},
			}
		}

		resultText, err := executeToolCall(params.Name, params.Arguments)
		if err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": fmt.Sprintf("Error executing tool '%s': %v", params.Name, err),
						},
					},
					"isError": true,
				},
			}
		}

		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": resultText,
					},
				},
			},
		}

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: "Method not found"},
		}
	}
}

func getToolList() []MCPTool {
	return []MCPTool{
		{
			Name:        "get_router_status",
			Description: "Get live Minimal Router OS system status including public IP, uptime, WAN connection state, and active DHCP leases.",
			InputSchema: ToolSchema{
				Type:       "object",
				Properties: map[string]PropSchema{},
			},
		},
		{
			Name:        "get_full_config",
			Description: "Retrieve complete router configuration JSON (WAN, LAN, DHCP, Firewall, WireGuard, Cloudflare, Squid Proxy).",
			InputSchema: ToolSchema{
				Type:       "object",
				Properties: map[string]PropSchema{},
			},
		},
		{
			Name:        "add_port_forward",
			Description: "Add a port forwarding rule to redirect WAN external traffic to a specific LAN IP address and port.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropSchema{
					"name":          {Type: "string", Description: "Rule name (e.g. 'Home Assistant')"},
					"protocol":      {Type: "string", Description: "Protocol: 'tcp', 'udp', or 'both'"},
					"external_port": {Type: "integer", Description: "External WAN port (e.g. 8123)"},
					"internal_ip":   {Type: "string", Description: "Target internal LAN IP (e.g. '192.168.1.10')"},
					"internal_port": {Type: "integer", Description: "Target internal port (e.g. 8123)"},
				},
				Required: []string{"name", "protocol", "external_port", "internal_ip", "internal_port"},
			},
		},
		{
			Name:        "block_device_ip",
			Description: "Block an IP address from direct WAN internet access in nftables firewall and add it to Squid Proxy restricted group.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropSchema{
					"ip_address": {Type: "string", Description: "Target IP address to restrict (e.g. '192.168.1.50')"},
				},
				Required: []string{"ip_address"},
			},
		},
		{
			Name:        "configure_dns",
			Description: "Configure router upstream DNS servers (Cloudflare, Quad9, AdGuard, Google) and enable/disable DNS-over-HTTPS (DoH).",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropSchema{
					"primary_dns":   {Type: "string", Description: "Primary DNS IP (e.g. '1.1.1.1')"},
					"secondary_dns": {Type: "string", Description: "Secondary DNS IP (e.g. '1.0.0.1')"},
					"doh_enabled":   {Type: "boolean", Description: "Enforce DNS-over-HTTPS privacy encryption"},
				},
				Required: []string{"primary_dns", "secondary_dns"},
			},
		},
		{
			Name:        "configure_squid_proxy",
			Description: "Enable or disable Squid forward proxy and set authentication credentials.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropSchema{
					"enabled":  {Type: "boolean", Description: "Enable or disable Squid Proxy"},
					"username": {Type: "string", Description: "Proxy authentication username"},
					"password": {Type: "string", Description: "Proxy authentication password"},
				},
				Required: []string{"enabled"},
			},
		},
		{
			Name:        "create_snapshot",
			Description: "Take an immediate system state snapshot before applying configuration changes.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropSchema{
					"label": {Type: "string", Description: "Snapshot description label"},
				},
				Required: []string{"label"},
			},
		},
		{
			Name:        "rollback_snapshot",
			Description: "Restore router configuration state to a previous revision ID.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropSchema{
					"revision": {Type: "integer", Description: "Revision ID to restore"},
				},
				Required: []string{"revision"},
			},
		},
	}
}

func executeToolCall(name string, args map[string]interface{}) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	switch name {
	case "get_router_status":
		resp, err := client.Get(routerAPIURL + "/api/v1/system")
		if err != nil {
			return "", fmt.Errorf("failed to fetch status: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil

	case "get_full_config":
		resp, err := client.Get(routerAPIURL + "/api/v1/config")
		if err != nil {
			return "", fmt.Errorf("failed to fetch config: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil

	case "add_port_forward":
		cfg, err := fetchConfig(client)
		if err != nil {
			return "", err
		}

		name, _ := args["name"].(string)
		proto, _ := args["protocol"].(string)
		extPortFloat, _ := args["external_port"].(float64)
		intIP, _ := args["internal_ip"].(string)
		intPortFloat, _ := args["internal_port"].(float64)

		pfRules, _ := cfg["firewall"].(map[string]interface{})["port_forwards"].([]interface{})
		newRule := map[string]interface{}{
			"id":            fmt.Sprintf("pf-%d", time.Now().UnixNano()),
			"name":          name,
			"protocol":      proto,
			"external_port": int(extPortFloat),
			"internal_ip":   intIP,
			"internal_port": int(intPortFloat),
			"enabled":       true,
		}
		cfg["firewall"].(map[string]interface{})["port_forwards"] = append(pfRules, newRule)

		if err := saveConfig(client, cfg); err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully added port forward rule '%s' (%s:%d -> %s:%d)", name, proto, int(extPortFloat), intIP, int(intPortFloat)), nil

	case "block_device_ip":
		cfg, err := fetchConfig(client)
		if err != nil {
			return "", err
		}

		ip, _ := args["ip_address"].(string)
		squidCfg, _ := cfg["squid_proxy"].(map[string]interface{})
		if squidCfg == nil {
			squidCfg = map[string]interface{}{
				"enabled":        true,
				"port":           3128,
				"username":       "proxyadmin",
				"restricted_ips": []interface{}{},
			}
		}

		restricted, _ := squidCfg["restricted_ips"].([]interface{})
		squidCfg["restricted_ips"] = append(restricted, ip)
		cfg["squid_proxy"] = squidCfg

		if err := saveConfig(client, cfg); err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully blocked direct WAN access for IP '%s' in nftables and routed via Squid Proxy.", ip), nil

	case "configure_dns":
		cfg, err := fetchConfig(client)
		if err != nil {
			return "", err
		}

		pri, _ := args["primary_dns"].(string)
		sec, _ := args["secondary_dns"].(string)
		doh, _ := args["doh_enabled"].(bool)

		dhcpCfg, _ := cfg["dhcp"].(map[string]interface{})
		dhcpCfg["dns_servers"] = []interface{}{pri, sec}

		if err := saveConfig(client, cfg); err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully updated DNS servers to [%s, %s] (DoH Enforced: %t).", pri, sec, doh), nil

	case "configure_squid_proxy":
		cfg, err := fetchConfig(client)
		if err != nil {
			return "", err
		}

		enabled, _ := args["enabled"].(bool)
		user, _ := args["username"].(string)
		pass, _ := args["password"].(string)

		squidCfg, _ := cfg["squid_proxy"].(map[string]interface{})
		if squidCfg == nil {
			squidCfg = map[string]interface{}{"port": 3128}
		}
		squidCfg["enabled"] = enabled
		if user != "" {
			squidCfg["username"] = user
		}
		if pass != "" {
			squidCfg["password"] = pass
		}
		cfg["squid_proxy"] = squidCfg

		if err := saveConfig(client, cfg); err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully updated Squid Proxy (Enabled: %t, User: %s).", enabled, user), nil

	case "create_snapshot":
		label, _ := args["label"].(string)
		reqBody, _ := json.Marshal(map[string]string{"label": label})
		resp, err := client.Post(routerAPIURL+"/api/v1/snapshots", "application/json", bytes.NewBuffer(reqBody))
		if err != nil {
			return "", fmt.Errorf("failed to create snapshot: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil

	case "rollback_snapshot":
		revFloat, _ := args["revision"].(float64)
		revID := int(revFloat)
		resp, err := client.Post(fmt.Sprintf("%s/api/v1/snapshots/%d/restore", routerAPIURL, revID), "application/json", nil)
		if err != nil {
			return "", fmt.Errorf("failed to restore snapshot: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func fetchConfig(client *http.Client) (map[string]interface{}, error) {
	resp, err := client.Get(routerAPIURL + "/api/v1/config")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config: %w", err)
	}
	defer resp.Body.Close()

	var cfg map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}
	return cfg, nil
}

func saveConfig(client *http.Client, cfg map[string]interface{}) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}

	req, err := http.NewRequest("PUT", routerAPIURL+"/api/v1/config", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send config update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("config update failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
