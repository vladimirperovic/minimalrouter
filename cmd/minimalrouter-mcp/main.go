package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
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
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema ToolSchema `json:"inputSchema"`
}

type ToolSchema struct {
	Type       string                `json:"type"`
	Properties map[string]PropSchema `json:"properties"`
	Required   []string              `json:"required,omitempty"`
}

type PropSchema struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

var routerAPIURL = "https://192.168.1.1:8443"
var routerClient *apiClient
var allowMutations bool

type apiClient struct {
	http *http.Client
	csrf string
}

func main() {
	if envURL := os.Getenv("MINIMALROUTER_API_URL"); envURL != "" {
		routerAPIURL = envURL
	}
	allowMutations = os.Getenv("MINIMALROUTER_MCP_MODE") == "admin"
	client, err := newAPIClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "minimalrouter-mcp: secure API initialization failed: %v\n", err)
		os.Exit(1)
	}
	routerClient = client

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

func newAPIClient() (*apiClient, error) {
	endpoint, err := url.Parse(routerAPIURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("MINIMALROUTER_API_URL must be a plain HTTPS origin")
	}
	caPath := os.Getenv("MINIMALROUTER_CA_CERT")
	passwordPath := os.Getenv("MINIMALROUTER_PASSWORD_FILE")
	if caPath == "" || passwordPath == "" {
		return nil, fmt.Errorf("MINIMALROUTER_CA_CERT and MINIMALROUTER_PASSWORD_FILE are required")
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read router CA certificate: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("router CA certificate is invalid")
	}
	info, err := os.Stat(passwordPath)
	if err != nil {
		return nil, fmt.Errorf("read password-file metadata: %w", err)
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("password file must not be accessible by group or others")
	}
	password, err := os.ReadFile(passwordPath)
	if err != nil {
		return nil, fmt.Errorf("read password file: %w", err)
	}
	jar, _ := cookiejar.New(nil)
	client := &apiClient{
		http: &http.Client{
			Timeout: 10 * time.Second,
			Jar:     jar,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
			}},
		},
	}
	loginBody, _ := json.Marshal(map[string]interface{}{
		"password":  strings.TrimSpace(string(password)),
		"totp_code": strings.TrimSpace(os.Getenv("MINIMALROUTER_TOTP_CODE")),
		"read_only": !allowMutations,
	})
	response, err := client.http.Post(routerAPIURL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		return nil, fmt.Errorf("router login: %w", err)
	}
	defer response.Body.Close()
	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&login) != nil || login.CSRFToken == "" {
		return nil, fmt.Errorf("router authentication rejected")
	}
	client.csrf = login.CSRFToken
	return client, nil
}

func (c *apiClient) do(method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, routerAPIURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	return c.http.Do(req)
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
				"instructions": mcpInstructions(),
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
	tools := []MCPTool{
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
	}
	if !allowMutations {
		return tools
	}
	return append(tools,
		MCPTool{
			Name:        "configure_dns",
			Description: "Configure the router's validated upstream DNS server IP addresses.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropSchema{
					"primary_dns":   {Type: "string", Description: "Primary DNS IP (e.g. '1.1.1.1')"},
					"secondary_dns": {Type: "string", Description: "Secondary DNS IP (e.g. '1.0.0.1')"},
				},
				Required: []string{"primary_dns", "secondary_dns"},
			},
		},
		MCPTool{
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
		MCPTool{
			Name:        "rollback_snapshot",
			Description: "Restore router configuration from an immutable snapshot ID.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropSchema{
					"snapshot_id": {Type: "string", Description: "Snapshot ID returned by the snapshots API"},
				},
				Required: []string{"snapshot_id"},
			},
		},
	)
}

func executeToolCall(name string, args map[string]interface{}) (string, error) {
	if isMutationTool(name) && !allowMutations {
		return "", fmt.Errorf("mutation tool %q is disabled; MCP starts read-only unless MINIMALROUTER_MCP_MODE=admin is explicitly set locally", name)
	}
	switch name {
	case "get_router_status":
		resp, err := routerClient.do(http.MethodGet, "/api/v1/system", nil)
		if err != nil {
			return "", fmt.Errorf("failed to fetch status: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil

	case "get_full_config":
		resp, err := routerClient.do(http.MethodGet, "/api/v1/config", nil)
		if err != nil {
			return "", fmt.Errorf("failed to fetch config: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil

	case "add_port_forward":
		cfg, err := fetchConfig()
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

		if err := saveConfig(cfg); err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully added port forward rule '%s' (%s:%d -> %s:%d)", name, proto, int(extPortFloat), intIP, int(intPortFloat)), nil

	case "block_device_ip":
		return "", fmt.Errorf("Squid policy is disabled until its privileged lifecycle adapter is implemented")

	case "configure_dns":
		cfg, err := fetchConfig()
		if err != nil {
			return "", err
		}

		pri, _ := args["primary_dns"].(string)
		sec, _ := args["secondary_dns"].(string)
		dhcpCfg, _ := cfg["dhcp"].(map[string]interface{})
		dhcpCfg["dns_servers"] = []interface{}{pri, sec}

		if err := saveConfig(cfg); err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully updated DNS servers to [%s, %s].", pri, sec), nil

	case "configure_squid_proxy":
		return "", fmt.Errorf("Squid configuration is disabled until its privileged lifecycle adapter is implemented")

	case "create_snapshot":
		label, _ := args["label"].(string)
		reqBody, _ := json.Marshal(map[string]string{"label": label})
		resp, err := routerClient.do(http.MethodPost, "/api/v1/snapshots", reqBody)
		if err != nil {
			return "", fmt.Errorf("failed to create snapshot: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil

	case "rollback_snapshot":
		snapshotID, _ := args["snapshot_id"].(string)
		resp, err := routerClient.do(http.MethodPost, "/api/v1/snapshots/"+url.PathEscape(snapshotID)+"/restore", []byte("{}"))
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

func mcpInstructions() string {
	if allowMutations {
		return "Minimal Router OS MCP Server in explicit admin mode. Configuration-changing calls still pass through router validation, CSRF protection, snapshots, and rollback."
	}
	return "Minimal Router OS MCP Server in read-only mode. AI clients can inspect redacted status and configuration but cannot change router state."
}

func isMutationTool(name string) bool {
	switch name {
	case "add_port_forward", "block_device_ip", "configure_dns", "configure_squid_proxy", "create_snapshot", "rollback_snapshot":
		return true
	default:
		return false
	}
}

func fetchConfig() (map[string]interface{}, error) {
	resp, err := routerClient.do(http.MethodGet, "/api/v1/config", nil)
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

func saveConfig(cfg map[string]interface{}) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}

	resp, err := routerClient.do(http.MethodPut, "/api/v1/config", data)
	if err != nil {
		return fmt.Errorf("failed to send config update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("config update failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
