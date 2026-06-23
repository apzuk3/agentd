package agentd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ---------------------------------------------------------------------------
// Configuration for the built-in Tavily tool
// ---------------------------------------------------------------------------

type tavilyConfig struct {
	apiKey     string
	apiKeyEnvs []string
	maxResults int // default when caller omits max_results in the tool call
}

var builtinTavilyCfg = tavilyConfig{}

// defaultTavilyEnvVars are tried (after any WithTavilyAPIKeyEnv values) when
// no explicit key has been supplied via ConfigureBuiltinTavily.
var defaultTavilyEnvVars = []string{
	"TAVILY_API_KEY",
	"TAVILY_KEY",
	"TAVILY_APIKEY",
	"TAVILY_SECRET",
}

// TavilyOption configures the BuiltinTavily tool.
type TavilyOption func(*tavilyConfig)

// WithTavilyAPIKey sets an explicit API key for the Tavily integration.
// This takes precedence over environment variables.
func WithTavilyAPIKey(key string) TavilyOption {
	return func(c *tavilyConfig) {
		c.apiKey = key
	}
}

// WithTavilyAPIKeyEnv appends additional environment variable names to try
// (before the built-in defaults) when resolving the Tavily API key.
func WithTavilyAPIKeyEnv(envs ...string) TavilyOption {
	return func(c *tavilyConfig) {
		c.apiKeyEnvs = append(c.apiKeyEnvs, envs...)
	}
}

// WithTavilyMaxResults sets the default max_results used by BuiltinTavily
// when the tool call does not specify a value.
func WithTavilyMaxResults(n int) TavilyOption {
	return func(c *tavilyConfig) {
		if n > 0 {
			c.maxResults = n
		}
	}
}

// ConfigureBuiltinTavily applies configuration options for the built-in
// Tavily tool (primarily API credentials).
//
// Call this early in main(), before constructing agents that reference
// BuiltinTavily:
//
//	agentd.ConfigureBuiltinTavily(
//	    agentd.WithTavilyAPIKey("tvly-..."),
//	)
//
// If no explicit key is configured, the tool will look for common
// environment variables (TAVILY_API_KEY and aliases) at call time.
func ConfigureBuiltinTavily(opts ...TavilyOption) {
	for _, opt := range opts {
		opt(&builtinTavilyCfg)
	}
}

func (c *tavilyConfig) resolveAPIKey() string {
	if c.apiKey != "" {
		return c.apiKey
	}
	candidates := append([]string{}, c.apiKeyEnvs...)
	candidates = append(candidates, defaultTavilyEnvVars...)
	for _, name := range candidates {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

func (c *tavilyConfig) resolveMaxResults(requested int) int {
	if requested > 0 {
		if requested > 20 {
			return 20
		}
		return requested
	}
	if c.maxResults > 0 {
		return c.maxResults
	}
	return 5
}

// ---------------------------------------------------------------------------
// Auto-registration of the built-in tool
// ---------------------------------------------------------------------------

func init() {
	registerBuiltinTavily()
}

func registerBuiltinTavily() {
	type input struct {
		Query      string `json:"query" jsonschema_description:"The search query. Be specific; include dates, entities, or context when helpful."`
		MaxResults int    `json:"max_results,omitempty" jsonschema_description:"Maximum results to return (1-20). Uses the configured default or 5 when omitted."`
	}

	type result struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score,omitempty"`
	}

	type output struct {
		Answer  string   `json:"answer,omitempty"`
		Results []result `json:"results"`
	}

	desc := "Web and real-time information search powered by Tavily. " +
		"Returns a list of results (title, url, content, score) and an optional synthesized answer. " +
		"Use for current events, research, product info, and any question requiring up-to-date data."

	if err := AddTool(BuiltinTavily, desc, func(_ ToolContext, in input) (output, error) {
		key := builtinTavilyCfg.resolveAPIKey()
		if key == "" {
			return output{}, fmt.Errorf("no Tavily API key configured: set via ConfigureBuiltinTavily(WithTavilyAPIKey(...)) or the TAVILY_API_KEY environment variable")
		}

		max := builtinTavilyCfg.resolveMaxResults(in.MaxResults)

		payload := map[string]any{
			"api_key":         key,
			"query":           in.Query,
			"max_results":     max,
			"include_answer":  true,
			"search_depth":    "basic",
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return output{}, err
		}

		req, err := http.NewRequest(http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
		if err != nil {
			return output{}, err
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return output{}, fmt.Errorf("tavily request failed: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return output{}, fmt.Errorf("reading tavily response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			// Try to surface a useful message
			var errResp struct {
				Detail string `json:"detail"`
			}
			_ = json.Unmarshal(respBody, &errResp)
			msg := errResp.Detail
			if msg == "" {
				msg = string(respBody)
			}
			return output{}, fmt.Errorf("tavily api error (status %d): %s", resp.StatusCode, msg)
		}

		var raw struct {
			Answer  string `json:"answer"`
			Results []struct {
				Title   string  `json:"title"`
				URL     string  `json:"url"`
				Content string  `json:"content"`
				Score   float64 `json:"score"`
			} `json:"results"`
		}
		if err := json.Unmarshal(respBody, &raw); err != nil {
			return output{}, fmt.Errorf("decoding tavily response: %w", err)
		}

		out := output{
			Answer:  raw.Answer,
			Results: make([]result, len(raw.Results)),
		}
		for i, r := range raw.Results {
			out.Results[i] = result{
				Title:   r.Title,
				URL:     r.URL,
				Content: r.Content,
				Score:   r.Score,
			}
		}
		return out, nil
	}); err != nil {
		panic("agentd: failed to register " + BuiltinTavily + ": " + err.Error())
	}
}
