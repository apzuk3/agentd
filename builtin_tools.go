package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/google/jsonschema-go/jsonschema"
)

// BuiltinToolFactory creates an ADK tool.Tool from server-side configuration.
type BuiltinToolFactory func(cfg *BuiltinToolConfig) (tool.Tool, error)

// BuiltinToolConfig holds server-side keys and settings needed by built-in tools.
type BuiltinToolConfig struct {
	TavilyAPIKey string
}

var builtinFactories = map[string]BuiltinToolFactory{
	"web_search": newWebSearchTool,
}

// ResolveBuiltinTool looks up a built-in tool by name and creates it. Returns
// an error if the name is unknown or required configuration is missing.
func ResolveBuiltinTool(name string, cfg *BuiltinToolConfig) (tool.Tool, error) {
	factory, ok := builtinFactories[name]
	if !ok {
		return nil, fmt.Errorf("unknown built-in tool %q", name)
	}
	return factory(cfg)
}

// BuiltinToolNames returns all registered built-in tool names.
func BuiltinToolNames() []string {
	names := make([]string, 0, len(builtinFactories))
	for k := range builtinFactories {
		names = append(names, k)
	}
	return names
}

// --- web_search (Tavily) ---

type webSearchInput struct {
	Query      string `json:"query" jsonschema:"description=The search query to look up on the web"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum number of results to return (1-20). Defaults to 5 if omitted."`
}

type tavilyRequest struct {
	APIKey     string `json:"api_key"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

type tavilyResponse struct {
	Answer  string         `json:"answer,omitempty"`
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

var tavilyHTTPClient = &http.Client{Timeout: 30 * time.Second}

func newWebSearchTool(cfg *BuiltinToolConfig) (tool.Tool, error) {
	if cfg.TavilyAPIKey == "" {
		return nil, fmt.Errorf("TAVILY_API_KEY is required for the web_search built-in tool")
	}

	schema, err := jsonschema.For[webSearchInput](nil)
	if err != nil {
		return nil, fmt.Errorf("generating web_search input schema: %w", err)
	}

	apiKey := cfg.TavilyAPIKey

	return functiontool.New(
		functiontool.Config{
			Name:        "web_search",
			Description: "Search the web for current information. Returns titles, URLs, and content snippets from relevant web pages.",
			InputSchema: schema,
		},
		func(ctx tool.Context, args map[string]any) (map[string]any, error) {
			query, _ := args["query"].(string)
			if query == "" {
				return nil, fmt.Errorf("query is required")
			}

			maxResults := 5
			if mr, ok := args["max_results"].(float64); ok && mr > 0 {
				maxResults = int(mr)
				if maxResults > 20 {
					maxResults = 20
				}
			}

			results, err := tavilySearch(ctx, apiKey, query, maxResults)
			if err != nil {
				return nil, fmt.Errorf("web search failed: %w", err)
			}

			return results, nil
		},
	)
}

func tavilySearch(ctx context.Context, apiKey, query string, maxResults int) (map[string]any, error) {
	reqBody := tavilyRequest{
		APIKey:     apiKey,
		Query:      query,
		MaxResults: maxResults,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tavilyHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var tavilyResp tavilyResponse
	if err := json.Unmarshal(respBody, &tavilyResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	results := make([]map[string]any, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		results = append(results, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"content": r.Content,
			"score":   r.Score,
		})
	}

	out := map[string]any{
		"results": results,
	}
	if tavilyResp.Answer != "" {
		out["answer"] = tavilyResp.Answer
	}

	return out, nil
}
