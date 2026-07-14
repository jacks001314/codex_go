package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionDefaultsDoNotInstallLocalAgentOrMemoryMCPStubs(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	checks := []struct{ path, forbidden string }{
		{filepath.Join(root, "internal", "appserver", "runtime_router.go"), "Agent:        model.NewLocalAgentRunner()"},
		{filepath.Join(root, "internal", "appserver", "agent_runtime.go"), "r.services.Agent = model.NewLocalAgentRunner()"},
		{filepath.Join(root, "internal", "mcp", "server_stdio.go"), "server.runner = NewMemoryCodexToolRunner()"},
		{filepath.Join(root, "internal", "exec", "exec.go"), "UseResponsesAPI: false"},
	}
	for _, check := range checks {
		data, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", check.path, err)
		}
		if strings.Contains(string(data), check.forbidden) {
			t.Fatalf("production stub leakage in %s: %s", check.path, check.forbidden)
		}
	}
}
