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

// TestExecuteHostRemoveCleansPathBeforeRemoving proves the path handed to
// Host.Remove is exactly the cleaned form underAnyPrefix validated as
// safe — not the raw, uncleaned item.Key — so a redundant "." segment
// can't leave the validated path and the removed path pointing at two
// different (if equivalent) strings.
func TestExecuteHostRemoveCleansPathBeforeRemoving(t *testing.T) {
	host := &hostfs.Fake{}
	cfg := config.Config{Checks: config.Checks{RecycleDirs: []string{"/data/recycle"}}}
	plan := Plan{Kind: KindRecycle, Items: []PlanItem{{Key: "/data/recycle/./old.mkv", Bytes: 10}}}

	res, err := executeHostRemove(context.Background(), baseDeps(cfg, withHost(host)), plan)
	if err != nil {
		t.Fatalf("executeHostRemove: %v", err)
	}
	if res.Executed != 1 || len(res.Errors) != 0 {
		t.Fatalf("res = %+v, want Executed=1 no errors", res)
	}
	if len(host.Removed) != 1 || host.Removed[0] != "/data/recycle/old.mkv" {
		t.Fatalf("Removed = %v, want [/data/recycle/old.mkv] (cleaned)", host.Removed)
	}
}

// TestExecuteHostRemoveRefusesDotDotTraversal proves a literal ".."
// traversal segment can't walk a removal outside the configured
// directory, even though the raw string still starts with the allowed
// prefix's characters.
func TestExecuteHostRemoveRefusesDotDotTraversal(t *testing.T) {
	host := &hostfs.Fake{}
	cfg := config.Config{Checks: config.Checks{RecycleDirs: []string{"/data/recycle"}}}
	plan := Plan{Kind: KindRecycle, Items: []PlanItem{{Key: "/data/recycle/../etc/x", Bytes: 10}}}

	res, err := executeHostRemove(context.Background(), baseDeps(cfg, withHost(host)), plan)
	if err != nil {
		t.Fatalf("executeHostRemove: %v", err)
	}
	if res.Executed != 0 || len(res.Errors) != 1 {
		t.Fatalf("res = %+v, want Executed=0 and one refusal", res)
	}
	if len(host.Removed) != 0 {
		t.Fatalf("Removed = %v, want nothing removed", host.Removed)
	}
}
