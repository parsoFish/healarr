package mounts

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

const mountRaceID = "mount_race"

// mountRaceCheck detects a bind mount that silently fell through to the
// container's own root filesystem: the entire point of a mount is that the
// container sees the host's data, and when the mount race loses, it
// instead sees (and can quietly write into) an empty directory on the
// container's ephemeral layer. That is what happened on 2026-09-12.
func mountRaceCheck() check.Check {
	return check.Check{
		ID:      mountRaceID,
		Nodes:   check.PiOnly,
		Tier:    check.TierCorrect,
		Cadence: check.Every5m,
		Run:     runMountRace,
	}
}

func runMountRace(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Docker == nil || d.Host == nil || len(d.Cfg.Mounts) == 0 {
		return check.Result{}, check.ErrNotConfigured
	}

	var findings []check.Finding
	for _, m := range d.Cfg.Mounts {
		f, err := inspectMount(ctx, d, m)
		if err != nil {
			return check.Result{}, err
		}
		if f != nil {
			findings = append(findings, *f)
		}
	}
	return check.Result{Findings: findings}, nil
}

// inspectMount execs a stat+ls probe inside the container for m and decides
// whether it shows a mount race, an empty-but-mounted directory, or is
// healthy. A container that can't be exec'd is itself a symptom, so that
// case becomes a finding rather than a check error.
func inspectMount(ctx context.Context, d check.Deps, m config.Mount) (*check.Finding, error) {
	key := m.Container + ":" + m.ContainerPath

	out, err := d.Docker.Exec(ctx, m.Container, "sh", "-c", probeCmd(m.ContainerPath))
	if err != nil {
		f := d.NewFinding(mountRaceID, key, check.SeverityCritical, check.TierCorrect, "cannot inspect mount inside container")
		f.Detail = err.Error()
		return &f, nil
	}

	rootDev, pathDev, entries, err := parseMountProbe(out)
	if err != nil {
		// A probe this check cannot read is itself a symptom worth
		// reporting on that mount, not a reason to abandon the rest: the
		// mount whose bind failed is often exactly the one whose probe
		// misbehaves.
		f := d.NewFinding(mountRaceID, key, check.SeverityCritical, check.TierCorrect,
			"cannot parse mount probe for "+key)
		f.Detail = err.Error()
		f.Data = map[string]any{"raw": out}
		return &f, nil
	}

	hostMounted, err := d.Host.IsMountpoint(ctx, m.Host)
	if err != nil {
		return nil, fmt.Errorf("is mountpoint %s: %w", m.Host, err)
	}

	return decideMountRace(d, m, key, rootDev, pathDev, entries, hostMounted), nil
}

// probeCmd builds the `sh -c` probe run inside the container: the root
// filesystem's device, path's device, and path's entry count, one per
// line. Stderr is redirected for the command as a whole, because a stat or
// ls diagnostic (a path the container cannot see is the common case) would
// otherwise interleave with the three lines parseMountProbe reads.
func probeCmd(containerPath string) string {
	q := shellQuote(containerPath)
	return "( stat -c %d / " + q + "; ls -A " + q + " | wc -l ) 2>/dev/null"
}

// parseMountProbe reads the three lines probeCmd prints: the root
// filesystem's device, the path's device, and its entry count.
func parseMountProbe(out string) (rootDev, pathDev uint64, entries int64, err error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		return 0, 0, 0, fmt.Errorf("expected 3 lines, got %d", len(lines))
	}
	rootDev, err = strconv.ParseUint(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("root device: %w", err)
	}
	pathDev, err = strconv.ParseUint(strings.TrimSpace(lines[1]), 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("path device: %w", err)
	}
	entries, err = strconv.ParseInt(strings.TrimSpace(lines[2]), 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("entry count: %w", err)
	}
	return rootDev, pathDev, entries, nil
}

// decideMountRace applies the mount_race rule: same device as root means
// the mount never took (critical); a distinct device but zero entries while
// the host side is healthy means something emptied the container's view
// (warn); anything else is healthy.
func decideMountRace(d check.Deps, m config.Mount, key string, rootDev, pathDev uint64, entries int64, hostMounted bool) *check.Finding {
	data := map[string]any{"rootDevice": rootDev, "pathDevice": pathDev, "entries": entries, "hostMounted": hostMounted}

	switch {
	case pathDev == rootDev:
		f := d.NewFinding(mountRaceID, key, check.SeverityCritical, check.TierCorrect,
			fmt.Sprintf("%s is bound to the container root filesystem (mount race)", key))
		f.Data = data
		return &f
	case entries == 0 && hostMounted:
		f := d.NewFinding(mountRaceID, key, check.SeverityWarn, check.TierCorrect,
			fmt.Sprintf("%s is empty while host mount is healthy", key))
		f.Data = data
		return &f
	default:
		return nil
	}
}

// shellQuote wraps s in single quotes for a POSIX `sh -c` argument. An
// embedded single quote is escaped by closing the quoted string, emitting
// a backslash-escaped quote, and reopening it — the standard POSIX idiom,
// since a single-quoted shell string has no escape character of its own.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
