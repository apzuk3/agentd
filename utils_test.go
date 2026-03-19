package agentd

import (
	"context"
	"strings"
	"testing"
)

func TestCreateModel_MissingAnthropicKey(t *testing.T) {
	_, err := createModel(context.Background(), "claude-sonnet-4-6", "", "", "")
	if err == nil {
		t.Fatal("expected error for missing Anthropic key")
	}
	if !strings.Contains(err.Error(), "Anthropic API key is required") {
		t.Errorf("error should mention Anthropic key requirement, got: %v", err)
	}
	if !strings.Contains(err.Error(), HeaderAnthropicAPIKey) {
		t.Errorf("error should mention BYOK header name, got: %v", err)
	}
}

func TestCreateModel_MissingOpenAIKey(t *testing.T) {
	_, err := createModel(context.Background(), "gpt-4o", "", "", "")
	if err == nil {
		t.Fatal("expected error for missing OpenAI key")
	}
	if !strings.Contains(err.Error(), "OpenAI API key is required") {
		t.Errorf("error should mention OpenAI key requirement, got: %v", err)
	}
	if !strings.Contains(err.Error(), HeaderOpenAIAPIKey) {
		t.Errorf("error should mention BYOK header name, got: %v", err)
	}
}

func TestCreateModel_MissingGeminiKey(t *testing.T) {
	_, err := createModel(context.Background(), "gemini-2.5-flash", "", "", "")
	if err == nil {
		t.Fatal("expected error for missing Gemini key")
	}
	if !strings.Contains(err.Error(), "Gemini API key is required") {
		t.Errorf("error should mention Gemini key requirement, got: %v", err)
	}
	if !strings.Contains(err.Error(), HeaderGeminiAPIKey) {
		t.Errorf("error should mention BYOK header name, got: %v", err)
	}
}

func TestCreateModel_KeysNotInErrorDetail(t *testing.T) {
	secretKey := "sk-super-secret-12345"
	_, err := createModel(context.Background(), "claude-sonnet-4-6", "", secretKey, "")

	// When a key IS provided but for the wrong provider, the error for the
	// correct provider should never leak the other provider's key value.
	if err != nil && strings.Contains(err.Error(), secretKey) {
		t.Errorf("error message must not contain raw API key material")
	}
}
