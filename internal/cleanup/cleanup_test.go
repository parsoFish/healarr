package cleanup

import (
	"context"
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/config"
)

func TestAllReturnsEveryKindInOrder(t *testing.T) {
	planners := All()
	want := []Kind{KindRecycle, KindOrphans, KindDocker, KindSeeded}
	if len(planners) != len(want) {
		t.Fatalf("All() = %d planners, want %d", len(planners), len(want))
	}
	for i, p := range planners {
		if p.Kind() != want[i] {
			t.Errorf("All()[%d].Kind() = %s, want %s", i, p.Kind(), want[i])
		}
	}
}

func TestAllPlannerNodes(t *testing.T) {
	byKind := map[Kind][]config.Node{}
	for _, p := range All() {
		byKind[p.Kind()] = p.Nodes()
	}
	wantNAS := []Kind{KindRecycle, KindOrphans, KindSeeded}
	for _, k := range wantNAS {
		nodes := byKind[k]
		if len(nodes) != 1 || nodes[0] != config.NodeNAS {
			t.Errorf("%s.Nodes() = %v, want [nas]", k, nodes)
		}
	}
	nodes := byKind[KindDocker]
	if len(nodes) != 1 || nodes[0] != config.NodePi {
		t.Errorf("docker.Nodes() = %v, want [pi]", nodes)
	}
}

// errFake is a client that always errors on any call the gate must never
// reach: if Execute called it before checking the actions gate, the
// error below would surface as something other than ErrActionsDisabled
// (or, for a nil client, panic on a nil pointer dereference), so this
// doubles as the "gate runs before any client call" test for each kind.
var errGateShouldNotBeReached = errors.New("cleanup_test: gate should have short-circuited before this call")

func TestExecuteRefusesWhenActionsDisabledBeforeAnyClientCall(t *testing.T) {
	cfg := config.Config{Actions: config.Actions{Enabled: false}, Cleanup: config.Cleanup{DryRun: false}}
	for _, p := range allKindPlans() {
		t.Run(string(p.Kind), func(t *testing.T) {
			// Every client is nil: a gate that called through to a client
			// before checking Actions.Enabled would panic here instead of
			// returning ErrActionsDisabled.
			d := baseDeps(cfg)
			_, err := Execute(context.Background(), d, p)
			if !errors.Is(err, ErrActionsDisabled) {
				t.Fatalf("Execute = %v, want ErrActionsDisabled", err)
			}
		})
	}
}

func TestExecuteRefusesWhenCleanupDryRunBeforeAnyClientCall(t *testing.T) {
	cfg := config.Config{Actions: config.Actions{Enabled: true}, Cleanup: config.Cleanup{DryRun: true}}
	host := &hostfs.Fake{Err: errGateShouldNotBeReached}
	dk := &docker.Fake{Err: errGateShouldNotBeReached}
	qb := &qbittorrent.Fake{Err: errGateShouldNotBeReached}
	d := baseDeps(cfg, withHost(host), withDocker(dk), withQBit(qb))

	for _, p := range allKindPlans() {
		t.Run(string(p.Kind), func(t *testing.T) {
			_, err := Execute(context.Background(), d, p)
			if !errors.Is(err, ErrActionsDisabled) {
				t.Fatalf("Execute = %v, want ErrActionsDisabled", err)
			}
		})
	}
	if len(host.Calls) != 0 || len(dk.Calls) != 0 || len(qb.Calls) != 0 {
		t.Fatalf("gate reached a client: host=%v docker=%v qbit=%v", host.Calls, dk.Calls, qb.Calls)
	}
}

func TestExecuteUnknownKindErrors(t *testing.T) {
	d := baseDeps(enabledCfg(nil))
	_, err := Execute(context.Background(), d, Plan{Kind: "bogus"})
	if err == nil {
		t.Fatal("expected an error for an unknown plan kind")
	}
}

// allKindPlans returns one (empty) Plan per Kind, used only to iterate
// every kind in the gate tests above: before the gate short-circuits,
// nothing but p.Kind is read.
func allKindPlans() []Plan {
	return []Plan{{Kind: KindRecycle}, {Kind: KindOrphans}, {Kind: KindDocker}, {Kind: KindSeeded}}
}
