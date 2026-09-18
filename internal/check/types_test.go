package check

import (
	"testing"

	"github.com/parsoFish/healarr/internal/config"
)

func TestFindingKey(t *testing.T) {
	f := Finding{CheckID: "a", EntityKey: "b"}
	if got := f.Key(); got != "a:b" {
		t.Fatalf("Key() = %q, want %q", got, "a:b")
	}
}

func TestCheckAppliesTo(t *testing.T) {
	c := Check{ID: "x", Nodes: []config.Node{config.NodePi}}
	if !c.AppliesTo(config.NodePi) {
		t.Fatal("expected AppliesTo(NodePi) to be true")
	}
	if c.AppliesTo(config.NodeNAS) {
		t.Fatal("expected AppliesTo(NodeNAS) to be false")
	}

	both := Check{ID: "y", Nodes: []config.Node{config.NodePi, config.NodeNAS}}
	if !both.AppliesTo(config.NodePi) || !both.AppliesTo(config.NodeNAS) {
		t.Fatal("expected AppliesTo to be true for both nodes")
	}
}
