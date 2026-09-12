package docker

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func withStubExecCommand(t *testing.T, fn func(ctx context.Context, name string, args ...string) *exec.Cmd) {
	t.Helper()
	orig := execCommand
	execCommand = fn
	t.Cleanup(func() { execCommand = orig })
}

func TestExecSuccess(t *testing.T) {
	var gotName string
	var gotArgs []string
	withStubExecCommand(t, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return exec.CommandContext(ctx, "echo", "-n", "container ok")
	})
	c := &HTTPClient{binary: "docker"}

	out, err := c.Exec(context.Background(), "sonarr", "curl", "-f", "http://localhost:8989/ping")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if out != "container ok" {
		t.Errorf("expected trimmed combined output, got %q", out)
	}
	if gotName != "docker" {
		t.Errorf("expected binary %q, got %q", "docker", gotName)
	}
	want := []string{"exec", "sonarr", "curl", "-f", "http://localhost:8989/ping"}
	if len(gotArgs) != len(want) {
		t.Fatalf("expected args %v, got %v", want, gotArgs)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
}

func TestExecNonZeroExitWrapsStderr(t *testing.T) {
	withStubExecCommand(t, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo boom-detail 1>&2; exit 3")
	})
	c := &HTTPClient{binary: "docker"}

	_, err := c.Exec(context.Background(), "sonarr", "false")
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom-detail") {
		t.Errorf("expected stderr detail in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "sonarr") {
		t.Errorf("expected container name in error, got: %v", err)
	}
}

func TestExecDoesNotMutateCallerArgs(t *testing.T) {
	withStubExecCommand(t, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	})
	c := &HTTPClient{binary: "docker"}

	args := []string{"ping"}
	if _, err := c.Exec(context.Background(), "sonarr", args...); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(args) != 1 || args[0] != "ping" {
		t.Errorf("caller's args slice was mutated: %v", args)
	}
}
