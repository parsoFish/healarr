package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// execCommand builds the command Exec runs; tests override it to stub the
// docker binary without needing Docker installed.
var execCommand = exec.CommandContext

// Exec runs `<binary> exec <container> <args...>` and returns its combined
// stdout+stderr output, trimmed. A non-zero exit is wrapped into an error
// that includes the captured output (so any stderr diagnostic reaches the
// caller).
func (c *HTTPClient) Exec(ctx context.Context, container string, args ...string) (string, error) {
	full := make([]string, 0, len(args)+2)
	full = append(full, "exec", container)
	full = append(full, args...)

	cmd := execCommand(ctx, c.binary, full...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker exec %s: %w: %s", container, err, strings.TrimSpace(buf.String()))
	}
	return strings.TrimSpace(buf.String()), nil
}
