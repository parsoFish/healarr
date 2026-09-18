package decision

import (
	"testing"

	"github.com/parsoFish/healarr/internal/store"
)

// decisionStoreSatisfiedByStore is a compile-time assertion that
// *store.Store satisfies DecisionStore, so Execute can be called with a
// real store in production without an adapter type.
var _ DecisionStore = (*store.Store)(nil)

func TestKindConstantsMatchStoredValues(t *testing.T) {
	if KindKeep != "keep" {
		t.Errorf("KindKeep = %q, want %q", KindKeep, "keep")
	}
	if KindDelete != "delete" {
		t.Errorf("KindDelete = %q, want %q", KindDelete, "delete")
	}
}
