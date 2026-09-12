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
