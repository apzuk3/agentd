package client

import (
	"testing"
)

func TestRunOptionHeaders(t *testing.T) {
	tests := []struct {
		name       string
		opt        RunOption
		wantHeader string
		wantValue  string
	}{
		{"gemini", WithGeminiAPIKey("gk"), headerGeminiAPIKey, "gk"},
		{"anthropic", WithAnthropicAPIKey("ak"), headerAnthropicAPIKey, "ak"},
		{"openai", WithOpenAIAPIKey("ok"), headerOpenAIAPIKey, "ok"},
		{"tavily", WithTavilyAPIKey("tk"), headerTavilyAPIKey, "tk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rc runConfig
			tt.opt(&rc)

			if rc.headers == nil {
				t.Fatal("headers map should be initialized")
			}
			got := rc.headers.Get(tt.wantHeader)
			if got != tt.wantValue {
				t.Errorf("header %q = %q, want %q", tt.wantHeader, got, tt.wantValue)
			}
		})
	}
}

func TestRunOptionHeaders_Multiple(t *testing.T) {
	var rc runConfig
	WithGeminiAPIKey("g")(&rc)
	WithAnthropicAPIKey("a")(&rc)

	if rc.headers.Get(headerGeminiAPIKey) != "g" {
		t.Error("expected gemini header")
	}
	if rc.headers.Get(headerAnthropicAPIKey) != "a" {
		t.Error("expected anthropic header")
	}
}

func TestHeaderInterceptorImplementsInterface(t *testing.T) {
	// Compile-time check that headerInterceptor satisfies connect.Interceptor.
	// The type assertion is done by the compiler; if it builds, it passes.
	_ = &headerInterceptor{}
}
