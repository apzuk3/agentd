package openai

import "testing"

func TestToMessageRole(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "user default", in: "", want: "user"},
		{name: "assistant", in: "assistant", want: "assistant"},
		{name: "model maps to assistant", in: "model", want: "assistant"},
		{name: "system", in: "system", want: "system"},
		{name: "developer", in: "developer", want: "developer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(toMessageRole(tt.in)); got != tt.want {
				t.Fatalf("toMessageRole(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStringifyFunctionResponse(t *testing.T) {
	if got := stringifyFunctionResponse(map[string]any{"result": 42}); got != "42" {
		t.Fatalf("expected result key to win, got %q", got)
	}
	if got := stringifyFunctionResponse(map[string]any{"output": "ok"}); got != "ok" {
		t.Fatalf("expected output key to be used, got %q", got)
	}
}
