package check

import (
	"context"
	"testing"

	"github.com/parsoFish/healarr/internal/config"
)

func noop(context.Context, Deps) (Result, error) { return Result{}, nil }

func TestRegistryRegisterAndFilter(t *testing.T) {
	r := NewRegistry()
	must := func(c Check) {
		t.Helper()
		if err := r.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	must(Check{ID: "a", Nodes: []config.Node{config.NodePi}, Run: noop})
	must(Check{ID: "b", Nodes: []config.Node{config.NodePi, config.NodeNAS}, Run: noop})
	must(Check{ID: "c", Nodes: []config.Node{config.NodeNAS}, Run: noop})
	if got := ids(r.All()); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("All order: %v", got)
	}
	if got := ids(r.ForNode(config.NodeNAS)); len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("ForNode nas: %v", got)
	}
	if _, ok := r.ByID("b"); !ok {
		t.Fatal("ByID b missing")
	}
	if _, ok := r.ByID("zzz"); ok {
		t.Fatal("ByID zzz should miss")
	}
}

func TestRegistryRejectsBadChecks(t *testing.T) {
	r := NewRegistry()
	cases := map[string]Check{
		"empty id": {Nodes: []config.Node{config.NodePi}, Run: noop},
		"nil run":  {ID: "x", Nodes: []config.Node{config.NodePi}},
		"no nodes": {ID: "y", Run: noop},
	}
	for name, c := range cases {
		if err := r.Register(c); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := r.Register(Check{ID: "dup", Nodes: []config.Node{config.NodePi}, Run: noop}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Check{ID: "dup", Nodes: []config.Node{config.NodePi}, Run: noop}); err == nil {
		t.Fatal("duplicate id accepted")
	}
}

func ids(cs []Check) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}
