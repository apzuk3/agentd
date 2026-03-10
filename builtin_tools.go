package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/expr-lang/expr"
	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// BuiltinToolFactory creates an ADK tool.Tool from server-side configuration.
type BuiltinToolFactory func(cfg *BuiltinToolConfig) (tool.Tool, error)

// BuiltinToolConfig holds server-side keys and settings needed by built-in tools.
type BuiltinToolConfig struct {
	TavilyAPIKey string
}

var builtinFactories = map[string]BuiltinToolFactory{
	"web_search":   newWebSearchTool,
	"exit_loop":    newLoopBreakerTool,
	"calculator":   newCalculatorTool,
	"current_time": newCurrentTimeTool,
}

func newLoopBreakerTool(cfg *BuiltinToolConfig) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "loop_breaker",
		Description: "Call this tool ONLY when the task is complete or you need to exit the current loop. Signals the loop agent to terminate iterations and proceed. Use when: the goal has been achieved, further iterations add no value, or you've hit a stopping condition.",
	}, func(ctx tool.Context, args map[string]any) (map[string]any, error) {
		ctx.Actions().Escalate = true
		ctx.Actions().SkipSummarization = true
		return map[string]any{
			"status":  "loop_exited",
			"message": "Loop terminated successfully. The agent will proceed to the next step.",
		}, nil
	})
}

// --- calculator ---

type calculatorInput struct {
	Expression string `json:"expression" jsonschema:"description=Math expression to evaluate (e.g. 2+3*4, 100/5, 2^10). Supports + - * / % and ^ for exponentiation."`
}

func newCalculatorTool(cfg *BuiltinToolConfig) (tool.Tool, error) {
	schema, err := jsonschema.For[calculatorInput](nil)
	if err != nil {
		return nil, fmt.Errorf("generating calculator input schema: %w", err)
	}
	return functiontool.New(
		functiontool.Config{
			Name:        "calculator",
			Description: "Evaluate a math expression. Use for arithmetic: addition (+), subtraction (-), multiplication (*), division (/), modulus (%), exponentiation (^). Example: 2+3*4, 100/5, 2^10.",
			InputSchema: schema,
		},
		func(ctx tool.Context, args map[string]any) (map[string]any, error) {
			exprStr, _ := args["expression"].(string)
			if exprStr == "" {
				return nil, fmt.Errorf("expression is required")
			}
			result, err := evaluateMath(exprStr)
			if err != nil {
				return nil, fmt.Errorf("calculator: %w", err)
			}
			return map[string]any{
				"result": result,
			}, nil
		},
	)
}

func evaluateMath(exprStr string) (float64, error) {
	program, err := expr.Compile(exprStr, expr.AsFloat64())
	if err != nil {
		return 0, err
	}
	output, err := expr.Run(program, nil)
	if err != nil {
		return 0, err
	}
	f, ok := output.(float64)
	if !ok {
		return 0, fmt.Errorf("expected numeric result, got %T", output)
	}
	return f, nil
}

// --- current_time ---

type currentTimeInput struct {
	Timezone string `json:"timezone,omitempty" jsonschema:"description=IANA timezone (e.g. America/New_York, Europe/London). Omit for UTC."`
}

func newCurrentTimeTool(cfg *BuiltinToolConfig) (tool.Tool, error) {
	schema, err := jsonschema.For[currentTimeInput](nil)
	if err != nil {
		return nil, fmt.Errorf("generating current_time input schema: %w", err)
	}
	return functiontool.New(
		functiontool.Config{
			Name:        "current_time",
			Description: "Get the current date and time. Use when the agent needs to know 'now'. Optionally specify a timezone (e.g. America/New_York); defaults to UTC.",
			InputSchema: schema,
		},
		func(ctx tool.Context, args map[string]any) (map[string]any, error) {
			now := time.Now()
			loc := time.UTC
			if tz, ok := args["timezone"].(string); ok && tz != "" {
				var err error
				loc, err = time.LoadLocation(tz)
				if err != nil {
					return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
				}
				now = now.In(loc)
			}
			return map[string]any{
				"iso8601":  now.Format(time.RFC3339),
				"unix":     now.Unix(),
				"date":     now.Format("2006-01-02"),
				"time":     now.Format("15:04:05"),
				"timezone": loc.String(),
				"weekday":  now.Weekday().String(),
			}, nil
		},
	)
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
