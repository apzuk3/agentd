package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	agentdv1 "github.com/apzuk3/agentd/gen/proto/go/agentd/v1"
)

func TestAddMCPServer_DiscoversAndCallsTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result":  map[string]any{"protocolVersion": "2025-03-26"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{}}`))
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "search",
							"description": "Search docs",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"query": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "hello from mcp"}},
				},
			})
		default:
			t.Fatalf("unexpected method: %s", method)
		}
	}))
	defer server.Close()

	c := New("http://localhost:8080", WithHTTPClient(server.Client()))
	names, err := c.AddMCPServer(context.Background(), MCPServerConfig{
		Name:       "docs",
		URL:        server.URL,
		ToolPrefix: "docs",
	})
	if err != nil {
		t.Fatalf("AddMCPServer returned error: %v", err)
	}

	if !reflect.DeepEqual(names, []string{"docs.search"}) {
		t.Fatalf("unexpected names: %#v", names)
	}

	rt, ok := c.tools["docs.search"]
	if !ok {
		t.Fatal("registered MCP tool not found")
	}

	out, _, err := rt.handler(context.Background(), `{"query":"agentd"}`)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if out != "hello from mcp" {
		t.Fatalf("unexpected handler output: %q", out)
	}
}

func TestAttachDiscoveredMCPToolsByAgent(t *testing.T) {
	agent := &agentdv1.Agent{
		Name: "root",
		AgentType: &agentdv1.Agent_Llm{Llm: &agentdv1.LlmAgent{
			ToolNames: []string{"existing"},
			McpAttachments: []*agentdv1.McpAttachment{
				{McpName: "docs"},
			},
			SubAgents: []*agentdv1.Agent{
				{
					Name: "child",
					AgentType: &agentdv1.Agent_Llm{Llm: &agentdv1.LlmAgent{
						ToolNames: []string{"child_tool"},
						McpAttachments: []*agentdv1.McpAttachment{
							{McpName: "github"},
						},
					}},
				},
			},
		}},
	}

	err := AttachDiscoveredMCPToolsByAgent(agent, map[string][]string{
		"docs":   {"docs.search", "docs.read"},
		"github": {"gh.list_prs"},
	})
	if err != nil {
		t.Fatalf("AttachDiscoveredMCPToolsByAgent returned error: %v", err)
	}

	rootTools := agent.GetLlm().GetToolNames()
	if !reflect.DeepEqual(rootTools, []string{"existing", "docs.search", "docs.read"}) {
		t.Fatalf("unexpected root tools: %#v", rootTools)
	}

	childTools := agent.GetLlm().GetSubAgents()[0].GetLlm().GetToolNames()
	if !reflect.DeepEqual(childTools, []string{"child_tool", "gh.list_prs"}) {
		t.Fatalf("unexpected child tools: %#v", childTools)
	}
}

func TestAttachDiscoveredMCPToolsByAgent_UnknownMCP(t *testing.T) {
	agent := &agentdv1.Agent{
		Name: "root",
		AgentType: &agentdv1.Agent_Llm{Llm: &agentdv1.LlmAgent{
			McpAttachments: []*agentdv1.McpAttachment{{McpName: "missing"}},
		}},
	}

	err := AttachDiscoveredMCPToolsByAgent(agent, map[string][]string{"docs": {"docs.search"}})
	if err == nil {
		t.Fatal("expected error for unknown MCP attachment")
	}
}

func TestAttachDiscoveredMCPToolsByAgent_WithIncludeToolNames(t *testing.T) {
	agent := &agentdv1.Agent{
		Name: "root",
		AgentType: &agentdv1.Agent_Llm{Llm: &agentdv1.LlmAgent{
			McpAttachments: []*agentdv1.McpAttachment{
				{
					McpName:          "github",
					IncludeToolNames: []string{"gh.list_prs"},
				},
			},
		}},
	}

	err := AttachDiscoveredMCPToolsByAgent(agent, map[string][]string{
		"github": {"gh.list_prs", "gh.create_issue"},
	})
	if err != nil {
		t.Fatalf("AttachDiscoveredMCPToolsByAgent returned error: %v", err)
	}

	got := agent.GetLlm().GetToolNames()
	want := []string{"gh.list_prs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tools: got=%#v want=%#v", got, want)
	}
}

func TestAttachDiscoveredMCPToolsByAgent_UnknownIncludedTool(t *testing.T) {
	agent := &agentdv1.Agent{
		Name: "root",
		AgentType: &agentdv1.Agent_Llm{Llm: &agentdv1.LlmAgent{
			McpAttachments: []*agentdv1.McpAttachment{
				{
					McpName:          "github",
					IncludeToolNames: []string{"gh.not_there"},
				},
			},
		}},
	}

	err := AttachDiscoveredMCPToolsByAgent(agent, map[string][]string{
		"github": {"gh.list_prs"},
	})
	if err == nil {
		t.Fatal("expected error for unknown included tool")
	}
}
