package agentd

import (
	"sort"
	"testing"

	"google.golang.org/adk/agent"
)

func newTestAgent(t *testing.T, name string) agent.Agent {
	t.Helper()
	a, err := agent.New(agent.Config{Name: name})
	if err != nil {
		t.Fatalf("agent.New(%q): %v", name, err)
	}
	return a
}

func TestResolveServerDefaults(t *testing.T) {
	root := newTestAgent(t, "root")

	cfg, args, err := resolveServer([]string{"api"}, root)
	if err != nil {
		t.Fatalf("resolveServer: %v", err)
	}

	if got := []string{"api"}; len(args) != 1 || args[0] != got[0] {
		t.Errorf("args = %v, want %v", args, got)
	}
	if cfg.AgentLoader == nil || cfg.AgentLoader.RootAgent().Name() != "root" {
		t.Errorf("loader root = %v, want root", cfg.AgentLoader)
	}
	if names := cfg.AgentLoader.ListAgents(); len(names) != 1 || names[0] != "root" {
		t.Errorf("ListAgents = %v, want [root] (single loader)", names)
	}
	if cfg.SessionService == nil {
		t.Error("SessionService is nil, want in-memory default")
	}
	if cfg.ArtifactService == nil {
		t.Error("ArtifactService is nil, want in-memory default")
	}
	if cfg.MemoryService != nil {
		t.Error("MemoryService is non-nil, want opt-in (nil) by default")
	}
}

func TestResolveServerWithSubAgents(t *testing.T) {
	root := newTestAgent(t, "root")
	sub1 := newTestAgent(t, "sub1")
	sub2 := newTestAgent(t, "sub2")

	cfg, _, err := resolveServer([]string{"api"}, root, WithSubAgents(sub1, sub2))
	if err != nil {
		t.Fatalf("resolveServer: %v", err)
	}

	got := cfg.AgentLoader.ListAgents()
	sort.Strings(got)
	want := []string{"root", "sub1", "sub2"}
	if len(got) != len(want) {
		t.Fatalf("ListAgents = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListAgents = %v, want %v", got, want)
		}
	}
}

func TestResolveServerArgsOverride(t *testing.T) {
	root := newTestAgent(t, "root")

	_, args, err := resolveServer([]string{"api"}, root, WithServerArgs("api", "-path_prefix=/v1"))
	if err != nil {
		t.Fatalf("resolveServer: %v", err)
	}

	want := []string{"api", "-path_prefix=/v1"}
	if len(args) != len(want) || args[0] != want[0] || args[1] != want[1] {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestResolveServerMemoryOptIn(t *testing.T) {
	root := newTestAgent(t, "root")

	cfg, _, err := resolveServer([]string{"api"}, root, WithInMemoryMemoryService())
	if err != nil {
		t.Fatalf("resolveServer: %v", err)
	}

	if cfg.MemoryService == nil {
		t.Error("MemoryService is nil after WithInMemoryMemoryService(), want non-nil")
	}
}
