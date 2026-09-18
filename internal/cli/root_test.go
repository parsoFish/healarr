package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootVersionPrintsVersion(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCmd(&Deps{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "healarr ") {
		t.Fatalf("expected version line, got %q", out.String())
	}
}

func TestRootHasGlobalFlags(t *testing.T) {
	cmd := NewRootCmd(&Deps{})
	for _, name := range []string{"config", "json", "dry-run"} {
		if cmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("missing persistent flag --%s", name)
		}
	}
}

// TestRootRegistersCleanupAndDecideCommands proves `cleanup` and `decide`
// (built by newCleanupCmd/newDecideCmd, previously left unregistered per
// their own task briefs) are reachable from the real root command tree.
func TestRootRegistersCleanupAndDecideCommands(t *testing.T) {
	cmd := NewRootCmd(&Deps{})
	for _, name := range []string{"cleanup", "decide"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil || found.Name() != name {
			t.Errorf("root command tree missing %q: found=%v err=%v", name, found, err)
		}
	}
}
