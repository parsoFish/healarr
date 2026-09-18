package cleanup

import (
	"context"
	"testing"

	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/config"
)

// TestIsUnderDirRejectsSiblingWithSharedPrefix guards against a naive
// strings.HasPrefix comparison: "/data/recycle-old" shares a string
// prefix with "/data/recycle" but is a sibling directory, not something
// nested under it, and must not be treated as allowed.
func TestIsUnderDirRejectsSiblingWithSharedPrefix(t *testing.T) {
	if isUnderDir("/data/recycle", "/data/recycle-old/file.mkv") {
		t.Error("isUnderDir matched a sibling directory sharing a string prefix")
	}
	if !isUnderDir("/data/recycle", "/data/recycle/sub/file.mkv") {
		t.Error("isUnderDir rejected a true descendant")
	}
	if !isUnderDir("/data/recycle", "/data/recycle") {
		t.Error("isUnderDir rejected the directory itself")
	}
}

// TestAllowedPrefixesRejectsAKindThatDoesNotRemoveHostPaths exercises the
// defensive default branch executeHostRemove's allowedPrefixes call would
// hit if it were ever reached for a kind other than recycle/orphans —
// unreachable via Execute's own switch today, but this keeps the
// defence-in-depth check itself proven correct in isolation.
func TestAllowedPrefixesRejectsAKindThatDoesNotRemoveHostPaths(t *testing.T) {
	if _, err := allowedPrefixes(config.Config{}, KindSeeded); err == nil {
		t.Fatal("expected an error for a kind that isn't recycle/orphans")
	}
}

// TestExecuteHostRemoveRejectsAKindThatDoesNotRemoveHostPaths proves
// executeHostRemove itself (not just allowedPrefixes) refuses a plan of a
// kind it doesn't know how to validate paths for, rather than silently
// deleting under no restriction at all.
func TestExecuteHostRemoveRejectsAKindThatDoesNotRemoveHostPaths(t *testing.T) {
	host := &hostfs.Fake{}
	_, err := executeHostRemove(context.Background(), baseDeps(config.Config{}, withHost(host)), Plan{Kind: KindSeeded, Items: []PlanItem{{Key: "/anything"}}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(host.Removed) != 0 {
		t.Fatalf("Removed = %v, want nothing removed", host.Removed)
	}
}
