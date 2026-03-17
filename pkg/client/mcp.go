package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

const defaultMCPRequestTimeout = 30 * time.Second

// MCPServerConfig configures a remote MCP server (streamable HTTP transport).
type MCPServerConfig struct {
	Name           string
	URL            string
	ToolPrefix     string
	Headers        map[string]string
	RequestTimeout time.Duration
	AutoAttach     *bool
}

func shouldAutoAttachMCPTools(cfg MCPServerConfig) bool {
	if cfg.AutoAttach == nil {
		return true
	}
	return *cfg.AutoAttach
}

type mcpClient struct {
	httpClient connect.HTTPClient
	url        string
	headers    map[string]string
	idCounter  int64
}

type mcpRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpRPCResponse struct {
	JSONRPC string       `json:"jsonrpc"`
	ID      any          `json:"id,omitempty"`
	Result  any          `json:"result,omitempty"`
	Error   *mcpRPCError `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

// AddMCPServer discovers tools from a remote MCP server and registers proxy
// handlers so they can be called by agentd via the normal tool dispatch flow.
func (c *Client) AddMCPServer(ctx context.Context, cfg MCPServerConfig) ([]string, error) {
	resolvedURL, err := validateMCPServerURL(cfg.URL)
	if err != nil {
		return nil, err
	}

	name := cfg.Name
	if name == "" {
		name = defaultMCPServerName(resolvedURL)
	}

	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = defaultMCPRequestTimeout
	}

	mc := &mcpClient{
		httpClient: c.httpClient,
		url:        resolvedURL,
		headers:    cfg.Headers,
	}

	if err := mc.initialize(ctx, timeout); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	tools, err := mc.listTools(ctx, timeout)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	prefix := strings.TrimSpace(cfg.ToolPrefix)
	if prefix == "" {
		prefix = strings.TrimSpace(name)
	}

	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			continue
		}

		registeredName := t.Name
		if prefix != "" {
			registeredName = prefix + "." + t.Name
		}

		desc := strings.TrimSpace(t.Description)
		if desc == "" {
			desc = "MCP tool proxied from " + name
		}

		inputSchemaJSON, err := marshalMCPInputSchema(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q schema: %w", t.Name, err)
		}

		toolProto := &agentdv1.Tool{
			Name:        registeredName,
			Description: desc,
			InputSchema: &inputSchemaJSON,
		}

		remoteName := t.Name
		c.tools[registeredName] = &registeredTool{
			proto: toolProto,
			handler: func(callCtx context.Context, input string) (string, map[string]any, error) {
				args, err := decodeMCPToolInput(input)
				if err != nil {
					return "", nil, err
				}
				result, err := mc.callTool(callCtx, timeout, remoteName, args)
				if err != nil {
					return "", nil, err
				}
				return normalizeMCPToolResult(result)
			},
		}

		names = append(names, registeredName)
	}

	return names, nil
}

// AttachToolsToAllLlmAgents appends tool names to each LLM agent in the tree.
func AttachToolsToAllLlmAgents(agent *agentdv1.Agent, toolNames []string) {
	if agent == nil || len(toolNames) == 0 {
		return
	}

	addToLLM := func(llm *agentdv1.LlmAgent) {
		seen := make(map[string]bool, len(llm.GetToolNames())+len(toolNames))
		for _, n := range llm.GetToolNames() {
			seen[n] = true
		}
		for _, n := range toolNames {
			if n == "" || seen[n] {
				continue
			}
			llm.ToolNames = append(llm.ToolNames, n)
			seen[n] = true
		}
	}

	var walk func(a *agentdv1.Agent)
	walk = func(a *agentdv1.Agent) {
		if a == nil {
			return
		}
		switch {
		case a.GetLlm() != nil:
			llm := a.GetLlm()
			addToLLM(llm)
			for _, sub := range llm.GetSubAgents() {
				walk(sub)
			}
		case a.GetSequential() != nil:
			for _, sub := range a.GetSequential().GetAgents() {
				walk(sub)
			}
		case a.GetParallel() != nil:
			for _, sub := range a.GetParallel().GetAgents() {
				walk(sub)
			}
		case a.GetLoop() != nil:
			for _, sub := range a.GetLoop().GetAgents() {
				walk(sub)
			}
		}
	}

	walk(agent)
}

func validateMCPServerURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("URL must use http or https")
	}
	if u.Host == "" {
		return "", errors.New("URL host is required")
	}
	return u.String(), nil
}

func defaultMCPServerName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "mcp"
	}
	host := u.Hostname()
	if host == "" {
		return "mcp"
	}
	return host
}

func (m *mcpClient) initialize(ctx context.Context, timeout time.Duration) error {
	_, err := m.request(ctx, timeout, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "agentd-go-client",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return err
	}
	return m.notify(ctx, timeout, "notifications/initialized", map[string]any{})
}

func (m *mcpClient) listTools(ctx context.Context, timeout time.Duration) ([]mcpTool, error) {
	raw, err := m.request(ctx, timeout, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var res mcpToolsListResult
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, fmt.Errorf("decoding tools/list response: %w", err)
	}
	return res.Tools, nil
}

func (m *mcpClient) callTool(ctx context.Context, timeout time.Duration, name string, args map[string]any) (map[string]any, error) {
	raw, err := m.request(ctx, timeout, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}
	rm, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid tools/call result type %T", raw)
	}
	return rm, nil
}

func (m *mcpClient) request(ctx context.Context, timeout time.Duration, method string, params any) (any, error) {
	m.idCounter++
	id := strconv.FormatInt(m.idCounter, 10)

	payload, err := json.Marshal(mcpRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}

	resp, err := m.post(ctx, timeout, payload)
	if err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("mcp error (%d): %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func (m *mcpClient) notify(ctx context.Context, timeout time.Duration, method string, params any) error {
	payload, err := json.Marshal(mcpRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	_, err = m.post(ctx, timeout, payload)
	return err
}

func (m *mcpClient) post(ctx context.Context, timeout time.Duration, payload []byte) (*mcpRPCResponse, error) {
	callCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, m.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range m.headers {
		req.Header.Set(k, v)
	}

	hc := m.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}

	httpResp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %s", httpResp.StatusCode, strings.TrimSpace(string(body)))
	}

	msg := extractJSONRPCMessage(body)
	if len(msg) == 0 {
		return &mcpRPCResponse{}, nil
	}

	var rpcResp mcpRPCResponse
	if err := json.Unmarshal(msg, &rpcResp); err != nil {
		return nil, fmt.Errorf("decoding json-rpc response: %w", err)
	}
	return &rpcResp, nil
}

func extractJSONRPCMessage(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil
	}

	if bytes.HasPrefix(trimmed, []byte("data:")) {
		lines := strings.Split(string(trimmed), "\n")
		var combined strings.Builder
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			combined.WriteString(payload)
		}
		return []byte(combined.String())
	}

	return trimmed
}

func marshalMCPInputSchema(schema map[string]any) (string, error) {
	if len(schema) == 0 {
		schema = map[string]any{"type": "object"}
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeMCPToolInput(input string) (map[string]any, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return nil, fmt.Errorf("invalid MCP tool input JSON: %w", err)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func normalizeMCPToolResult(result map[string]any) (string, map[string]any, error) {
	if sc, ok := result["structuredContent"]; ok {
		b, err := json.Marshal(sc)
		if err != nil {
			return "", nil, fmt.Errorf("encoding structuredContent: %w", err)
		}
		return string(b), nil, nil
	}

	if content, ok := result["content"].([]any); ok {
		var parts []string
		for _, item := range content {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := m["text"].(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n"), nil, nil
		}
	}

	b, err := json.Marshal(result)
	if err != nil {
		return "", nil, fmt.Errorf("encoding MCP result: %w", err)
	}
	return string(b), nil, nil
}
