package openai

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go/responses"
	"google.golang.org/genai"
)

func TestPartsFromOutputItem_FunctionCall(t *testing.T) {
	raw := `{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"}`

	var item responses.ResponseOutputItemUnion
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatalf("unmarshal output item: %v", err)
	}

	parts, err := partsFromOutputItem(item)
	if err != nil {
		t.Fatalf("partsFromOutputItem returned error: %v", err)
	}
	if len(parts) != 1 || parts[0].FunctionCall == nil {
		t.Fatalf("expected one function call part, got %+v", parts)
	}

	call := parts[0].FunctionCall
	if call.ID != "call_1" || call.Name != "lookup" {
		t.Fatalf("unexpected function call metadata: %+v", call)
	}
	if call.Args["q"] != "x" {
		t.Fatalf("expected parsed args, got %+v", call.Args)
	}
}

func TestBuildFinishReason_IncompleteMaxTokens(t *testing.T) {
	raw := `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}`

	var resp responses.Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got := buildFinishReason(&resp); got != genai.FinishReasonMaxTokens {
		t.Fatalf("buildFinishReason() = %v, want %v", got, genai.FinishReasonMaxTokens)
	}
}
