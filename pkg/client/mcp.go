package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

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

type mcpBridge struct {
	client         *mcpclient.Client
	requestTimeout time.Duration
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

	bridge, err := newMCPBridge(ctx, c.httpClient, resolvedURL, cfg.Headers, timeout)
	if err != nil {
		return nil, err
	}

	tools, err := bridge.listTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	prefix := strings.TrimSpace(cfg.ToolPrefix)
	if prefix == "" {
		prefix = strings.TrimSpace(name)
	}

	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if strings.TrimSpace(t.Name) == "" {
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

		schema, err := mcpToolInputSchema(t)
		if err != nil {
			return nil, fmt.Errorf("tool %q schema: %w", t.Name, err)
		}
		inputSchemaJSON, err := marshalMCPInputSchema(schema)
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
				result, err := bridge.callTool(callCtx, remoteName, args)
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

func newMCPBridge(ctx context.Context, baseHTTPClient any, rawURL string, headers map[string]string, timeout time.Duration) (*mcpBridge, error) {
	transportOpts := []mcptransport.StreamableHTTPCOption{}
	if len(headers) > 0 {
		transportOpts = append(transportOpts, mcptransport.WithHTTPHeaders(cloneStringMap(headers)))
	}

	hc := deriveHTTPClient(baseHTTPClient)
	if hc != nil {
		transportOpts = append(transportOpts, mcptransport.WithHTTPBasicClient(hc))
	}
	if timeout > 0 {
		transportOpts = append(transportOpts, mcptransport.WithHTTPTimeout(timeout))
	}

	client, err := mcpclient.NewStreamableHttpClient(rawURL, transportOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating mcp client: %w", err)
	}

	initCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		initCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if err := client.Start(initCtx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("starting mcp client: %w", err)
	}

	_, err = client.Initialize(initCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "agentd-go-client",
				Version: "0.1.0",
			},
		},
	})
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	return &mcpBridge{
		client:         client,
		requestTimeout: timeout,
	}, nil
}

func (m *mcpBridge) listTools(ctx context.Context) ([]mcp.Tool, error) {
	callCtx := ctx
	if m.requestTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, m.requestTimeout)
		defer cancel()
	}

	res, err := m.client.ListTools(callCtx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	return res.Tools, nil
}

func (m *mcpBridge) callTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	callCtx := ctx
	if m.requestTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, m.requestTimeout)
		defer cancel()
	}

	return m.client.CallTool(callCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
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

func deriveHTTPClient(base any) *http.Client {
	hc, ok := base.(*http.Client)
	if !ok || hc == nil {
		return &http.Client{}
	}
	clone := *hc
	return &clone
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mcpToolInputSchema(tool mcp.Tool) (map[string]any, error) {
	if len(tool.RawInputSchema) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(tool.RawInputSchema, &schema); err != nil {
			return nil, err
		}
		return schema, nil
	}

	b, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		return nil, err
	}
	return schema, nil
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

func normalizeMCPToolResult(result *mcp.CallToolResult) (string, map[string]any, error) {
	if result == nil {
		return "", nil, errors.New("empty MCP tool result")
	}

	textOutput := textFromMCPContent(result.Content)

	if result.IsError {
		if textOutput == "" {
			textOutput = "MCP tool returned an error"
		}
		return "", nil, errors.New(textOutput)
	}

	if result.StructuredContent != nil {
		b, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return "", nil, fmt.Errorf("encoding structuredContent: %w", err)
		}
		return string(b), nil, nil
	}

	if textOutput != "" {
		return textOutput, nil, nil
	}

	b, err := json.Marshal(result)
	if err != nil {
		return "", nil, fmt.Errorf("encoding MCP result: %w", err)
	}
	return string(b), nil, nil
}

func textFromMCPContent(content []mcp.Content) string {
	parts := make([]string, 0, len(content))
	for _, c := range content {
		if txt, ok := mcp.AsTextContent(c); ok {
			if t := strings.TrimSpace(txt.Text); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}
